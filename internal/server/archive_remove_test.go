// 归档移除接口（#236）：黑盒测试通过 HTTP 与公开的数据目录观察外部行为。
package server_test

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type archiveRemoveBody struct {
	TicketCount     int64                  `json:"ticket_count"`
	AttachmentBytes int64                  `json:"attachment_bytes"`
	PackageName     string                 `json:"package_name"`
	RemovedAt       string                 `json:"removed_at"`
	Tombstones      []archiveTombstoneBody `json:"tombstones"`
	CleanupFailures int                    `json:"cleanup_failures"`
}

type archiveTombstoneBody struct {
	Seq          int64  `json:"seq"`
	Number       string `json:"number"`
	Title        string `json:"title"`
	ClosedAt     string `json:"closed_at"`
	ArchivedAt   string `json:"archived_at"`
	OperatorName string `json:"operator_name"`
	PackageName  string `json:"package_name"`
}

type archivedTicketErrorBody struct {
	Error struct {
		Code           string               `json:"code"`
		Message        string               `json:"message"`
		ArchivedTicket archiveTombstoneBody `json:"archived_ticket"`
	} `json:"error"`
}

type ticketListNumbersBody struct {
	Total    int                   `json:"total"`
	Items    []ticketListItemBody  `json:"items"`
	Archived *archiveTombstoneBody `json:"archived"`
}

type ticketListItemBody struct {
	Number string `json:"number"`
}

