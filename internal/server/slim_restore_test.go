// 瘦身恢复接口（#285）：黑盒测试只通过 HTTP、上传的 zip 与公开数据目录观察。
package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type slimRestoreCandidateBody struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	AttachmentCount int    `json:"attachment_count"`
	AttachmentBytes int64  `json:"attachment_bytes"`
	Restorable      bool   `json:"restorable"`
	Reason          string `json:"reason"`
}

type slimRestoreInspectionBody struct {
	FormatVersion   int                        `json:"format_version"`
	PackageName     string                     `json:"package_name"`
	GeneratedAt     string                     `json:"generated_at"`
	TicketCount     int                        `json:"ticket_count"`
	AttachmentCount int                        `json:"attachment_count"`
	AttachmentBytes int64                      `json:"attachment_bytes"`
	Tickets         []slimRestoreCandidateBody `json:"tickets"`
}

type slimRestoreResultBody struct {
	PackageName string                     `json:"package_name"`
	RestoredAt  string                     `json:"restored_at"`
	Restored    []slimRestoreCandidateBody `json:"restored"`
	Rejected    []slimRestoreCandidateBody `json:"rejected"`
}

type slimRestoreFixture struct {
	s       *tbtest.Server
	admin   *http.Client
	pkg     slimPackageBody
	body    []byte
	numbers []string
	atts    map[string]attachmentUploadBody
}

func postSlimRestore(t testing.TB, s *tbtest.Server, c *http.Client, path string,
	pkg []byte, numbers ...string) (*http.Response, slimRestoreInspectionBody) {
	t.Helper()
	res := sendSlimRestore(t, s, c, path, pkg, numbers...)
	var out slimRestoreInspectionBody
	if res.StatusCode == http.StatusOK {
		tbtest.DecodeJSON(t, res, &out)
	}
	return res, out
}

func sendSlimRestore(t testing.TB, s *tbtest.Server, c *http.Client, path string,
	pkg []byte, numbers ...string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	file, err := mw.CreateFormFile("package", "slim.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(pkg); err != nil {
		t.Fatal(err)
	}
	for _, number := range numbers {
		if err := mw.WriteField("ticket_numbers", number); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.URL()+path, &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return res
}

func restoreSlim(t testing.TB, s *tbtest.Server, c *http.Client, pkg []byte,
	numbers ...string) (*http.Response, slimRestoreResultBody) {
	t.Helper()
	res := sendSlimRestore(t, s, c, "/api/v1/admin/slim/restore", pkg, numbers...)
	var out slimRestoreResultBody
	if res.StatusCode == http.StatusOK {
		tbtest.DecodeJSON(t, res, &out)
	}
	return res, out
}

func buildRemovedSlimPackage(t testing.TB, title string) (*tbtest.Server, *http.Client,
	slimPackageBody, []byte, string, attachmentUploadBody) {
	f := buildRemovedSlimPackages(t, title)
	return f.s, f.admin, f.pkg, f.body, f.numbers[0], f.atts[f.numbers[0]]
}

func buildRemovedSlimPackages(t testing.TB, titles ...string) *slimRestoreFixture {
	t.Helper()
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	f := &slimRestoreFixture{s: s, admin: admin, atts: map[string]attachmentUploadBody{}}
	for i, title := range titles {
		number := mustCreateTicket(t, s, admin, title)
		att := mustUploadAttachment(t, s, admin, number, "服务日志.log", []byte(fmt.Sprintf("restore-%d", i+1)))
		if res := revise(t, s, admin, number, map[string]any{
			"brief_md":       "见 [服务日志](att:" + att.ID + ")。",
			"attachment_ids": []string{att.ID},
			"base_revision":  1,
		}); res.StatusCode != http.StatusOK {
			t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
		}
		if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
			t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
		}
		f.numbers = append(f.numbers, number)
		f.atts[number] = att
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")
	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": f.numbers}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}
	pkgRes := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if pkgRes.StatusCode != http.StatusOK {
		t.Fatalf("下载瘦身包状态码 = %d", pkgRes.StatusCode)
	}
	zipBytes, err := io.ReadAll(pkgRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身移出状态码 = %d", res.StatusCode)
	}
	f.pkg, f.body = pkg, zipBytes
	return f
}

func Test检查瘦身包列出可恢复工单(t *testing.T) {
	s, admin, pkg, zipBytes, number, _ := buildRemovedSlimPackage(t, "待恢复瘦身附件")

	res, got := postSlimRestore(t, s, admin, "/api/v1/admin/slim/restore/inspect", zipBytes)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("检查瘦身包状态码 = %d", res.StatusCode)
	}
	if got.PackageName != pkg.Name || got.FormatVersion != 1 || got.TicketCount != 1 ||
		got.AttachmentCount != 1 || got.AttachmentBytes != int64(len("restore-1")) {
		t.Fatalf("检查结果概要 = %+v，想包 %s 里的 1 个附件", got, pkg.Name)
	}
	if len(got.Tickets) != 1 || got.Tickets[0].Number != number ||
		got.Tickets[0].Title != "待恢复瘦身附件" || !got.Tickets[0].Restorable ||
		got.Tickets[0].Reason != "" {
		t.Fatalf("可恢复工单 = %+v，想 %s 且可恢复", got.Tickets, number)
	}
}

