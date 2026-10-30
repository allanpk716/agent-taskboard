// 归档导入接口（#237）：黑盒测试只通过 HTTP 与上传的 zip 观察外部行为。
package server_test

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type archiveImportInspectionBody struct {
	FormatVersion   int                          `json:"format_version"`
	PlatformVersion string                       `json:"platform_version"`
	PackageName     string                       `json:"package_name"`
	GeneratedAt     string                       `json:"generated_at"`
	TicketCount     int                          `json:"ticket_count"`
	AttachmentBytes int64                        `json:"attachment_bytes"`
	Tickets         []archiveImportCandidateBody `json:"tickets"`
}

type archiveImportCandidateBody struct {
	Seq             int64  `json:"seq"`
	Number          string `json:"number"`
	Title           string `json:"title"`
	ClosedAt        string `json:"closed_at"`
	AttachmentBytes int64  `json:"attachment_bytes"`
	Importable      bool   `json:"importable"`
	Reason          string `json:"reason"`
}

type archiveImportResultBody struct {
	PackageName string                       `json:"package_name"`
	ImportedAt  string                       `json:"imported_at"`
	Imported    []archiveImportCandidateBody `json:"imported"`
	Rejected    []archiveImportCandidateBody `json:"rejected"`
}

func inspectArchive(t testing.TB, s *tbtest.Server, c *http.Client, pkg []byte) (*http.Response, archiveImportInspectionBody) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	file, err := mw.CreateFormFile("package", "archive.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(pkg); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/admin/archive/import/inspect", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	var out archiveImportInspectionBody
	if res.StatusCode == http.StatusOK {
		tbtest.DecodeJSON(t, res, &out)
	}
	return res, out
}

func importArchive(t testing.TB, s *tbtest.Server, c *http.Client, pkg []byte, numbers ...string) (*http.Response, archiveImportResultBody) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	file, err := mw.CreateFormFile("package", "archive.zip")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(pkg); err != nil {
		t.Fatal(err)
	}
	if err := mw.WriteField("ticket_numbers", strings.Join(numbers, ",")); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/admin/archive/import", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	var out archiveImportResultBody
	if res.StatusCode == http.StatusOK {
		tbtest.DecodeJSON(t, res, &out)
	}
	return res, out
}

func Test检查归档包列出可导入工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "待恢复")
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
	res := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("下载归档包状态码 = %d, 想 200", res.StatusCode)
	}
	zipBytes, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档包状态码 = %d, 想 200", res.StatusCode)
	}

	gotRes, got := inspectArchive(t, s, admin, zipBytes)
	if gotRes.StatusCode != http.StatusOK {
		t.Fatalf("检查归档包状态码 = %d, 想 200", gotRes.StatusCode)
	}
	if got.PackageName != pkg.Name || got.TicketCount != 1 || len(got.Tickets) != 1 {
		t.Fatalf("检查结果概要 = %+v, 想包 %s 里 1 张工单", got, pkg.Name)
	}
	ticket := got.Tickets[0]
	if ticket.Number != number || ticket.Title != "待恢复" || !ticket.Importable || ticket.Reason != "" {
		t.Fatalf("可导入工单 = %+v, 想 %s 待恢复且可导入", ticket, number)
	}
	if ticket.Seq == 0 || ticket.ClosedAt == "" {
		t.Fatalf("检查结果缺少序号或关闭时间：%+v", ticket)
	}

	importRes, imported := importArchive(t, s, admin, zipBytes, number)
	if importRes.StatusCode != http.StatusOK {
		t.Fatalf("导入归档包状态码 = %d, 想 200", importRes.StatusCode)
	}
	if imported.PackageName != pkg.Name || len(imported.Imported) != 1 ||
		imported.Imported[0].Number != number || len(imported.Rejected) != 0 {
		t.Fatalf("导入结果 = %+v, 想导入 %s", imported, number)
	}
	var detail ticketResponseBody
	res = s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &detail)
	if res.StatusCode != http.StatusOK || detail.Status != "closed" || detail.Seq != ticket.Seq {
		t.Fatalf("导入后的工单 = %d/%+v, 想 200 且按原序号恢复为 closed", res.StatusCode, detail)
	}
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("导入后重新开启状态码 = %d, 想 200", res.StatusCode)
	}
	next := mustCreateTicket(t, s, admin, "导入后新工单")
	var nextDetail ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+next, nil, &nextDetail)
	if nextDetail.Seq <= ticket.Seq {
		t.Fatalf("导入后新工单序号 = %d, 想大于恢复序号 %d", nextDetail.Seq, ticket.Seq)
	}

	var operations adminOperationsBody
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/operations", nil, &operations)
	found := false
	for _, op := range operations.Operations {
		if op.OperationType == "archive_import" && op.PackageName == pkg.Name &&
			len(op.TicketNumbers) == 1 && op.TicketNumbers[0] == number {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少 archive_import 管理操作：%+v", operations.Operations)
	}
}

