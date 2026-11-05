package main

import (
	"bytes"
	"context"
	"fmt"
	"hash/crc64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test帮助输出包含附件迁移命令(t *testing.T) {
	out := &bytes.Buffer{}
	usage(out)
	got := out.String()
	for _, want := range []string{
		"taskboard migrate-attachments -to oss|local [flags]",
		"-dry-run",
		"-accept-missing",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("帮助输出缺少 %q：\n%s", want, got)
		}
	}
}

func Test附件迁移命令在服务运行时拒绝且不打开库(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	configPath := filepath.Join(dir, "config.toml")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听临时端口失败：%v", err)
	}
	defer listener.Close()
	port := listener.Addr().(*net.TCPAddr).Port
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(
		"data_dir = '%s'\nport = %d\n", filepath.ToSlash(dataDir), port,
	)), 0o644); err != nil {
		t.Fatal(err)
	}

	err = run([]string{"migrate-attachments", "-to", "oss", "-config", configPath})
	if err == nil || !strings.Contains(err.Error(), "仍有服务在监听") {
		t.Fatalf("服务运行时 err = %v, 想拒绝执行", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "taskboard.db")); !os.IsNotExist(err) {
		t.Fatalf("拒绝执行时不应打开数据库：%v", err)
	}
}

type fileSnapshot map[string][2]int64

func snapshotFiles(t *testing.T, dir string) fileSnapshot {
	t.Helper()
	snapshot := fileSnapshot{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		snapshot[filepath.ToSlash(rel)] = [2]int64{info.Size(), info.ModTime().UnixNano()}
		return nil
	})
	if err != nil {
		t.Fatalf("快照 %s 失败：%v", dir, err)
	}
	return snapshot
}

