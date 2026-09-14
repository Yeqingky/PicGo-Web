package service

import (
	"context"
	"log/slog"
	"strings"

	"github.com/YeqingKy/PicGo-Web/server/internal/auth"
	"github.com/YeqingKy/PicGo-Web/server/internal/id"
	"github.com/YeqingKy/PicGo-Web/server/internal/model"
	"github.com/YeqingKy/PicGo-Web/server/internal/repository"
	"github.com/YeqingKy/PicGo-Web/server/internal/response"
	"github.com/YeqingKy/PicGo-Web/server/internal/settings"
)

// 密码长度约束。
//
// ⚠️ 上限 72 字节来自 bcrypt 本身：超过 72 字节的部分会被**静默丢弃**，
// 于是「两个不同的长密码」可能都能登录。因此这里直接拒绝过长密码，而不是悄悄截断。
const (
	MinPasswordBytes = 8
	MaxPasswordBytes = 72
)

// 默认分页。
const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// UserView 是用户的对外视图（Users + UserProfiles 合并，见 API.md §2）。
//
// ImageCount / AlbumCount 是**实时统计**而非冗余列：
// DATA-MODEL 未给 Users 加这两个计数列（D77「只增不删」而不是「随手加列」），
// 所以按需聚合查询即可。
type UserView struct {
	UID                string `json:"UID"`
	Email              string `json:"Email"`
	Role               string `json:"Role"`
	Status             string `json:"Status"`
	MustChangePassword bool   `json:"MustChangePassword"`
	Nickname           string `json:"Nickname"`
	AvatarURL          string `json:"AvatarURL"`
	Homepage           string `json:"Homepage"`
	CapacityBytes      int64  `json:"CapacityBytes"` // 0 = 不限额
	UsedBytes          int64  `json:"UsedBytes"`
	ImageCount         int64  `json:"ImageCount"`
	AlbumCount         int64  `json:"AlbumCount"`
	HasPassword        bool   `json:"HasPassword"`
	LastLoginAt        int64  `json:"LastLoginAt"`
	CreatedAt          int64  `json:"CreatedAt"`
	UpdatedAt          int64  `json:"UpdatedAt"`
}

// IdentityView 是一个已绑定的第三方身份。
type IdentityView struct {
	Provider      string `json:"Provider"`
	ProviderLogin string `json:"ProviderLogin"`
	ProviderEmail string `json:"ProviderEmail"`
	AvatarURL     string `json:"AvatarURL"`
	BoundAt       int64  `json:"BoundAt"`
}

// CreateUserInput 是创建用户的入参。
type CreateUserInput struct {
	Email         string
	Password      string
	Role          string
	Nickname      string
	CapacityBytes *int64 // nil → 用 settings:user.defaultCapacityBytes（D21）
}

// UpdateUserInput 是更新用户的入参（nil 表示该字段不变）。
type UpdateUserInput struct {
	Email              *string
	Nickname           *string
	AvatarURL          *string
	Homepage           *string
	Role               *string
	Status             *string
	CapacityBytes      *int64
	NewPassword        *string
	MustChangePassword *bool
}

// DeleteUserResult 是注销账号的返回。
type DeleteUserResult struct {
	DeletedUploads     int64 `json:"DeletedUploads"`
	FreedBytes         int64 `json:"FreedBytes"`
	RemoteDeleteFailed int64 `json:"RemoteDeleteFailed"`
}

// UserService 负责用户 CRUD 与登录认证。
type UserService struct {
	settings *settings.Service
	users    *repository.UserRepo
	tokens   *repository.TokenRepo
	attempts *repository.LoginAttemptRepo
	audit    *AuditService
	log      *slog.Logger
}

// NewUserService 构造。
func NewUserService(
	st *settings.Service,
	users *repository.UserRepo,
	tokens *repository.TokenRepo,
	attempts *repository.LoginAttemptRepo,
	audit *AuditService,
	log *slog.Logger,
) *UserService {
	return &UserService{settings: st, users: users, tokens: tokens, attempts: attempts, audit: audit, log: log}
}

// ---- 登录 ----

// LoginResult 是登录成功的结果。
type LoginResult struct {
	User *model.User
}

