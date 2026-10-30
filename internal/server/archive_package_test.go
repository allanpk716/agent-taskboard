// 归档包接口（#235）：黑盒测试只通过 HTTP 观察预览、生成、下载与暂存清除。
package server_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type archivePreviewBody struct {
	TicketCount     int64                 `json:"ticket_count"`
	AttachmentBytes int64                 `json:"attachment_bytes"`
	Tickets         []archiveTicketBody   `json:"tickets"`
	Drafts          []archiveDraftSummary `json:"drafts"`
}

type archiveTicketBody struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	ClosedAt        string `json:"closed_at"`
	AttachmentBytes int64  `json:"attachment_bytes"`
}

type archiveDraftSummary struct {
	ID            string   `json:"id"`
	TicketNumber  string   `json:"ticket_number"`
	Kind          string   `json:"kind"`
	MemberName    string   `json:"member_name"`
	AgentName     string   `json:"agent_name"`
	Version       int64    `json:"version"`
	UpdatedAt     string   `json:"updated_at"`
	AttachmentIDs []string `json:"attachment_ids"`
}

type archivePackageBody struct {
	Name            string   `json:"name"`
	TicketCount     int      `json:"ticket_count"`
	AttachmentBytes int64    `json:"attachment_bytes"`
	GeneratedAt     string   `json:"generated_at"`
	ExpiresAt       string   `json:"expires_at"`
	TicketNumbers   []string `json:"ticket_numbers"`
	DownloadURL     string   `json:"download_url"`
}

type archivePackageListBody struct {
	Count    int                  `json:"count"`
	Packages []archivePackageBody `json:"packages"`
}

type archiveManifestBody struct {
	FormatVersion   int    `json:"format_version"`
	PlatformVersion string `json:"platform_version"`
	GeneratedAt     string `json:"generated_at"`
	PackageName     string `json:"package_name"`
	Operator        struct {
		Name string `json:"name"`
	} `json:"operator"`
	Tickets []struct {
		Number string `json:"number"`
	} `json:"tickets"`
	Files []struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	} `json:"files"`
}

type archiveStructuredTicketBody struct {
	Replies       []map[string]any `json:"replies"`
	Attachments   []map[string]any `json:"attachments"`
	Timeline      []map[string]any `json:"timeline"`
	AccessRecords []map[string]any `json:"access_records"`
	Collaborators []map[string]any `json:"collaborators"`
	GuestLinks    []map[string]any `json:"guest_links"`
	GuestDevices  []map[string]any `json:"guest_devices"`
	TicketReads   []map[string]any `json:"ticket_reads"`
}

func downloadArchiveFiles(t testing.TB, s *tbtest.Server, c *http.Client, url string) map[string][]byte {
	t.Helper()
	res := s.Do(t, c, http.MethodGet, url, nil, nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("下载归档包状态码/类型 = %d/%s, 想 200 application/zip", res.StatusCode, res.Header.Get("Content-Type"))
	}
	zipBytes, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("打开归档包失败：%v", err)
	}
	files := map[string][]byte{}
	for _, f := range reader.File {
		rc, err := f.Open()
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
		files[f.Name] = body
	}
	return files
}