func Test移除归档包必须确认已保存并删除附件与镜像(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "要移除的归档工单")
	att := mustUploadAttachment(t, s, admin, number, "日志.txt", []byte("remove-me"))
	if res := revise(t, s, admin, number, map[string]any{
		"brief_md":       "见 [日志](att:" + att.ID + ")。",
		"attachment_ids": []string{att.ID},
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
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatalf("生成备份镜像失败：%v", err)
	}
	attachmentID := strings.TrimPrefix(att.ID, "att_")
	attachmentFile := filepath.Join(s.DataDir(), "attachments", attachmentID)
	mirrorFile := filepath.Join(s.Backups().TargetDir(), "attachments", attachmentID)
	if _, err := os.Stat(attachmentFile); err != nil {
		t.Fatalf("移除前附件文件应存在：%v", err)
	}
	if _, err := os.Stat(mirrorFile); err != nil {
		t.Fatalf("移除前附件镜像应存在：%v", err)
	}

	removeURL := "/api/v1/admin/archive/packages/" + pkg.Name + "/remove"
	var denied errorBody
	if res := s.Do(t, admin, http.MethodPost, removeURL, map[string]any{"saved": false}, &denied); res.StatusCode != http.StatusBadRequest ||
		denied.Error.Code != "archive_saved_confirmation_required" {
		t.Fatalf("未确认已保存 = %d/%s, 想 400 archive_saved_confirmation_required", res.StatusCode, denied.Error.Code)
	}

	var removed archiveRemoveBody
	if res := s.Do(t, admin, http.MethodPost, removeURL, map[string]any{"saved": true}, &removed); res.StatusCode != http.StatusOK {
		t.Fatalf("移除状态码 = %d, 想 200", res.StatusCode)
	}
	if removed.TicketCount != 1 || removed.AttachmentBytes != 2*int64(len("remove-me")) ||
		removed.PackageName != pkg.Name || len(removed.Tombstones) != 1 {
		t.Fatalf("移除响应 = %+v", removed)
	}
	tombstone := removed.Tombstones[0]
	if tombstone.Number != number || tombstone.Title != "要移除的归档工单" ||
		tombstone.OperatorName != "陈工" || tombstone.PackageName != pkg.Name ||
		tombstone.ClosedAt == "" || tombstone.ArchivedAt == "" || tombstone.Seq == 0 {
		t.Fatalf("墓碑信息 = %+v", tombstone)
	}
	if _, err := os.Stat(attachmentFile); !os.IsNotExist(err) {
		t.Fatalf("移除后附件文件应删除，stat = %v", err)
	}
	if _, err := os.Stat(mirrorFile); !os.IsNotExist(err) {
		t.Fatalf("移除后附件镜像应删除，stat = %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.DataDir(), "archive-packages", pkg.Name)); err != nil {
		t.Fatalf("移除后暂存包应保留到 7 天到期：%v", err)
	}

	var packages archivePackageListBody
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/archive/packages", nil, &packages)
	markedRemoved := false
	for _, listed := range packages.Packages {
		if listed.Name == pkg.Name {
			markedRemoved = listed.RemovedTicketCount == 1
		}
	}
	if !markedRemoved {
		t.Fatalf("列表应标注已移除的暂存包：%+v", packages.Packages)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusConflict {
		t.Fatalf("重复移除状态码 = %d, 想 409", res.StatusCode)
	}
}

func Test已归档工单读取返回墓碑且列表搜索不出现(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	wang := s.Login("王工", "member-pass")
	_, token := s.IssueAgentToken(t, admin, "秘书")
	adminAgent := s.AgentClient(token)

	setStorageClock(t, s, 2026, time.June, 2)
	created := createTicket(t, s, admin, createTicketBody{
		Title: "归档后看墓碑", BriefMD: "旧工单", Labels: []string{"机型:UD-2300"},
	})
	if created.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", created.StatusCode)
	}
	var ticket createTicketResponse
	tbtest.DecodeJSON(t, created, &ticket)
	number := ticket.Number
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "王工"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("指派工单状态码 = %d, 想 200", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	kept := mustCreateTicket(t, s, admin, "仍在平台")
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	wang = s.Login("王工", "member-pass")

	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档包状态码 = %d, 想 200", res.StatusCode)
	}

	var detail archivedTicketErrorBody
	res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &detail)
	if res.StatusCode != http.StatusGone || detail.Error.Code != "ticket_archived" {
		t.Fatalf("成员读取已归档工单 = %d/%s, 想 410 ticket_archived", res.StatusCode, detail.Error.Code)
	}
	if detail.Error.ArchivedTicket.Number != number || detail.Error.ArchivedTicket.Title != "归档后看墓碑" ||
		detail.Error.ArchivedTicket.OperatorName != "陈工" || detail.Error.ArchivedTicket.PackageName != pkg.Name ||
		detail.Error.ArchivedTicket.ClosedAt == "" || detail.Error.ArchivedTicket.ArchivedAt == "" {
		t.Fatalf("读取错误里的墓碑 = %+v", detail.Error.ArchivedTicket)
	}

	agentDetail := archivedTicketErrorBody{}
	res = s.Do(t, adminAgent, http.MethodGet, "/api/v1/tickets/"+number, nil, &agentDetail)
	if res.StatusCode != http.StatusGone || agentDetail.Error.Code != "ticket_archived" ||
		agentDetail.Error.ArchivedTicket.Number != number {
		t.Fatalf("成员 Agent 读取已归档工单 = %d/%+v, 想 410 ticket_archived", res.StatusCode, agentDetail)
	}

	var list ticketListNumbersBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets?status=all&limit=200", nil, &list)
	for _, item := range list.Items {
		if item.Number == number {
			t.Fatalf("默认列表出现已归档工单：%+v", list.Items)
		}
	}
	found := false
	for _, item := range list.Items {
		if item.Number == kept {
			found = true
		}
	}
	if !found {
		t.Fatalf("列表缺少仍在平台的工单 %s：%+v", kept, list.Items)
	}

	list = ticketListNumbersBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets?status=all&q=归档后看墓碑", nil, &list)
	if list.Total != 0 || len(list.Items) != 0 {
		t.Fatalf("搜索应默认不出现已归档工单：%+v", list)
	}

	var notes notificationsBody
	s.Do(t, wang, http.MethodGet, "/api/v1/notifications?limit=200", nil, &notes)
	keptNote := false
	for _, item := range notes.Items {
		if item.Ticket.Number == number {
			keptNote = true
		}
	}
	if !keptNote {
		t.Fatalf("已归档工单的历史通知应保留：%+v", notes.Items)
	}
}

