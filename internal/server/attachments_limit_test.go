package server_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 单个文件超限（验收点）：413 attachment_too_large，hint 给出可转发的 NAS
// 路径示例（含工单编号与文件名）。用调小的上限测试，不打真实 5MB 载荷。
func Test单个文件超限返回413并提示NAS(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithAttachments(1, 5, 10, 50))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	// 恰好等于上限（1MB）可以通过；超过被拒。
	exact := bytes.Repeat([]byte{0x61}, 1<<20)
	if got := mustUploadAttachment(t, s, web, number, "正好一兆.bin", exact); got.Size != int64(1<<20) {
		t.Fatalf("size = %d, 想 %d", got.Size, int64(1<<20))
	}
	over := bytes.Repeat([]byte{0x62}, (1<<20)+1)
	res := uploadAttachment(t, s, web, number, "超过一兆.bin", over)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("超限状态码 = %d, 想 413", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "attachment_too_large" {
		t.Errorf("错误码 = %q, 想 attachment_too_large", got.Error.Code)
	}
	if !strings.Contains(got.Error.Hint, `\\nas01\共享\工单\`+number+`\超过一兆.bin`) {
		t.Errorf("hint 应带 NAS 路径示例（含工单编号与文件名）：%q", got.Error.Hint)
	}
	// 被拒的上传不占配额。
	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.AttachmentUsage.UsedBytes != int64(1<<20) {
		t.Errorf("used_bytes = %d, 想 %d（被拒的不占配额）", view.AttachmentUsage.UsedBytes, int64(1<<20))
	}
}

// 整张工单合计超限（验收点）：待用附件也计入配额；413 ticket_quota_exceeded
// 附 used_mb 与上限，hint 提示改用 NAS 路径。
func Test整单附件合计超限返回413并附用量(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithAttachments(1, 5, 10, 1))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	first := bytes.Repeat([]byte{0x61}, 700*(1<<10)) // 700KB，第一个可以
	if got := mustUploadAttachment(t, s, web, number, "第一个.zip", first); got.State != "pending" {
		t.Fatalf("state = %q, 想 pending", got.State)
	}
	second := bytes.Repeat([]byte{0x62}, 700*(1<<10)) // 再来 700KB：合计超过 1MB
	res := uploadAttachment(t, s, web, number, "第二个.zip", second)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("合计超限状态码 = %d, 想 413", res.StatusCode)
	}
	var raw struct {
		Error map[string]any `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &raw)
	if raw.Error["code"] != "ticket_quota_exceeded" {
		t.Fatalf("错误码 = %v, 想 ticket_quota_exceeded", raw.Error["code"])
	}
	if hint, _ := raw.Error["hint"].(string); !strings.Contains(hint, "NAS") {
		t.Errorf("hint 应提示 NAS 路径：%v", raw.Error["hint"])
	}
	// 附 used_mb 与 ticket_limit_mb（api-contract：413 附 used_mb）。
	if raw.Error["used_mb"] == nil || raw.Error["ticket_limit_mb"] == nil {
		t.Errorf("错误体应附 used_mb 与 ticket_limit_mb：%+v", raw.Error)
	}

	// 未引用（待用）的附件同样计入配额——用量回显里看得到。
	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if view.AttachmentUsage.UsedBytes != int64(len(first)) {
		t.Errorf("used_bytes = %d, 想 %d（待用附件计入配额）", view.AttachmentUsage.UsedBytes, int64(len(first)))
	}

	// 另一张工单互不影响（配额按单计）。
	other := mustCreateTicket(t, s, web, "另一张工单")
	if got := mustUploadAttachment(t, s, web, other, "别单的文件.zip", second); got.State != "pending" {
		t.Errorf("别单上传 state = %q, 想 pending", got.State)
	}
}

// count_exceeded 的 hint 也提示 NAS（三种 413 都带 NAS 提示，验收点）。
func Test个数超限的hint也提示NAS(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithAttachments(1, 1, 1, 50))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	a := mustUploadAttachment(t, s, web, number, "a.zip", []byte("a"))
	b := mustUploadAttachment(t, s, web, number, "b.zip", []byte("b"))
	res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{a.ID, b.ID}})
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d, 想 413", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "attachment_count_exceeded" || !strings.Contains(got.Error.Hint, `\\nas01\共享\工单\`+number) {
		t.Errorf("错误体不对：%+v", got.Error)
	}
}
