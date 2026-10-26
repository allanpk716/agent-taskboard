package client

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func Test客户端下载不受普通请求总超时限制(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte("client"))
	}))
	defer server.Close()
	a := &App{
		cfg:          Config{ServerURL: server.URL},
		httpClient:   &http.Client{Timeout: 10 * time.Millisecond},
		streamClient: &http.Client{},
	}

	sum, size, err := a.downloadClient(t.Context(), server.URL+"/download", filepath.Join(t.TempDir(), "client.exe"))
	if err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256([]byte("client"))
	if size != 6 || sum != want {
		t.Fatalf("下载结果 = %d/%v", size, sum)
	}
}