func Test墓碑读取与精确搜索按可见范围过滤(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	s.CreateMember("李工", "member-pass")
	s.CreateMember("赵工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	wang := s.Login("王工", "member-pass")
	li := s.Login("李工", "member-pass")
	zhao := s.Login("赵工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "不能泄露给无关成员")
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "王工"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("指派给王工状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "李工"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("改派给李工状态码 = %d, 想 200", res.StatusCode)
	}
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	kept := mustCreateTicket(t, s, admin, "他工单访客链接")
	keptLink := s.CreateGuestLink(t, admin, kept, map[string]any{"note": "现场", "valid_days": 90})
	keptGuest := s.GuestClient(keptLink.Key(t))
	wang = s.Login("王工", "member-pass")
	li = s.Login("李工", "member-pass")
	zhao = s.Login("赵工", "member-pass")
	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档包状态码 = %d, 想 200", res.StatusCode)
	}

	for name, tc := range map[string]struct {
		c    *http.Client
		want int
	}{
		"可见成员":  {li, http.StatusGone},
		"通知持有者": {wang, http.StatusGone},
		"管理员":   {admin, http.StatusGone},
		"无关成员":  {zhao, http.StatusNotFound},
		"他工单访客": {keptGuest, http.StatusNotFound},
	} {
		var got archivedTicketErrorBody
		res := s.Do(t, tc.c, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
		if res.StatusCode != tc.want || (tc.want == http.StatusGone && got.Error.Code != "ticket_archived") ||
			(tc.want == http.StatusNotFound && (got.Error.Code != "ticket_not_found" || got.Error.ArchivedTicket.Number != "")) {
			t.Fatalf("%s读取墓碑 = %d/%+v, 想 %d", name, res.StatusCode, got, tc.want)
		}
	}

	for name, tc := range map[string]struct {
		c    *http.Client
		want bool
	}{
		"可见成员": {li, true},
		"管理员":  {admin, true},
		"无关成员": {zhao, false},
	} {
		var list ticketListNumbersBody
		res := s.Do(t, tc.c, http.MethodGet, "/api/v1/tickets?status=all&q="+number, nil, &list)
		if res.StatusCode != http.StatusOK || list.Total != 0 || len(list.Items) != 0 {
			t.Fatalf("%s精确搜索已归档编号 = %d/%+v, 想 200 且默认结果不变", name, res.StatusCode, list)
		}
		if (list.Archived != nil) != tc.want || (tc.want && list.Archived.Number != number) {
			t.Fatalf("%s精确搜索墓碑提示 = %+v, want=%v", name, list.Archived, tc.want)
		}
	}
}

func Test归档后新工单不复用当日编号(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	first := mustCreateTicket(t, s, admin, "当天第一张")
	if first != "20260602-001" {
		t.Fatalf("第一张编号 = %s, 想 20260602-001", first)
	}
	if res := closeTicket(t, s, admin, first, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	var pkg archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{first}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d, 想 201", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档包状态码 = %d, 想 200", res.StatusCode)
	}

	setStorageClock(t, s, 2026, time.June, 2)
	admin = s.Login("陈工", "secret-pass")
	second := mustCreateTicket(t, s, admin, "回到同一天")
	if second != "20260602-002" {
		t.Fatalf("归档后新编号 = %s, 想 20260602-002", second)
	}

	var operations adminOperationsBody
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/operations", nil, &operations); res.StatusCode != http.StatusOK {
		t.Fatalf("读取管理操作状态码 = %d, 想 200", res.StatusCode)
	}
	found := false
	for _, op := range operations.Operations {
		if op.OperationType == "archive_remove" && op.PackageName == pkg.Name &&
			len(op.TicketNumbers) == 1 && op.TicketNumbers[0] == first && op.OperatorName == "陈工" {
			found = true
		}
	}
	if !found {
		t.Fatalf("缺少 archive_remove 管理操作：%+v", operations.Operations)
	}
}

