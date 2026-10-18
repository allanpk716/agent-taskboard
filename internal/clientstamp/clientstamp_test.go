package clientstamp_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/clientstamp"
)

func Test追加后可从文件末尾读回平台地址(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taskboard-client.exe")
	if err := os.WriteFile(path, []byte("MZ original executable"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := clientstamp.AppendFile(path, "https://taskboard.example.com"); err != nil {
		t.Fatal(err)
	}
	got, ok, err := clientstamp.Read(path)
	if err != nil || !ok {
		t.Fatalf("Read = %q, %v, %v；想读到尾部地址", got, ok, err)
	}
	if got != "https://taskboard.example.com" {
		t.Fatalf("平台地址 = %q", got)
	}
}

func Test无尾部或尾部损坏时返回没有地址(t *testing.T) {
	path := filepath.Join(t.TempDir(), "taskboard-client.exe")
	original := []byte("MZ original executable")
	if err := os.WriteFile(path, original, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := clientstamp.Read(path); err != nil || ok || got != "" {
		t.Fatalf("无尾部 Read = %q, %v, %v", got, ok, err)
	}
	if err := clientstamp.AppendFile(path, "https://taskboard.example.com"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// 破坏长度字段，模拟下载中断或手工改动。
	data[len(data)-12] ^= 0xff
	if err := os.WriteFile(path, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok, err := clientstamp.Read(path); err != nil || ok || got != "" {
		t.Fatalf("损坏尾部 Read = %q, %v, %v", got, ok, err)
	}
}

func Test非法地址不会被追加(t *testing.T) {
	if _, err := clientstamp.Append([]byte("MZ"), "ftp://taskboard.example.com"); err == nil {
		t.Fatal("ftp 地址应被拒绝")
	}
	if _, err := clientstamp.Append([]byte("MZ"), "https://"+string(make([]byte, 4097))+"/"); err == nil {
		t.Fatal("超长地址应被拒绝")
	}
}
