package pkg

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// EncryptedSecretPrefix 标记平台加密值。没有前缀的旧数据按明文读取，
// 下次写入时自动升级为密文，保证升级无感。
const EncryptedSecretPrefix = "enc:v1:"

// SecretBox 用 data/credentials.key 对配置中的敏感字段做 AES-GCM 加密。
// 与凭据存储共用同一个密钥文件：备份 data 时只需备份一份 credentials.key。
type SecretBox struct {
	keyPath string
	mu      sync.Mutex
	key     []byte
	err     error
}

// NewSecretBox 创建加密器，keyPath 通常为 DATA_HOME/credentials.key。
func NewSecretBox(keyPath string) *SecretBox {
	return &SecretBox{keyPath: keyPath}
}

// Seal 加密明文；空值或已加密值原样返回。
func (b *SecretBox) Seal(plain string) (string, error) {
	if plain == "" || IsEncryptedSecret(plain) {
		return plain, nil
	}
	key, err := b.encryptionKey()
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
	return EncryptedSecretPrefix + base64.StdEncoding.EncodeToString(out), nil
}

// Unseal 解密；旧明文原样返回，保持向后兼容。
func (b *SecretBox) Unseal(value string) (string, error) {
	if !IsEncryptedSecret(value) {
		return value, nil
	}
	// 解密绝不能创建新密钥：密钥文件丢失时继续生成只会让旧密文
	// 永久不可恢复，并让运维误以为数据还能读。必须显式报错。
	key, err := b.existingKey()
	if err != nil {
		return "", err
	}
	raw, err := base64.StdEncoding.DecodeString(value[len(EncryptedSecretPrefix):])
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

// KeyReady 检查密钥文件可读且长度为 32 字节，供 readiness 使用。
// 注意：这里只读，不创建密钥；首次写入时由 Seal 负责创建。
func (b *SecretBox) KeyReady() error {
	_, err := b.existingKey()
	return err
}

// IsEncryptedSecret 判断值是否已加密。
func IsEncryptedSecret(value string) bool {
	return len(value) >= len(EncryptedSecretPrefix) && value[:len(EncryptedSecretPrefix)] == EncryptedSecretPrefix
}

func (b *SecretBox) encryptionKey() ([]byte, error) {
	return b.loadKey(true)
}

// existingKey 只读取已有密钥，供解密/readiness 使用。
func (b *SecretBox) existingKey() ([]byte, error) {
	return b.loadKey(false)
}

func (b *SecretBox) loadKey(create bool) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.key != nil {
		return b.key, nil
	}
	if b.err != nil {
		return nil, b.err
	}
	if !Exists(b.keyPath) {
		if !create {
			return nil, fmt.Errorf("credentials key is missing: %s", b.keyPath)
		}
		if err := EnsureDir(filepath.Dir(b.keyPath)); err != nil {
			b.err = fmt.Errorf("prepare credentials key dir: %w", err)
			return nil, b.err
		}
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			b.err = fmt.Errorf("generate credentials key: %w", err)
			return nil, b.err
		}
		// O_EXCL：多实例/并发启动时只有一个能创建成功，另一个读取现有密钥。
		f, err := os.OpenFile(b.keyPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			if !errors.Is(err, os.ErrExist) {
				b.err = fmt.Errorf("create credentials key: %w", err)
				return nil, b.err
			}
		} else {
			if _, err = f.Write(key); err != nil {
				_ = f.Close()
				b.err = fmt.Errorf("write credentials key: %w", err)
				return nil, b.err
			}
			if err = f.Close(); err != nil {
				b.err = fmt.Errorf("close credentials key: %w", err)
				return nil, b.err
			}
		}
	}
	key, err := os.ReadFile(b.keyPath)
	if err != nil {
		b.err = fmt.Errorf("read credentials key: %w", err)
		return nil, b.err
	}
	if len(key) != 32 {
		b.err = fmt.Errorf("credentials key must be 32 bytes, got %d", len(key))
		return nil, b.err
	}
	b.key = key
	return b.key, nil
}