func Test生成归档包后工单变化时移除被拒绝(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "生成后变化")
	if res := closeTicket(t, s, admin, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d, 想 200", res.StatusCode)
	}
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	var stale archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &stale); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成旧包状态码 = %d, 想 201", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("生成后读取工单状态码 = %d, 想 200", res.StatusCode)
	}

	var rejected errorBody
	removeURL := "/api/v1/admin/archive/packages/" + stale.Name + "/remove"
	if res := s.Do(t, admin, http.MethodPost, removeURL, map[string]any{"saved": true}, &rejected); res.StatusCode != http.StatusConflict ||
		rejected.Error.Code != "archive_package_stale" {
		t.Fatalf("旧包移除 = %d/%s, 想 409 archive_package_stale", res.StatusCode, rejected.Error.Code)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("被拒绝后工单状态码 = %d, 想 200", res.StatusCode)
	}

	var fresh archivePackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{number}}, &fresh); res.StatusCode != http.StatusCreated {
		t.Fatalf("重新生成包状态码 = %d, 想 201", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+fresh.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重新生成后移除状态码 = %d, 想 200", res.StatusCode)
	}
}

func Test提交后清理失败不影响移除成功(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "清理失败仍算移除成功")
	att := mustUploadAttachment(t, s, admin, number, "日志.txt", []byte("cleanup-me"))
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
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatalf("生成备份镜像失败：%v", err)
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

	var removed archiveRemoveBody
	res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, &removed)
	if res.StatusCode != http.StatusOK || removed.CleanupFailures < 1 {
		t.Fatalf("清理失败后的移除响应 = %d/%+v, 想 200 且报告清理失败", res.StatusCode, removed)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusGone {
		t.Fatalf("工单状态码 = %d, 想 410", res.StatusCode)
	}
}

func Test重新开启后的归档包不能移除并返回不可归档错误(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "生成后被重新开启")
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
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重新开启状态码 = %d, 想 200", res.StatusCode)
	}
	var rejected errorBody
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, &rejected); res.StatusCode != http.StatusBadRequest ||
		rejected.Error.Code != "ticket_not_archivable" {
		t.Fatalf("重新开启后移除 = %d/%s, 想 400 ticket_not_archivable", res.StatusCode, rejected.Error.Code)
	}
	if res := s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("被拒绝后工单状态码 = %d, 想 200", res.StatusCode)
	}
}

func Test访客旧链接命中墓碑返回已归档(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, admin, "访客旧链接")
	link := s.CreateGuestLink(t, admin, number, map[string]any{"note": "现场", "valid_days": 90})
	rawGuest := s.AgentClient(link.Key(t))
	claimedGuest := s.GuestClient(link.Key(t))
	if res := s.Do(t, claimedGuest, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("归档前访客访问状态码 = %d, 想 200", res.StatusCode)
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
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("移除归档包状态码 = %d, 想 200", res.StatusCode)
	}

	for name, client := range map[string]*http.Client{"链接密钥": rawGuest, "已认领设备": claimedGuest} {
		var got archivedTicketErrorBody
		res := s.Do(t, client, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
		if res.StatusCode != http.StatusGone || got.Error.Code != "ticket_archived" ||
			got.Error.ArchivedTicket.Number != number || got.Error.ArchivedTicket.PackageName != pkg.Name {
			t.Fatalf("%s读取已归档工单 = %d/%+v, 想 410 ticket_archived 和墓碑", name, res.StatusCode, got)
		}
	}

	var got archivedTicketErrorBody
	res := s.Do(t, rawGuest, http.MethodGet, "/api/v1/guest-link", nil, &got)
	if res.StatusCode != http.StatusGone || got.Error.Code != "ticket_archived" ||
		got.Error.ArchivedTicket.Number != number {
		t.Fatalf("旧链接读取认领信息 = %d/%+v, 想 410 ticket_archived 和墓碑", res.StatusCode, got)
	}
}
