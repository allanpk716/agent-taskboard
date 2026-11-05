package server_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type slimPackageObjectListBody struct {
	Count    int               `json:"count"`
	Packages []slimPackageBody `json:"packages"`
}

// 假存储下完成归档的生成、移除与导入：附件对象始终经存储接口进出，
// 包内容和导入后的对象保持一致。
func Test内存假存储下归档生成移除导入走存储接口(t *testing.T) {
	mem := attachments.NewMemoryStorage()
	s := tbtest.Start(t, tbtest.WithAttachmentStorage(mem))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "假存储归档工单")
	content := []byte("memory archive attachment")
	uploaded := mustUploadAttachment(t, s, admin, number, "归档.txt", content)
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见附件。",
		"attachment_ids": []string{uploaded.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d, 想 200", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")

	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	if _, exists := mem.ObjectByKey("archive-packages/" + pkg.Name); exists {
		t.Fatal("本地后端生成归档包不应上传 OSS 对象")
	}
	pkgRes := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if pkgRes.StatusCode != http.StatusOK {
		t.Fatalf("下载归档包状态码 = %d, 想 200", pkgRes.StatusCode)
	}
	zipBytes, err := io.ReadAll(pkgRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	files := zipFiles(t, zipBytes)
	attachmentPath := "tickets/" + number + "/attachments/" + uploaded.ID + "/归档.txt"
	if string(files[attachmentPath]) != string(content) {
		t.Fatalf("归档包里的附件内容不符：%q", files[attachmentPath])
	}

	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatalf("生成附件镜像失败：%v", err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档状态码 = %d, 想 200", res.StatusCode)
	}
	id, err := attachments.ParseRef(uploaded.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := mem.Object(id); exists {
		t.Fatalf("归档移除后附件 %s 应从存储删除", uploaded.ID)
	}

	res, imported := importArchive(t, s, admin, zipBytes, number)
	if res.StatusCode != http.StatusOK || len(imported.Imported) != 1 || imported.Imported[0].Number != number {
		t.Fatalf("导入归档 = %d/%+v, 想 200 且导入 %s", res.StatusCode, imported, number)
	}
	obj, exists := mem.Object(id)
	if !exists {
		t.Fatalf("导入归档后附件 %s 应写回存储", uploaded.ID)
	}
	if string(obj.Content) != string(content) || obj.Size != int64(len(content)) {
		t.Fatalf("导入后的附件对象不对：size=%d content=%q", obj.Size, obj.Content)
	}
	if obj.ContentType != "text/plain; charset=utf-8" {
		t.Fatalf("导入后的 Content-Type = %q, 想按文件名推断", obj.ContentType)
	}
}

func startOSSPackageServer(t *testing.T) (*tbtest.Server, *attachments.MemoryStorage, *http.Client, string, string) {
	t.Helper()
	mem := attachments.NewMemoryStorage()
	mem.DownloadURLPrefix = "https://oss.example.test/signed/"
	s := tbtest.Start(t,
		tbtest.WithAttachmentStorage(mem),
		tbtest.WithAttachmentConfig(config.Attachments{
			Backend: config.AttachmentBackendOSS,
			OSS: config.OSS{
				Region: "cn-hangzhou", Bucket: "files", Prefix: "agent-taskboard/prod/",
				InternalEndpoint: "oss-cn-hangzhou-internal.aliyuncs.com",
			},
		}),
	)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "OSS 包工单")
	uploaded := mustUploadAttachment(t, s, admin, number, "服务日志.log", []byte("attachment data"))
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见附件。",
		"attachment_ids": []string{uploaded.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂附件状态码 = %d, 想 200", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	return s, mem, s.Login("陈工", "secret-pass"), number, uploaded.ID
}

func assertPackageObjectAndRedirect(t *testing.T, mem *attachments.MemoryStorage, localDir, name, downloadURL string,
	client *http.Client, serverURL string) {
	t.Helper()
	relKey := "archive-packages/" + name
	obj, exists := mem.ObjectByKey(relKey)
	if !exists {
		t.Fatalf("OSS 后端生成包 %s 后应有对象 %s", name, relKey)
	}
	local, err := os.ReadFile(filepath.Join(localDir, name))
	if err != nil {
		t.Fatalf("读取本地权威包失败：%v", err)
	}
	if string(obj.Content) != string(local) {
		t.Fatal("OSS 包对象应与本地权威包一致")
	}
	res, err := noRedirect(t, client).Get(serverURL + downloadURL)
	if err != nil {
		t.Fatalf("下载包失败：%v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusFound {
		t.Fatalf("下载包状态码 = %d, 想 302", res.StatusCode)
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("下载包 Cache-Control = %q, 想 no-store", res.Header.Get("Cache-Control"))
	}
	location, err := url.Parse(res.Header.Get("Location"))
	if err != nil {
		t.Fatalf("解析包跳转地址失败：%v", err)
	}
	if location.Path != "/signed/"+relKey ||
		!strings.HasPrefix(location.Query().Get("disposition"), "attachment") {
		t.Fatalf("包跳转地址 = %s, 想指向 archive-packages 且带 attachment", location)
	}
}

func TestOSS后端归档包生成下载与七天清理(t *testing.T) {
	s, mem, admin, number, _ := startOSSPackageServer(t)

	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	assertPackageObjectAndRedirect(t, mem, filepath.Join(s.DataDir(), "archive-packages"),
		pkg.Name, pkg.DownloadURL, admin, s.URL())

	s.Clock().Set(time.Date(2026, time.November, 7, 10, 1, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	var list archivePackageListBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/archive/packages", nil, &list); res.StatusCode != http.StatusOK {
		t.Fatalf("清理后归档列表状态码 = %d, 想 200", res.StatusCode)
	}
	if _, exists := mem.ObjectByKey("archive-packages/" + pkg.Name); exists {
		t.Fatal("归档包过期后 OSS 副本应删除")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "archive-packages", pkg.Name)); !os.IsNotExist(err) {
		t.Fatalf("归档包过期后本地副本应删除：err=%v", err)
	}
}

func TestOSS后端瘦身包生成下载与七天清理(t *testing.T) {
	s, mem, admin, number, _ := startOSSPackageServer(t)

	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d, 想 201", res.StatusCode)
	}
	assertPackageObjectAndRedirect(t, mem, filepath.Join(s.DataDir(), "slim-packages"),
		pkg.Name, pkg.DownloadURL, admin, s.URL())

	s.Clock().Set(time.Date(2026, time.November, 7, 10, 1, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	var list slimPackageObjectListBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/slim/packages", nil, &list); res.StatusCode != http.StatusOK {
		t.Fatalf("清理后瘦身列表状态码 = %d, 想 200", res.StatusCode)
	}
	if _, exists := mem.ObjectByKey("archive-packages/" + pkg.Name); exists {
		t.Fatal("瘦身包过期后 OSS 副本应删除")
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "slim-packages", pkg.Name)); !os.IsNotExist(err) {
		t.Fatalf("瘦身包过期后本地副本应删除：err=%v", err)
	}
}

func TestOSS后端归档移除删除失败仍完成(t *testing.T) {
	s, mem, admin, number, ref := startOSSPackageServer(t)

	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	id, err := attachments.ParseRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	mem.DeleteErr = errors.New("injected delete failure")

	var removed archiveRemoveBody
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, &removed); res.StatusCode != http.StatusOK {
		t.Fatalf("归档移除状态码 = %d, 想 200", res.StatusCode)
	}
	if removed.CleanupFailures != 1 {
		t.Fatalf("清理失败数 = %d, 想 1", removed.CleanupFailures)
	}
	if _, exists := mem.Object(id); !exists {
		t.Fatal("注入删除失败后对象应留给每日对账清理")
	}
	var detail errorBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &detail); res.StatusCode != http.StatusGone ||
		detail.Error.Code != "ticket_archived" {
		t.Fatalf("删除失败不影响归档完成，工单响应 = %d/%s", res.StatusCode, detail.Error.Code)
	}
}