// Login 校验邮箱密码并处理登录限流与审计（D24 / D30 / D45）。
//
// 返回的错误码：
//   - 40101 凭据错误（**不区分**邮箱不存在与密码错误，防枚举）
//   - 40104 账号被禁用
//   - 42901 触发登录限流
func (s *UserService) Login(ctx context.Context, email, password, clientIP, userAgent string) (*LoginResult, error) {
	normalized := normalizeEmail(email)
	if normalized == "" || password == "" {
		// 空入参也走一次等时比较，避免与「邮箱不存在」在耗时上可区分
		auth.SpendDummyCompare(password)
		return nil, NewError(response.CodeBadCredentials, "")
	}

	// ---- 限流（按 Email + ClientIP 统计窗口内失败次数）----
	windowMinutes := s.settings.GetInt("security.loginWindowMinutes", 5)
	if windowMinutes <= 0 {
		windowMinutes = 5
	}
	maxAttempts := s.settings.GetInt("security.loginMaxAttempts", 5)
	if maxAttempts <= 0 {
		maxAttempts = 5
	}

	since := model.Now() - windowMinutes*60
	failures, err := s.attempts.CountFailuresSince(normalized, clientIP, since)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "检查登录频率失败", err)
	}
	if failures >= maxAttempts {
		// ⚠️ 被限流的请求**不写入 LoginAttempts**：
		// 否则攻击者只要持续重试，窗口里就永远有新的失败记录，账号会被无限期锁死。
		// 审计仍然记录，但走 OperationLogs（D45）。
		s.audit.Log(ctx, AuditEntry{
			Type:       model.LogTypeAuthFailed,
			Status:     model.LogStatusFailed,
			Username:   normalized,
			TargetType: "user",
			Detail:     map[string]any{"Reason": "rate_limited", "Failures": failures, "WindowMinutes": windowMinutes},
			Cause:      NewError(response.CodeTooMany, ""),
			ClientIP:   clientIP,
			UserAgent:  userAgent,
		})
		return nil, Errorf(response.CodeTooMany,
			"登录失败次数过多，请 %d 分钟后再试", windowMinutes)
	}

	// ---- 查账号 ----
	user, err := s.users.FindByEmail(normalized)
	if err != nil {
		if repository.IsNotFound(err) {
			// 防枚举：先烧掉一次等价耗时，再记一次失败
			auth.SpendDummyCompare(password)
			s.recordAttempt(normalized, clientIP, userAgent, false)
			s.audit.Log(ctx, AuditEntry{
				Type:       model.LogTypeAuthFailed,
				Status:     model.LogStatusFailed,
				Username:   normalized,
				TargetType: "user",
				Detail:     map[string]any{"Reason": "unknown_email"},
				Cause:      NewError(response.CodeBadCredentials, ""),
				ClientIP:   clientIP,
				UserAgent:  userAgent,
			})
			return nil, NewError(response.CodeBadCredentials, "")
		}
		return nil, Wrap(response.CodeInternal, "查询账号失败", err)
	}

	// ---- 校验密码 ----
	// 纯 OAuth 用户没有密码：同样烧掉一次等价耗时，避免耗时差异泄露账号类型。
	if user.PasswordHash == "" {
		auth.SpendDummyCompare(password)
		s.recordAttempt(normalized, clientIP, userAgent, false)
		s.audit.Log(ctx, AuditEntry{
			Type:       model.LogTypeAuthFailed,
			Status:     model.LogStatusFailed,
			UserUID:    user.UID,
			Username:   normalized,
			TargetType: "user",
			TargetUID:  user.UID,
			Detail:     map[string]any{"Reason": "no_password_set"},
			Cause:      NewError(response.CodeBadCredentials, ""),
			ClientIP:   clientIP,
			UserAgent:  userAgent,
		})
		return nil, NewError(response.CodeBadCredentials, "")
	}
	if !auth.VerifyPassword(user.PasswordHash, password) {
		s.recordAttempt(normalized, clientIP, userAgent, false)
		s.audit.Log(ctx, AuditEntry{
			Type:       model.LogTypeAuthFailed,
			Status:     model.LogStatusFailed,
			UserUID:    user.UID,
			Username:   normalized,
			TargetType: "user",
			TargetUID:  user.UID,
			Detail:     map[string]any{"Reason": "bad_password"},
			Cause:      NewError(response.CodeBadCredentials, ""),
			ClientIP:   clientIP,
			UserAgent:  userAgent,
		})
		return nil, NewError(response.CodeBadCredentials, "")
	}

	// ---- 账号状态 ----
	if !user.IsActive() {
		s.recordAttempt(normalized, clientIP, userAgent, false)
		s.audit.Log(ctx, AuditEntry{
			Type:       model.LogTypeAuthFailed,
			Status:     model.LogStatusFailed,
			UserUID:    user.UID,
			Username:   normalized,
			TargetType: "user",
			TargetUID:  user.UID,
			Detail:     map[string]any{"Reason": "disabled"},
			Cause:      NewError(response.CodeAccountDisabled, ""),
			ClientIP:   clientIP,
			UserAgent:  userAgent,
		})
		return nil, NewError(response.CodeAccountDisabled, "")
	}

	// ---- 成功 ----
	s.recordAttempt(normalized, clientIP, userAgent, true)
	if _, err := s.attempts.ClearFailures(normalized, clientIP); err != nil {
		s.log.Warn("清理登录失败记录失败", "email", normalized, "err", err)
	}

	now := model.Now()
	if err := s.users.UpdateFields(user.UID, map[string]any{"LastLoginAt": now}); err != nil {
		// 记录登录时间是尽力而为，不因它失败而拒绝登录
		s.log.Warn("更新最后登录时间失败", "uid", user.UID, "err", err)
	}
	user.LastLoginAt = now

	s.audit.Log(ctx, AuditEntry{
		Type:       model.LogTypeAuthLogin,
		Status:     model.LogStatusSuccess,
		UserUID:    user.UID,
		Username:   user.Email,
		TargetType: "user",
		TargetUID:  user.UID,
		ClientIP:   clientIP,
		UserAgent:  userAgent,
	})

	return &LoginResult{User: user}, nil
}