func Test归档预览只接受可归档工单并列出未发出草稿(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "可归档工单")
	_, agent := issueRequiredAgent(t, s, admin, "笔记本")
	attachment := mustUploadAttachment(t, s, agent, number, "日志.txt", []byte("1234"))
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "草稿正文", "attachment_ids": []string{attachment.ID}}, nil)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("保存草稿状态码 = %d, 想 202", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	open := mustCreateTicket(t, s, admin, "仍打开的工单")
	reopened := mustCreateTicket(t, s, admin, "重开后仍打开的工单")
	if res := closeTicket(t, s, admin, reopened, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭重开工单状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+reopened+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重开工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	var invalid errorBody
	res = s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{open}}, &invalid)
	if res.StatusCode != http.StatusBadRequest || invalid.Error.Code != "ticket_not_archivable" {
		t.Fatalf("未关闭工单预览 = %d/%s, 想 400 ticket_not_archivable", res.StatusCode, invalid.Error.Code)
	}

	invalid = errorBody{}
	res = s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{reopened}}, &invalid)
	if res.StatusCode != http.StatusBadRequest || invalid.Error.Code != "ticket_not_archivable" {
		t.Fatalf("重开后预览 = %d/%s, 想 400 ticket_not_archivable", res.StatusCode, invalid.Error.Code)
	}

	var memberErr errorBody
	res = s.Do(t, member, http.MethodPost, "/api/v1/admin/archive/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &memberErr)
	if res.StatusCode != http.StatusForbidden || memberErr.Error.Code != "admin_required" {
		t.Fatalf("普通成员预览 = %d/%s, 想 403 admin_required", res.StatusCode, memberErr.Error.Code)
	}

	var preview archivePreviewBody
	res = s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &preview)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("预览状态码 = %d, 想 200", res.StatusCode)
	}
	if preview.TicketCount != 1 || preview.AttachmentBytes != 0 {
		t.Fatalf("预览数量/附件大小 = %d/%d, 想 1/0（草稿独占附件不计入包）", preview.TicketCount, preview.AttachmentBytes)
	}
	if len(preview.Tickets) != 1 || preview.Tickets[0].Number != number || preview.Tickets[0].Title != "可归档工单" {
		t.Fatalf("预览工单清单 = %+v", preview.Tickets)
	}
	if len(preview.Drafts) != 1 || preview.Drafts[0].TicketNumber != number ||
		preview.Drafts[0].Kind != "reply" || preview.Drafts[0].MemberName != "陈工" ||
		preview.Drafts[0].AgentName != "笔记本" || preview.Drafts[0].Version != 1 ||
		preview.Drafts[0].UpdatedAt == "" || len(preview.Drafts[0].AttachmentIDs) != 1 {
		t.Fatalf("预览未发出草稿 = %+v", preview.Drafts)
	}
	var rawPreview struct {
		Drafts []map[string]any `json:"drafts"`
	}
	s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &rawPreview)
	if _, leaked := rawPreview.Drafts[0]["body_md"]; leaked {
		t.Fatalf("预览泄露了未发出草稿正文：%+v", rawPreview.Drafts[0])
	}
}