func Test归档导入完整恢复工单并保留访客链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	wang := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	_, agent := issueRequiredAgent(t, s, admin, "笔记本")
	res := createTicket(t, s, admin, createTicketBody{
		Title: "完整往返", BriefMD: "第一版说明。", Labels: []string{"机型:UD-2300"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)
	number := created.Number
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "王工"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("指派状态码 = %d, 想 200", res.StatusCode)
	}
	if res := addCollaborator(t, s, admin, number, "王工"); res.StatusCode != http.StatusCreated {
		t.Fatalf("加入协作者状态码 = %d, 想 201", res.StatusCode)
	}

	briefAtt := mustUploadAttachment(t, s, agent, number, "现场记录.txt", []byte("brief-record"))
	var briefDraft draftAcceptedBody
	if res := s.Do(t, agent, http.MethodPatch, "/api/v1/tickets/"+number,
		map[string]any{"brief_md": "Agent 修订后的说明。", "attachment_ids": []string{briefAtt.ID},
			"base_revision": 1}, &briefDraft); res.StatusCode != http.StatusAccepted {
		t.Fatalf("说明修订草稿状态码 = %d, 想 202", res.StatusCode)
	}
	var briefSecond draftBody
	if res := s.Do(t, admin, http.MethodPut, "/api/v1/drafts/"+briefDraft.Draft.ID,
		map[string]any{"base_version": briefDraft.Draft.Version,
			"request": map[string]any{"brief_md": "成员确认后的说明。", "attachment_ids": []string{briefAtt.ID},
				"base_revision": 1}}, &briefSecond); res.StatusCode != http.StatusOK {
		t.Fatalf("修改说明草稿状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/drafts/"+briefDraft.Draft.ID+"/send",
		map[string]any{"version": briefSecond.Version}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("发出说明草稿状态码 = %d, 想 201", res.StatusCode)
	}

	replyAtt := mustUploadAttachment(t, s, agent, number, "回复日志.txt", []byte("reply-record"))
	var replyDraft draftAcceptedBody
	if res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "Agent 起草的回复。", "attachment_ids": []string{replyAtt.ID}}, &replyDraft); res.StatusCode != http.StatusAccepted {
		t.Fatalf("回复草稿状态码 = %d, 想 202", res.StatusCode)
	}
	var replySecond draftBody
	if res := s.Do(t, admin, http.MethodPut, "/api/v1/drafts/"+replyDraft.Draft.ID,
		map[string]any{"base_version": replyDraft.Draft.Version,
			"request": map[string]any{"body_md": "成员确认后的回复。", "attachment_ids": []string{replyAtt.ID}}}, &replySecond); res.StatusCode != http.StatusOK {
		t.Fatalf("修改回复草稿状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/drafts/"+replyDraft.Draft.ID+"/send",
		map[string]any{"version": replySecond.Version}, nil); res.StatusCode != http.StatusCreated {
		var sendErr errorBody
		tbtest.DecodeJSON(t, res, &sendErr)
		t.Fatalf("发出回复草稿状态码 = %d/%+v, 想 201", res.StatusCode, sendErr)
	}

	link := s.CreateGuestLink(t, admin, number,
		map[string]any{"note": "检验科-张三", "expires_at": "2027-12-31", "can_edit_brief": true})
	guest := s.GuestClient(link.Key(t))
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("访客认领链接状态码 = %d, 想 200", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}

	beforeDetail := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number)
	beforeBriefVersions := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/brief-versions")
	beforeReplyDrafting := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/drafting/1")
	beforeBriefDrafting := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/brief-drafting/2")
	beforeGuestLinks := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/guest-links")
	beforeAccess := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/access-records")
	beforeBriefAttachment := downloadArchiveAttachment(t, s, admin, beforeDetail, "现场记录.txt")
	beforeReplyAttachment := downloadArchiveAttachment(t, s, admin, beforeDetail, "回复日志.txt")
	beforeAccess = getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/access-records")

	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	wang = s.Login("王工", "member-pass")
	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	pkgRes := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if pkgRes.StatusCode != http.StatusOK {
		t.Fatalf("下载归档包状态码 = %d, 想 200", pkgRes.StatusCode)
	}
	zipBytes, err := io.ReadAll(pkgRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档包状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusGone {
		t.Fatalf("移除后读工单状态码 = %d, 想 410", res.StatusCode)
	}

	importRes, imported := importArchive(t, s, admin, zipBytes, number)
	if importRes.StatusCode != http.StatusOK || len(imported.Imported) != 1 || len(imported.Rejected) != 0 {
		t.Fatalf("导入结果 = %d/%+v, 想只导入成功", importRes.StatusCode, imported)
	}
	afterAccess := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/access-records")
	afterDetail := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number)
	afterBriefVersions := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/brief-versions")
	afterReplyDrafting := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/drafting/1")
	afterBriefDrafting := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/brief-drafting/2")
	afterGuestLinks := getArchiveRoundtripJSON(t, s, admin, "/api/v1/tickets/"+number+"/guest-links")
	afterBriefAttachment := downloadArchiveAttachment(t, s, admin, afterDetail, "现场记录.txt")
	afterReplyAttachment := downloadArchiveAttachment(t, s, admin, afterDetail, "回复日志.txt")

	assertArchiveRoundtripEqual(t, "工单详情", beforeDetail, afterDetail)
	assertArchiveRoundtripEqual(t, "说明版本", beforeBriefVersions, afterBriefVersions)
	assertArchiveRoundtripEqual(t, "回复起草过程", beforeReplyDrafting, afterReplyDrafting)
	assertArchiveRoundtripEqual(t, "说明起草过程", beforeBriefDrafting, afterBriefDrafting)
	assertArchiveRoundtripEqual(t, "访客链接", beforeGuestLinks, afterGuestLinks)
	assertArchiveRoundtripEqual(t, "访问记录", beforeAccess, afterAccess)
	if string(beforeBriefAttachment) != string(afterBriefAttachment) ||
		string(beforeReplyAttachment) != string(afterReplyAttachment) {
		t.Fatal("恢复后的附件内容与归档前不一致")
	}
	if status := afterDetail["status"]; status != "closed" {
		t.Fatalf("恢复后状态 = %v, 想 closed", status)
	}
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("恢复后原访客设备状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, s.GuestClient(link.Key(t)), http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("恢复后原访客链接状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, wang, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("协作者读取恢复工单状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("恢复后重新开启状态码 = %d, 想 200", res.StatusCode)
	}
}

