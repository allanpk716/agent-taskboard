//go:build oss_smoke

package attachments

import (
	"context"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestRealOSSSmoke 只由 workflow_dispatch 的 OSS 冒烟任务触发。默认 go test
// 不编译这个文件；即使带 oss_smoke tag，缺环境变量也会跳过而不是连真实 OSS。
func TestRealOSSSmoke(t *testing.T) {
	envNames := []string{
		"OSS_ACCESS_KEY_ID", "OSS_ACCESS_KEY_SECRET", "OSS_SMOKE_REGION",
		"OSS_SMOKE_BUCKET", "OSS_SMOKE_PREFIX", "OSS_SMOKE_ENDPOINT",
	}
	missing := make([]string, 0, len(envNames))
	for _, name := range envNames {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Skipf("missing OSS smoke environment variables: %s", strings.Join(missing, ", "))
	}

	prefix, err := NormalizeOSSPrefix(os.Getenv("OSS_SMOKE_PREFIX"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewOSSStorage(OSSConfig{
		Region: os.Getenv("OSS_SMOKE_REGION"), Bucket: os.Getenv("OSS_SMOKE_BUCKET"),
		Prefix: prefix, InternalEndpoint: os.Getenv("OSS_SMOKE_ENDPOINT"),
	}, OSSStorageOptions{
		AccessKeyID:     os.Getenv("OSS_ACCESS_KEY_ID"),
		AccessKeySecret: os.Getenv("OSS_ACCESS_KEY_SECRET"),
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := store.Probe(ctx); err != nil {
		t.Fatal(err)
	}

	id := time.Now().UnixNano()
	content := "agent-taskboard real OSS smoke " + strconv.FormatInt(id, 10) + "\n"
	defer func() {
		if err := store.Delete(context.Background(), id); err != nil {
			t.Errorf("cleanup delete attachment %d: %v", id, err)
		}
	}()
	if err := store.Put(ctx, id, strings.NewReader(content), int64(len(content)),
		PutMeta{ContentType: "text/plain"}); err != nil {
		t.Fatalf("upload attachment: %v", err)
	}

	body, size, err := store.Open(ctx, id)
	if err != nil {
		t.Fatalf("open uploaded attachment: %v", err)
	}
	got, readErr := io.ReadAll(body)
	closeErr := body.Close()
	if readErr != nil {
		t.Fatalf("read uploaded attachment: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close uploaded attachment: %v", closeErr)
	}
	if string(got) != content || size != int64(len(content)) {
		t.Fatalf("uploaded content = %q, size = %d; want %q, %d", got, size, content, len(content))
	}

	signedURL, ok, err := store.DownloadURL(ctx, id, DownloadURLOpts{
		Disposition: `attachment; filename="oss-smoke.txt"`,
	})
	if err != nil || !ok {
		t.Fatalf("download URL: ok=%v err=%v", ok, err)
	}
	response, err := http.Get(signedURL)
	if err != nil {
		t.Fatalf("GET signed URL: %v", err)
	}
	defer response.Body.Close()
	downloaded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read signed URL response: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET signed URL status = %d, body = %q", response.StatusCode, downloaded)
	}
	if got := response.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("signed URL Content-Type = %q, want text/plain", got)
	}
	if got := response.Header.Get("Content-Disposition"); !strings.Contains(got, "attachment") {
		t.Fatalf("signed URL Content-Disposition = %q, want attachment", got)
	}
	if got := response.Header.Get("Cache-Control"); got != "private, max-age=600" {
		t.Fatalf("signed URL Cache-Control = %q, want private, max-age=600", got)
	}

	outsideKey := strings.TrimSuffix(prefix, "/") + "-outside-prefix-smoke"
	outsideURL, ok, err := store.signedObjectURL(ctx, outsideKey, 5*time.Minute, "attachment")
	if err != nil || !ok {
		t.Fatalf("outside-prefix URL: ok=%v err=%v", ok, err)
	}
	outsideResponse, err := http.Get(outsideURL)
	if err != nil {
		t.Fatalf("GET outside-prefix URL: %v", err)
	}
	defer outsideResponse.Body.Close()
	_, _ = io.Copy(io.Discard, outsideResponse.Body)
	if outsideResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("outside-prefix GET status = %d, want 403", outsideResponse.StatusCode)
	}
	if err := store.DeleteKey(ctx, outsideKey); err == nil {
		t.Fatal("DeleteKey accepted an object outside the system prefix")
	}
}