// dry-run 绝不能写任何东西：不跑迁移、不切 WAL、更不能新建库。
func Test附件迁移dryrun不写数据目录(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	ctx := context.Background()
	if _, err := st.DB.Exec(`INSERT INTO tickets (seq, number, kind, title, created_at)
		VALUES (1, '20261105-001', 'task', 'dry-run 迁移', '2026-11-05T09:00:00+08:00')`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO attachments (id, ticket_seq, filename, size, state, uploaded_at)
		VALUES (1, 1, '报告.txt', 6, 'attached', '2026-11-05T09:00:00+08:00')`); err != nil {
		t.Fatal(err)
	}
	if err := attachments.WriteBackendMarker(ctx, st.DB, attachments.BackendMarker{
		Backend: attachments.AttachmentBackendLocal,
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "attachments", "1"), []byte("内容"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "2")
			w.Header().Set("x-oss-hash-crc64ecma", fmt.Sprint(crc64.Checksum([]byte("内容"), crc64.MakeTable(crc64.ECMA))))
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer fake.Close()
	configPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`data_dir = '%s'
port = 1
[attachments]
backend = 'local'
[attachments.oss]
region = 'cn-hangzhou'
bucket = 'files'
prefix = 'agent-taskboard/prod/'
internal_endpoint = '%s'
`, filepath.ToSlash(dataDir), fake.URL)), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSS_ACCESS_KEY_ID", "test-ak")
	t.Setenv("OSS_ACCESS_KEY_SECRET", "test-sk")

	before := snapshotFiles(t, dataDir)
	if err := run([]string{"migrate-attachments", "-to", "oss", "-dry-run", "-config", configPath}); err != nil {
		t.Fatalf("dry-run 失败：%v", err)
	}
	if after := snapshotFiles(t, dataDir); !reflect.DeepEqual(after, before) {
		t.Fatalf("dry-run 改动了数据目录：\n之前 %+v\n之后 %+v", before, after)
	}

	// 库不存在时直接报错，不能新建一个空库再失败。
	empty := t.TempDir()
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(
		"data_dir = '%s'\nport = 1\n", filepath.ToSlash(empty))), 0o644); err != nil {
		t.Fatal(err)
	}
	err = run([]string{"migrate-attachments", "-to", "oss", "-dry-run", "-config", configPath})
	if err == nil || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("库不存在时 dry-run err = %v，想直接报错", err)
	}
	if _, err := os.Stat(filepath.Join(empty, "taskboard.db")); !os.IsNotExist(err) {
		t.Fatalf("dry-run 不应新建数据库：%v", err)
	}
}

func Test附件迁移命令迁到假OSS并保留配置(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "attachments"), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dataDir)
	if err != nil {
		t.Fatalf("打开数据库失败：%v", err)
	}
	if _, err := st.DB.Exec(`INSERT INTO tickets (seq, number, kind, title, created_at)
		VALUES (1, '20261105-001', 'task', 'CLI 迁移', '2026-11-05T09:00:00+08:00')`); err != nil {
		t.Fatalf("造工单失败：%v", err)
	}
	content := "CLI迁移附件内容"
	if _, err := st.DB.Exec(`INSERT INTO attachments (id, ticket_seq, filename, size, state, uploaded_at)
		VALUES (1, 1, '报告.txt', ?, 'attached', '2026-11-05T09:00:00+08:00')`, len(content)); err != nil {
		t.Fatalf("造附件失败：%v", err)
	}
	ctx := context.Background()
	if err := attachments.WriteBackendMarker(ctx, st.DB, attachments.BackendMarker{
		Backend: attachments.AttachmentBackendLocal,
	}); err != nil {
		t.Fatalf("写入标记失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "attachments", "1"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	objects := map[string]string{}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		switch r.Method {
		case http.MethodPut:
			objects[r.URL.Path] = string(body)
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			delete(objects, r.URL.Path)
			w.WriteHeader(http.StatusNoContent)
		case http.MethodHead:
			if r.URL.Path == "/files/" {
				w.WriteHeader(http.StatusOK)
				return
			}
			object, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			sum := crc64.Checksum([]byte(object), crc64.MakeTable(crc64.ECMA))
			w.Header().Set("Content-Length", fmt.Sprint(len(object)))
			w.Header().Set("x-oss-hash-crc64ecma", fmt.Sprint(sum))
			w.WriteHeader(http.StatusOK)
		case http.MethodGet:
			path := r.URL.Path
			if !strings.HasPrefix(path, "/files/") {
				path = "/files" + path
			}
			object, ok := objects[path]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_, _ = w.Write([]byte(object))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer fake.Close()

	configPath := filepath.Join(dir, "config.toml")
	configToml := fmt.Sprintf(`data_dir = '%s'
port = 1
[attachments]
backend = 'local'
[attachments.oss]
region = 'cn-hangzhou'
bucket = 'files'
prefix = 'agent-taskboard/prod/'
internal_endpoint = '%s'
`, filepath.ToSlash(dataDir), fake.URL)
	if err := os.WriteFile(configPath, []byte(configToml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OSS_ACCESS_KEY_ID", "test-ak")
	t.Setenv("OSS_ACCESS_KEY_SECRET", "test-sk")

	if err := run([]string{"migrate-attachments", "-to", "oss", "-config", configPath}); err != nil {
		t.Fatalf("迁移命令失败：%v", err)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || string(after) != configToml {
		t.Fatalf("迁移命令不应改配置文件：%v", err)
	}
	mu.Lock()
	object := objects["/files/agent-taskboard/prod/attachments/1"]
	mu.Unlock()
	if object != content {
		t.Fatalf("OSS 对象 = %q", object)
	}
	if local, err := os.ReadFile(filepath.Join(dataDir, "attachments", "1")); err != nil || string(local) != content {
		t.Fatalf("本地源对象 = (%q, %v), 想保留", local, err)
	}
	st, err = store.Open(dataDir)
	if err != nil {
		t.Fatalf("重新打开数据库失败：%v", err)
	}
	defer st.Close()
	marker, exists, err := attachments.ReadBackendMarker(ctx, st.DB)
	if err != nil || !exists || marker.Backend != attachments.AttachmentBackendOSS ||
		marker.Bucket != "files" || marker.Prefix != "agent-taskboard/prod/" {
		t.Fatalf("迁移后标记 = (%+v, %v, %v)", marker, exists, err)
	}

	s := tbtest.Start(t,
		tbtest.WithDataDir(dataDir),
		tbtest.WithAttachmentConfig(config.Attachments{
			Backend: attachments.AttachmentBackendOSS,
			OSS: config.OSS{
				Region: "cn-hangzhou", Bucket: "files", Prefix: "agent-taskboard/prod/",
				InternalEndpoint: fake.URL, SigningHost: fake.URL,
			},
		}),
	)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	download := s.URL() + "/api/v1/attachments/att_1/" + url.PathEscape("报告.txt")
	res, err := web.Get(download)
	if err != nil {
		t.Fatalf("按新后端下载失败：%v", err)
	}
	defer res.Body.Close()
	got, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || string(got) != content {
		t.Fatalf("新后端下载 = (%d, %q), 想 (200, %q)", res.StatusCode, got, content)
	}
}
