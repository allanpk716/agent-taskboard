package attachments_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
)

type fakeOSSRequest struct {
	Method string
	Path   string
	Query  url.Values
	Header http.Header
	Body   string
}

type fakeOSS struct {
	mu                     sync.Mutex
	server                 *httptest.Server
	requests               []fakeOSSRequest
	objects                map[string]string
	rejectPersistentHeader bool
	headChecksum           string
}

func newFakeOSS(t *testing.T) *fakeOSS {
	t.Helper()
	f := &fakeOSS{objects: map[string]string{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requests = append(f.requests, fakeOSSRequest{
			Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(),
			Header: r.Header.Clone(), Body: string(body),
		})
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/files/":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Query().Has("bucketInfo"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<Bucket><Name>files</Name></Bucket>`))
		case r.Method == http.MethodPut && r.URL.Path != "/files/":
			if f.rejectPersistentHeader && r.Header.Get("X-Oss-Persistent-Headers") != "" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.objects[r.URL.Path] = string(body)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodHead && r.URL.Path != "/files/":
			body, ok := f.objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(body)))
			if f.headChecksum != "" {
				w.Header().Set("x-oss-hash-crc64ecma", f.headChecksum)
			}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path != "/files/":
			body, ok := f.objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(body))
		case r.Method == http.MethodDelete && r.URL.Path != "/files/":
			delete(f.objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("假 OSS 收到未实现的请求：%s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

// OSS 不接受持久 nosniff 头时，启动探测会降级；后续上传仍成功，只是不带该头。
func TestOSS存储不支持持久头时降级上传(t *testing.T) {
	fake := newFakeOSS(t)
	fake.rejectPersistentHeader = true
	store, err := attachments.NewOSSStorage(attachments.OSSConfig{
		Region: "cn-hangzhou", Bucket: "files", Prefix: "agent-taskboard/prod/",
		InternalEndpoint: fake.server.URL,
	}, attachments.OSSStorageOptions{AccessKeyID: "ak", AccessKeySecret: "sk"})
	if err != nil {
		t.Fatalf("创建 OSS 存储失败：%v", err)
	}
	if err := store.Probe(context.Background()); err != nil {
		t.Fatalf("启动探测失败：%v", err)
	}
	content := "内容"
	if err := store.Put(context.Background(), 3, strings.NewReader(content), int64(len(content)),
		attachments.PutMeta{ContentType: "text/plain"}); err != nil {
		t.Fatalf("Put 失败：%v", err)
	}
	put := fake.requestByPathSuffix("/attachments/3")
	if put.Header.Get("X-Oss-Persistent-Headers") != "" {
		t.Fatalf("降级后 Put 仍带持久头：%q", put.Header.Get("X-Oss-Persistent-Headers"))
	}
}

func TestOSS签名地址默认使用桶外网域名(t *testing.T) {
	store, err := attachments.NewOSSStorage(attachments.OSSConfig{
		Region: "cn-hangzhou", Bucket: "files", Prefix: "agent-taskboard/prod/",
		InternalEndpoint: "oss-cn-hangzhou-internal.aliyuncs.com",
	}, attachments.OSSStorageOptions{AccessKeyID: "ak", AccessKeySecret: "sk"})
	if err != nil {
		t.Fatalf("创建 OSS 存储失败：%v", err)
	}
	target, ok, err := store.DownloadURL(context.Background(), 8, attachments.DownloadURLOpts{Disposition: "attachment"})
	if err != nil || !ok {
		t.Fatalf("DownloadURL = (%q, %v, %v)", target, ok, err)
	}
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("解析签名地址失败：%v", err)
	}
	if parsed.Host != "files.oss-cn-hangzhou.aliyuncs.com" {
		t.Fatalf("默认签名域名 = %q, 想桶默认外网域名", parsed.Host)
	}
}

func TestOSS存储读取对象自带CRC64(t *testing.T) {
	fake := newFakeOSS(t)
	content := "校验内容"
	fake.objects["/files/agent-taskboard/prod/attachments/7"] = content
	// 这个值来自 OSS 对 `校验内容` 返回的 x-oss-hash-crc64ecma 十进制表示。
	fake.headChecksum = "8349912128173140676"
	store, err := attachments.NewOSSStorage(attachments.OSSConfig{
		Region: "cn-hangzhou", Bucket: "files", Prefix: "agent-taskboard/prod/",
		InternalEndpoint: fake.server.URL,
	}, attachments.OSSStorageOptions{AccessKeyID: "ak", AccessKeySecret: "sk"})
	if err != nil {
		t.Fatalf("创建 OSS 存储失败：%v", err)
	}
	sum, exists, err := store.ObjectChecksum(context.Background(), 7)
	if err != nil || !exists {
		t.Fatalf("ObjectChecksum = (%d, %v, %v)", sum, exists, err)
	}
	if sum != 8349912128173140676 {
		t.Fatalf("CRC64 = %d, 想对象响应自带的值", sum)
	}
}

func (f *fakeOSS) request(i int) fakeOSSRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[i]
}

