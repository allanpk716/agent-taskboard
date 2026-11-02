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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type slimPreviewTicketBody struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	AttachmentCount int    `json:"attachment_count"`
	AttachmentBytes int64  `json:"attachment_bytes"`
}

type slimPreviewBody struct {
	TicketCount     int                     `json:"ticket_count"`
	AttachmentCount int                     `json:"attachment_count"`
	AttachmentBytes int64                   `json:"attachment_bytes"`
	Tickets         []slimPreviewTicketBody `json:"tickets"`
}

type slimCandidateBody struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	AttachmentCount int    `json:"attachment_count"`
	AttachmentBytes int64  `json:"attachment_bytes"`
}

type slimCandidatesBody struct {
	ClosedMonths int                  `json:"closed_months"`
	Months       []slimCandidateMonth `json:"months"`
}

type slimCandidateMonth struct {
	Month           string              `json:"month"`
	TicketCount     int                 `json:"ticket_count"`
	AttachmentCount int                 `json:"attachment_count"`
	AttachmentBytes int64               `json:"attachment_bytes"`
	Tickets         []slimCandidateBody `json:"tickets"`
}

type slimManifestFileBody struct {
	Path         string `json:"path"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	TicketNumber string `json:"ticket_number"`
	AttachedTo   string `json:"attached_to"`
	AttachmentID string `json:"attachment_id"`
	Filename     string `json:"filename"`
}

type slimManifestBody struct {
	FormatVersion int    `json:"format_version"`
	PackageName   string `json:"package_name"`
	GeneratedAt   string `json:"generated_at"`
	Operator      struct {
		Name string `json:"name"`
	} `json:"operator"`
	AttachmentCount int                    `json:"attachment_count"`
	AttachmentBytes int64                  `json:"attachment_bytes"`
	Files           []slimManifestFileBody `json:"files"`
}

type slimPackageBody struct {
	Name            string   `json:"name"`
	TicketNumbers   []string `json:"ticket_numbers"`
	TicketCount     int      `json:"ticket_count"`
	AttachmentCount int      `json:"attachment_count"`
	AttachmentBytes int64    `json:"attachment_bytes"`
	GeneratedAt     string   `json:"generated_at"`
	ExpiresAt       string   `json:"expires_at"`
	OperatorName    string   `json:"operator_name"`
	DownloadURL     string   `json:"download_url"`
	RemovedCount    int      `json:"removed_attachment_count"`
}

type slimRemoveBody struct {
	PackageName     string   `json:"package_name"`
	TicketNumbers   []string `json:"ticket_numbers"`
	AttachmentCount int      `json:"attachment_count"`
	AttachmentBytes int64    `json:"attachment_bytes"`
	RemovedAt       string   `json:"removed_at"`
	CleanupFailures int      `json:"cleanup_failures"`
}

func Test瘦身预览只统计附件不统计插图(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	s.Clock().Set(time.Date(2026, time.June, 2, 10, 0, 0, 0, clock.Beijing))
	number := mustCreateTicket(t, s, admin, "老工单")
	log := mustUploadAttachment(t, s, admin, number, "服务日志.log", []byte("12345"))
	image := mustUploadAttachment(t, s, admin, number, "现场图.png", []byte("image"))
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见 [服务日志](att:" + log.ID + ")。![现场图](att:" + image.ID + ")。",
		"attachment_ids": []string{log.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 16, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")

	var preview slimPreviewBody
	res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &preview)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("瘦身预览状态码 = %d，想 200", res.StatusCode)
	}
	if preview.TicketCount != 1 || preview.AttachmentCount != 1 || preview.AttachmentBytes != 5 {
		t.Fatalf("瘦身汇总 = %+v，想 1 张 / 1 个 / 5 字节", preview)
	}
	if len(preview.Tickets) != 1 || preview.Tickets[0].Number != number ||
		preview.Tickets[0].AttachmentCount != 1 || preview.Tickets[0].AttachmentBytes != 5 {
		t.Fatalf("瘦身工单明细 = %+v", preview.Tickets)
	}

	var invalid errorBody
	res = s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/preview",
		map[string]any{"closed_months": 0, "ticket_numbers": []string{number}}, &invalid)
	if res.StatusCode != http.StatusBadRequest || invalid.Error.Code != "invalid_closed_months" {
		t.Fatalf("N=0 预览 = %d/%s，想 400 invalid_closed_months", res.StatusCode, invalid.Error.Code)
	}
	if res := s.Do(t, member, http.MethodPost, "/api/v1/admin/slim/preview",
		map[string]any{"ticket_numbers": []string{number}}, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员瘦身预览状态码 = %d，想 403", res.StatusCode)
	}
}

func Test瘦身候选按关闭月数筛选且排除没有附件的工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	old := mustCreateTicket(t, s, admin, "老附件单")
	oldAtt := mustUploadAttachment(t, s, admin, old, "旧日志.log", []byte("old"))
	if res := revise(t, s, admin, old, map[string]any{
		"attachment_ids": []string{oldAtt.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, old, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭老工单状态码 = %d", res.StatusCode)
	}
	empty := mustCreateTicket(t, s, admin, "老空单")
	if res := closeTicket(t, s, admin, empty, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭空老工单状态码 = %d", res.StatusCode)
	}

	setStorageClock(t, s, 2026, time.September, 20)
	recent := mustCreateTicket(t, s, admin, "新附件单")
	recentAtt := mustUploadAttachment(t, s, admin, recent, "新日志.log", []byte("new"))
	if res := revise(t, s, admin, recent, map[string]any{
		"attachment_ids": []string{recentAtt.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, recent, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭新工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")

	var candidates slimCandidatesBody
	res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/slim/candidates?closed_months=3", nil, &candidates)
	if res.StatusCode != http.StatusOK || candidates.ClosedMonths != 3 || len(candidates.Months) != 1 {
		t.Fatalf("瘦身候选 = %d/%+v，想只剩一个月份", res.StatusCode, candidates)
	}
	month := candidates.Months[0]
	if month.Month != "2026-06" || month.TicketCount != 1 || month.AttachmentCount != 1 ||
		month.AttachmentBytes != 3 || len(month.Tickets) != 1 || month.Tickets[0].Number != old {
		t.Fatalf("瘦身候选月份 = %+v，想只包含 %s", month, old)
	}
	if res := s.Do(t, member, http.MethodGet, "/api/v1/admin/slim/candidates", nil, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员读取瘦身候选状态码 = %d，想 403", res.StatusCode)
	}
	var invalid errorBody
	res = s.Do(t, admin, http.MethodGet, "/api/v1/admin/slim/candidates?closed_months=121", nil, &invalid)
	if res.StatusCode != http.StatusBadRequest || invalid.Error.Code != "invalid_closed_months" {
		t.Fatalf("越界候选 = %d/%s，想 400 invalid_closed_months", res.StatusCode, invalid.Error.Code)
	}
}

func Test生成瘦身包包含清单与校验和并写管理操作(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "要瘦身的工单")
	log := mustUploadAttachment(t, s, admin, number, "服务 日志.log", []byte("slim-body"))
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见 [服务日志](att:" + log.ID + ")。",
		"attachment_ids": []string{log.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")

	if res := s.Do(t, member, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员生成瘦身包状态码 = %d，想 403", res.StatusCode)
	}
	if res := s.Do(t, member, http.MethodGet, "/api/v1/admin/slim/packages", nil, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员读取瘦身包列表状态码 = %d，想 403", res.StatusCode)
	}
	var pkg slimPackageBody
	res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d，想 201", res.StatusCode)
	}
	if pkg.Name == "" || pkg.TicketCount != 1 || pkg.AttachmentCount != 1 || pkg.AttachmentBytes != 9 ||
		pkg.OperatorName != "陈工" || pkg.DownloadURL == "" || pkg.ExpiresAt == "" {
		t.Fatalf("瘦身包信息 = %+v", pkg)
	}

	var list struct {
		Count    int               `json:"count"`
		Packages []slimPackageBody `json:"packages"`
	}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/slim/packages", nil, &list)
	if list.Count != 1 || list.Packages[0].Name != pkg.Name {
		t.Fatalf("瘦身包列表 = %+v，想包含 %s", list, pkg.Name)
	}
	if res := s.Do(t, member, http.MethodGet, pkg.DownloadURL, nil, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员下载瘦身包状态码 = %d，想 403", res.StatusCode)
	}

	zipBytes := readAll(t, s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil))
	reader, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatalf("打开瘦身包失败：%v", err)
	}
	files := map[string][]byte{}
	for _, entry := range reader.File {
		rc, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name] = body
	}
	var manifest slimManifestBody
	if err := json.Unmarshal(files["manifest.json"], &manifest); err != nil {
		t.Fatalf("解析瘦身清单失败：%v", err)
	}
	if manifest.FormatVersion != 1 || manifest.PackageName != pkg.Name || manifest.Operator.Name != "陈工" ||
		manifest.AttachmentCount != 1 || manifest.AttachmentBytes != 9 || len(manifest.Files) != 1 {
		t.Fatalf("瘦身清单 = %+v", manifest)
	}
	entry := manifest.Files[0]
	wantPath := "tickets/" + number + "/attachments/" + log.ID + "/服务 日志.log"
	if entry.Path != wantPath || entry.TicketNumber != number || entry.AttachedTo != "说明" ||
		entry.AttachmentID != log.ID || entry.Filename != "服务 日志.log" || entry.Size != 9 {
		t.Fatalf("清单文件归属 = %+v，想路径 %s", entry, wantPath)
	}
	sum := sha256.Sum256(files[wantPath])
	if entry.SHA256 != hex.EncodeToString(sum[:]) || string(files[wantPath]) != "slim-body" {
		t.Fatalf("瘦身附件或哈希不正确：%q / %s", files[wantPath], entry.SHA256)
	}

	var operations adminOperationsBody
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/operations?limit=20", nil, &operations)
	found := false
	for _, op := range operations.Operations {
		if op.OperationType == "slim_generate" && op.PackageName == pkg.Name &&
			len(op.TicketNumbers) == 1 && op.TicketNumbers[0] == number && op.OperatorName == "陈工" {
			found = true
		}
	}
	if !found {
		t.Fatalf("没有 slim_generate 操作记录：%+v", operations.Operations)
	}
}

func Test暂存瘦身包七天到期自动删除(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "瘦身到期")
	att := mustUploadAttachment(t, s, admin, number, "日志.txt", []byte("body"))
	if res := revise(t, s, admin, number, map[string]any{
		"attachment_ids": []string{att.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")
	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}

	s.Clock().Set(time.Date(2026, time.October, 16, 10, 0, 1, 0, clock.Beijing).Add(7 * 24 * time.Hour))
	admin = s.Login("陈工", "secret-pass")
	if err := s.Slims().CleanupExpired(); err != nil {
		t.Fatalf("触发到期清除失败：%v", err)
	}
	var list struct {
		Count int `json:"count"`
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/slim/packages", nil, &list); res.StatusCode != http.StatusOK || list.Count != 0 {
		t.Fatalf("到期后瘦身包列表 = %d/%+v，想空", res.StatusCode, list)
	}
	res := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if res.StatusCode != http.StatusNotFound || got.Error.Code != "slim_package_not_found" {
		t.Fatalf("到期后下载 = %d/%s，想 404 slim_package_not_found", res.StatusCode, got.Error.Code)
	}
}

func Test确认瘦身移出删除附件与备份并保留工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "瘦身保留正文")
	log := mustUploadAttachment(t, s, admin, number, "服务日志.log", []byte("slim-body"))
	image := mustUploadAttachment(t, s, admin, number, "现场图.png", []byte("image"))
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见 [服务日志](att:" + log.ID + ")。![现场图](att:" + image.ID + ")。",
		"attachment_ids": []string{log.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := reply(t, s, admin, number, "现场已经确认。"); res.StatusCode != http.StatusCreated {
		t.Fatalf("回复状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")

	var before ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &before)
	recordsBefore := len(readAccessRecords(t, s, admin, number).Records)
	notificationsBefore := notificationCount(t, s, admin)

	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatalf("生成备份镜像失败：%v", err)
	}
	id := strings.TrimPrefix(log.ID, "att_")
	mainFile := filepath.Join(s.DataDir(), "attachments", id)
	inlineFile := filepath.Join(s.DataDir(), "attachments", strings.TrimPrefix(image.ID, "att_"))
	mirrorFile := filepath.Join(s.Backups().TargetDir(), "attachments", id)
	for _, path := range []string{mainFile, inlineFile, mirrorFile} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("移出前应存在 %s：%v", path, err)
		}
	}

	removeURL := "/api/v1/admin/slim/packages/" + pkg.Name + "/remove"
	if res := s.Do(t, member, http.MethodPost, removeURL, map[string]any{"saved": true}, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员确认瘦身状态码 = %d，想 403", res.StatusCode)
	}
	var denied errorBody
	if res := s.Do(t, admin, http.MethodPost, removeURL, map[string]any{"saved": false}, &denied); res.StatusCode != http.StatusBadRequest ||
		denied.Error.Code != "slim_saved_confirmation_required" {
		t.Fatalf("未确认保存 = %d/%s，想 400 slim_saved_confirmation_required", res.StatusCode, denied.Error.Code)
	}

	var removed slimRemoveBody
	if res := s.Do(t, admin, http.MethodPost, removeURL, map[string]any{"saved": true}, &removed); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d", res.StatusCode)
	}
	if removed.PackageName != pkg.Name || len(removed.TicketNumbers) != 1 || removed.TicketNumbers[0] != number ||
		removed.AttachmentCount != 1 || removed.AttachmentBytes != 18 || removed.CleanupFailures != 0 || removed.RemovedAt == "" {
		t.Fatalf("瘦身移出响应 = %+v", removed)
	}
	if _, err := os.Stat(mainFile); !os.IsNotExist(err) {
		t.Fatalf("移出后附件主文件应删除：%v", err)
	}
	if _, err := os.Stat(mirrorFile); !os.IsNotExist(err) {
		t.Fatalf("移出后附件镜像应删除：%v", err)
	}
	if _, err := os.Stat(inlineFile); err != nil {
		t.Fatalf("插图应保留：%v", err)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "slim-packages", pkg.Name)); err != nil {
		t.Fatalf("瘦身包应暂存到 7 天：%v", err)
	}

	var after ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.Title != before.Title || after.Status != before.Status ||
		after.Brief.Markdown != before.Brief.Markdown || after.Brief.Revision != before.Brief.Revision ||
		len(after.Timeline) != len(before.Timeline) {
		t.Fatalf("瘦身改动了工单正文或时间线：before %+v，after %+v", before, after)
	}
	var list ticketListNumbersBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets?status=all&q="+url.QueryEscape("瘦身保留正文"), nil, &list)
	if list.Total != 1 || len(list.Items) != 1 || list.Items[0].Number != number {
		t.Fatalf("瘦身后搜索不到工单：%+v", list)
	}
	if got := len(readAccessRecords(t, s, admin, number).Records); got != recordsBefore {
		t.Fatalf("瘦身操作写访问记录：%d → %d", recordsBefore, got)
	}
	if got := notificationCount(t, s, admin); got != notificationsBefore {
		t.Fatalf("瘦身操作发通知：%d → %d", notificationsBefore, got)
	}
	var operations adminOperationsBody
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/operations?limit=20", nil, &operations)
	found := false
	for _, op := range operations.Operations {
		if op.OperationType == "slim_remove" && op.PackageName == pkg.Name && op.TicketNumbers[0] == number {
			found = true
		}
	}
	if !found {
		t.Fatalf("没有 slim_remove 操作记录：%+v", operations.Operations)
	}

	// 附件已全部移出（插图本来就不属于瘦身范围）后，同一张工单不再是候选。
	var stale errorBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/preview",
		map[string]any{"ticket_numbers": []string{number}}, &stale); res.StatusCode != http.StatusUnprocessableEntity ||
		stale.Error.Code != "ticket_not_slimmable" {
		t.Fatalf("再次预览 = %d/%s，想 422 ticket_not_slimmable", res.StatusCode, stale.Error.Code)
	}
}

func notificationCount(t testing.TB, s *tbtest.Server, c *http.Client) int {
	t.Helper()
	var notes notificationsBody
	if res := s.Do(t, c, http.MethodGet, "/api/v1/notifications?limit=200", nil, &notes); res.StatusCode != http.StatusOK {
		t.Fatalf("读取通知状态码 = %d", res.StatusCode)
	}
	return len(notes.Items)
}

func Test确认瘦身移出前重新校验工单与附件变化(t *testing.T) {
	t.Run("生成后重新打开", func(t *testing.T) {
		s := tbtest.Start(t)
		s.CreateAdmin("陈工", "secret-pass")
		admin := s.Login("陈工", "secret-pass")

		setStorageClock(t, s, 2026, time.June, 2)
		number := mustCreateTicket(t, s, admin, "生成后重开")
		att := mustUploadAttachment(t, s, admin, number, "日志.txt", []byte("body"))
		if res := revise(t, s, admin, number, map[string]any{
			"attachment_ids": []string{att.ID}, "base_revision": 1,
		}); res.StatusCode != http.StatusOK {
			t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
		}
		if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
			t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
		}
		setStorageClock(t, s, 2026, time.October, 16)
		admin = s.Login("陈工", "secret-pass")
		var pkg slimPackageBody
		if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
			map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
			t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
		}
		if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("重新开启状态码 = %d", res.StatusCode)
		}
		var rejected errorBody
		res := s.Do(t, admin, http.MethodPost,
			"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, &rejected)
		if res.StatusCode != http.StatusConflict || rejected.Error.Code != "slim_package_stale" {
			t.Fatalf("重开后确认瘦身 = %d/%s，想 409 slim_package_stale", res.StatusCode, rejected.Error.Code)
		}
		if _, err := os.Stat(filepath.Join(s.DataDir(), "attachments", strings.TrimPrefix(att.ID, "att_"))); err != nil {
			t.Fatalf("被拒绝后附件文件不应删除：%v", err)
		}
	})

	t.Run("生成后主文件丢失", func(t *testing.T) {
		s := tbtest.Start(t)
		s.CreateAdmin("陈工", "secret-pass")
		admin := s.Login("陈工", "secret-pass")

		setStorageClock(t, s, 2026, time.June, 2)
		number := mustCreateTicket(t, s, admin, "生成后文件丢失")
		att := mustUploadAttachment(t, s, admin, number, "日志.txt", []byte("body"))
		if res := revise(t, s, admin, number, map[string]any{
			"attachment_ids": []string{att.ID}, "base_revision": 1,
		}); res.StatusCode != http.StatusOK {
			t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
		}
		if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
			t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
		}
		setStorageClock(t, s, 2026, time.October, 16)
		admin = s.Login("陈工", "secret-pass")
		var pkg slimPackageBody
		if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
			map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
			t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
		}
		path := filepath.Join(s.DataDir(), "attachments", strings.TrimPrefix(att.ID, "att_"))
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		var rejected errorBody
		res := s.Do(t, admin, http.MethodPost,
			"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, &rejected)
		if res.StatusCode != http.StatusConflict || rejected.Error.Code != "slim_package_stale" {
			t.Fatalf("文件丢失后确认瘦身 = %d/%s，想 409 slim_package_stale", res.StatusCode, rejected.Error.Code)
		}
		var operations adminOperationsBody
		s.Do(t, admin, http.MethodGet, "/api/v1/admin/operations?limit=20", nil, &operations)
		for _, op := range operations.Operations {
			if op.OperationType == "slim_remove" {
				t.Fatalf("被拒绝的移出不应写 slim_remove：%+v", op)
			}
		}
	})
}

func Test瘦身移出提交后清理失败只在响应里计数(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "瘦身清理失败")
	att := mustUploadAttachment(t, s, admin, number, "日志.txt", []byte("cleanup-me"))
	if res := revise(t, s, admin, number, map[string]any{
		"attachment_ids": []string{att.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")
	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	backupAttachments := filepath.Join(s.Backups().TargetDir(), "attachments")
	if err := os.Chmod(backupAttachments, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(backupAttachments, 0o755); err != nil {
			t.Fatal(err)
		}
	}()

	var removed slimRemoveBody
	res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, &removed)
	if res.StatusCode != http.StatusOK || removed.CleanupFailures != 1 {
		t.Fatalf("清理失败响应 = %d/%+v，想 200 且 cleanup_failures=1", res.StatusCode, removed)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "attachments", strings.TrimPrefix(att.ID, "att_"))); !os.IsNotExist(err) {
		t.Fatalf("主附件文件应已删除：%v", err)
	}
	var operations adminOperationsBody
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/operations?limit=20", nil, &operations)
	found := false
	for _, op := range operations.Operations {
		if op.OperationType == "slim_remove" && op.PackageName == pkg.Name {
			found = true
		}
	}
	if !found {
		t.Fatalf("清理失败仍应写 slim_remove：%+v", operations.Operations)
	}
}
