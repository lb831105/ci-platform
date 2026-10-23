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

// normalizeCredential 做字段归一化与按类型的必填校验。
func normalizeCredential(in *model.Credential) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Type = strings.TrimSpace(in.Type)
	in.Username = strings.TrimSpace(in.Username)
	in.Description = strings.TrimSpace(in.Description)

	if in.Name == "" {
		return pkg.ErrBadRequest("凭据名称不能为空")
	}
	switch in.Type {
	case model.CredentialUsernamePassword:
		if in.Username == "" {
			return pkg.ErrBadRequest("用户名/密码类型需要填写用户名")
		}
	case model.CredentialSecretText, model.CredentialSSHKey:
		// 仅需 secret
	default:
		return pkg.ErrBadRequest("不支持的凭据类型: %s", in.Type)
	}
	return nil
}