func (f *fakeOSS) requestCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeOSS) requestByPathSuffix(suffix string) fakeOSSRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, request := range f.requests {
		if strings.HasSuffix(request.Path, suffix) {
			return request
		}
	}
	return fakeOSSRequest{}
}

func TestOSS存储协议与签名地址(t *testing.T) {
	fake := newFakeOSS(t)
	now := time.Date(2026, 11, 5, 10, 12, 34, 0, time.FixedZone("CST", 8*3600))
	store, err := attachments.NewOSSStorage(attachments.OSSConfig{
		Region: "cn-hangzhou", Bucket: "files", Prefix: "agent-taskboard/prod/",
		InternalEndpoint: fake.server.URL, SigningHost: "files.example.test",
	}, attachments.OSSStorageOptions{
		AccessKeyID: "test-ak", AccessKeySecret: "test-sk", Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("创建 OSS 存储失败：%v", err)
	}
	ctx := context.Background()
	if err := store.Probe(ctx); err != nil {
		t.Fatalf("启动探测失败：%v", err)
	}
	if first := fake.request(0); first.Method != http.MethodHead || first.Path != "/files/" {
		t.Fatalf("桶探测请求 = %s %s, 想 HEAD /files/", first.Method, first.Path)
	}

	content := "附件内容"
	if err := store.Put(ctx, 7, strings.NewReader(content), int64(len(content)),
		attachments.PutMeta{ContentType: "text/plain; charset=utf-8"}); err != nil {
		t.Fatalf("Put 失败：%v", err)
	}
	put := fake.requestByPathSuffix("/attachments/7")
	if put.Method != http.MethodPut || put.Path != "/files/agent-taskboard/prod/attachments/7" {
		t.Fatalf("Put 请求 = %s %s, 想走内网 endpoint 的附件键", put.Method, put.Path)
	}
	if got := put.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Put Content-Type = %q", got)
	}
	wantPersistent := "X-Content-Type-Options:" + base64.StdEncoding.EncodeToString([]byte("nosniff"))
	if got := put.Header.Get("X-Oss-Persistent-Headers"); got != wantPersistent {
		t.Errorf("Put 持久头 = %q, 想 %q", got, wantPersistent)
	}

	body, size, err := store.Open(ctx, 7)
	if err != nil {
		t.Fatalf("Open 失败：%v", err)
	}
	defer body.Close()
	gotBody, _ := io.ReadAll(body)
	if string(gotBody) != content || size != int64(len(content)) {
		t.Fatalf("Open = (%q, %d)", gotBody, size)
	}
	if got, ok := store.Stat(ctx, 7); !ok || got != int64(len(content)) {
		t.Fatalf("Stat = (%d, %v)", got, ok)
	}

	disposition := `attachment; filename="报告.txt"`
	first, ok, err := store.DownloadURL(ctx, 7, attachments.DownloadURLOpts{Disposition: disposition})
	if err != nil || !ok {
		t.Fatalf("DownloadURL = (%q, %v, %v)", first, ok, err)
	}
	second, _, _ := store.DownloadURL(ctx, 7, attachments.DownloadURLOpts{Disposition: disposition})
	if first != second {
		t.Fatalf("同一时间桶内签名地址不稳定：\n%s\n%s", first, second)
	}
	parsed, err := url.Parse(first)
	if err != nil {
		t.Fatalf("解析签名地址失败：%v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "files.example.test" ||
		parsed.Path != "/agent-taskboard/prod/attachments/7" {
		t.Fatalf("签名地址 = %s%s, 想自定义域名和根前缀下的附件键", parsed.Host, parsed.Path)
	}
	query := parsed.Query()
	want := url.Values{
		"x-oss-date":                   []string{"20261105T021000Z"},
		"x-oss-expires":                []string{"900"},
		"x-oss-signature-version":      []string{"OSS4-HMAC-SHA256"},
		"x-oss-credential":             []string{"test-ak/20261105/cn-hangzhou/oss/aliyun_v4_request"},
		"response-content-disposition": []string{disposition},
		"response-cache-control":       []string{"private, max-age=600"},
	}
	for key, values := range want {
		if got := query[key]; len(got) != 1 || got[0] != values[0] {
			t.Errorf("签名参数 %s = %q, 想 %q", key, got, values[0])
		}
	}
	if query.Get("x-oss-signature") == "" {
		t.Error("签名地址缺少 x-oss-signature")
	}
	if _, exists := query["response-content-type"]; exists {
		t.Error("签名地址不应覆盖 Content-Type")
	}

	before := fake.requestCount()
	if err := store.DeleteKey(ctx, "other-system/important.txt"); err == nil {
		t.Fatal("删除根前缀外的键应被拒绝")
	}
	if fake.requestCount() != before {
		t.Fatal("前缀外的键不应发到 OSS")
	}
	if err := store.Delete(ctx, 7); err != nil {
		t.Fatalf("Delete 失败：%v", err)
	}
	last := fake.request(fake.requestCount() - 1)
	if last.Method != http.MethodDelete || last.Path != "/files/agent-taskboard/prod/attachments/7" {
		t.Fatalf("Delete 请求 = %s %s", last.Method, last.Path)
	}
}
