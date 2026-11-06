package service

import (
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/tools"
)

// SettingsStore 是 service 依赖的系统配置存储抽象。
type SettingsStore interface {
	Load() (*model.Settings, error)
	Save(*model.Settings) error
}

// SettingsService 管理可持久化的系统配置：启动时用默认值补齐文件缺失字段，
// 更新时做校验并原子落盘，同时供调度器 / 构建服务 / 认证服务读取运行时开关。
type SettingsService struct {
	mu      sync.RWMutex
	store   SettingsStore
	current model.Settings
}

// NewSettingsService 载入配置；文件不存在时用默认值初始化并落盘。
func NewSettingsService(store SettingsStore, defaults model.Settings) (*SettingsService, error) {
	s := &SettingsService{store: store}
	loaded, err := store.Load()
	if err != nil {
		return nil, err
	}
	if loaded == nil {
		s.current = normalizeSettings(defaults)
	} else {
		s.current = mergeSettings(normalizeSettings(defaults), *loaded)
	}
	// 兼容旧版明文 auth_password：启动时一次性转为 bcrypt，并清空明文。
	if err = migratePasswordHash(&s.current); err != nil {
		return nil, err
	}
	if err = store.Save(&s.current); err != nil {
		return nil, err
	}
	return s, nil
}

// migratePasswordHash 把旧配置中的明文密码转成 bcrypt hash。
// 已经存在 hash 时优先使用 hash，并清除可能残留的明文。
func migratePasswordHash(s *model.Settings) error {
	if s.AuthPasswordHash != "" {
		s.AuthPassword = ""
		return nil
	}
	plain := strings.TrimSpace(s.AuthPassword)
	if plain == "" {
		return nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(plain), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash auth password: %w", err)
	}
	s.AuthPasswordHash = string(hash)
	s.AuthPassword = ""
	return nil
}

// Current 返回配置副本（调用方不会互相影响）。
func (s *SettingsService) Current() model.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// Update 校验并持久化配置。敏感字段（登录密码、SMTP 密码）为空时沿用旧值。
func (s *SettingsService) Update(in model.Settings) (model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 密码只接受明文输入并立即转为 bcrypt；空值表示沿用旧 hash。
	// 不接受客户端直接提交 hash，避免绕过密码策略或无意覆盖。
	if strings.TrimSpace(in.AuthPassword) != "" {
		if err := validatePasswordStrength(in.AuthPassword); err != nil {
			return model.Settings{}, err
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(in.AuthPassword), bcrypt.DefaultCost)
		if err != nil {
			return model.Settings{}, pkg.ErrBadRequest("管理员密码不符合要求: %v", err)
		}
		in.AuthPasswordHash = string(hash)
	} else {
		in.AuthPasswordHash = s.current.AuthPasswordHash
	}
	in.AuthPassword = ""
	if strings.TrimSpace(in.Email.Password) == "" {
		in.Email.Password = s.current.Email.Password
	}
	// SSH 服务器密码：按 ID 回填，留空表示不修改。
	oldServers := make(map[string]model.SshServer, len(s.current.SshServers))
	for _, srv := range s.current.SshServers {
		oldServers[srv.ID] = srv
	}
	for i := range in.SshServers {
		in.SshServers[i].Password = strings.TrimSpace(in.SshServers[i].Password)
		if in.SshServers[i].Password == "" {
			if old, ok := oldServers[in.SshServers[i].ID]; ok {
				in.SshServers[i].Password = old.Password
			}
		}
	}
	// Webhook 通知 URL：含 access_token，GET 不回传；留空表示沿用旧值。
	// 需要清空时先禁用 Enabled（禁用后 URL 不会参与通知）。
	if strings.TrimSpace(in.WebhookNotify.URL) == "" {
		in.WebhookNotify.URL = s.current.WebhookNotify.URL
	}

	// 全局密钥变量在 API 中不回传原文：前端留空表示沿用旧值。
	// 按 key 合并，避免保存一次系统配置就把所有密钥清空。
	oldEnv := make(map[string]string, len(s.current.GlobalEnv))
	for _, item := range s.current.GlobalEnv {
		if item.Key != "" {
			oldEnv[strings.ToUpper(item.Key)] = item.Value
		}
	}
	for i := range in.GlobalEnv {
		item := &in.GlobalEnv[i]
		if strings.TrimSpace(item.Value) != "" {
			continue
		}
		if !item.Secret && !model.IsSecretEnvName(item.Key) {
			continue
		}
		if old, ok := oldEnv[strings.ToUpper(item.Key)]; ok {
			item.Value = old
		}
	}

	if err := validateSettings(&in); err != nil {
		return model.Settings{}, err
	}
	// 启动期只读字段以当前进程配置为准，禁止界面覆盖。
	in.Workers = s.current.Workers
	in.QueueSize = s.current.QueueSize
	in.WorkspaceTTLMinutes = s.current.WorkspaceTTLMinutes
	in.CleanIntervalMinutes = s.current.CleanIntervalMinutes

	in.UpdatedAt = time.Now().UTC()
	if err := s.store.Save(&in); err != nil {
		return model.Settings{}, err
	}
	s.current = in
	return in, nil
}