// recordAttempt 写一条登录尝试记录（成功与失败都写，D30）。
func (s *UserService) recordAttempt(email, clientIP, userAgent string, success bool) {
	row := &model.LoginAttempt{
		Email:     email,
		ClientIP:  clientIP,
		Success:   success,
		UserAgent: truncate(userAgent, 255),
		CreatedAt: model.Now(),
	}
	if err := s.attempts.Record(row); err != nil {
		// 记录失败不应阻断登录流程
		s.log.Warn("记录登录尝试失败", "email", email, "err", err)
	}
}

// ---- 查询 ----

// Get 返回某用户的视图（含实时统计）。
func (s *UserService) Get(uid string) (*UserView, error) {
	u, err := s.users.FindByUID(uid)
	if err != nil {
		if repository.IsNotFound(err) {
			return nil, NewError(response.CodeNotFound, "用户不存在")
		}
		return nil, Wrap(response.CodeInternal, "查询用户失败", err)
	}

	profile, err := s.profileOf(uid)
	if err != nil {
		return nil, err
	}

	imageCount, size, err := s.users.UploadStats(uid)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "统计图片数量失败", err)
	}
	albumCounts, err := s.users.CountAlbumsByUser([]string{uid})
	if err != nil {
		return nil, Wrap(response.CodeInternal, "统计相册数量失败", err)
	}

	v := toUserView(*u, profile, imageCount, albumCounts[uid])
	if v.UsedBytes == 0 {
		// UsedBytes 是权威列（D20/D72）；仅在为 0 而实际有图片时用聚合值兜底，
		// 避免历史数据未回填时前端显示 0。
		v.UsedBytes = size
	}
	return &v, nil
}

// List 分页查询用户。
func (s *UserService) List(f repository.UserListFilter) ([]UserView, int64, error) {
	if f.Page <= 0 {
		f.Page = DefaultPage
	}
	if f.PageSize <= 0 {
		f.PageSize = DefaultPageSize
	}
	if f.PageSize > MaxPageSize {
		f.PageSize = MaxPageSize
	}

	rows, total, err := s.users.List(f)
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "查询用户列表失败", err)
	}
	if len(rows) == 0 {
		return []UserView{}, total, nil
	}

	uids := make([]string, 0, len(rows))
	for _, r := range rows {
		uids = append(uids, r.UID)
	}

	profiles, err := s.users.FindProfiles(uids)
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "查询用户资料失败", err)
	}
	imageCounts, err := s.users.CountUploadsByUser(uids)
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "统计图片数量失败", err)
	}
	albumCounts, err := s.users.CountAlbumsByUser(uids)
	if err != nil {
		return nil, 0, Wrap(response.CodeInternal, "统计相册数量失败", err)
	}

	out := make([]UserView, 0, len(rows))
	for _, r := range rows {
		p, ok := profiles[r.UID]
		var profilePtr *model.UserProfile
		if ok {
			cp := p
			profilePtr = &cp
		}
		// 列表页不为每个用户跑 SUM(Size)（避免 N+1），直接展示 Users.UsedBytes
		// —— 它是权威列，由上传/删除流程维护（D20/D72）。
		out = append(out, toUserView(r, profilePtr, imageCounts[r.UID], albumCounts[r.UID]))
	}
	return out, total, nil
}

