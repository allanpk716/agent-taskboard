// #286：先瘦身再归档的衔接。黑盒测试只通过 HTTP 与下载的 zip 观察行为。
package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// slimThenArchiveFixture 造两张已关闭满 3 个月的工单：一张已把附件瘦身移出，
// 另一张附件仍在服务器上，返回编号与瘦身包名。
type slimArchiveFixture struct {
	Slimmed     string
	Kept        string
	SlimPackage string
	SlimmedLog  attachmentUploadBody
	KeptRecord  attachmentUploadBody
}

func slimThenArchiveFixture(t *testing.T, s *tbtest.Server, admin *http.Client) slimArchiveFixture {
	t.Helper()
	setStorageClock(t, s, 2026, time.June, 2)
	var out slimArchiveFixture
	out.Slimmed = mustCreateTicket(t, s, admin, "已瘦身的工单")
	out.SlimmedLog = mustUploadAttachment(t, s, admin, out.Slimmed, "服务日志.log", []byte("slim-body"))
	if res := revise(t, s, admin, out.Slimmed, map[string]any{
		"brief_md":       "见 [服务日志](att:" + out.SlimmedLog.ID + ")。",
		"attachment_ids": []string{out.SlimmedLog.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	out.Kept = mustCreateTicket(t, s, admin, "附件仍在的工单")
	out.KeptRecord = mustUploadAttachment(t, s, admin, out.Kept, "现场记录.txt", []byte("kept-body"))
	if res := revise(t, s, admin, out.Kept, map[string]any{
		"brief_md":       "见 [现场记录](att:" + out.KeptRecord.ID + ")。",
		"attachment_ids": []string{out.KeptRecord.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	for _, number := range []string{out.Slimmed, out.Kept} {
		if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
			t.Fatalf("关闭工单 %s 状态码 = %d", number, res.StatusCode)
		}
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")

	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{out.Slimmed}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d，想 201", res.StatusCode)
	}
	out.SlimPackage = pkg.Name
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身移出状态码 = %d，想 200", res.StatusCode)
	}
	return out
}

func Test归档预览响应带需一并保存的瘦身包(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	f := slimThenArchiveFixture(t, s, admin)
	slimmed, slimPackage := f.Slimmed, f.SlimPackage

	var preview archivePreviewBody
	res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{slimmed}}, &preview)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("归档预览状态码 = %d，想 200", res.StatusCode)
	}
	if len(preview.SlimPackages) != 1 || preview.SlimPackages[0] != slimPackage {
		t.Fatalf("预览引用的瘦身包 = %+v，想 [%s]", preview.SlimPackages, slimPackage)
	}
}

func Test生成归档包跳过已瘦身附件并记录引用瘦身包(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	f := slimThenArchiveFixture(t, s, admin)
	slimmed, slimPackage := f.Slimmed, f.SlimPackage

	var pkg archivePackageBody
	res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{slimmed, f.Kept}}, &pkg)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d，想 201", res.StatusCode)
	}
	if len(pkg.SlimPackages) != 1 || pkg.SlimPackages[0] != slimPackage {
		t.Fatalf("生成响应引用的瘦身包 = %+v，想 [%s]", pkg.SlimPackages, slimPackage)
	}

	files := downloadArchiveFiles(t, s, admin, pkg.DownloadURL)
	var rawManifest struct {
		Tickets []struct {
			Number       string   `json:"number"`
			SlimPackages []string `json:"slim_packages"`
		} `json:"tickets"`
		Files []struct {
			Path string `json:"path"`
		} `json:"files"`
	}
	if err := json.Unmarshal(files["manifest.json"], &rawManifest); err != nil {
		t.Fatalf("解析归档清单失败：%v", err)
	}
	for _, ticket := range rawManifest.Tickets {
		want := []string(nil)
		if ticket.Number == slimmed {
			want = []string{slimPackage}
		}
		if !reflect.DeepEqual(ticket.SlimPackages, want) {
			t.Fatalf("%s 的清单瘦身包 = %+v，想 %+v", ticket.Number, ticket.SlimPackages, want)
		}
	}

	for _, file := range rawManifest.Files {
		if strings.Contains(file.Path, "/"+slimmed+"/") && strings.Contains(file.Path, "/attachments/") {
			t.Fatalf("归档包不应包含已瘦身附件文件：%s", file.Path)
		}
	}
	if !hasArchiveAttachmentFile(t, files, f.Kept) {
		t.Fatalf("归档包缺少仍在服务器上的附件文件：%s", f.Kept)
	}

	var export archiveStructuredTicketBody
	if err := json.Unmarshal(files["tickets/"+slimmed+"/ticket.json"], &export); err != nil {
		t.Fatalf("解析 %s 的结构化数据失败：%v", slimmed, err)
	}
	if len(export.Attachments) != 1 {
		t.Fatalf("%s 应保留附件记录：%+v", slimmed, export.Attachments)
	}
	att := export.Attachments[0]
	if att["slim_package_name"] != slimPackage || att["slim_removed_at"] == nil {
		t.Fatalf("%s 的附件瘦身状态 = %+v，想包 %s 且移出时间非空", slimmed, att, slimPackage)
	}
	readme := string(files["tickets/"+slimmed+"/README.md"])
	if !strings.Contains(readme, "已瘦身移出") || !strings.Contains(readme, slimPackage) {
		t.Fatalf("%s 的 README 未说明瘦身附件去向：%s", slimmed, readme)
	}
}