// Reload 丢弃内存配置，重新从文件读取（「读取设置」动作）。
func (s *SettingsService) Reload() (model.Settings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	loaded, err := s.store.Load()
	if err != nil {
		return model.Settings{}, err
	}
	if loaded == nil {
		return s.current, nil
	}
	s.current = mergeSettings(s.current, *loaded)
	if err = migratePasswordHash(&s.current); err != nil {
		return model.Settings{}, err
	}
	// 把旧明文的迁移结果写回磁盘；否则每次 reload 都会重复 hash。
	if err = s.store.Save(&s.current); err != nil {
		return model.Settings{}, err
	}
	return s.current, nil
}

// SetQuiet 切换静默模式（准备关机），仅影响调度器是否接受新构建。
func (s *SettingsService) SetQuiet(quiet bool) model.Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current.QuietMode = quiet
	s.current.UpdatedAt = time.Now().UTC()
	_ = s.store.Save(&s.current)
	return s.current
}

// normalizeSettings 填充空值默认，保证下游拿到可用配置。
func normalizeSettings(in model.Settings) model.Settings {
	if strings.TrimSpace(in.DisplayName) == "" {
		in.DisplayName = "CI Platform"
	}
	if strings.TrimSpace(in.DefaultBranch) == "" {
		in.DefaultBranch = "main"
	}
	if strings.TrimSpace(in.AuthUser) == "" {
		in.AuthUser = "admin"
	}
	if strings.TrimSpace(in.Shell) == "" {
		in.Shell = model.DefaultShell()
	}
	if in.Workers <= 0 {
		in.Workers = 3
	}
	if in.QueueSize <= 0 {
		in.QueueSize = 100
	}
	if in.WorkspaceTTLMinutes <= 0 {
		in.WorkspaceTTLMinutes = 60
	}
	if in.CleanIntervalMinutes <= 0 {
		in.CleanIntervalMinutes = 5
	}
	if in.ToolCategories == nil {
		in.ToolCategories = []model.ToolCategory{}
	}
	if in.GlobalEnv == nil {
		in.GlobalEnv = []model.EnvVar{}
	}
	if in.Email.SMTPPort <= 0 {
		in.Email.SMTPPort = 25
	}
	return in
}

