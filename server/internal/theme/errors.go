package theme

import "errors"

// 主题域的业务错误。
//
// handler 层用 errors.Is 判断后映射到统一错误码：
//
//	ErrThemeNotFound / ErrNotFound      → 40401
//	ErrThemeActive / ErrBuiltinTheme    → 40901
//	install 系列（zip 校验失败）          → 40001
//	ErrSettingUnknown / ErrSettingType  → 40001
//	其它                                  → 50005（主题操作失败）
var (
	// ErrNotFound 主题不存在。
	ErrNotFound = errors.New("主题不存在")

	// ErrThemeActive 主题正在使用中（不能卸载当前启用的主题）。
	ErrThemeActive = errors.New("主题正在使用中，请先切换到其他主题")

	// ErrBuiltinTheme 内置主题不可卸载（default 是兜底锚点）。
	ErrBuiltinTheme = errors.New("内置主题不可卸载")

	// ErrSettingUnknown 该主题未声明此配置项（防脏写，D95）。
	ErrSettingUnknown = errors.New("该主题未声明此配置项")

	// ErrSettingType 配置项类型不匹配。
	ErrSettingType = errors.New("配置项类型不匹配")

	// ErrSettingsInUse 不允许清理当前启用主题的配置。
	ErrSettingsInUse = errors.New("不能清理当前启用主题的配置")

	// ---- zip 安装（D96 的 9 条校验）----

	// ErrPackageTooLarge 压缩包超过 theme.maxPackageBytes。
	ErrPackageTooLarge = errors.New("主题包超过大小限制")

	// ErrArchiveInvalid 不是合法的 zip。
	ErrArchiveInvalid = errors.New("主题包不是合法的 zip 文件")

	// ErrZipSlip 包含越界路径（`..` 或绝对路径）。
	ErrZipSlip = errors.New("主题包包含非法路径")

	// ErrZipSymlink 包含符号链接 entry。
	ErrZipSymlink = errors.New("主题包包含符号链接，已拒绝")

	// ErrExtractTooLarge 解压后总体积超过 theme.maxExtractBytes。
	ErrExtractTooLarge = errors.New("主题包解压后超过体积限制")

	// ErrSingleFileTooLarge 单个文件超过 theme.maxFileBytes。
	ErrSingleFileTooLarge = errors.New("主题包内单个文件超过体积限制")

	// ErrTooManyFiles 文件数超过 theme.maxFiles。
	ErrTooManyFiles = errors.New("主题包内文件数量超过限制")

	// ErrThemeExists 目标目录已存在且未指定覆盖。
	ErrThemeExists = errors.New("同名主题已存在（如需替换请勾选覆盖）")

	// ---- Git 安装（D100）----

	// ErrGitInvalid Git 地址不合法（仅允许 https，且不得携带凭据）。
	ErrGitInvalid = errors.New("Git 地址不合法")

	// ErrGitClone git clone 失败（网络 / 凭据 / 非 Git 仓库）。
	ErrGitClone = errors.New("从 Git 拉取失败")

	// ErrScrollUnsafe 预留：目录遍历失败。
	ErrScanFailed = errors.New("扫描主题目录失败")
)
