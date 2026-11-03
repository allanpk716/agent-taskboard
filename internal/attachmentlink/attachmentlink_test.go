package attachmentlink

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadOrCreate持久化密钥且签名绑定附件和Agent(t *testing.T) {
	dir := t.TempDir()
	signer, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("首次加载失败：%v", err)
	}
	expiresAt := time.Unix(1790000000, 0)
	sig := signer.Sign("att_1", 7, expiresAt)

	again, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("再次加载失败：%v", err)
	}
	if !again.Verify("att_1", 7, expiresAt.Unix(), sig) {
		t.Fatal("同一持久化密钥应能验签")
	}
	if again.Verify("att_2", 7, expiresAt.Unix(), sig) {
		t.Fatal("签名不应能挪到其他附件")
	}
	if again.Verify("att_1", 8, expiresAt.Unix(), sig) {
		t.Fatal("签名不应能挪到其他 Agent")
	}
	info, err := os.Stat(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatalf("密钥文件不存在：%v", err)
	}
	if got := info.Mode().Perm() & 0o777; got != 0o600 {
		t.Fatalf("密钥文件权限 = %o, 想 600", got)
	}
}
