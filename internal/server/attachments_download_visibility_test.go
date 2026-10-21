package server_test

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test附件下载成员不可见时返回附件404并记录附件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("旁观者", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")
	number := mustCreateTicket(t, s, creator, "附件下载可见范围")
	att := mustUploadAttachment(t, s, creator, number, "内部日志.log", []byte("secret"))
	if res := claimByReply(t, s, creator, number, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d", res.StatusCode)
	}

	res := downloadAttachment(t, s, outsider, "/api/v1/attachments/"+att.ID+"/"+url.PathEscape("内部日志.log"))
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("可见范围外附件下载状态码 = %d, 想 404", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "attachment_not_found" || got.Error.Message != "没有这个附件" ||
		got.Error.Hint != "附件引用从上传响应或工单读取接口拿" {
		t.Fatalf("可见范围外附件响应不对：%+v", got.Error)
	}

	records := readAccessRecords(t, s, creator, number)
	if len(records.Records) != 1 {
		t.Fatalf("访问记录 = %+v, 想 1 条被拒记录", records.Records)
	}
	record := records.Records[0]
	if record.Action != "denied" || record.Actor.Kind != "member" || record.Actor.Name != "旁观者" ||
		record.Attachment.ID != att.ID || record.Attachment.Filename != "内部日志.log" {
		t.Fatalf("附件被拒记录不对：%+v", record)
	}
}

func Test附件下载访客跨工单保持工单404且不记录(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("创建者", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	mine := mustCreateTicket(t, s, creator, "目标附件所在工单")
	other := mustCreateTicket(t, s, creator, "访客链接指向的工单")
	att := mustUploadAttachment(t, s, creator, mine, "现场.png", []byte("png"))
	if res := claimByReply(t, s, creator, mine, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d", res.StatusCode)
	}
	badLink := mustCreateGuestLink(t, s, creator, other, map[string]any{"note": "别单"})
	guest := s.GuestClient(guestKeyOf(t, badLink))

	res := downloadAttachment(t, s, guest, "/api/v1/attachments/"+att.ID+"/"+url.PathEscape("现场.png"))
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("访客跨工单下载状态码 = %d, 想 404", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "ticket_not_found" || got.Error.Message != "没有这张工单" ||
		got.Error.Hint != "访客链接只能访问它所指向的那张工单" {
		t.Fatalf("访客跨工单响应不对：%+v", got.Error)
	}
	if records := readAccessRecords(t, s, creator, mine); len(records.Records) != 0 {
		t.Fatalf("访客跨工单不应写访问记录：%+v", records.Records)
	}
}