func hasArchiveAttachmentFile(t *testing.T, files map[string][]byte, number string) bool {
	t.Helper()
	prefix := "tickets/" + number + "/attachments/"
	for name := range files {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func Test导入先瘦身后归档的包保持附件瘦身状态(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	f := slimThenArchiveFixture(t, s, admin)

	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{f.Slimmed, f.Kept}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d，想 201", res.StatusCode)
	}
	zipBytes := readAll(t, s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil))
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档工单状态码 = %d，想 200", res.StatusCode)
	}

	inspectRes, inspection := inspectArchive(t, s, admin, zipBytes)
	if inspectRes.StatusCode != http.StatusOK || len(inspection.Tickets) != 2 {
		t.Fatalf("检查归档包 = %d/%+v，想 200 且 2 张可导入", inspectRes.StatusCode, inspection.Tickets)
	}
	for _, ticket := range inspection.Tickets {
		if !ticket.Importable {
			t.Fatalf("%s 应可导入：%s", ticket.Number, ticket.Reason)
		}
	}
	importRes, imported := importArchive(t, s, admin, zipBytes, f.Slimmed, f.Kept)
	if importRes.StatusCode != http.StatusOK || len(imported.Imported) != 2 {
		t.Fatalf("导入归档包 = %d/%+v，想 200 且 2 张导入", importRes.StatusCode, imported)
	}
	slimmedFile := filepath.Join(s.DataDir(), "attachments", strings.TrimPrefix(f.SlimmedLog.ID, "att_"))
	if _, err := os.Stat(slimmedFile); !os.IsNotExist(err) {
		t.Fatalf("导入不应写回已瘦身附件文件：%v", err)
	}

	// 导入后的工单再次归档时仍应引用同一个瘦身包，且仍不打包该附件文件。
	var again archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{f.Slimmed}}, &again); res.StatusCode != http.StatusCreated {
		t.Fatalf("再次生成归档包状态码 = %d，想 201", res.StatusCode)
	}
	if len(again.SlimPackages) != 1 || again.SlimPackages[0] != f.SlimPackage {
		t.Fatalf("再次归档引用的瘦身包 = %+v，想 [%s]", again.SlimPackages, f.SlimPackage)
	}
	files := downloadArchiveFiles(t, s, admin, again.DownloadURL)
	if hasArchiveAttachmentFile(t, files, f.Slimmed) {
		t.Fatalf("再次归档不应包含已瘦身附件文件")
	}
	var export archiveStructuredTicketBody
	if err := json.Unmarshal(files["tickets/"+f.Slimmed+"/ticket.json"], &export); err != nil {
		t.Fatalf("解析再次归档的结构化数据失败：%v", err)
	}
	if len(export.Attachments) != 1 || export.Attachments[0]["slim_package_name"] != f.SlimPackage ||
		export.Attachments[0]["slim_removed_at"] == nil {
		t.Fatalf("导入后的附件瘦身状态 = %+v，想包 %s 且移出时间非空", export.Attachments, f.SlimPackage)
	}
}

func Test不涉及瘦身的归档包格式不变(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "普通归档")
	att := mustUploadAttachment(t, s, admin, number, "记录.txt", []byte("plain"))
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见 [记录](att:" + att.ID + ")。",
		"attachment_ids": []string{att.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")

	var preview archivePreviewBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &preview); res.StatusCode != http.StatusOK {
		t.Fatalf("归档预览状态码 = %d，想 200", res.StatusCode)
	}
	if len(preview.SlimPackages) != 0 {
		t.Fatalf("不涉及瘦身的预览不应带瘦身包：%+v", preview.SlimPackages)
	}
	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d，想 201", res.StatusCode)
	}
	if len(pkg.SlimPackages) != 0 {
		t.Fatalf("不涉及瘦身的生成响应不应带瘦身包：%+v", pkg.SlimPackages)
	}
	files := downloadArchiveFiles(t, s, admin, pkg.DownloadURL)
	if strings.Contains(string(files["manifest.json"]), "slim_packages") {
		t.Fatalf("不涉及瘦身的清单不应出现 slim_packages 字段：%s", files["manifest.json"])
	}
	if !hasArchiveAttachmentFile(t, files, number) {
		t.Fatalf("归档包缺少附件文件：%s", number)
	}
	var export archiveStructuredTicketBody
	if err := json.Unmarshal(files["tickets/"+number+"/ticket.json"], &export); err != nil {
		t.Fatalf("解析结构化数据失败：%v", err)
	}
	if len(export.Attachments) != 1 {
		t.Fatalf("附件记录 = %+v", export.Attachments)
	}
	for _, key := range []string{"slim_removed_at", "slim_package_name"} {
		if _, ok := export.Attachments[0][key]; ok {
			t.Fatalf("不涉及瘦身的附件记录不应带 %s：%+v", key, export.Attachments[0])
		}
	}
}