func Test恢复瘦身包按工单写回附件并进入备份镜像(t *testing.T) {
	s, admin, pkg, zipBytes, number, att := buildRemovedSlimPackage(t, "恢复后可下载")

	res, got := restoreSlim(t, s, admin, zipBytes, number)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("恢复瘦身包状态码 = %d", res.StatusCode)
	}
	if got.PackageName != pkg.Name || len(got.Restored) != 1 || got.Restored[0].Number != number ||
		len(got.Rejected) != 0 || got.RestoredAt == "" {
		t.Fatalf("恢复结果 = %+v，想恢复 %s", got, number)
	}
	download := s.Do(t, admin, http.MethodGet,
		"/api/v1/attachments/"+att.ID+"/"+att.Filename, nil, nil)
	if download.StatusCode != http.StatusOK {
		t.Fatalf("恢复后下载附件状态码 = %d", download.StatusCode)
	}
	if body := string(readAll(t, download)); body != "restore-1" {
		t.Fatalf("恢复后的附件内容 = %q", body)
	}
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	mirror, err := os.ReadFile(filepath.Join(s.Backups().TargetDir(), "attachments", strings.TrimPrefix(att.ID, "att_")))
	if err != nil || string(mirror) != "restore-1" {
		t.Fatalf("恢复附件应进入下一次备份镜像：%v / %q", err, mirror)
	}

	var operations adminOperationsBody
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/operations?limit=20", nil, &operations)
	found := false
	for _, op := range operations.Operations {
		if op.OperationType == "slim_restore" && op.PackageName == pkg.Name &&
			len(op.TicketNumbers) == 1 && op.TicketNumbers[0] == number && op.OperatorName == "陈工" {
			found = true
		}
	}
	if !found {
		t.Fatalf("没有 slim_restore 操作记录：%+v", operations.Operations)
	}

	inspectRes, inspection := postSlimRestore(t, s, admin, "/api/v1/admin/slim/restore/inspect", zipBytes)
	if inspectRes.StatusCode != http.StatusOK || len(inspection.Tickets) != 1 ||
		inspection.Tickets[0].Restorable || !strings.Contains(inspection.Tickets[0].Reason, "附件已在服务器上") {
		t.Fatalf("重复恢复检查 = %d/%+v，想提示附件已在服务器上", inspectRes.StatusCode, inspection)
	}
}