func Test生成归档包可下载且校验和与Markdown有效(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	wang := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "完整归档工单")
	log := mustUploadAttachment(t, s, admin, number, "服务日志.txt", []byte("archive-body"))
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "请查收 [服务日志](att:" + log.ID + ")。",
		"attachment_ids": []string{log.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d, 想 200", res.StatusCode)
	}
	link := s.CreateGuestLink(t, admin, number, map[string]any{"note": "现场确认", "valid_days": 90})
	if link.URL == "" {
		t.Fatal("创建访客链接失败")
	}
	guest := s.GuestClient(link.Key(t))
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("访客认领后访问状态码 = %d, 想 200", res.StatusCode)
	}
	if res := addCollaborator(t, s, admin, number, "王工"); res.StatusCode != http.StatusCreated {
		t.Fatalf("加入协作者状态码 = %d, 想 201", res.StatusCode)
	}
	if res := reply(t, s, wang, number, "现场已确认。"); res.StatusCode != http.StatusCreated {
		t.Fatalf("协作者回复状态码 = %d, 想 201", res.StatusCode)
	}
	_, agent := issueRequiredAgent(t, s, admin, "待确认稿")
	if res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "归档前会丢弃的草稿"}, nil); res.StatusCode != http.StatusAccepted {
		t.Fatalf("保存草稿状态码 = %d, 想 202", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	var before ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &before)
	beforeAccess := readAccessRecords(t, s, admin, number)
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")

	var created archivePackageBody
	res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	if created.Name == "" || created.DownloadURL == "" || len(created.TicketNumbers) != 1 ||
		created.TicketNumbers[0] != number || created.AttachmentBytes != int64(len("archive-body")) {
		t.Fatalf("生成响应 = %+v", created)
	}

	download := s.Do(t, admin, http.MethodGet, created.DownloadURL, nil, nil)
	if download.StatusCode != http.StatusOK || download.Header.Get("Content-Type") != "application/zip" {
		t.Fatalf("下载状态码/类型 = %d/%s, 想 200 application/zip", download.StatusCode, download.Header.Get("Content-Type"))
	}
	zipBytes, err := io.ReadAll(download.Body)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("打开归档包失败：%v", err)
	}
	files := map[string][]byte{}
	for _, f := range reader.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = body
	}
	var manifest archiveManifestBody
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatalf("manifest 不是合法 JSON：%v", err)
	}
	if manifest.FormatVersion != 1 || manifest.PackageName != created.Name || manifest.Operator.Name != "陈工" ||
		len(manifest.Tickets) != 1 || manifest.Tickets[0].Number != number {
		t.Fatalf("manifest 基本信息 = %+v", manifest)
	}
	for _, entry := range manifest.Files {
		body, ok := files[entry.Path]
		if !ok {
			t.Fatalf("manifest 指向的文件不存在：%s", entry.Path)
		}
		sum := sha256.Sum256(body)
		if got := hex.EncodeToString(sum[:]); got != entry.SHA256 || int64(len(body)) != entry.Size {
			t.Fatalf("%s 校验失败：manifest=%s/%d，实际=%s/%d", entry.Path, entry.SHA256, entry.Size, got, len(body))
		}
	}
	var rawStructured map[string]any
	if err := json.Unmarshal(files["tickets/"+number+"/ticket.json"], &rawStructured); err != nil {
		t.Fatalf("结构化工单 JSON 无效：%v", err)
	}
	for _, key := range []string{"ticket", "brief_versions", "replies", "attachments", "timeline",
		"drafts", "access_records", "labels", "collaborators", "guest_links", "guest_devices", "ticket_reads"} {
		if _, ok := rawStructured[key]; !ok {
			t.Errorf("结构化工单 JSON 缺少 %s", key)
		}
	}
	var structured archiveStructuredTicketBody
	if err := json.Unmarshal(files["tickets/"+number+"/ticket.json"], &structured); err != nil {
		t.Fatalf("结构化工单 JSON 无效：%v", err)
	}
	if len(structured.Replies) != 1 || len(structured.Attachments) != 1 ||
		len(structured.Collaborators) != 1 || len(structured.GuestLinks) != 1 ||
		len(structured.GuestDevices) != 1 || len(structured.TicketReads) != 1 {
		t.Fatalf("结构化工单关联数据不完整：replies=%d attachments=%d collaborators=%d guest_links=%d guest_devices=%d ticket_reads=%d",
			len(structured.Replies), len(structured.Attachments), len(structured.Collaborators),
			len(structured.GuestLinks), len(structured.GuestDevices), len(structured.TicketReads))
	}
	for _, key := range []string{"id", "ticket_seq", "key_hash", "note", "expires_at", "revoked_at",
		"can_edit_brief", "created_by_member_id", "created_by_agent_id", "created_at", "claimed_at",
		"claim_name", "access_code_hash", "access_code_failures", "access_code_locked_at", "key_ciphertext"} {
		if _, ok := structured.GuestLinks[0][key]; !ok {
			t.Errorf("导出的访客链接缺少恢复列 %s：%+v", key, structured.GuestLinks[0])
		}
	}
	for _, key := range []string{"id", "guest_link_id", "token_hash", "created_at", "entry_method"} {
		if _, ok := structured.GuestDevices[0][key]; !ok {
			t.Errorf("导出的访客设备缺少恢复列 %s：%+v", key, structured.GuestDevices[0])
		}
	}
	markdownTarget := "attachments/" + log.ID + "/%E6%9C%8D%E5%8A%A1%E6%97%A5%E5%BF%97.txt"
	zipPath := "tickets/" + number + "/attachments/" + log.ID + "/服务日志.txt"
	md := string(files["tickets/"+number+"/README.md"])
	if !strings.Contains(md, markdownTarget) {
		t.Fatalf("Markdown 缺少附件相对链接 %s：\n%s", markdownTarget, md)
	}
	if _, ok := files[zipPath]; !ok {
		t.Fatalf("包里缺少 Markdown 指向的附件：%s", zipPath)
	}

	var after ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.MyDraft != nil {
		t.Fatalf("生成后未发出草稿仍在： %+v", after.MyDraft)
	}
	if after.Title != before.Title || after.Status != before.Status || after.Brief.Revision != before.Brief.Revision ||
		len(after.Timeline) != len(before.Timeline) {
		t.Fatalf("生成归档包改动了其他工单数据：before %+v，after %+v", before, after)
	}
	afterAccess := readAccessRecords(t, s, admin, number)
	if len(afterAccess.Records) < len(beforeAccess.Records) {
		t.Fatalf("访问记录被删除：%d → %d", len(beforeAccess.Records), len(afterAccess.Records))
	}
}