func getArchiveRoundtripJSON(t testing.TB, s *tbtest.Server, c *http.Client, path string) map[string]any {
	t.Helper()
	var out map[string]any
	res := s.Do(t, c, http.MethodGet, path, nil, &out)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d, 想 200", path, res.StatusCode)
	}
	return out
}

func downloadArchiveAttachment(t testing.TB, s *tbtest.Server, c *http.Client, detail map[string]any, filename string) []byte {
	t.Helper()
	attachments, _ := detail["attachments"].([]any)
	for _, item := range attachments {
		attachment, _ := item.(map[string]any)
		if attachment["filename"] != filename {
			continue
		}
		url, _ := attachment["url"].(string)
		res := s.Do(t, c, http.MethodGet, url, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("下载 %s 状态码 = %d, 想 200", filename, res.StatusCode)
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return body
	}
	t.Fatalf("详情里没有附件 %s", filename)
	return nil
}

func assertArchiveRoundtripEqual(t testing.TB, name string, before, after map[string]any) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("%s 与归档前不一致：\n归档前：%+v\n导入后：%+v", name, before, after)
	}
}

func Test导入归档包可部分导入并拒绝重复导入(t *testing.T) {
	s, admin, numbers, pkg := buildTwoRemovedArchiveTickets(t)
	inspectionRes, inspection := inspectArchive(t, s, admin, pkg)
	if inspectionRes.StatusCode != http.StatusOK || len(inspection.Tickets) != 2 ||
		!inspection.Tickets[0].Importable || !inspection.Tickets[1].Importable {
		t.Fatalf("初始检查 = %d/%+v, 想两张都可导入", inspectionRes.StatusCode, inspection)
	}

	importRes, imported := importArchive(t, s, admin, pkg, numbers[0])
	if importRes.StatusCode != http.StatusOK || len(imported.Imported) != 1 ||
		imported.Imported[0].Number != numbers[0] || len(imported.Rejected) != 0 {
		t.Fatalf("部分导入 = %d/%+v, 想只导入第一张", importRes.StatusCode, imported)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+numbers[0], nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("已导入工单读取状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+numbers[1], nil, nil); res.StatusCode != http.StatusGone {
		t.Fatalf("未导入工单读取状态码 = %d, 想 410", res.StatusCode)
	}

	againRes, again := inspectArchive(t, s, admin, pkg)
	if againRes.StatusCode != http.StatusOK || len(again.Tickets) != 2 ||
		again.Tickets[0].Importable || !again.Tickets[1].Importable ||
		again.Tickets[0].Reason == "" {
		t.Fatalf("重复导入前检查 = %d/%+v, 想第一张拒绝、第二张可导入", againRes.StatusCode, again)
	}
	repeatRes, repeat := importArchive(t, s, admin, pkg)
	if repeatRes.StatusCode != http.StatusOK || len(repeat.Imported) != 1 ||
		repeat.Imported[0].Number != numbers[1] || len(repeat.Rejected) != 1 ||
		repeat.Rejected[0].Number != numbers[0] || repeat.Rejected[0].Reason == "" {
		t.Fatalf("重复导入结果 = %d/%+v, 想只导入第二张并拒绝第一张", repeatRes.StatusCode, repeat)
	}
}

func buildTwoRemovedArchiveTickets(t *testing.T) (*tbtest.Server, *http.Client, []string, []byte) {
	t.Helper()
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	setStorageClock(t, s, 2026, time.June, 2)
	numbers := []string{
		mustCreateTicket(t, s, admin, "第一张可恢复"),
		mustCreateTicket(t, s, admin, "第二张可恢复"),
	}
	for _, number := range numbers {
		if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
			t.Fatalf("关闭 %s 状态码 = %d, 想 200", number, res.StatusCode)
		}
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": numbers}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成两张工单归档包状态码 = %d, 想 201", res.StatusCode)
	}
	res := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("下载归档包状态码 = %d, 想 200", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档包状态码 = %d, 想 200", res.StatusCode)
	}
	return s, admin, numbers, body
}