// Identities 返回某用户已绑定的第三方身份与是否设有密码。
func (s *UserService) Identities(uid string) ([]IdentityView, bool, error) {
	u, err := s.users.FindByUID(uid)
	if err != nil {
		if repository.IsNotFound(err) {
			return nil, false, NewError(response.CodeNotFound, "用户不存在")
		}
		return nil, false, Wrap(response.CodeInternal, "查询用户失败", err)
	}

	rows, err := s.users.ListIdentities(uid)
	if err != nil {
		return nil, false, Wrap(response.CodeInternal, "查询绑定失败", err)
	}
	out := make([]IdentityView, 0, len(rows))
	for _, r := range rows {
		out = append(out, IdentityView{
			Provider:      r.Provider,
			ProviderLogin: r.ProviderLogin,
			ProviderEmail: r.ProviderEmail,
			AvatarURL:     r.AvatarURL,
			BoundAt:       r.CreatedAt,
		})
	}
	return out, u.PasswordHash != "", nil
}

// ---- 创建 / 更新 / 删除 ----

// Create 由管理员创建用户（D25：这是唯一的建号入口）。
func (s *UserService) Create(ctx context.Context, in CreateUserInput, byUID, clientIP, userAgent string) (*UserView, error) {
	email := normalizeEmail(in.Email)
	if err := validateEmail(email); err != nil {
		return nil, err
	}
	if err := validatePassword(in.Password); err != nil {
		return nil, err
	}

	role := strings.TrimSpace(in.Role)
	if role == "" {
		role = model.UserRoleUser
	}
	if role != model.UserRoleAdmin && role != model.UserRoleUser {
		return nil, Errorf(response.CodeInvalidParam, "角色只能是 admin 或 user")
	}

	// 邮箱唯一性：靠数据库唯一索引兜底，这里先查一次给出友好提示
	if existing, err := s.users.FindByEmail(email); err == nil && existing != nil {
		return nil, Errorf(response.CodeConflict, "邮箱已存在：%s", email)
	} else if err != nil && !repository.IsNotFound(err) {
		return nil, Wrap(response.CodeInternal, "检查邮箱失败", err)
	}

	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "生成密码哈希失败", err)
	}

	now := model.Now()
	u := &model.User{
		UID:           id.User(),
		Email:         email,
		PasswordHash:  hash,
		Role:          role,
		Status:        s.defaultStatus(),
		CapacityBytes: s.resolveCapacity(in.CapacityBytes),
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	nickname := strings.TrimSpace(in.Nickname)
	if nickname == "" {
		nickname = defaultNickname(email)
	}
	profile := &model.UserProfile{
		UserUID:   u.UID,
		Nickname:  truncate(nickname, 128),
		Locale:    "zh-CN",
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.users.CreateWithProfile(u, profile); err != nil {
		// 唯一索引冲突 → 并发创建同一邮箱
		if isDuplicateKey(err) {
			return nil, Errorf(response.CodeConflict, "邮箱已存在：%s", email)
		}
		return nil, Wrap(response.CodeInternal, "创建用户失败", err)
	}

	s.auditAs(ctx, byUID, AuditEntry{
		Type:       model.LogTypeUserCreate,
		Status:     model.LogStatusSuccess,
		TargetType: "user",
		TargetUID:  u.UID,
		Detail: map[string]any{
			"Email": email, "Role": role, "Status": u.Status, "CapacityBytes": u.CapacityBytes,
		},
		ClientIP:  clientIP,
		UserAgent: userAgent,
	})

	return &UserView{
		UID: u.UID, Email: u.Email, Role: u.Role, Status: u.Status,
		MustChangePassword: u.MustChangePassword,
		Nickname:           profile.Nickname,
		CapacityBytes:      u.CapacityBytes, UsedBytes: u.UsedBytes,
		HasPassword: true,
		LastLoginAt: u.LastLoginAt,
		CreatedAt:   u.CreatedAt, UpdatedAt: u.UpdatedAt,
	}, nil
}

// Update 由管理员更新用户（含配额、状态、角色、密码重置）。
//
// 返回的 hint 非空时，handler 应把它作为响应 Message（例如配额低于已用量）。
func (s *UserService) Update(ctx context.Context, targetUID string, in UpdateUserInput, byUID, clientIP, userAgent string) (*UserView, string, error) {
	u, err := s.users.FindByUID(targetUID)
	if err != nil {
		if repository.IsNotFound(err) {
			return nil, "", NewError(response.CodeNotFound, "用户不存在")
		}
		return nil, "", Wrap(response.CodeInternal, "查询用户失败", err)
	}

	isSelf := targetUID == byUID
	fields := map[string]any{}
	changes := map[string]any{}
	var hint string

	// ---- 邮箱 ----
	if in.Email != nil {
		email := normalizeEmail(*in.Email)
		if err := validateEmail(email); err != nil {
			return nil, "", err
		}
		if email != u.Email {
			if existing, err := s.users.FindByEmail(email); err == nil && existing != nil {
				return nil, "", Errorf(response.CodeConflict, "邮箱已存在：%s", email)
			} else if err != nil && !repository.IsNotFound(err) {
				return nil, "", Wrap(response.CodeInternal, "检查邮箱失败", err)
			}
			fields["Email"] = email
			changes["Email"] = map[string]any{"From": u.Email, "To": email}
		}
	}

	// ---- 角色 ----
	if in.Role != nil {
		role := strings.TrimSpace(*in.Role)
		if role != model.UserRoleAdmin && role != model.UserRoleUser {
			return nil, "", Errorf(response.CodeInvalidParam, "角色只能是 admin 或 user")
		}
		if role != u.Role {
			if isSelf {
				// 不允许自我降权/提权（防误操作把自己锁在门外）
				return nil, "", NewError(response.CodeForbidden, "不可修改自己的角色")
			}
			if u.Role == model.UserRoleAdmin && role != model.UserRoleAdmin {
				if err := s.ensureNotLastAdmin(); err != nil {
					return nil, "", err
				}
			}
			fields["Role"] = role
			changes["Role"] = map[string]any{"From": u.Role, "To": role}
		}
	}

	// ---- 状态 ----
	if in.Status != nil {
		status := strings.TrimSpace(*in.Status)
		if status != model.UserStatusActive && status != model.UserStatusDisabled {
			return nil, "", Errorf(response.CodeInvalidParam, "状态只能是 active 或 disabled")
		}
		if status != u.Status {
			if isSelf {
				return nil, "", NewError(response.CodeForbidden, "不可修改自己的状态")
			}
			if status == model.UserStatusDisabled {
				if err := s.ensureNotLastActiveAdmin(u); err != nil {
					return nil, "", err
				}
			}
			fields["Status"] = status
			changes["Status"] = map[string]any{"From": u.Status, "To": status}
		}
	}

	// ---- 配额 ----
	if in.CapacityBytes != nil {
		if *in.CapacityBytes < 0 {
			return nil, "", Errorf(response.CodeInvalidParam, "配额不能为负数")
		}
		if *in.CapacityBytes != u.CapacityBytes {
			fields["CapacityBytes"] = *in.CapacityBytes
			changes["CapacityBytes"] = map[string]any{"From": u.CapacityBytes, "To": *in.CapacityBytes}
			// 下调到低于已用：允许（不追溯删图），但要提示
			if *in.CapacityBytes > 0 && u.UsedBytes > *in.CapacityBytes {
				hint = "配额已低于该用户当前已用容量，该用户将无法继续上传"
			}
		}
	}

	// ---- 密码重置 ----
	revokeSessions := false
	if in.NewPassword != nil && *in.NewPassword != "" {
		if err := validatePassword(*in.NewPassword); err != nil {
			return nil, "", err
		}
		hash, err := auth.HashPassword(*in.NewPassword)
		if err != nil {
			return nil, "", Wrap(response.CodeInternal, "生成密码哈希失败", err)
		}
		fields["PasswordHash"] = hash
		revokeSessions = true
		changes["PasswordReset"] = true
	}

	// ---- 强制改密标志 ----
	if in.MustChangePassword != nil {
		if *in.MustChangePassword != u.MustChangePassword {
			fields["MustChangePassword"] = *in.MustChangePassword
			changes["MustChangePassword"] = *in.MustChangePassword
		}
	} else if revokeSessions && !u.MustChangePassword {
		// 管理员重置密码 → 强制对方首次登录后改密（见 API.md §1.1）
		fields["MustChangePassword"] = true
		changes["MustChangePassword"] = true
	}

	if len(fields) > 0 {
		if err := s.users.UpdateFields(targetUID, fields); err != nil {
			if isDuplicateKey(err) {
				return nil, "", NewError(response.CodeConflict, "邮箱已存在")
			}
			return nil, "", Wrap(response.CodeInternal, "更新用户失败", err)
		}
	}

	// ---- Profile ----
	if in.Nickname != nil || in.AvatarURL != nil || in.Homepage != nil {
		profile, err := s.profileOf(targetUID)
		if err != nil {
			return nil, "", err
		}
		if profile == nil {
			profile = &model.UserProfile{UserUID: targetUID, Locale: "zh-CN", CreatedAt: model.Now()}
		}
		if in.Nickname != nil {
			profile.Nickname = truncate(strings.TrimSpace(*in.Nickname), 128)
		}
		if in.AvatarURL != nil {
			profile.AvatarURL = truncate(strings.TrimSpace(*in.AvatarURL), 512)
		}
		if in.Homepage != nil {
			profile.Homepage = truncate(strings.TrimSpace(*in.Homepage), 512)
		}
		if err := s.users.SaveProfile(profile); err != nil {
			return nil, "", Wrap(response.CodeInternal, "更新用户资料失败", err)
		}
	}

	if revokeSessions {
		if _, err := s.tokens.RevokeAllRefreshForUser(targetUID, model.Now()); err != nil {
			s.log.Warn("重置密码后吊销会话失败", "uid", targetUID, "err", err)
		}
	}

	if len(changes) > 0 {
		s.auditAs(ctx, byUID, AuditEntry{
			Type:       model.LogTypeUserUpdate,
			Status:     model.LogStatusSuccess,
			TargetType: "user",
			TargetUID:  targetUID,
			// 注意：只记字段名的变化，**不记录密码内容**（密钥类一律脱敏）
			Detail:    map[string]any{"Changes": changes},
			ClientIP:  clientIP,
			UserAgent: userAgent,
		})
	}

	view, err := s.Get(targetUID)
	if err != nil {
		return nil, "", err
	}
	return view, hint, nil
}

// ResetPassword 重置密码并返回一次性明文（D32 同样的语义：强制首次改密）。
func (s *UserService) ResetPassword(ctx context.Context, targetUID, byUID, clientIP, userAgent string) (string, error) {
	if _, err := s.users.FindByUID(targetUID); err != nil {
		if repository.IsNotFound(err) {
			return "", NewError(response.CodeNotFound, "用户不存在")
		}
		return "", Wrap(response.CodeInternal, "查询用户失败", err)
	}

	password, err := auth.GenerateRandomPassword(16)
	if err != nil {
		return "", Wrap(response.CodeInternal, "生成随机密码失败", err)
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return "", Wrap(response.CodeInternal, "生成密码哈希失败", err)
	}

	if err := s.users.UpdateFields(targetUID, map[string]any{
		"PasswordHash":       hash,
		"MustChangePassword": true,
	}); err != nil {
		return "", Wrap(response.CodeInternal, "重置密码失败", err)
	}

	// 重置密码后原有会话必须失效
	if _, err := s.tokens.RevokeAllRefreshForUser(targetUID, model.Now()); err != nil {
		s.log.Warn("重置密码后吊销会话失败", "uid", targetUID, "err", err)
	}

	s.auditAs(ctx, byUID, AuditEntry{
		Type:       model.LogTypeUserUpdate,
		Status:     model.LogStatusSuccess,
		TargetType: "user",
		TargetUID:  targetUID,
		Detail:     map[string]any{"Action": "reset_password", "MustChangePassword": true},
		ClientIP:   clientIP,
		UserAgent:  userAgent,
	})
	return password, nil
}

// ChangePassword 用户修改自己的密码（D24）。
//
// 语义：
//   - `MustChangePassword = true`（首启引导 / 管理员重置）时 `oldPassword` 可省略
//   - 否则必须提供并校验旧密码
//   - 成功后清除 `MustChangePassword` 并**吊销该用户全部 refresh token**（强制重新登录）
func (s *UserService) ChangePassword(ctx context.Context, uid, oldPassword, newPassword, clientIP, userAgent string) error {
	u, err := s.users.FindByUID(uid)
	if err != nil {
		if repository.IsNotFound(err) {
			return NewError(response.CodeNotFound, "用户不存在")
		}
		return Wrap(response.CodeInternal, "查询用户失败", err)
	}

	if err := validatePassword(newPassword); err != nil {
		return err
	}

	// 未处于「强制改密」状态时，必须验证旧密码
	if !u.MustChangePassword {
		if oldPassword == "" {
			return NewError(response.CodeInvalidParam, "请输入当前密码")
		}
		if u.PasswordHash == "" || !auth.VerifyPassword(u.PasswordHash, oldPassword) {
			return NewError(response.CodeBadCredentials, "当前密码不正确")
		}
	} else if oldPassword != "" && u.PasswordHash != "" && !auth.VerifyPassword(u.PasswordHash, oldPassword) {
		// 强制改密时若填了旧密码，也顺手校验，避免误操作
		return NewError(response.CodeBadCredentials, "当前密码不正确")
	}

	if u.PasswordHash != "" && auth.VerifyPassword(u.PasswordHash, newPassword) {
		return NewError(response.CodeInvalidParam, "新密码不能与当前密码相同")
	}

	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return Wrap(response.CodeInternal, "生成密码哈希失败", err)
	}

	if err := s.users.UpdateFields(uid, map[string]any{
		"PasswordHash":       hash,
		"MustChangePassword": false,
	}); err != nil {
		return Wrap(response.CodeInternal, "更新密码失败", err)
	}

	if _, err := s.tokens.RevokeAllRefreshForUser(uid, model.Now()); err != nil {
		s.log.Warn("改密后吊销会话失败", "uid", uid, "err", err)
	}

	s.auditAs(ctx, uid, AuditEntry{
		Type:       model.LogTypeUserUpdate,
		Status:     model.LogStatusSuccess,
		TargetType: "user",
		TargetUID:  uid,
		Detail:     map[string]any{"Action": "change_password", "SessionsRevoked": true},
		ClientIP:   clientIP,
		UserAgent:  userAgent,
	})
	return nil
}

