package service

import (
	"context"
	"strconv"
	"strings"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// CredentialStore 是 service 依赖的凭据存储抽象。
type CredentialStore interface {
	Exists(id string) bool
	Create(ctx context.Context, c *model.Credential) error
	Update(ctx context.Context, c *model.Credential) error
	Get(ctx context.Context, id string) (*model.Credential, error)
	List(ctx context.Context) ([]*model.Credential, error)
	Delete(ctx context.Context, id string) error
}

// CredentialService 管理凭据的增删改查；对外返回一律脱敏。
type CredentialService struct {
	store CredentialStore
}

// NewCredentialService 构造凭据服务。
func NewCredentialService(store CredentialStore) *CredentialService {
	return &CredentialService{store: store}
}

// List 返回脱敏后的凭据列表。
func (s *CredentialService) List(ctx context.Context) ([]model.Credential, error) {
	items, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]model.Credential, 0, len(items))
	for _, item := range items {
		out = append(out, item.Mask())
	}
	return out, nil
}

// Get 返回脱敏后的单个凭据。
func (s *CredentialService) Get(ctx context.Context, id string) (model.Credential, error) {
	c, err := s.store.Get(ctx, id)
	if err != nil {
		return model.Credential{}, err
	}
	return c.Mask(), nil
}

// Resolve 返回未脱敏的凭据，仅供构建等内部流程使用，不对外暴露。
func (s *CredentialService) Resolve(ctx context.Context, id string) (*model.Credential, error) {
	return s.store.Get(ctx, id)
}

// SealSecret 加密构建密码参数等敏感值（与凭据共用 credentials.key）。
func (s *CredentialService) SealSecret(plain string) (string, error) {
	sealer, ok := s.store.(interface{ SealSecret(string) (string, error) })
	if !ok {
		return "", pkg.NewError(pkg.CodeInternal, "凭据存储不支持加密")
	}
	return sealer.SealSecret(plain)
}

// UnsealSecret 解密构建密码参数。
func (s *CredentialService) UnsealSecret(value string) (string, error) {
	unsealer, ok := s.store.(interface{ UnsealSecret(string) (string, error) })
	if !ok {
		return "", pkg.NewError(pkg.CodeInternal, "凭据存储不支持解密")
	}
	return unsealer.UnsealSecret(value)
}

// Create 校验并落盘新凭据。
func (s *CredentialService) Create(ctx context.Context, in *model.Credential) (*model.Credential, error) {
	if err := normalizeCredential(in); err != nil {
		return nil, err
	}
	// 所有类型都必须有 secret；否则会生成一个永远无法用于认证的“空凭据”。
	if strings.TrimSpace(in.Secret) == "" {
		return nil, pkg.ErrBadRequest("凭据内容不能为空")
	}
	in.ID = s.allocateID()
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	if err := s.store.Create(ctx, in); err != nil {
		return nil, err
	}
	masked := in.Mask()
	return &masked, nil
}

// Update 更新凭据；secret 为空时沿用旧值（前端编辑时不会回显密钥）。
func (s *CredentialService) Update(ctx context.Context, id string, in *model.Credential) (*model.Credential, error) {
	old, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// 未提交 allowed_hosts（nil）时沿用旧作用域；显式提交 [] 才清空。
	if in.AllowedHosts == nil {
		in.AllowedHosts = old.AllowedHosts
	}
	if err = normalizeCredential(in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Secret) == "" {
		if old.Type != in.Type {
			return nil, pkg.ErrBadRequest("切换凭据类型时必须重新填写凭据内容")
		}
		in.Secret = old.Secret
	}
	in.ID = id
	in.CreatedAt = old.CreatedAt
	in.UpdatedAt = time.Now().UTC()
	if err = s.store.Update(ctx, in); err != nil {
		return nil, err
	}
	masked := in.Mask()
	return &masked, nil
}

// Delete 删除凭据。
func (s *CredentialService) Delete(ctx context.Context, id string) error {
	return s.store.Delete(ctx, id)
}

// allocateID 以纳秒时间戳为基线分配可读且唯一的 ID。
func (s *CredentialService) allocateID() string {
	id := time.Now().UTC().UnixNano()
	for s.store.Exists(strconv.FormatInt(id, 10)) {
		id++
	}
	return strconv.FormatInt(id, 10)
}

// normalizeCredentialHosts 归一化允许的 Git 主机：小写、去 scheme/path、去重。
// 支持精确域名与 *.example.com；空列表表示兼容旧凭据（不限制）。
func normalizeCredentialHosts(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, raw := range in {
		host := strings.ToLower(strings.TrimSpace(raw))
		host = strings.TrimPrefix(host, "https://")
		host = strings.TrimPrefix(host, "http://")
		if i := strings.IndexAny(host, "/?#"); i >= 0 {
			host = host[:i]
		}
		host = strings.TrimSpace(host)
		if host == "" {
			continue
		}
		if strings.ContainsAny(host, " \t\r\n") || strings.Contains(host, "..") {
			return nil, pkg.ErrBadRequest("凭据允许主机格式不正确: %q", raw)
		}
		if seen[host] {
			continue
		}
		seen[host] = true
		out = append(out, host)
	}
	if len(out) > 20 {
		return nil, pkg.ErrBadRequest("凭据允许主机最多 20 个")
	}
	return out, nil
}

// normalizeCredential 做字段归一化与按类型的必填校验。
func normalizeCredential(in *model.Credential) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Type = strings.TrimSpace(in.Type)
	in.Username = strings.TrimSpace(in.Username)
	in.Description = strings.TrimSpace(in.Description)
	hosts, err := normalizeCredentialHosts(in.AllowedHosts)
	if err != nil {
		return err
	}
	in.AllowedHosts = hosts

	if in.Name == "" {
		return pkg.ErrBadRequest("凭据名称不能为空")
	}
	switch in.Type {
	case model.CredentialUsernamePassword:
		if in.Username == "" {
			return pkg.ErrBadRequest("用户名/密码类型需要填写用户名")
		}
	case model.CredentialSecretText:
		// 仅需 secret
	case model.CredentialSSHKey:
		// 平台没有「凭据 → 私钥文件」的使用方（Git 走 HTTPS，SSH 发布的私钥路径配在服务器上），
		// 存下来只会是一份永远不会被使用的密钥。
		return pkg.ErrBadRequest("暂不支持 SSH 私钥类型凭据：请在「系统配置 → SSH 服务器」中填写私钥路径")
	default:
		return pkg.ErrBadRequest("不支持的凭据类型: %s", in.Type)
	}
	return nil
}
