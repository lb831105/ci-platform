package store

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// encryptedSecretPrefix 标记「已加密」的密钥字段。
// 没有前缀的旧数据按明文读取，并在下次写入时自动升级为密文。
const encryptedSecretPrefix = "enc:v1:"

// CredentialStore 把凭据以 JSON 落盘到 data/credentials/{id}.json。
// 密钥字段使用 AES-GCM 加密，密钥保存在 data/credentials.key（0600）。
type CredentialStore struct {
	root    string
	keyPath string

	keyOnce sync.Once
	key     []byte
	keyErr  error

	// mu 串行化「写入」与「明文迁移」，避免迁移覆盖并发更新写入的新密钥。
	mu sync.Mutex
}

// NewCredentialStore 创建凭据存储，dataHome 为 data 目录。
func NewCredentialStore(dataHome string) *CredentialStore {
	return &CredentialStore{
		root:    filepath.Join(dataHome, "credentials"),
		keyPath: filepath.Join(dataHome, "credentials.key"),
	}
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
	return s.write(c)
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
	return s.write(c)
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
	rawSecret := out.Secret
	if err := s.openCredential(&out); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(rawSecret, encryptedSecretPrefix) {
		s.migrateLegacy(id)
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
		rawSecret := c.Secret
		if err := s.openCredential(&c); err != nil {
			// 单个凭据损坏（例如密钥文件丢失）不能拖垮整个列表。
			continue
		}
		if !strings.HasPrefix(rawSecret, encryptedSecretPrefix) {
			s.migrateLegacy(c.ID)
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

// write 加密 Secret 后落盘；不修改调用方传入的对象，避免 service 层读到密文。
func (s *CredentialStore) write(c *model.Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(c)
}

func (s *CredentialStore) writeLocked(c *model.Credential) error {
	copy := *c
	sealed, err := s.seal(c.Secret)
	if err != nil {
		return err
	}
	copy.Secret = sealed
	return pkg.WriteJSONAtomic(s.path(c.ID), &copy)
}

// openCredential 把磁盘上的 Secret 解密回明文。
func (s *CredentialStore) openCredential(c *model.Credential) error {
	plain, err := s.unseal(c.Secret)
	if err != nil {
		return fmt.Errorf("decrypt credential %s: %w", c.ID, err)
	}
	c.Secret = plain
	return nil
}

// migrateLegacy 把旧明文凭据在读取时升级为密文；失败不影响本次读取。
func (s *CredentialStore) migrateLegacy(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var c model.Credential
	if err := pkg.ReadJSON(s.path(id), &c); err != nil {
		return
	}
	if strings.HasPrefix(c.Secret, encryptedSecretPrefix) {
		return
	}
	_ = s.writeLocked(&c)
}

// KeyReady 检查 credentials.key 是否可读且长度正确，供 readiness 使用。
// 密钥缺失时不自动生成：如果已有密文，自动生成新密钥会让旧数据永久不可读。
func (s *CredentialStore) KeyReady() error {
	if _, err := s.existingKey(); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if encryptedDataExists(filepath.Dir(s.keyPath)) {
		return errors.New("credentials.key is missing but encrypted data exists; restore the key from backup")
	}
	return nil
}

// existingKey 只读取已有密钥，绝不生成；供解密与 readiness 使用。
func (s *CredentialStore) existingKey() ([]byte, error) {
	if !pkg.Exists(s.keyPath) {
		return nil, os.ErrNotExist
	}
	key, err := os.ReadFile(s.keyPath)
	if err != nil {
		return nil, err
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("credentials key must be 32 bytes, got %d", len(key))
	}
	return key, nil
}

// encryptedDataExists 在密钥缺失时判断数据目录里是否已有密文。
// 只在密钥缺失的异常路径调用，避免正常启动时扫描大量构建记录。
func encryptedDataExists(dataHome string) bool {
	found := false
	err := filepath.WalkDir(dataHome, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			switch d.Name() {
			case "workspace", "tools", "logs":
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 16<<20 {
			return nil
		}
		data, err := os.ReadFile(path)
		if err == nil && bytes.Contains(data, []byte(encryptedSecretPrefix)) {
			found = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		// 无法确认时按“有密文”处理，宁可 readiness 失败也不生成新密钥。
		return true
	}
	return found
}

// SealSecret 供构建参数等其它敏感数据复用同一个 AES-GCM 密钥。
func (s *CredentialStore) SealSecret(plain string) (string, error) {
	return s.seal(plain)
}

// UnsealSecret 解密由 SealSecret 生成的值；没有 enc:v1: 前缀时按旧明文返回。
func (s *CredentialStore) UnsealSecret(value string) (string, error) {
	return s.unseal(value)
}

// seal 用 AES-GCM 加密任意长度的明文，输出 enc:v1:<base64(nonce|ciphertext)>。
func (s *CredentialStore) seal(plain string) (string, error) {
	if plain == "" || strings.HasPrefix(plain, encryptedSecretPrefix) {
		return plain, nil
	}
	key, err := s.encryptionKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	out := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return encryptedSecretPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// unseal 解密 Secret；旧明文原样返回，保持向后兼容。
func (s *CredentialStore) unseal(value string) (string, error) {
	if !strings.HasPrefix(value, encryptedSecretPrefix) {
		return value, nil
	}
	key, err := s.existingKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, encryptedSecretPrefix))
	if err != nil {
		return "", fmt.Errorf("decode secret: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create gcm: %w", err)
	}
	if len(raw) < gcm.NonceSize() {
		return "", errors.New("encrypted secret is too short")
	}
	plain, err := gcm.Open(nil, raw[:gcm.NonceSize()], raw[gcm.NonceSize():], nil)
	if err != nil {
		return "", errors.New("decrypt secret failed (credentials.key changed or corrupt)")
	}
	return string(plain), nil
}

// encryptionKey 惰性加载/生成 32 字节 AES-256 密钥。
// 首次启动时用 crypto/rand 生成并原子写入 0600 文件；后续凭据依赖该文件解密。
func (s *CredentialStore) encryptionKey() ([]byte, error) {
	s.keyOnce.Do(func() {
		if err := pkg.EnsureDir(filepath.Dir(s.keyPath)); err != nil {
			s.keyErr = fmt.Errorf("prepare credentials key dir: %w", err)
			return
		}
		if !pkg.Exists(s.keyPath) {
			key := make([]byte, 32)
			if _, err := rand.Read(key); err != nil {
				s.keyErr = fmt.Errorf("generate credentials key: %w", err)
				return
			}
			// O_EXCL：多实例/并发启动时只有一个能创建成功，另一个读取现有密钥。
			f, err := os.OpenFile(s.keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				if !errors.Is(err, os.ErrExist) {
					s.keyErr = fmt.Errorf("create credentials key: %w", err)
					return
				}
			} else {
				if _, err = f.Write(key); err != nil {
					_ = f.Close()
					s.keyErr = fmt.Errorf("write credentials key: %w", err)
					return
				}
				if err = f.Close(); err != nil {
					s.keyErr = fmt.Errorf("close credentials key: %w", err)
					return
				}
			}
		}
		key, err := os.ReadFile(s.keyPath)
		if err != nil {
			s.keyErr = fmt.Errorf("read credentials key: %w", err)
			return
		}
		if len(key) != 32 {
			s.keyErr = fmt.Errorf("credentials key must be 32 bytes, got %d", len(key))
			return
		}
		s.key = key
	})
	return s.key, s.keyErr
}
