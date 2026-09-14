// Package mail 提供 SMTP 发信能力（D29）。
//
// 只用标准库 `net/smtp`（+ `crypto/tls`），**不引入第三方邮件库**：
// 需求就是「发一封 HTML 邮件」，第三方库带来的依赖面与收益不成比例。
//
// 支持的加密方式（对应设置 `mail.encryption`）：
//
//	ssl      隐式 TLS，通常 465 —— 连接建立即握手
//	starttls 明文连接后升级，通常 587
//	none     明文（仅限内网/本机中继）
//
// ⚠️ 邮件日志**不保存正文**（用户明确要求）：只记发给谁 / 主题 / 模板 / 结果。
package mail

import (
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// 加密方式取值。
const (
	EncryptionSSL      = "ssl"
	EncryptionStartTLS = "starttls"
	EncryptionNone     = "none"
)

// 发送超时。SMTP 握手 + 投递通常几秒内完成；30s 足够且不会让请求挂太久。
const sendTimeout = 30 * time.Second

// ErrDisabled 表示邮件功能未启用（设置 `mail.enabled = false`）。
var ErrDisabled = errors.New("邮件功能未启用")

// Message 是一封待发送的邮件。
type Message struct {
	To      string
	Subject string
	// HTML 邮件正文（HTML 片段；模板已由调用方渲染）。
	HTML string
}

// Config 是 SMTP 连接参数（全部来自数据库设置，非 env）。
type Config struct {
	Enabled     bool
	Host        string
	Port        int
	Encryption  string
	Username    string
	Password    string
	FromAddress string
	FromName    string
}

// Normalize 把缺失值补成合理默认（端口按加密方式推断）。
func (c Config) Normalize() Config {
	out := c
	out.Encryption = strings.ToLower(strings.TrimSpace(out.Encryption))
	switch out.Encryption {
	case EncryptionSSL, EncryptionStartTLS, EncryptionNone:
	default:
		out.Encryption = EncryptionSSL
	}
	if out.Port <= 0 {
		switch out.Encryption {
		case EncryptionStartTLS:
			out.Port = 587
		case EncryptionNone:
			out.Port = 25
		default:
			out.Port = 465
		}
	}
	if strings.TrimSpace(out.FromAddress) == "" {
		out.FromAddress = out.Username
	}
	if strings.TrimSpace(out.FromName) == "" {
		out.FromName = "PicGo Web"
	}
	return out
}

// Validate 校验必填项。
func (c Config) Validate() error {
	if !c.Enabled {
		return ErrDisabled
	}
	if strings.TrimSpace(c.Host) == "" {
		return errors.New("SMTP 服务器地址未配置")
	}
	if strings.TrimSpace(c.FromAddress) == "" {
		return errors.New("发件人地址未配置（且无法从用户名推断）")
	}
	return nil
}

// Sender 发信器。
type Sender struct {
	cfg Config
	// dialer 可被测试替换（避免真连 SMTP）。
	dialer func(addr string, cfg Config) (smtpClient, error)
}

// New 构造发信器。
func New(cfg Config) *Sender {
	s := &Sender{cfg: cfg.Normalize()}
	s.dialer = defaultDial
	return s
}

// Config 返回归一化后的配置（供调用方展示/日志）。
func (s *Sender) Config() Config { return s.cfg }

// Send 发送一封 HTML 邮件。
//
// 返回的错误是**可安全展示给管理员**的（不含密码），
// 调用方应把它写进 EmailLogs.Error。
func (s *Sender) Send(msg Message) error {
	if err := s.cfg.Validate(); err != nil {
		return err
	}
	to := strings.TrimSpace(msg.To)
	if to == "" {
		return errors.New("收件人地址为空")
	}
	if strings.ContainsAny(to, "\r\n") {
		// 防邮件头注入
		return errors.New("收件人地址含非法字符")
	}
	subject := strings.TrimSpace(msg.Subject)
	if strings.ContainsAny(subject, "\r\n") {
		return errors.New("邮件主题含非法字符")
	}

	client, err := s.dialer(net.JoinHostPort(s.cfg.Host, fmt.Sprintf("%d", s.cfg.Port)), s.cfg)
	if err != nil {
		return fmt.Errorf("连接 SMTP 服务器失败: %w", err)
	}
	defer func() { _ = client.Close() }()

	// 认证：用户名非空才认证（部分内网中继不要求认证）
	if strings.TrimSpace(s.cfg.Username) != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.Username, s.cfg.Password, s.cfg.Host)); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := client.Mail(s.cfg.FromAddress); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("设置收件人失败: %w", err)
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("开始写入邮件正文失败: %w", err)
	}
	if _, err := w.Write(buildMessage(s.cfg, to, subject, msg.HTML)); err != nil {
		_ = w.Close()
		return fmt.Errorf("写入邮件正文失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("提交邮件失败: %w", err)
	}
	if err := client.Quit(); err != nil {
		// Quit 失败通常不影响投递（邮件已提交），因此只当作警告
		return nil
	}
	return nil
}

// buildMessage 组装 RFC 5322 报文。
//
// 用 `Content-Transfer-Encoding: 8bit` + UTF-8 编码主题：
// 中文主题必须编码（RFC 2047 的 B 编码），否则部分客户端会显示乱码。
func buildMessage(cfg Config, to, subject, html string) []byte {
	var b strings.Builder

	from := cfg.FromAddress
	if strings.TrimSpace(cfg.FromName) != "" {
		from = fmt.Sprintf("%s <%s>", encodeHeader(cfg.FromName), cfg.FromAddress)
	}

	b.WriteString("From: " + from + "\r\n")
	b.WriteString("To: " + to + "\r\n")
	b.WriteString("Subject: " + encodeHeader(subject) + "\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/html; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("\r\n")
	b.WriteString(html)
	return []byte(b.String())
}

// encodeHeader 按 RFC 2047 编码非 ASCII 头的值。
//
// 纯 ASCII 且不含特殊字符时原样返回，避免给英文用户添乱。
func encodeHeader(s string) string {
	if isASCII(s) && !strings.ContainsAny(s, "?=") {
		return s
	}
	return "=?UTF-8?B?" + base64Std(s) + "?="
}

// base64Std 是 RFC 2047 的 B 编码要求的标准 base64。
func base64Std(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// smtpClient 是本包对 *smtp.Client 的最小抽象（便于测试替换）。
type smtpClient interface {
	Auth(auth smtp.Auth) error
	Mail(from string) error
	Rcpt(to string) error
	Data() (io.WriteCloser, error)
	StartTLS(*tls.Config) error
	Extension(string) (bool, string)
	Quit() error
	Close() error
}

// defaultDial 按加密方式建立连接并返回 smtp.Client。
func defaultDial(addr string, cfg Config) (smtpClient, error) {
	dialer := &net.Dialer{Timeout: sendTimeout}

	switch cfg.Encryption {
	case EncryptionSSL:
		conn, err := tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName: cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
		if err != nil {
			return nil, err
		}
		c, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return c, nil

	case EncryptionStartTLS:
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		c, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if ok, _ := c.Extension("STARTTLS"); !ok {
			_ = c.Close()
			return nil, errors.New("服务器不支持 STARTTLS（如需明文请把加密方式改为 none）")
		}
		if err := c.StartTLS(&tls.Config{
			ServerName: cfg.Host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			_ = c.Close()
			return nil, err
		}
		return c, nil

	default: // none
		conn, err := dialer.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		c, err := smtp.NewClient(conn, cfg.Host)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return c, nil
	}
}