// mergeSettings 用旧值补齐新加载配置中的空/零字段，避免新增字段丢失默认。
func mergeSettings(base, loaded model.Settings) model.Settings {
	if strings.TrimSpace(loaded.DisplayName) == "" {
		loaded.DisplayName = base.DisplayName
	}
	if strings.TrimSpace(loaded.DefaultBranch) == "" {
		loaded.DefaultBranch = base.DefaultBranch
	}
	if strings.TrimSpace(loaded.AuthUser) == "" {
		loaded.AuthUser = base.AuthUser
	}
	if loaded.AuthPasswordHash == "" && strings.TrimSpace(loaded.AuthPassword) == "" {
		loaded.AuthPasswordHash = base.AuthPasswordHash
	}
	if strings.TrimSpace(loaded.Shell) == "" {
		loaded.Shell = base.Shell
	}
	if loaded.Workers <= 0 {
		loaded.Workers = base.Workers
	}
	if loaded.QueueSize <= 0 {
		loaded.QueueSize = base.QueueSize
	}
	if loaded.WorkspaceTTLMinutes <= 0 {
		loaded.WorkspaceTTLMinutes = base.WorkspaceTTLMinutes
	}
	if loaded.CleanIntervalMinutes <= 0 {
		loaded.CleanIntervalMinutes = base.CleanIntervalMinutes
	}
	if loaded.ArtifactMaxTotalMB <= 0 {
		loaded.ArtifactMaxTotalMB = base.ArtifactMaxTotalMB
	}
	if loaded.ArtifactMaxFiles <= 0 {
		loaded.ArtifactMaxFiles = base.ArtifactMaxFiles
	}
	if loaded.LogMaxMB <= 0 {
		loaded.LogMaxMB = base.LogMaxMB
	}
	if loaded.ToolCategories == nil {
		loaded.ToolCategories = base.ToolCategories
	} else {
		loaded.ToolCategories = mergeToolCategories(base.ToolCategories, loaded.ToolCategories)
	}
	if loaded.SshServers == nil {
		loaded.SshServers = base.SshServers
	}
	if loaded.GlobalEnv == nil {
		loaded.GlobalEnv = base.GlobalEnv
	}
	if loaded.Maven == (model.MavenSettings{}) {
		loaded.Maven = base.Maven
	}
	if loaded.Email.SMTPPort <= 0 {
		loaded.Email.SMTPPort = base.Email.SMTPPort
	}
	return loaded
}

// mergeToolCategories 确保内置工具类别始终存在（新增默认类别可自动补齐），
// 同时保留用户对已有类别安装项的修改。
func mergeToolCategories(defaults, loaded []model.ToolCategory) []model.ToolCategory {
	existing := make(map[string]bool, len(loaded))
	for _, c := range loaded {
		existing[c.ID] = true
	}
	out := loaded
	for _, c := range defaults {
		if !existing[c.ID] {
			out = append(out, c)
		}
	}
	return out
}

