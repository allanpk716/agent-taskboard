// Package attachmentlink 签发成员 Agent 用的短时效附件下载地址。
package attachmentlink

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	// FileName 是附件链接 HMAC 密钥文件在数据目录中的固定文件名。
	FileName = "attachment-link-key.bin"
	keySize  = 32
	// TTL 是附件链接的有效期；下载时会连同 Agent 状态和权限一起重新校验。
	TTL = 10 * time.Minute
)

// ErrInvalidSignature 表示签名不匹配（调用方应把它与其他无凭证失败一致处理）。
var ErrInvalidSignature = errors.New("附件链接签名无效")

// Signer 用持久化的服务端密钥做 HMAC-SHA256。
type Signer struct {
	key []byte
}

// LoadOrCreate 读取数据目录中的密钥；首次启动生成 32 字节随机密钥并保存。
func LoadOrCreate(dataDir string) (*Signer, error) {
	path := filepath.Join(dataDir, FileName)
	key, err := os.ReadFile(path)
	if err == nil {
		signer, newErr := New(key)
		if newErr == nil {
			return signer, nil
		}
		err = newErr
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		corrupt := path + ".corrupt-" + time.Now().Format("20060102-150405.000000000")
		if renameErr := os.Rename(path, corrupt); renameErr != nil {
			return nil, fmt.Errorf("附件链接密钥 %s 不可用（%v），且改名留存失败：%w", path, err, renameErr)
		}
		log.Printf("附件链接密钥 %s 不可用（%v），已改名留存为 %s；生成新密钥，未过期附件链接按无效处理",
			path, err, corrupt)
	}
	key = make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("生成附件链接密钥失败：%w", err)
	}
	if err := os.WriteFile(path, key, 0o600); err != nil {
		return nil, fmt.Errorf("写入附件链接密钥 %s 失败：%w", path, err)
	}
	return New(key)
}

// New 用已有 256 位密钥构造签名器。
func New(key []byte) (*Signer, error) {
	if len(key) != keySize {
		return nil, fmt.Errorf("附件链接密钥长度 = %d 字节, 想 %d", len(key), keySize)
	}
	return &Signer{key: append([]byte(nil), key...)}, nil
}

// Sign 对附件引用、Agent id 和过期 Unix 时间签名，输出小写 hex。
func (s *Signer) Sign(ref string, agentID int64, expiresAt time.Time) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(payload(ref, agentID, expiresAt.Unix())))
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify 常数时间比较签名。过期由调用方判定；这里只校验签名字节本身。
func (s *Signer) Verify(ref string, agentID, expiresAt int64, received string) bool {
	if s == nil {
		return false
	}
	expected := []byte(s.Sign(ref, agentID, time.Unix(expiresAt, 0)))
	return hmac.Equal(expected, []byte(received))
}

func payload(ref string, agentID, expiresAt int64) string {
	return ref + "|" + strconv.FormatInt(agentID, 10) + "|" + strconv.FormatInt(expiresAt, 10)
}