// Delete 注销账号（D34 / D46）。
//
// ⚠️ 已知限制：本函数完成**数据库侧**的硬删除（含该用户的图片记录），
// 但「同时删除图床上的远端文件」（D47）依赖 picgo-agent，而 agent 集成在 W4/W5。
// 因此当前实现**不会**触碰远端文件，`RemoteDeleteFailed` 恒为 0（表示「未尝试」而非「失败」），
// 并在审计日志的 Detail 里显式标注 `RemoteDeletion: "not_attempted"`。
func (s *UserService) Delete(ctx context.Context, targetUID, byUID, clientIP, userAgent string) (*DeleteUserResult, error) {
	if targetUID == byUID {
		return nil, NewError(response.CodeForbidden, "不可删除自己的账号")
	}

	u, err := s.users.FindByUID(targetUID)
	if err != nil {
		if repository.IsNotFound(err) {
			return nil, NewError(response.CodeNotFound, "用户不存在")
		}
		return nil, Wrap(response.CodeInternal, "查询用户失败", err)
	}
	if u.IsAdmin() {
		if err := s.ensureNotLastAdmin(); err != nil {
			return nil, err
		}
	}

	stats, err := s.users.Purge(targetUID)
	if err != nil {
		return nil, Wrap(response.CodeInternal, "删除用户失败", err)
	}

	s.auditAs(ctx, byUID, AuditEntry{
		Type:       model.LogTypeUserDelete,
		Status:     model.LogStatusSuccess,
		TargetType: "user",
		TargetUID:  targetUID,
		Detail: map[string]any{
			"Email":          u.Email,
			"DeletedUploads": stats.Uploads,
			"FreedBytes":     stats.FreedBytes,
			"Albums":         stats.Albums,
			"Identities":     stats.Identities,
			"Tokens":         stats.Tokens,
			"RemoteDeletion": "not_attempted (agent 集成在 W4/W5)",
		},
		ClientIP:  clientIP,
		UserAgent: userAgent,
	})

	return &DeleteUserResult{
		DeletedUploads:     stats.Uploads,
		FreedBytes:         stats.FreedBytes,
		RemoteDeleteFailed: 0,
	}, nil
}

