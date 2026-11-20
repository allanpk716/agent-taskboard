package outlet_test

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/outlet"
)

func Test出口密钥从环境变量读取并用AESGCM加密(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TASKBOARD_OUTLET_KEY", base64.StdEncoding.EncodeToString(key))
	cipher, err := outlet.NewCipherFromEnv()
	if err != nil {
		t.Fatal(err)
	}

	const webhookKey = "webhook-secret-only-for-test"
	first, err := cipher.Encrypt(webhookKey)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cipher.Encrypt(webhookKey)
	if err != nil {
		t.Fatal(err)
	}
	if first == webhookKey || strings.Contains(first, webhookKey) {
		t.Fatalf("密文包含明文 key：%q", first)
	}
	if first == second {
		t.Fatal("重复加密应使用随机 nonce，得到不同密文")
	}
	plain, err := cipher.Decrypt(first)
	if err != nil || plain != webhookKey {
		t.Fatalf("解密失败或明文不符：err=%v", err)
	}

	t.Setenv("TASKBOARD_OUTLET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("w", 32))))
	other, err := outlet.NewCipherFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Decrypt(first); !errors.Is(err, outlet.ErrCipherText) {
		t.Fatalf("错误密钥应返回 ErrCipherText：%v", err)
	}
}

func Test出口主密钥缺失或格式错误时给出明确错误(t *testing.T) {
	t.Setenv("TASKBOARD_OUTLET_KEY", "")
	if _, err := outlet.NewCipherFromEnv(); !errors.Is(err, outlet.ErrMasterKeyMissing) ||
		!strings.Contains(err.Error(), "TASKBOARD_OUTLET_KEY") {
		t.Fatalf("缺失主密钥错误 = %v，想包含变量名", err)
	}

	t.Setenv("TASKBOARD_OUTLET_KEY", "not-base64!!")
	_, err := outlet.NewCipherFromEnv()
	if !errors.Is(err, outlet.ErrMasterKeyInvalid) || !strings.Contains(err.Error(), "TASKBOARD_OUTLET_KEY") {
		t.Fatalf("非法主密钥错误 = %v，想包含变量名且不包含值", err)
	}
}
