package attachments_test

import (
	"context"
	"io"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
)

func TestMemoryStorage按相对键操作非附件对象(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	mem.DownloadURLPrefix = "https://oss.example.test/signed/"
	var objects attachments.ObjectStorage = mem
	ctx := context.Background()
	relKey := "archive-packages/archive-example.zip"
	content := "package content"

	if err := objects.PutObject(ctx, relKey, strings.NewReader(content), int64(len(content)),
		attachments.PutMeta{ContentType: "application/zip"}); err != nil {
		t.Fatalf("PutObject 失败：%v", err)
	}
	obj, exists := mem.ObjectByKey(relKey)
	if !exists || obj.ContentType != "application/zip" || string(obj.Content) != content {
		t.Fatalf("PutObject 后对象不存在或内容不符：exists=%v obj=%+v", exists, obj)
	}

	body, size, err := objects.OpenObject(ctx, relKey)
	if err != nil {
		t.Fatalf("OpenObject 失败：%v", err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	if string(got) != content || size != int64(len(content)) {
		t.Fatalf("OpenObject = (%q, %d), 想原内容", got, size)
	}

	target, ok, err := objects.ObjectURL(ctx, relKey, 15*time.Minute, "attachment; filename=a.zip")
	if err != nil || !ok {
		t.Fatalf("ObjectURL = (%q, %v, %v), 想可用地址", target, ok, err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("解析 ObjectURL 失败：%v", err)
	}
	if parsed.Path != "/signed/archive-packages/archive-example.zip" ||
		parsed.Query().Get("disposition") != "attachment; filename=a.zip" ||
		parsed.Query().Get("ttl") != "900" {
		t.Fatalf("ObjectURL = %s, 想带相对键、Disposition 和 TTL", target)
	}

	if err := objects.DeleteObject(ctx, relKey); err != nil {
		t.Fatalf("DeleteObject 失败：%v", err)
	}
	if _, exists := mem.ObjectByKey(relKey); exists {
		t.Fatal("DeleteObject 后对象仍存在")
	}
}

func TestOSS存储按相对键操作非附件对象(t *testing.T) {
	fake := newFakeOSS(t)
	now := time.Date(2026, time.November, 5, 11, 0, 0, 0, time.UTC)
	store, err := attachments.NewOSSStorage(attachments.OSSConfig{
		Region: "cn-hangzhou", Bucket: "files", Prefix: "agent-taskboard/prod/",
		InternalEndpoint: fake.server.URL, SigningHost: "files.example.test",
	}, attachments.OSSStorageOptions{
		AccessKeyID: "test-ak", AccessKeySecret: "test-sk", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("创建 OSS 存储失败：%v", err)
	}
	var objects attachments.ObjectStorage = store
	ctx := context.Background()
	relKey := "archive-packages/archive-example.zip"
	content := "oss package"

	if err := objects.PutObject(ctx, relKey, strings.NewReader(content), int64(len(content)),
		attachments.PutMeta{ContentType: "application/zip"}); err != nil {
		t.Fatalf("PutObject 失败：%v", err)
	}
	put := fake.requestByPathSuffix("/agent-taskboard/prod/archive-packages/archive-example.zip")
	if put.Method != "PUT" || put.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("PutObject 请求 = %s %q, 想写根前缀下的对象", put.Method, put.Header.Get("Content-Type"))
	}

	body, size, err := objects.OpenObject(ctx, relKey)
	if err != nil {
		t.Fatalf("OpenObject 失败：%v", err)
	}
	defer body.Close()
	got, _ := io.ReadAll(body)
	if string(got) != content || size != int64(len(content)) {
		t.Fatalf("OpenObject = (%q, %d), 想原内容", got, size)
	}

	target, ok, err := objects.ObjectURL(ctx, relKey, 10*time.Minute, "attachment; filename=a.zip")
	if err != nil || !ok {
		t.Fatalf("ObjectURL = (%q, %v, %v), 想可用地址", target, ok, err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("解析 ObjectURL 失败：%v", err)
	}
	if parsed.Path != "/agent-taskboard/prod/archive-packages/archive-example.zip" ||
		parsed.Query().Get("x-oss-expires") != "600" ||
		parsed.Query().Get("response-content-disposition") != "attachment; filename=a.zip" ||
		parsed.Query().Get("x-oss-signature") == "" {
		t.Fatalf("ObjectURL = %s, 想复用附件签名逻辑", target)
	}

	before := fake.requestCount()
	if err := objects.DeleteObject(ctx, "../other-system/object.txt"); err == nil {
		t.Fatal("相对键走出根前缀应被拒绝")
	}
	if fake.requestCount() != before {
		t.Fatal("越界 DeleteObject 不应发到 OSS")
	}
	if err := objects.DeleteObject(ctx, relKey); err != nil {
		t.Fatalf("DeleteObject 失败：%v", err)
	}
}