// ---- 内部助手 ----

// defaultStatus 返回新用户的默认状态（settings:user.defaultStatus，D21）。
func (s *UserService) defaultStatus() string {
	status := strings.TrimSpace(s.settings.GetString("user.defaultStatus"))
	if status != model.UserStatusActive && status != model.UserStatusDisabled {
		return model.UserStatusActive
	}
	return status
}

// resolveCapacity 计算新用户的配额（D21）：
//
//	请求显式指定 > user.unlimitedCapacity 为真则 0 > user.defaultCapacityBytes
func (s *UserService) resolveCapacity(requested *int64) int64 {
	if requested != nil {
		if *requested < 0 {
			return 0
		}
		return *requested
	}
	if s.settings.GetBool("user.unlimitedCapacity", false) {
		return 0
	}
	capacity := s.settings.GetInt("user.defaultCapacityBytes", 0)
	if capacity < 0 {
		return 0
	}
	return capacity
}

// profileOf 取 Profile；不存在返回 (nil, nil)（Profile 是可选展示数据）。
func (s *UserService) profileOf(uid string) (*model.UserProfile, error) {
	p, err := s.users.FindProfile(uid)
	if err != nil {
		if repository.IsNotFound(err) {
			return nil, nil
		}
		return nil, Wrap(response.CodeInternal, "查询用户资料失败", err)
	}
	return p, nil
}

