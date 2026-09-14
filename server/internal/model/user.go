package model

import "time"

func defaultNow() int64 { return time.Now().Unix() }

// 角色与状态取值（枚举存字符串，D77）。
const (
	UserRoleAdmin = "admin"
	UserRoleUser  = "user"

	UserStatusActive   = "active"
	UserStatusDisabled = "disabled"
)

// User 是身份核心表：只放**认证与授权必需**的字段。
//
// 展示类信息（昵称/头像）在 UserProfiles —— 拆表原因（D78 可选性不同）：
// 邮箱/密码/角色是认证数据（查询少、绝不能泄露），
// 昵称/头像是展示数据（详情页才读）。拆开后列表查询天然不会带出敏感列。
type User struct {
	ID                 uint64 `gorm:"primaryKey;autoIncrement"`
	UID                string `gorm:"size:32;uniqueIndex;not null"`
	Email              string `gorm:"size:255;uniqueIndex;not null"`
	PasswordHash       string `gorm:"size:255"`                        // bcrypt cost=12；纯 OAuth 用户为空
	Role               string `gorm:"size:16;not null;default:user"`   // admin | user
	Status             string `gorm:"size:16;index;not null;default:active"` // active | disabled
	CapacityBytes      int64  `gorm:"not null;default:0"`              // 0 = 不限额（D20）
	UsedBytes          int64  `gorm:"not null;default:0"`              // 当前持有的图片体积合计（D72）
	MustChangePassword bool   `gorm:"not null;default:false"`
	LastLoginAt        int64  `gorm:"not null;default:0"`
	Metadata           string `gorm:"type:text"` // JSON 扩展位
	CreatedAt          int64  `gorm:"not null"`
	UpdatedAt          int64  `gorm:"not null"`
}

func (User) TableName() string { return "Users" }

// IsAdmin 是否为管理员。管理员跳过配额校验与上传限流（D20 / D73）。
func (u *User) IsAdmin() bool { return u.Role == UserRoleAdmin }

// IsActive 账号是否可用。
func (u *User) IsActive() bool { return u.Status == UserStatusActive }

// Unlimited 是否不限额（CapacityBytes = 0 视为不限额，D20）。
func (u *User) Unlimited() bool { return u.CapacityBytes <= 0 }

// UserProfile 是展示信息（与 User 1:1）。
type UserProfile struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	UserUID   string `gorm:"size:32;uniqueIndex;not null"`
	Nickname  string `gorm:"size:128"`
	AvatarURL string `gorm:"size:512"`
	Homepage  string `gorm:"size:512"`
	Locale    string `gorm:"size:16;default:zh-CN"`
	Metadata  string `gorm:"type:text"`
	CreatedAt int64  `gorm:"not null"`
	UpdatedAt int64  `gorm:"not null"`
}

func (UserProfile) TableName() string { return "UserProfiles" }

// OAuthIdentity 是已绑定的第三方身份。
//
// 一个用户可绑定多个；**必须先绑定才能用于登录**（D27）。
// 本项目只支持 GitHub（D26），Provider 字段保留以便将来扩展。
type OAuthIdentity struct {
	ID             uint64 `gorm:"primaryKey;autoIncrement"`
	UID            string `gorm:"size:32;uniqueIndex;not null"`
	UserUID        string `gorm:"size:32;index;not null"`
	Provider       string `gorm:"size:32;not null"`     // github
	ProviderUserID string `gorm:"size:191;not null"`    // GitHub 数字 ID（稳定唯一标识，D28）
	ProviderLogin  string `gorm:"size:191"`                                                 // GitHub username（仅展示）
	ProviderEmail  string `gorm:"size:255"`
	AvatarURL      string `gorm:"size:512"`
	Metadata       string `gorm:"type:text"`
	CreatedAt      int64  `gorm:"not null"`
	UpdatedAt      int64  `gorm:"not null"`
}

func (OAuthIdentity) TableName() string { return "OAuthIdentities" }

// ProviderGitHub GitHub OAuth 的 provider 名。
const ProviderGitHub = "github"

// RefreshToken 是本机会话（可吊销、可轮换）。只存哈希（D30）。
type RefreshToken struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	UID       string `gorm:"size:32;uniqueIndex;not null"`
	UserUID   string `gorm:"size:32;index;not null"`
	TokenHash string `gorm:"size:64;uniqueIndex;not null"` // sha256 hex
	UserAgent string `gorm:"size:255"`
	ClientIP  string `gorm:"size:64"`
	ExpiresAt int64  `gorm:"index;not null"`
	RevokedAt int64  `gorm:"not null;default:0"` // 0 = 未吊销
	CreatedAt int64  `gorm:"not null"`
}

func (RefreshToken) TableName() string { return "RefreshTokens" }

// Valid 是否仍然有效。
func (t *RefreshToken) Valid() bool {
	return t.RevokedAt == 0 && t.ExpiresAt > Now()
}

// APIToken 是长期令牌：明文只在创建时返回一次（D31）。
type APIToken struct {
	ID         uint64 `gorm:"primaryKey;autoIncrement"`
	UID        string `gorm:"size:32;uniqueIndex;not null"`
	UserUID    string `gorm:"size:32;index;not null"`
	Name       string `gorm:"size:64;not null"`
	TokenHash  string `gorm:"size:64;uniqueIndex;not null"` // sha256(明文)
	Prefix     string `gorm:"size:16;not null"`             // pcw_xxxxxxxx，用于展示
	LastUsedAt int64  `gorm:"not null;default:0"`
	ExpiresAt  int64  `gorm:"not null;default:0"` // 0 = 永不过期
	CreatedAt  int64  `gorm:"not null"`
}

func (APIToken) TableName() string { return "APITokens" }

// Valid 是否仍然有效。
func (t *APIToken) Valid() bool {
	return t.ExpiresAt == 0 || t.ExpiresAt > Now()
}

// LoginAttempt 是登录尝试记录。
//
// 独立表（而非计数器列）的原因（D78 写入频率不同 + 一对多）：
// 写入频繁、需要按时间窗查询、可定期清理。
type LoginAttempt struct {
	ID        uint64 `gorm:"primaryKey;autoIncrement"`
	Email     string `gorm:"size:255;not null"`
	ClientIP  string `gorm:"size:64;not null"`
	Success   bool   `gorm:"not null;default:false"`
	UserAgent string `gorm:"size:255"`
	CreatedAt int64  `gorm:"not null;index"`
}

func (LoginAttempt) TableName() string { return "LoginAttempts" }
