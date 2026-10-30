// 归档包接口（#235）：黑盒测试只通过 HTTP 观察预览、生成、下载与暂存清除。
package server_test

import (
	"net/http"
	"testing"
	"time"

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
	BodyMD        string   `json:"body_md"`
	AttachmentIDs []string `json:"attachment_ids"`
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
	s.Clock().Set(time.Date(2026, time.October, 30, 10, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	var invalid errorBody
	res = s.Do(t, admin, http.MethodPost, "/api/v1/admin/archive/preview",
		map[string]any{"closed_months": 3, "ticket_numbers": []string{open}}, &invalid)
	if res.StatusCode != http.StatusBadRequest || invalid.Error.Code != "ticket_not_archivable" {
		t.Fatalf("未关闭工单预览 = %d/%s, 想 400 ticket_not_archivable", res.StatusCode, invalid.Error.Code)
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
	if preview.TicketCount != 1 || preview.AttachmentBytes != int64(len("1234")) {
		t.Fatalf("预览数量/附件大小 = %d/%d, 想 1/4", preview.TicketCount, preview.AttachmentBytes)
	}
	if len(preview.Tickets) != 1 || preview.Tickets[0].Number != number || preview.Tickets[0].Title != "可归档工单" {
		t.Fatalf("预览工单清单 = %+v", preview.Tickets)
	}
	if len(preview.Drafts) != 1 || preview.Drafts[0].TicketNumber != number ||
		preview.Drafts[0].BodyMD != "草稿正文" || len(preview.Drafts[0].AttachmentIDs) != 1 {
		t.Fatalf("预览未发出草稿 = %+v", preview.Drafts)
	}
}