// ensureNotLastAdmin 阻止「把最后一个管理员删除或降权」。
func (s *UserService) ensureNotLastAdmin() error {
	n, err := s.users.CountAdmins()
	if err != nil {
		return Wrap(response.CodeInternal, "统计管理员数量失败", err)
	}
	if n <= 1 {
		return NewError(response.CodeConflict, "系统必须保留至少一个管理员")
	}
	return nil
}

// ensureNotLastActiveAdmin 阻止「禁用最后一个可用管理员」。
func (s *UserService) ensureNotLastActiveAdmin(target *model.User) error {
	if target.Role != model.UserRoleAdmin || target.Status != model.UserStatusActive {
		return nil
	}
	n, err := s.users.CountActiveAdmins()
	if err != nil {
		return Wrap(response.CodeInternal, "统计管理员数量失败", err)
	}
	if n <= 1 {
		return NewError(response.CodeConflict, "系统必须保留至少一个可用的管理员")
	}
	return nil
}

// auditAs 写审计日志，并顺手补上操作者的可读名称（便于后台按用户名搜索）。
func (s *UserService) auditAs(ctx context.Context, operatorUID string, e AuditEntry) {
	if e.Username == "" && operatorUID != "" {
		if op, err := s.users.FindByUID(operatorUID); err == nil {
			e.Username = op.Email
		}
	}
	s.audit.Log(ctx, e)
}

