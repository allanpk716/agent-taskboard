// Package outlet 提供通知出口的加密、URL 与出站 HTTP 基建。
package outlet

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	masterKeyEnv = "TASKBOARD_OUTLET_KEY"
	keySize      = 32
	nonceSize    = 12
	cipherPrefix = "outlet-v1:"
)

var (
	// ErrMasterKeyMissing 表示部署级出口主密钥未注入。
	ErrMasterKeyMissing = errors.New("缺少 TASKBOARD_OUTLET_KEY 环境变量")
	// ErrMasterKeyInvalid 表示出口主密钥不是可用的 32 字节 base64 密钥。
	ErrMasterKeyInvalid = errors.New("TASKBOARD_OUTLET_KEY 不是有效的 base64 编码 32 字节密钥")
	// ErrCipherText 表示密文格式错误或不是当前主密钥加密的。
	ErrCipherText = errors.New("通知出口密文无法解开")
)

// Cipher 用环境注入的 256 位主密钥做 AES-GCM 加解密。
type Cipher struct {
	aead cipher.AEAD
}

// NewCipherFromEnv 从 TASKBOARD_OUTLET_KEY 读取 base64 主密钥。
func NewCipherFromEnv() (*Cipher, error) {
	raw := os.Getenv(masterKeyEnv)
	if raw == "" {
		return nil, ErrMasterKeyMissing
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != keySize {
		return nil, ErrMasterKeyInvalid
	}
	return NewCipher(key)
}

// NewCipher 用已有 256 位主密钥构造 Cipher。
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != keySize {
		return nil, ErrMasterKeyInvalid
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt 加密成员提交的 webhook key，输出带版本前缀的 base64 密文。
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成通知出口密文 nonce 失败：%w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	body := append(nonce, sealed...)
	return cipherPrefix + base64.RawURLEncoding.EncodeToString(body), nil
}

// Decrypt 解开 Encrypt 生成的密文；密钥丢失或换钥时返回 ErrCipherText。
func (c *Cipher) Decrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, cipherPrefix) {
		return "", ErrCipherText
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ciphertext, cipherPrefix))
	if err != nil || len(raw) <= nonceSize {
		return "", ErrCipherText
	}
	plain, err := c.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], nil)
	if err != nil {
		return "", ErrCipherText
	}
	return string(plain), nil
}