func Test归档包清洗附件路径并精确替换附件引用(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	res := createTicket(t, s, admin, createTicketBody{
		Title:   "附件路径安全",
		BriefMD: "见 [第一份](att:att_1) 和 [第十二份](att:att_12)。",
		Labels:  []string{"机型:UD-2300"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	var ticket createTicketResponse
	tbtest.DecodeJSON(t, res, &ticket)
	number := ticket.Number
	atts := []struct {
		id       int64
		filename string
		content  string
	}{
		{1, "a b(c).txt", "one"},
		{2, "..", "dotdot"},
		{12, "twelve (12).txt", "twelve"},
	}
	attsDir := filepath.Join(s.DataDir(), "attachments")
	if err := os.MkdirAll(attsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, att := range atts {
		if _, err := s.Store().DB.Exec(`INSERT INTO attachments(
			id, ticket_seq, filename, size, state, attached_to, uploader_member_id, uploaded_at, claimed_at)
			VALUES (?, ?, ?, ?, 'attached', '说明',
			        (SELECT id FROM members WHERE username = '陈工'), '2026-06-02T09:00:00+08:00',
			        '2026-06-02T09:00:01+08:00')`,
			att.id, ticket.Seq, att.filename, len(att.content)); err != nil {
			t.Fatalf("造附件元数据失败：%v", err)
		}
		if err := os.WriteFile(filepath.Join(attsDir, strconv.Itoa(int(att.id))), []byte(att.content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")

	var created archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &created); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	files := downloadArchiveFiles(t, s, admin, created.DownloadURL)
	prefix := "tickets/" + number + "/"
	for name := range files {
		for _, part := range strings.Split(name, "/") {
			if part == "" || part == "." || part == ".." || strings.ContainsRune(part, 0) ||
				strings.ContainsFunc(part, func(r rune) bool { return unicode.IsControl(r) }) {
				t.Fatalf("归档包里有不安全路径段 %q（完整条目 %q）", part, name)
			}
		}
	}
	expected := map[string]string{
		prefix + "attachments/att_1/a b(c).txt":       "one",
		prefix + "attachments/att_2/_":                "dotdot",
		prefix + "attachments/att_12/twelve (12).txt": "twelve",
	}
	for path, want := range expected {
		if got := string(files[path]); got != want {
			t.Fatalf("附件 %s 内容 = %q, 想 %q", path, got, want)
		}
	}
	md := string(files[prefix+"README.md"])
	if !strings.Contains(md, "[a b(c).txt](attachments/att_1/a%20b%28c%29.txt)") {
		t.Fatalf("Markdown 没有安全编码 att_1 链接：\n%s", md)
	}
	if !strings.Contains(md, "[twelve (12).txt](attachments/att_12/twelve%20%2812%29.txt)") {
		t.Fatalf("Markdown 没有精确替换 att_12 链接：\n%s", md)
	}
}

// 生成失败点用「暂存目录不可写」构造：zip 还没开始写，草稿与工单数据必须
// 原样保留；恢复目录权限后同一选择可以重试成功。
func Test生成归档包失败不改平台数据且可重试(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "失败后重试")
	_, agent := issueRequiredAgent(t, s, admin, "待确认稿")
	if res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "失败时不能丢"}, nil); res.StatusCode != http.StatusAccepted {
		t.Fatalf("保存草稿状态码 = %d, 想 202", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	var before ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &before)
	if before.MyDraft == nil {
		t.Fatal("造数据失败：生成前应有未发出草稿")
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")

	stage := filepath.Join(s.DataDir(), "archive-packages")
	if err := os.MkdirAll(stage, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stage, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stage, 0o700) })
	failed := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, nil)
	if failed.StatusCode != http.StatusInternalServerError {
		t.Fatalf("生成失败状态码 = %d, 想 500", failed.StatusCode)
	}
	var failedBody errorBody
	tbtest.DecodeJSON(t, failed, &failedBody)
	if failedBody.Error.Code != "internal_error" {
		t.Fatalf("生成失败错误码 = %q, 想 internal_error", failedBody.Error.Code)
	}

	var unchanged ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &unchanged)
	if unchanged.MyDraft == nil || unchanged.MyDraft.ID != before.MyDraft.ID {
		t.Fatalf("失败后草稿被改动：before %+v，after %+v", before.MyDraft, unchanged.MyDraft)
	}
	if unchanged.Title != before.Title || unchanged.Status != before.Status ||
		unchanged.Brief.Revision != before.Brief.Revision || len(unchanged.Timeline) != len(before.Timeline) {
		t.Fatalf("失败后工单数据被改动：before %+v，after %+v", before, unchanged)
	}
	var packages archivePackageListBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/archive/packages", nil, &packages); res.StatusCode != http.StatusOK || packages.Count != 0 {
		t.Fatalf("失败后暂存包列表 = %d/%+v, 想 200 且 0 个", res.StatusCode, packages)
	}

	if err := os.Chmod(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	var created archivePackageBody
	res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("重试生成状态码 = %d, 想 201", res.StatusCode)
	}
	var after ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.MyDraft != nil {
		t.Fatalf("重试成功后草稿仍在：%+v", after.MyDraft)
	}
	if after.Title != before.Title || after.Status != before.Status ||
		after.Brief.Revision != before.Brief.Revision || len(after.Timeline) != len(before.Timeline) {
		t.Fatalf("重试改动其他数据：before %+v，after %+v", before, after)
	}
}