func toUserView(u model.User, p *model.UserProfile, imageCount, albumCount int64) UserView {
	v := UserView{
		UID:                u.UID,
		Email:              u.Email,
		Role:               u.Role,
		Status:             u.Status,
		MustChangePassword: u.MustChangePassword,
		CapacityBytes:      u.CapacityBytes,
		UsedBytes:          u.UsedBytes,
		ImageCount:         imageCount,
		AlbumCount:         albumCount,
		HasPassword:        u.PasswordHash != "",
		LastLoginAt:        u.LastLoginAt,
		CreatedAt:          u.CreatedAt,
		UpdatedAt:          u.UpdatedAt,
	}
	if p != nil {
		v.Nickname = p.Nickname
		v.AvatarURL = p.AvatarURL
		v.Homepage = p.Homepage
	}
	return v
}

// normalizeEmail 规范邮箱：去空白 + 转小写（与 middleware.NormalizeEmail 同义，
// 这里重复实现是为了让 service 不依赖 middleware）。
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// validateEmail 做轻量邮箱校验（不做 RFC 全量解析，够用且不引入依赖）。
func validateEmail(email string) error {
	if email == "" {
		return NewError(response.CodeInvalidParam, "邮箱不能为空")
	}
	if len(email) > 255 {
		return NewError(response.CodeInvalidParam, "邮箱过长")
	}
	at := strings.LastIndex(email, "@")
	if at <= 0 || at == len(email)-1 {
		return NewError(response.CodeInvalidParam, "邮箱格式不正确")
	}
	domain := email[at+1:]
	if !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return NewError(response.CodeInvalidParam, "邮箱格式不正确")
	}
	if strings.ContainsAny(email, " \t\r\n") {
		return NewError(response.CodeInvalidParam, "邮箱不能包含空白字符")
	}
	return nil
}

// validatePassword 校验密码长度（含 bcrypt 的 72 字节上限）。
func validatePassword(pw string) error {
	if pw == "" {
		return NewError(response.CodeInvalidParam, "密码不能为空")
	}
	n := len([]byte(pw))
	if n < MinPasswordBytes {
		return Errorf(response.CodeInvalidParam, "密码至少 %d 个字符", MinPasswordBytes)
	}
	if n > MaxPasswordBytes {
		return Errorf(response.CodeInvalidParam, "密码过长（最多 %d 字节）", MaxPasswordBytes)
	}
	return nil
}

// defaultNickname 从邮箱前缀推导默认昵称。
func defaultNickname(email string) string {
	if i := strings.Index(email, "@"); i > 0 {
		return email[:i]
	}
	return email
}

// isDuplicateKey 判断是否为唯一约束冲突。
//
// 两个方言的错误文本不同，因此做宽松匹配；这只是为了给出更友好的提示，
// 真正的唯一性保证来自数据库索引。
func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "constraint failed")
}