// S3：spec 故事 27 要求「按工单逐张选择恢复」，空选择不允许代表整包恢复。
func Test恢复瘦身包空选择返回400(t *testing.T) {
	s, admin, _, zipBytes, _, _ := buildRemovedSlimPackage(t, "空选择恢复")

	res, _ := restoreSlim(t, s, admin, zipBytes)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("空选择恢复状态码 = %d，想 400", res.StatusCode)
	}
	var rejected errorBody
	tbtest.DecodeJSON(t, res, &rejected)
	if rejected.Error.Code != "slim_ticket_selection_required" {
		t.Fatalf("空选择恢复错误码 = %q，想 slim_ticket_selection_required", rejected.Error.Code)
	}
	// 被拒绝后不应恢复任何文件：重新检查仍显示可恢复。
	inspectRes, inspection := postSlimRestore(t, s, admin, "/api/v1/admin/slim/restore/inspect", zipBytes)
	if inspectRes.StatusCode != http.StatusOK || len(inspection.Tickets) != 1 || !inspection.Tickets[0].Restorable {
		t.Fatalf("空选择被拒后检查 = %d/%+v，想仍可恢复", inspectRes.StatusCode, inspection)
	}
}

func Test恢复瘦身包只恢复勾选工单(t *testing.T) {
	f := buildRemovedSlimPackages(t, "部分恢复一", "部分恢复二")
	first, second := f.numbers[0], f.numbers[1]

	res, got := restoreSlim(t, f.s, f.admin, f.body, first)
	if res.StatusCode != http.StatusOK || len(got.Restored) != 1 || got.Restored[0].Number != first ||
		len(got.Rejected) != 0 {
		t.Fatalf("按工单恢复结果 = %d/%+v，想只恢复 %s", res.StatusCode, got, first)
	}
	firstPath := filepath.Join(f.s.DataDir(), "attachments", strings.TrimPrefix(f.atts[first].ID, "att_"))
	secondPath := filepath.Join(f.s.DataDir(), "attachments", strings.TrimPrefix(f.atts[second].ID, "att_"))
	if body, err := os.ReadFile(firstPath); err != nil || string(body) != "restore-1" {
		t.Fatalf("勾选工单附件应恢复：%v / %q", err, body)
	}
	if _, err := os.Stat(secondPath); !os.IsNotExist(err) {
		t.Fatalf("未勾选工单附件不应恢复：%v", err)
	}

	res, got = restoreSlim(t, f.s, f.admin, f.body, second)
	if res.StatusCode != http.StatusOK || len(got.Restored) != 1 || got.Restored[0].Number != second {
		t.Fatalf("恢复剩余工单结果 = %d/%+v", res.StatusCode, got)
	}
	if body, err := os.ReadFile(secondPath); err != nil || string(body) != "restore-2" {
		t.Fatalf("剩余附件应恢复：%v / %q", err, body)
	}
	var list struct {
		Packages []slimPackageBody `json:"packages"`
	}
	f.s.Do(t, f.admin, http.MethodGet, "/api/v1/admin/slim/packages", nil, &list)
	found := false
	for _, pkg := range list.Packages {
		if pkg.Name == f.pkg.Name {
			found = true
			if pkg.RemovedCount != 0 {
				t.Fatalf("全部恢复后瘦身移出计数 = %d，想 0", pkg.RemovedCount)
			}
		}
	}
	if !found {
		t.Fatalf("瘦身包列表缺少 %s：%+v", f.pkg.Name, list)
	}
}

