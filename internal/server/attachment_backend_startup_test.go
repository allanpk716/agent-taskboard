package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type startupFakeOSS struct {
	mu           sync.Mutex
	server       *httptest.Server
	objects      map[string]startupOSSObject
	requestPaths []string
	fail         bool
}

type startupOSSObject struct {
	Content     string
	ContentType string
}

func newStartupFakeOSS(t *testing.T) *startupFakeOSS {
	t.Helper()
	f := &startupFakeOSS{objects: map[string]startupOSSObject{}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.requestPaths = append(f.requestPaths, r.Method+" "+r.URL.Path)
		if f.fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		switch {
		case r.Method == http.MethodHead && r.URL.Path == "/files/":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Query().Has("bucketInfo"):
			_, _ = w.Write([]byte(`<Bucket><Name>files</Name></Bucket>`))
		case r.Method == http.MethodPut:
			f.objects[r.URL.Path] = startupOSSObject{Content: string(body), ContentType: r.Header.Get("Content-Type")}
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet:
			object, ok := f.objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(object.Content))
		case r.Method == http.MethodHead:
			object, ok := f.objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Length", strconv.Itoa(len(object.Content)))
		case r.Method == http.MethodDelete:
			delete(f.objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func ossAttachments(endpoint, prefix string) config.Attachments {
	return config.Attachments{
		Backend: config.AttachmentBackendOSS,
		OSS: config.OSS{
			Region: "cn-hangzhou", Bucket: "files", Prefix: prefix,
			InternalEndpoint: endpoint,
		},
	}
}

func TestOSS后端启动探测并写入后端标记(t *testing.T) {
	fake := newStartupFakeOSS(t)
	t.Setenv("OSS_ACCESS_KEY_ID", "test-ak")
	t.Setenv("OSS_ACCESS_KEY_SECRET", "test-sk")
	dataDir := t.TempDir()
	s := tbtest.Start(t,
		tbtest.WithDataDir(dataDir),
		tbtest.WithAttachmentConfig(ossAttachments(fake.server.URL, "agent-taskboard/prod/")),
	)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "OSS 上传工单")
	uploaded := mustUploadAttachment(t, s, web, number, "报告.txt", []byte("OSS 内容"))
	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{uploaded.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}

	fake.mu.Lock()
	object, ok := fake.objects["/files/agent-taskboard/prod/attachments/1"]
	paths := append([]string(nil), fake.requestPaths...)
	fake.mu.Unlock()
	if !ok || object.Content != "OSS 内容" || object.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("上传后的 OSS 对象 = (%q, %q, %v)", object.Content, object.ContentType, ok)
	}
	joined := strings.Join(paths, "\n")
	if !strings.Contains(joined, "PUT /files/agent-taskboard/prod/.taskboard-startup-probe") ||
		!strings.Contains(joined, "DELETE /files/agent-taskboard/prod/.taskboard-startup-probe") {
		t.Fatalf("启动探测请求 = %q", joined)
	}

	var backend, bucket, prefix string
	if err := s.Store().DB.QueryRow(`SELECT value FROM settings WHERE key='attachments_backend'`).Scan(&backend); err != nil || backend != "oss" {
		t.Fatalf("后端标记 = %q/%v, 想 oss", backend, err)
	}
	if err := s.Store().DB.QueryRow(`SELECT value FROM settings WHERE key='attachments_backend_bucket'`).Scan(&bucket); err != nil || bucket != "files" {
		t.Fatalf("桶标记 = %q/%v", bucket, err)
	}
	if err := s.Store().DB.QueryRow(`SELECT value FROM settings WHERE key='attachments_backend_prefix'`).Scan(&prefix); err != nil || prefix != "agent-taskboard/prod/" {
		t.Fatalf("前缀标记 = %q/%v", prefix, err)
	}
}

func TestOSS后端启动校验失败时不回退本地(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T, endpoint string) error
	}{
		{name: "缺少AK", run: func(t *testing.T, endpoint string) error {
			t.Setenv("OSS_ACCESS_KEY_ID", "")
			t.Setenv("OSS_ACCESS_KEY_SECRET", "")
			return startBackendError(t, ossAttachments(endpoint, "agent-taskboard/prod/"))
		}},
		{name: "根前缀为空", run: func(t *testing.T, endpoint string) error {
			t.Setenv("OSS_ACCESS_KEY_ID", "ak")
			t.Setenv("OSS_ACCESS_KEY_SECRET", "sk")
			return startBackendError(t, ossAttachments(endpoint, " "))
		}},
		{name: "探测失败", run: func(t *testing.T, endpoint string) error {
			t.Setenv("OSS_ACCESS_KEY_ID", "ak")
			t.Setenv("OSS_ACCESS_KEY_SECRET", "sk")
			return startBackendError(t, ossAttachments(endpoint, "agent-taskboard/prod/"))
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := newStartupFakeOSS(t)
			if tc.name == "探测失败" {
				fake.fail = true
			}
			err := tc.run(t, fake.server.URL)
			if err == nil {
				t.Fatal("启动校验失败时应拒绝启动")
			}
			want := map[string]string{
				"缺少AK":  "OSS_ACCESS_KEY_ID",
				"根前缀为空": "根前缀",
				"探测失败":  "探测",
			}[tc.name]
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("错误 = %v, 想包含 %q", err, want)
			}
		})
	}
}

func startBackendError(t *testing.T, attachments config.Attachments, dataDir ...string) error {
	t.Helper()
	var err error
	opts := []tbtest.Option{
		tbtest.WithAttachmentConfig(attachments),
		tbtest.WithStartupError(&err),
	}
	if len(dataDir) > 0 {
		opts = append(opts, tbtest.WithDataDir(dataDir[0]))
	}
	tbtest.Start(t, opts...)
	return err
}

func Test附件后端标记与配置不一致时拒绝启动(t *testing.T) {
	t.Setenv("OSS_ACCESS_KEY_ID", "ak")
	t.Setenv("OSS_ACCESS_KEY_SECRET", "sk")
	dataDir := t.TempDir()
	first := tbtest.Start(t, tbtest.WithDataDir(dataDir))
	first.Shutdown(t)

	fake := newStartupFakeOSS(t)
	err := startBackendError(t, ossAttachments(fake.server.URL, "agent-taskboard/prod/"), dataDir)
	if err == nil || !strings.Contains(err.Error(), "taskboard migrate-attachments -to oss") {
		t.Fatalf("配置为 OSS 而库里是 local 时错误 = %v, 想提示迁移命令", err)
	}
}
