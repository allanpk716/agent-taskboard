package server_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// #130：整张工单的附件。attachment_ids 是说明每个修订版本的有序列表；不传沿用、
// [] 清空、传了整体替换，只改列表也推进 revision。
func Test修订说明的整单附件列表(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateAdmin("李工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")
	otherNumber := mustCreateTicket(t, s, web, "另一张工单")

	log := mustUploadAttachment(t, s, web, number, "服务日志.log", []byte("first-log"))
	photo := mustUploadAttachment(t, s, web, number, "现场照片.png", []byte("photo"))

	type briefBody struct {
		Markdown string `json:"markdown"`
		Revision int64  `json:"revision"`
		Fields   *struct {
			Value string `json:"value"`
		} `json:"fields"`
		AttachmentIDs []string `json:"attachment_ids"`
	}
	type body struct {
		Brief       briefBody            `json:"brief"`
		Attachments []attachmentViewBody `json:"attachments"`
		Timeline    []timelineEventBody  `json:"timeline"`
		Unused      map[string]any       `json:"-"`
	}
	read := func(ref string) body {
		var got body
		s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+ref, nil, &got)
		return got
	}

	// 旧说明没有附件：brief.attachment_ids 是空数组，不是 null。
	got := read(number)
	empty := []string{}
	if got.Brief.AttachmentIDs == nil || len(got.Brief.AttachmentIDs) != 0 {
		t.Fatalf("旧说明的 brief.attachment_ids = %#v, 想 []", got.Brief.AttachmentIDs)
	}
	_ = empty

	// 首次挂上；引用不出现在说明正文或 fields。
	if res := revise(t, s, web, number, map[string]any{
		"attachment_ids": []string{log.ID, photo.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂整单附件状态码 = %d, 想 200", res.StatusCode)
	}
	got = read(number)
	if strings.Join(got.Brief.AttachmentIDs, ",") != log.ID+","+photo.ID {
		t.Errorf("brief.attachment_ids = %v, 想 [%s %s]", got.Brief.AttachmentIDs, log.ID, photo.ID)
	}
	if got.Brief.Revision != 2 {
		t.Errorf("只改列表后的 revision = %d, 想 2", got.Brief.Revision)
	}
	if strings.Contains(got.Brief.Markdown, log.ID) || strings.Contains(got.Brief.Markdown, photo.ID) {
		t.Errorf("附件区引用不应写进 brief_md：%q", got.Brief.Markdown)
	}
	if got.Brief.Fields != nil {
		t.Errorf("非模板工单的 fields = %v, 想 nil", got.Brief.Fields)
	}

	// 不传就沿用上一版；传 [] 才清空。
	if res := revise(t, s, web, number, map[string]any{"title": "整理 Q3 规则", "base_revision": 2}); res.StatusCode != http.StatusOK {
		t.Fatalf("不传 attachment_ids 状态码 = %d, 想 200", res.StatusCode)
	}
	got = read(number)
	if strings.Join(got.Brief.AttachmentIDs, ",") != log.ID+","+photo.ID {
		t.Errorf("不传时列表被改动：%v", got.Brief.AttachmentIDs)
	}
	if res := revise(t, s, web, number, map[string]any{"attachment_ids": []string{}, "base_revision": 3}); res.StatusCode != http.StatusOK {
		t.Fatalf("清空附件状态码 = %d, 想 200", res.StatusCode)
	}
	got = read(number)
	if len(got.Brief.AttachmentIDs) != 0 {
		t.Errorf("[] 后 brief.attachment_ids = %v, 想空", got.Brief.AttachmentIDs)
	}
	if len(got.Attachments) != 2 {
		t.Fatalf("删出列表的附件不应物理删除：%+v", got.Attachments)
	}
	for _, att := range got.Attachments {
		if att.AttachedTo != "说明" {
			t.Errorf("%s 的 attached_to = %q, 想 说明", att.ID, att.AttachedTo)
		}
		if res := s.Do(t, web, http.MethodGet, att.URL, nil, nil); res.StatusCode != http.StatusOK {
			t.Errorf("下载 %s 状态码 = %d, 想 200", att.ID, res.StatusCode)
		}
	}
	md := string(readAll(t, s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)))
	if !strings.Contains(md, "## 附件") || !strings.Contains(md, "服务日志.log") || !strings.Contains(md, "说明") {
		t.Errorf("Markdown 附件小节应保留删出列表的附件：\n%s", md)
	}

	// 清空后整体替换回一个；说明修订事件也带这一版的列表。
	if res := revise(t, s, web, number, map[string]any{"attachment_ids": []string{photo.ID}, "base_revision": 4}); res.StatusCode != http.StatusOK {
		t.Fatalf("整体替换状态码 = %d, 想 200", res.StatusCode)
	}
	got = read(number)
	if strings.Join(got.Brief.AttachmentIDs, ",") != photo.ID {
		t.Errorf("整体替换后 = %v, 想 [%s]", got.Brief.AttachmentIDs, photo.ID)
	}
	var revised *timelineEventBody
	for i := range got.Timeline {
		if got.Timeline[i].Type == "brief_revised" {
			revise := got.Timeline[i]
			revised = &revise
		}
	}
	if revised == nil || len(revised.AttachmentIDs) != 1 || revised.AttachmentIDs[0] != photo.ID {
		t.Errorf("brief_revised 事件的 attachment_ids = %+v, 想 [%s]", revised, photo.ID)
	}

	// 不能引用别人的待用附件、别的工单的附件，或已过期待用被清除的附件。
	foreign := mustUploadAttachment(t, s, li, number, "李工的日志.log", []byte("li"))
	otherTicket := mustUploadAttachment(t, s, web, otherNumber, "别的单.log", []byte("other"))
	stale := mustUploadAttachment(t, s, web, number, "过期.log", []byte("stale"))
	s.Clock().Advance(25 * time.Hour)
	if n, err := s.Attachments().CleanupUnreferenced(context.Background()); err != nil || n < 1 {
		t.Fatalf("清除过期待用附件 = %d, err = %v, 想至少 1 个", n, err)
	}
	for name, id := range map[string]string{"别人的": foreign.ID, "别的工单": otherTicket.ID, "过期的": stale.ID} {
		res := revise(t, s, web, number, map[string]any{"attachment_ids": []string{id}, "base_revision": 5})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("[%s] 状态码 = %d, 想 422", name, res.StatusCode)
			continue
		}
		var errBody errorBody
		tbtest.DecodeJSON(t, res, &errBody)
		if errBody.Error.Code != "attachment_not_usable" {
			t.Errorf("[%s] 错误码 = %q, 想 attachment_not_usable", name, errBody.Error.Code)
		}
		if !strings.Contains(errBody.Error.Hint, "上传接口") {
			t.Errorf("[%s] hint 应指向上传接口：%q", name, errBody.Error.Hint)
		}
	}
}
