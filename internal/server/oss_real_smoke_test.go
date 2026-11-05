//go:build oss_smoke

package server_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
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

	prefix, err := attachments.NormalizeOSSPrefix(os.Getenv("OSS_SMOKE_PREFIX"))
	if err != nil {
		t.Fatal(err)
	}
	ossConfig := config.OSS{
		Region: os.Getenv("OSS_SMOKE_REGION"), Bucket: os.Getenv("OSS_SMOKE_BUCKET"),
		Prefix: prefix, InternalEndpoint: os.Getenv("OSS_SMOKE_ENDPOINT"),
	}
	store, err := attachments.NewOSSStorage(attachments.OSSConfig{
		Region: ossConfig.Region, Bucket: ossConfig.Bucket, Prefix: ossConfig.Prefix,
		InternalEndpoint: ossConfig.InternalEndpoint,
	}, attachments.OSSStorageOptions{
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

	s := tbtest.Start(t,
		tbtest.WithAttachmentStorage(store),
		tbtest.WithAttachmentConfig(config.Attachments{
			Backend: attachments.AttachmentBackendOSS, OSS: ossConfig,
		}),
	)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	ref := mustCreateTicket(t, s, web, "真实 OSS 冒烟")
	content := "agent-taskboard real OSS smoke\n"
	uploaded := mustUploadAttachment(t, s, web, ref, "oss-smoke.txt", []byte(content))
	if res := claimByReply(t, s, web, ref, map[string]any{
		"attachment_ids": []string{uploaded.ID},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}

	attachmentID, err := strconv.ParseInt(strings.TrimPrefix(uploaded.ID, "att_"), 10, 64)
	if err != nil {
		t.Fatalf("解析附件 ID %q: %v", uploaded.ID, err)
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if err := store.Delete(cleanupCtx, attachmentID); err != nil {
			t.Errorf("cleanup delete attachment %d: %v", attachmentID, err)
		}
	}()

	path := "/api/v1/attachments/" + uploaded.ID + "/" + url.PathEscape("oss-smoke.txt")
	redirect := downloadAttachment(t, s, noRedirect(t, web), path)
	if redirect.StatusCode != http.StatusFound {
		body, _ := io.ReadAll(redirect.Body)
		t.Fatalf("附件下载状态码 = %d, 想 302（%s）", redirect.StatusCode, body)
	}
	if got := redirect.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("附件 302 Cache-Control = %q, want no-store", got)
	}
	signedURL := redirect.Header.Get("Location")
	if signedURL == "" {
		t.Fatal("附件 302 缺少 Location")
	}
	response, err := http.Get(signedURL)
	if err != nil {
		t.Fatalf("GET 签名地址失败：%v", err)
	}
	defer response.Body.Close()
	downloaded, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("读取签名地址响应失败：%v", err)
	}
	if response.StatusCode != http.StatusOK || string(downloaded) != content {
		t.Fatalf("签名下载 status=%d body=%q, want 200 %q", response.StatusCode, downloaded, content)
	}
	if got := response.Header.Get("Content-Type"); got != "text/plain" {
		t.Fatalf("签名下载 Content-Type = %q, want text/plain", got)
	}

	if err := store.Delete(ctx, attachmentID); err != nil {
		t.Fatalf("删除附件对象失败：%v", err)
	}
	if _, exists, err := store.Stat(ctx, attachmentID); err != nil || exists {
		t.Fatal("附件对象删除后仍然存在")
	}

	outsideKey := strings.TrimSuffix(prefix, "/") + "-outside-prefix-smoke"
	if err := store.DeleteKey(ctx, outsideKey); err == nil {
		t.Fatal("DeleteKey accepted an object outside the system prefix")
	}
}
