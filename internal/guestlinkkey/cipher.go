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
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
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
// 文件存在但读不出来或长度不对时，把坏文件改名留存（不删除）后生成新密钥
// 照常使用——旧密文解不开，一律由调用方按“老链接”处理。只有坏文件留存不了
// 或新密钥写不进去时才返回错误，由调用方降级为无密钥模式。
func LoadOrCreate(dataDir string) (*Cipher, error) {
	path := filepath.Join(dataDir, fileName)
	key, readErr := os.ReadFile(path)
	if readErr == nil {
		c, err := New(key)
		if err == nil {
			return c, nil
		}
		readErr = err // 内容无效，同样按密钥丢失处理。
	} else if os.IsNotExist(readErr) {
		readErr = nil // 文件不存在：首次启动，正常生成。
	}
	if readErr != nil {
		corrupt := path + ".corrupt-" + time.Now().Format("20060102-150405.000000000")
		if err := os.Rename(path, corrupt); err != nil {
			return nil, fmt.Errorf("访客链接加密密钥 %s 不可用（%v），且改名留存失败：%w", path, readErr, err)
		}
		log.Printf("访客链接加密密钥 %s 不可用（%v），已改名留存为 %s；生成新密钥，旧链接按老链接处理",
			path, readErr, corrupt)
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