func Test检查瘦身包给出不可恢复原因并拒绝危险包(t *testing.T) {
	f := buildRemovedSlimPackages(t, "哈希不符")
	number := f.numbers[0]
	files := zipFiles(t, f.body)
	var manifest map[string]any
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	filesList, _ := manifest["files"].([]any)
	fileEntry, _ := filesList[0].(map[string]any)
	path, _ := fileEntry["path"].(string)
	files[path] = []byte("被篡改的附件")
	hashMismatch := writeZipFiles(t, files)
	res, got := postSlimRestore(t, f.s, f.admin, "/api/v1/admin/slim/restore/inspect", hashMismatch)
	if res.StatusCode != http.StatusOK || len(got.Tickets) != 1 || got.Tickets[0].Restorable ||
		!strings.Contains(got.Tickets[0].Reason, "哈希") {
		t.Fatalf("哈希不符检查 = %d/%+v，想按工单提示不可恢复", res.StatusCode, got)
	}

	files = zipFiles(t, f.body)
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	filesList, _ = manifest["files"].([]any)
	fileEntry, _ = filesList[0].(map[string]any)
	fileEntry["ticket_number"] = "20990101-999"
	fileEntry["path"] = strings.Replace(path, number, "20990101-999", 1)
	body := files[path]
	delete(files, path)
	files[fileEntry["path"].(string)] = body
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files["manifest.json"] = encoded
	missingTicket := writeZipFiles(t, files)
	res, got = postSlimRestore(t, f.s, f.admin, "/api/v1/admin/slim/restore/inspect", missingTicket)
	if res.StatusCode != http.StatusOK || len(got.Tickets) != 1 || got.Tickets[0].Restorable ||
		!strings.Contains(got.Tickets[0].Reason, "工单已不在平台") {
		var rejected errorBody
		if res.StatusCode != http.StatusOK {
			tbtest.DecodeJSON(t, res, &rejected)
		}
		t.Fatalf("工单缺失检查 = %d/%+v/%+v", res.StatusCode, got, rejected)
	}

	for name, tc := range map[string]struct {
		mutate func()
		code   string
	}{
		"恶意路径": {
			mutate: func() {
				fileEntry["path"] = "../outside/" + fileEntry["attachment_id"].(string) + "/" + fileEntry["filename"].(string)
				body := files[path]
				delete(files, path)
				files[fileEntry["path"].(string)] = body
			},
			code: "slim_package_invalid",
		},
		"高版本": {
			mutate: func() {
				manifest["format_version"] = 2
			},
			code: "slim_format_unsupported",
		},
		"超大声明": {
			mutate: func() {
				fileEntry["size"] = 1<<30 + 1
			},
			code: "slim_package_invalid",
		},
	} {
		files = zipFiles(t, f.body)
		if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
			t.Fatal(err)
		}
		filesList = manifest["files"].([]any)
		fileEntry, _ = filesList[0].(map[string]any)
		path, _ = fileEntry["path"].(string)
		tc.mutate()
		encoded, err := json.Marshal(manifest)
		if err != nil {
			t.Fatal(err)
		}
		files["manifest.json"] = encoded
		res, _ := postSlimRestore(t, f.s, f.admin,
			"/api/v1/admin/slim/restore/inspect", writeZipFiles(t, files))
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s检查状态码 = %d，想 422", name, res.StatusCode)
			continue
		}
		var errBody errorBody
		tbtest.DecodeJSON(t, res, &errBody)
		if errBody.Error.Code != tc.code {
			t.Errorf("%s错误码 = %q，想 %q", name, errBody.Error.Code, tc.code)
		}
	}
}

func Test恢复瘦身包只对管理员网页会话开放(t *testing.T) {
	f := buildRemovedSlimPackages(t, "权限恢复")
	f.s.CreateMember("王工", "member-pass")
	member := f.s.Login("王工", "member-pass")
	_, token := f.s.IssueAgentToken(t, f.admin, "秘书")
	agent := f.s.AgentClient(token)

	for name, tc := range map[string]struct {
		c    *http.Client
		code int
		err  string
	}{
		"普通成员":     {member, http.StatusForbidden, "admin_required"},
		"管理员Agent": {agent, http.StatusForbidden, "web_session_required"},
		"匿名":       {&http.Client{}, http.StatusUnauthorized, "unauthenticated"},
	} {
		res, _ := postSlimRestore(t, f.s, tc.c, "/api/v1/admin/slim/restore/inspect", f.body)
		if res.StatusCode != tc.code {
			t.Errorf("%s检查状态码 = %d，想 %d", name, res.StatusCode, tc.code)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.err {
			t.Errorf("%s检查错误码 = %q，想 %q", name, got.Error.Code, tc.err)
		}
		res, _ = restoreSlim(t, f.s, tc.c, f.body, f.numbers[0])
		if res.StatusCode != tc.code {
			t.Errorf("%s恢复状态码 = %d，想 %d", name, res.StatusCode, tc.code)
			continue
		}
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.err {
			t.Errorf("%s恢复错误码 = %q，想 %q", name, got.Error.Code, tc.err)
		}
	}
}
