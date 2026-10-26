// Package guestlinkkey 管理访客链接密钥的服务端加密密钥，并用 AEAD 加密
// 数据库里的可恢复密钥。鉴权仍然只使用哈希。
package guestlinkkey

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	fileName = "guest-link-key.bin"
	keySize  = 32
	nonceLen = 12
	version  = "v1:"
)

// ErrCipherText 表示密文不是当前密钥能打开的格式。
var ErrCipherText = errors.New("访客链接密文无法用当前密钥解开")

// Cipher 用一个 256 位密钥做 AES-GCM 加解密。
type Cipher struct {
	aead cipher.AEAD
}

// LoadOrCreate 在数据目录读取服务端密钥；文件不存在时生成一个独立密钥文件。
// 文件存在但不可读、长度不对时返回错误，由调用方降级为“老链接”模式。
func LoadOrCreate(dataDir string) (*Cipher, error) {
	path := filepath.Join(dataDir, fileName)
	key, err := os.ReadFile(path)
	if err == nil {
		return New(key)
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("读取访客链接加密密钥 %s 失败：%w", path, err)
	}
	key = make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("生成访客链接加密密钥失败：%w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("写入访客链接加密密钥 %s 失败：%w", path, err)
	}
	return New(key)
}

// New 用已有 256 位密钥构造 Cipher。
func New(key []byte) (*Cipher, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("访客链接加密密钥长度 = %d 字节, 想 %d", len(key), keySize)
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

// Encrypt 加密明文，输出带版本的 base64 字符串（随机 nonce 由 AEAD 使用）。
func (c *Cipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("生成访客链接密文 nonce 失败：%w", err)
	}
	sealed := c.aead.Seal(nil, nonce, []byte(plaintext), nil)
	out := make([]byte, 0, len(nonce)+len(sealed))
	out = append(out, nonce...)
	out = append(out, sealed...)
	return version + base64.RawURLEncoding.EncodeToString(out), nil
}

// Decrypt 解开 Encrypt 生成的密文；换过服务端密钥或数据损坏时返回
// ErrCipherText，调用方按老链接处理。
func (c *Cipher) Decrypt(ciphertext string) (string, error) {
	if !strings.HasPrefix(ciphertext, version) {
		return "", ErrCipherText
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(ciphertext, version))
	if err != nil || len(raw) <= nonceLen {
		return "", ErrCipherText
	}
	plain, err := c.aead.Open(nil, raw[:nonceLen], raw[nonceLen:], nil)
	if err != nil {
		return "", ErrCipherText
	}
	return string(plain), nil
}

// CanDecrypt 报告当前服务端密钥能否解开这段密文。
func (c *Cipher) CanDecrypt(ciphertext string) bool {
	_, err := c.Decrypt(ciphertext)
	return err == nil
}