func Test篡改或高版本归档包整包拒绝(t *testing.T) {
	s, admin, numbers, pkg := buildTwoRemovedArchiveTickets(t)
	files := zipFiles(t, pkg)
	files["tickets/"+numbers[0]+"/README.md"] = []byte("被篡改的说明\n")
	tampered := writeZipFiles(t, files)
	res, _ := inspectArchive(t, s, admin, tampered)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("篡改包检查状态码 = %d, 想 422", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "archive_package_invalid" {
		t.Fatalf("篡改包错误码 = %q, 想 archive_package_invalid", errBody.Error.Code)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+numbers[0], nil, nil); res.StatusCode != http.StatusGone {
		t.Fatalf("篡改包被拒后工单状态码 = %d, 想 410", res.StatusCode)
	}

	manifest := map[string]any{}
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["format_version"] = 2
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files["manifest.json"] = encoded
	newer := writeZipFiles(t, files)
	res, _ = inspectArchive(t, s, admin, newer)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("高版本包检查状态码 = %d, 想 422", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "archive_format_unsupported" {
		t.Fatalf("高版本包错误码 = %q, 想 archive_format_unsupported", errBody.Error.Code)
	}
}

func Test导入时附件写回失败保持墓碑且可重试(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "附件恢复失败可重试")
	att := mustUploadAttachment(t, s, admin, number, "日志.txt", []byte("retry-me"))
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md": "见日志。", "attachment_ids": []string{att.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂附件状态码 = %d, 想 200", res.StatusCode)
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
	pkgRes := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	zipBytes, err := io.ReadAll(pkgRes.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档包状态码 = %d, 想 200", res.StatusCode)
	}
	attachmentsDir := filepath.Join(s.DataDir(), "attachments")
	if err := os.Chmod(attachmentsDir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(attachmentsDir, 0o755); err != nil {
			t.Fatal(err)
		}
	}()
	failedRes, _ := importArchive(t, s, admin, zipBytes, number)
	if failedRes.StatusCode != http.StatusInternalServerError {
		t.Fatalf("附件目录不可写时导入状态码 = %d, 想 500", failedRes.StatusCode)
	}
	if err := os.Chmod(attachmentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusGone {
		t.Fatalf("失败后工单状态码 = %d, 想 410", res.StatusCode)
	}
	retryRes, retried := importArchive(t, s, admin, zipBytes, number)
	if retryRes.StatusCode != http.StatusOK || len(retried.Imported) != 1 {
		t.Fatalf("重试导入 = %d/%+v, 想成功", retryRes.StatusCode, retried)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重试后工单状态码 = %d, 想 200", res.StatusCode)
	}
}

func Test导入归档包引用缺失时拒绝该张(t *testing.T) {
	s, admin, numbers, pkg := buildTwoRemovedArchiveTickets(t)
	files := zipFiles(t, pkg)
	path := "tickets/" + numbers[0] + "/ticket.json"
	var structured map[string]any
	if err := json.Unmarshal(files[path], &structured); err != nil {
		t.Fatal(err)
	}
	ticket, _ := structured["ticket"].(map[string]any)
	ticket["creator_member_id"] = 999999
	encoded, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	files[path] = encoded
	withMissingRef := updateZipChecksums(t, files)

	res, got := inspectArchive(t, s, admin, withMissingRef)
	if res.StatusCode != http.StatusOK || len(got.Tickets) != 2 || got.Tickets[0].Importable ||
		!strings.Contains(got.Tickets[0].Reason, "成员") || !got.Tickets[1].Importable {
		t.Fatalf("引用缺失检查 = %d/%+v, 想第一张拒绝、第二张可导入", res.StatusCode, got)
	}
	importRes, imported := importArchive(t, s, admin, withMissingRef)
	if importRes.StatusCode != http.StatusOK || len(imported.Imported) != 1 ||
		imported.Imported[0].Number != numbers[1] || len(imported.Rejected) != 1 ||
		imported.Rejected[0].Number != numbers[0] {
		t.Fatalf("引用缺失导入 = %d/%+v, 想只导入第二张", importRes.StatusCode, imported)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+numbers[0], nil, nil); res.StatusCode != http.StatusGone {
		t.Fatalf("被拒工单状态码 = %d, 想 410", res.StatusCode)
	}
}

func Test导入归档包只对管理员网页会话开放(t *testing.T) {
	s, admin, _, pkg := buildTwoRemovedArchiveTickets(t)
	s.CreateMember("王工", "member-pass")
	member := s.Login("王工", "member-pass")
	_, token := s.IssueAgentToken(t, admin, "秘书")
	agent := s.AgentClient(token)

	for name, tc := range map[string]struct {
		c    *http.Client
		code int
		err  string
	}{
		"普通成员检查":   {member, http.StatusForbidden, "admin_required"},
		"管理员Agent": {agent, http.StatusForbidden, "web_session_required"},
		"匿名":       {&http.Client{}, http.StatusUnauthorized, "unauthenticated"},
	} {
		res, _ := inspectArchive(t, s, tc.c, pkg)
		if res.StatusCode != tc.code {
			t.Errorf("%s检查归档包状态码 = %d, 想 %d", name, res.StatusCode, tc.code)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.err {
			t.Errorf("%s检查归档包错误码 = %q, 想 %q", name, got.Error.Code, tc.err)
		}
		res, _ = importArchive(t, s, tc.c, pkg)
		if res.StatusCode != tc.code {
			t.Errorf("%s导入归档包状态码 = %d, 想 %d", name, res.StatusCode, tc.code)
			continue
		}
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.err {
			t.Errorf("%s导入归档包错误码 = %q, 想 %q", name, got.Error.Code, tc.err)
		}
	}
}

func zipFiles(t testing.TB, pkg []byte) map[string][]byte {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(pkg), int64(len(pkg)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]byte{}
	for _, entry := range reader.File {
		rc, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		out[entry.Name] = body
	}
	return out
}

func writeZipFiles(t testing.TB, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		entry, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func updateZipChecksums(t testing.TB, files map[string][]byte) []byte {
	t.Helper()
	var manifest map[string]any
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	entries, _ := manifest["files"].([]any)
	for _, item := range entries {
		entry, _ := item.(map[string]any)
		path, _ := entry["path"].(string)
		if path == "" || path == "manifest.json" {
			continue
		}
		sum := sha256.Sum256(files[path])
		entry["sha256"] = hex.EncodeToString(sum[:])
		entry["size"] = len(files[path])
	}
	encoded, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	files["manifest.json"] = encoded
	return writeZipFiles(t, files)
}