func TestOSS后端瘦身移出删除失败仍完成(t *testing.T) {
	s, mem, admin, number, ref := startOSSPackageServer(t)

	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d, 想 201", res.StatusCode)
	}
	id, err := attachments.ParseRef(ref)
	if err != nil {
		t.Fatal(err)
	}
	mem.DeleteErr = errors.New("injected delete failure")

	var removed slimRemoveBody
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, &removed); res.StatusCode != http.StatusOK {
		t.Fatalf("瘦身移出状态码 = %d, 想 200", res.StatusCode)
	}
	if removed.CleanupFailures != 1 {
		t.Fatalf("清理失败数 = %d, 想 1", removed.CleanupFailures)
	}
	if _, exists := mem.Object(id); !exists {
		t.Fatal("注入删除失败后对象应留给每日对账清理")
	}
	var slimmedAt *string
	if err := s.Store().DB.QueryRow(`SELECT slim_removed_at FROM attachments WHERE id = ?`, id).Scan(&slimmedAt); err != nil || slimmedAt == nil {
		t.Fatalf("删除失败不影响瘦身状态：row=%v err=%v", slimmedAt, err)
	}
}

func TestOSS后端归档导入与瘦身恢复写回当前后端(t *testing.T) {
	s, mem, admin, number, ref := startOSSPackageServer(t)
	id, err := attachments.ParseRef(ref)
	if err != nil {
		t.Fatal(err)
	}

	var archivePkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &archivePkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	archiveBody, err := os.ReadFile(filepath.Join(s.DataDir(), "archive-packages", archivePkg.Name))
	if err != nil {
		t.Fatal(err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+archivePkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("归档移除状态码 = %d, 想 200", res.StatusCode)
	}
	if _, exists := mem.Object(id); exists {
		t.Fatal("归档移除后附件对象应删除")
	}
	res, imported := importArchive(t, s, admin, archiveBody, number)
	if res.StatusCode != http.StatusOK || len(imported.Imported) != 1 {
		t.Fatalf("导入归档 = %d/%+v, 想 200 且导入一张", res.StatusCode, imported)
	}
	if _, exists := mem.Object(id); !exists {
		t.Fatal("导入归档后附件应写回 OSS 假后端")
	}
	attachmentPath := "/api/v1/attachments/" + ref + "/" + url.PathEscape("服务日志.log")
	attRes, err := noRedirect(t, admin).Get(s.URL() + attachmentPath)
	if err != nil || attRes.StatusCode != http.StatusFound {
		t.Fatalf("导入归档后附件下载状态码 = %d, 想 302", attRes.StatusCode)
	}

	var slimPkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &slimPkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d, 想 201", res.StatusCode)
	}
	slimBody, err := os.ReadFile(filepath.Join(s.DataDir(), "slim-packages", slimPkg.Name))
	if err != nil {
		t.Fatal(err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+slimPkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("瘦身移出状态码 = %d, 想 200", res.StatusCode)
	}
	if _, exists := mem.Object(id); exists {
		t.Fatal("瘦身移出后附件对象应删除")
	}
	res, restored := restoreSlim(t, s, admin, slimBody, number)
	if res.StatusCode != http.StatusOK || len(restored.Restored) != 1 {
		t.Fatalf("恢复瘦身 = %d/%+v, 想 200 且恢复一张", res.StatusCode, restored)
	}
	obj, exists := mem.Object(id)
	if !exists || string(obj.Content) != "attachment data" {
		t.Fatalf("恢复瘦身后附件对象不存在或内容不符：exists=%v obj=%+v", exists, obj)
	}
	attRes, err = noRedirect(t, admin).Get(s.URL() + attachmentPath)
	if err != nil || attRes.StatusCode != http.StatusFound {
		t.Fatalf("恢复瘦身后附件下载状态码 = %d, 想 302", attRes.StatusCode)
	}
}