func Test暂存归档包七天到期清除(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "到期清除")
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	var created archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &created); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/archive/packages", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("生成后列表状态码 = %d, 想 200", res.StatusCode)
	}

	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 1, 0, clock.Beijing).Add(7 * 24 * time.Hour))
	admin = s.Login("陈工", "secret-pass")
	if err := s.Archives().CleanupExpired(context.Background()); err != nil {
		t.Fatalf("触发到期清除失败：%v", err)
	}
	var packages archivePackageListBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/archive/packages", nil, &packages); res.StatusCode != http.StatusOK ||
		packages.Count != 0 || len(packages.Packages) != 0 {
		t.Fatalf("到期后暂存包列表 = %d/%+v, 想 200 且空", res.StatusCode, packages)
	}
	res := s.Do(t, admin, http.MethodGet, created.DownloadURL, nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("到期后下载状态码 = %d, 想 404", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "archive_not_found" {
		t.Fatalf("到期后下载错误码 = %q, 想 archive_not_found", got.Error.Code)
	}
}

func Test归档接口只对管理员开放(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")
	_, token := s.IssueAgentToken(t, admin, "秘书")
	adminAgent := s.AgentClient(token)

	cases := []struct {
		name   string
		c      *http.Client
		method string
		path   string
		body   any
		code   string
	}{
		{"普通成员预览", member, http.MethodPost, "/api/v1/admin/archive/preview",
			map[string]any{"ticket_numbers": []string{"20260602-001"}}, "admin_required"},
		{"普通成员生成", member, http.MethodPost, "/api/v1/admin/archive/packages",
			map[string]any{"ticket_numbers": []string{"20260602-001"}}, "admin_required"},
		{"普通成员列表", member, http.MethodGet, "/api/v1/admin/archive/packages", nil, "admin_required"},
		{"普通成员下载", member, http.MethodGet,
			"/api/v1/admin/archive/packages/archive-test.zip/download", nil, "admin_required"},
		{"管理员Agent列表", adminAgent, http.MethodGet, "/api/v1/admin/archive/packages", nil,
			"web_session_required"},
	}
	for _, tc := range cases {
		res := s.Do(t, tc.c, tc.method, tc.path, tc.body, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s 状态码 = %d, 想 403", tc.name, tc.path, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.code {
			t.Errorf("%s %s 错误码 = %q, 想 %q", tc.name, tc.path, got.Error.Code, tc.code)
		}
	}
}

func Test暂存归档包列表空态可用(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	var packages archivePackageListBody
	res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/archive/packages", nil, &packages)
	if res.StatusCode != http.StatusOK || packages.Count != 0 || packages.Packages == nil {
		t.Fatalf("空暂存区列表 = %d/%+v, 想 200 且空数组", res.StatusCode, packages)
	}
}