// validateSettings 只允许业务规则出现在 service。
func validateSettings(in *model.Settings) error {
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.DefaultImage = strings.TrimSpace(in.DefaultImage)
	in.DefaultBranch = strings.TrimSpace(in.DefaultBranch)
	in.AuthUser = strings.TrimSpace(in.AuthUser)
	in.JobNameRegex = strings.TrimSpace(in.JobNameRegex)
	in.Shell = strings.TrimSpace(in.Shell)
	in.JenkinsURL = strings.TrimSpace(in.JenkinsURL)
	// GitLab 地址：允许用户粘贴带 /api/v4 的地址，统一剥掉，避免拼成 /api/v4/api/v4。
	in.GitlabBaseURL = strings.TrimRight(strings.TrimSpace(in.GitlabBaseURL), "/")
	in.GitlabBaseURL = strings.TrimSuffix(in.GitlabBaseURL, "/api/v4")
	in.AdminEmail = strings.TrimSpace(in.AdminEmail)

	if in.DefaultBranch == "" {
		in.DefaultBranch = "main"
	}
	if in.Shell == "" {
		in.Shell = model.DefaultShell()
	}
	if in.AuthEnabled && in.AuthUser == "" {
		return pkg.ErrBadRequest("开启登录校验时用户名不能为空")
	}
	if in.AuthEnabled && strings.TrimSpace(in.AuthPasswordHash) == "" {
		return pkg.ErrBadRequest("开启登录校验时必须设置密码")
	}
	if in.JobNameRegex != "" {
		if _, err := regexp.Compile(in.JobNameRegex); err != nil {
			return pkg.ErrBadRequest("工程命名限制不是合法正则: %v", err)
		}
	}
	if in.QuietPeriodSec < 0 {
		in.QuietPeriodSec = 0
	}
	if in.SCMRetryCount < 0 {
		in.SCMRetryCount = 0
	}
	if in.BuildRetentionMaxPerJob < 0 {
		in.BuildRetentionMaxPerJob = 0
	}
	if in.BuildRetentionMaxAgeDays < 0 {
		in.BuildRetentionMaxAgeDays = 0
	}
	if in.ArtifactMaxTotalMB <= 0 {
		in.ArtifactMaxTotalMB = 4096
	}
	if in.ArtifactMaxFiles <= 0 {
		in.ArtifactMaxFiles = 10000
	}
	if in.LogMaxMB <= 0 {
		in.LogMaxMB = 100
	}
	if in.LogMaxMB > 10240 {
		in.LogMaxMB = 10240
	}
	for i := range in.GlobalEnv {
		item := &in.GlobalEnv[i]
		item.Key = strings.TrimSpace(item.Key)
		item.Value = strings.TrimSpace(item.Value)
		if item.Key == "" {
			return pkg.ErrBadRequest("全局环境变量的名称不能为空")
		}
	}
	in.Email.SMTPHost = strings.TrimSpace(in.Email.SMTPHost)
	in.Email.Username = strings.TrimSpace(in.Email.Username)
	in.Email.From = strings.TrimSpace(in.Email.From)
	in.Email.Recipients = strings.TrimSpace(in.Email.Recipients)
	in.Email.DefaultSuffix = strings.TrimSpace(in.Email.DefaultSuffix)
	if in.Email.Enabled {
		if in.Email.SMTPHost == "" {
			return pkg.ErrBadRequest("启用邮件通知时必须填写 SMTP 服务器")
		}
		if in.Email.SMTPPort <= 0 {
			in.Email.SMTPPort = 25
		}
	}
	for i := range in.ToolCategories {
		category := &in.ToolCategories[i]
		category.ID = strings.TrimSpace(category.ID)
		category.Title = strings.TrimSpace(category.Title)
		if category.ID == "" || category.Title == "" {
			return pkg.ErrBadRequest("工具类别的 id 与 title 均为必填")
		}
		for j := range category.Installations {
			item := &category.Installations[j]
			item.Name = strings.TrimSpace(item.Name)
			item.Home = strings.TrimSpace(item.Home)
			item.Note = strings.TrimSpace(item.Note)
			item.InstallMode = strings.ToLower(strings.TrimSpace(item.InstallMode))
			item.Version = strings.TrimSpace(item.Version)
			if item.Name == "" {
				return pkg.ErrBadRequest("工具 %q 下的安装项 name 为必填", category.Title)
			}
			switch item.InstallMode {
			case "", model.ToolInstallManual:
				item.InstallMode = model.ToolInstallManual
				item.Version = ""
			case model.ToolInstallAuto:
				// 自动安装：只要类别人认可、版本在预置清单里即可，主目录由平台下载后填写。
				tool, version, ok := tools.FindVersion(category.ID, item.Version)
				if !ok {
					hint := ""
					if tool.VersionHint != "" {
						hint = "，可填：" + tool.VersionHint
					}
					return pkg.ErrBadRequest("工具 %q 不支持自动安装该版本：%s%s（也可改用手动填写主目录）",
						category.Title, item.Version, hint)
				}
				if !tool.AutoInstall {
					return pkg.ErrBadRequest("工具 %q 不支持自动安装，请手动填写主目录", category.Title)
				}
				if !hasPlatformURL(version) {
					return pkg.ErrBadRequest("工具 %q 的版本 %s 没有预置下载地址", category.Title, item.Version)
				}
				item.Home = "" // 以磁盘上的实际安装结果为准，避免保存过期路径
			default:
				return pkg.ErrBadRequest("工具 %q 的安装方式只能是 manual 或 auto：%s", category.Title, item.InstallMode)
			}
			if item.Home != "" {
				if !isAbsoluteToolPath(item.Home) {
					return pkg.ErrBadRequest("工具 %q 的主目录必须是绝对路径（宿主机路径）：%s", category.Title, item.Home)
				}
				item.Home = strings.TrimRight(filepathToSlash(item.Home), "/")
				if item.Home == "" || item.Home == "/" || (len(item.Home) == 2 && item.Home[1] == ':') {
					return pkg.ErrBadRequest("工具 %q 的主目录不能是根目录", category.Title)
				}
			}
		}
	}

	// SSH 服务器：集中维护发布目标，任务只引用 ID。
	// 先收集显式 ID，再给新增项分配不与已有项冲突的 ID。
	explicit := make(map[string]bool, len(in.SshServers))
	for i := range in.SshServers {
		srv := &in.SshServers[i]
		srv.Name = strings.TrimSpace(srv.Name)
		srv.Host = strings.TrimSpace(srv.Host)
		srv.Username = strings.TrimSpace(srv.Username)
		srv.PrivateKeyPath = strings.TrimSpace(srv.PrivateKeyPath)
		srv.KnownHostsPath = strings.TrimSpace(srv.KnownHostsPath)
		srv.RemoteBaseDir = strings.TrimSpace(srv.RemoteBaseDir)
		srv.Note = strings.TrimSpace(srv.Note)
		srv.AuthType = strings.ToLower(strings.TrimSpace(srv.AuthType))
		if srv.ID != "" {
			if explicit[srv.ID] {
				return pkg.ErrBadRequest("SSH 服务器 ID 重复: %s", srv.ID)
			}
			explicit[srv.ID] = true
		}
	}
	nextID := 1
	servers := make([]model.SshServer, 0, len(in.SshServers))
	for _, srv := range in.SshServers {
		if srv.ID == "" {
			for explicit[fmt.Sprintf("ssh-%d", nextID)] {
				nextID++
			}
			srv.ID = fmt.Sprintf("ssh-%d", nextID)
			explicit[srv.ID] = true
			nextID++
		}
		if srv.Name == "" || srv.Host == "" || srv.Username == "" {
			return pkg.ErrBadRequest("SSH 服务器的名称、主机与用户名均为必填")
		}
		if srv.AuthType == "" {
			srv.AuthType = "key"
		}
		if srv.AuthType != "key" && srv.AuthType != "password" {
			return pkg.ErrBadRequest("SSH 服务器 %q 的认证方式必须是 key 或 password", srv.Name)
		}
		if srv.AuthType == "key" && srv.PrivateKeyPath == "" {
			return pkg.ErrBadRequest("SSH 服务器 %q 使用密钥认证时必须填写私钥路径", srv.Name)
		}
		if srv.AuthType == "password" && srv.Password == "" {
			return pkg.ErrBadRequest("SSH 服务器 %q 使用密码认证时必须填写密码", srv.Name)
		}
		if srv.Port <= 0 {
			srv.Port = 22
		}
		srv.HasPassword = false
		servers = append(servers, srv)
	}
	in.SshServers = servers
	return nil
}

// filepathToSlash 把 Windows 风格分隔符统一为 /，便于校验路径。
func filepathToSlash(p string) string {
	return strings.ReplaceAll(p, `\`, "/")
}

// isAbsoluteToolPath 判断工具主目录是否为绝对路径（支持 Linux /... 与 Windows X:/...）。
func isAbsoluteToolPath(p string) bool {
	s := filepathToSlash(p)
	if strings.HasPrefix(s, "/") {
		return true
	}
	return len(s) >= 3 && s[1] == ':' && s[2] == '/'
}

// hasPlatformURL 判断版本是否有任意平台的下载地址（保存时不做平台限定的硬校验：
// 用户可能在别的机器上编辑配置，真正下载时才按当前平台挑选）。
func hasPlatformURL(v tools.Version) bool {
	return len(v.URLs) > 0
}
