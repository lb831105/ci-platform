package store

import (
	"context"
	"path/filepath"
	"sort"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// CredentialStore 把凭据以 JSON 落盘到 data/credentials/{id}.json。
type CredentialStore struct {
	root string
}

// NewCredentialStore 创建凭据存储，dataHome 为 data 目录。
func NewCredentialStore(dataHome string) *CredentialStore {
	return &CredentialStore{root: filepath.Join(dataHome, "credentials")}
}

func (s *CredentialStore) path(id string) string {
	return filepath.Join(s.root, id+".json")
}

// Exists 判断凭据是否存在。
func (s *CredentialStore) Exists(id string) bool {
	if sanitizeID(id) != nil {
		return false
	}
	return pkg.Exists(s.path(id))
}

// Create 写入新凭据。
func (s *CredentialStore) Create(ctx context.Context, c *model.Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sanitizeID(c.ID); err != nil {
		return err
	}
	return pkg.WriteJSONAtomic(s.path(c.ID), c)
}

// Update 覆盖写入凭据。
func (s *CredentialStore) Update(ctx context.Context, c *model.Credential) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sanitizeID(c.ID); err != nil {
		return err
	}
	if !pkg.Exists(s.path(c.ID)) {
		return pkg.ErrNotFound("credential %s not found", c.ID)
	}
	return pkg.WriteJSONAtomic(s.path(c.ID), c)
}

// Get 读取单个凭据。
func (s *CredentialStore) Get(ctx context.Context, id string) (*model.Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sanitizeID(id); err != nil {
		return nil, err
	}
	if !pkg.Exists(s.path(id)) {
		return nil, pkg.ErrNotFound("credential %s not found", id)
	}
	var out model.Credential
	if err := pkg.ReadJSON(s.path(id), &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// List 返回全部凭据，按创建时间倒序。
func (s *CredentialStore) List(ctx context.Context) ([]*model.Credential, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !pkg.IsDir(s.root) {
		return []*model.Credential{}, nil
	}
	entries, err := filepath.Glob(filepath.Join(s.root, "*.json"))
	if err != nil {
		return nil, err
	}
	out := make([]*model.Credential, 0, len(entries))
	for _, path := range entries {
		var c model.Credential
		if err := pkg.ReadJSON(path, &c); err != nil {
			continue
		}
		out = append(out, &c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Delete 删除凭据文件。
func (s *CredentialStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sanitizeID(id); err != nil {
		return err
	}
	if !pkg.Exists(s.path(id)) {
		return pkg.ErrNotFound("credential %s not found", id)
	}
	return pkg.RemoveAll(s.path(id))
}
