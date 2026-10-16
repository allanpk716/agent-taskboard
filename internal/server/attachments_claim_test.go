package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 回复认领附件（两步上传的第二步）：attachment_ids 与正文 att: 引用合并去重，
// 每个附件挂到「回复 N」上；回复事件带 attachment_ids；空正文但有附件不算空回复；
// 不存在、已挂在别处或不是本次凭证上传的引用都返回 422 attachment_not_usable。
func Test回复认领附件并挂到时间线(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "BH-7600CS 质控结果偏高")

	png := mustUploadAttachment(t, s, web, number, "质控图.png", []byte("png-bytes"))
	zip := mustUploadAttachment(t, s, web, number, "历史数据.zip", []byte("zip-bytes"))

	// 正文插图引用 + 显式 attachment_ids，合并去重后挂上。
	res := claimByReply(t, s, web, number, map[string]any{
		"body_md":        "试剂批号已更换，复测结果见图。\n\n![质控图.png](att:" + png.ID + ")",
		"attachment_ids": []string{zip.ID, zip.ID},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("带附件回复状态码 = %d, 想 201", res.StatusCode)
	}

	// 工单 GET：两个附件都挂上（attached_to = 回复 1），带下载地址。
	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Attachments) != 2 {
		t.Fatalf("附件列表有 %d 个, 想 2：%+v", len(view.Attachments), view.Attachments)
	}
	for _, att := range view.Attachments {
		if att.AttachedTo != "回复 1" {
			t.Errorf("%s 的 attached_to = %q, 想 回复 1", att.ID, att.AttachedTo)
		}
		if !strings.HasPrefix(att.URL, "/api/v1/attachments/att_") {
			t.Errorf("%s 的 url 不对：%q", att.ID, att.URL)
		}
	}

	// 时间线：回复事件带合并去重后的 attachment_ids。
	var full ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &full)
	var replyEv *timelineEventBody
	for i := range full.Timeline {
		if full.Timeline[i].Type == "reply" {
			replyEv = &full.Timeline[i]
		}
	}
	if replyEv == nil {
		t.Fatal("没有回复事件")
	}
	if len(replyEv.AttachmentIDs) != 2 || replyEv.AttachmentIDs[0] != png.ID || replyEv.AttachmentIDs[1] != zip.ID {
		t.Errorf("回复事件的 attachment_ids = %v, 想 [%s %s]", replyEv.AttachmentIDs, png.ID, zip.ID)
	}

	// 空正文但有附件：不算空回复（422 empty_reply 只在两者皆无时）。
	third := mustUploadAttachment(t, s, web, number, "补充.txt", []byte("txt"))
	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{third.ID}}); res.StatusCode != http.StatusCreated {
		t.Errorf("空正文带附件的状态码 = %d, 想 201", res.StatusCode)
	}

	// 引用不存在的附件、已挂到别处的附件：422 attachment_not_usable。
	for name, body := range map[string]map[string]any{
		"不存在的引用": {"body_md": "见 [旧文件](att:att_99)"},
		"重复引用":   {"body_md": "再看一次 ![质控图](att:" + png.ID + ")"},
	} {
		res := claimByReply(t, s, web, number, body)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("[%s] 状态码 = %d, 想 422", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "attachment_not_usable" {
			t.Errorf("[%s] 错误码 = %q, 想 attachment_not_usable", name, got.Error.Code)
		}
	}

	// Markdown 视图：附件小节列出链接；回复正文后跟附件引用。
	md := string(readAll(t, s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)))
	if !strings.Contains(md, "## 附件") || !strings.Contains(md, "[质控图.png](/api/v1/attachments/att_1/") {
		t.Errorf("Markdown 应有附件小节与下载链接：\n%s", md)
	}
	if !strings.Contains(md, "附件：att_1、att_2") {
		t.Errorf("Markdown 的回复应列出附件引用：\n%s", md)
	}
}

// 认领按凭证归属校验：访客链接 A 上传的附件，成员或另一条链接 B 引用都算
// 「不是你上传的」（422）；同一成员的 Agent 与本人算同一方（署名不同、归属相同）。
func Test认领附件按上传者归属校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")
	keyA := guestKeyOf(t, mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"}))
	guestA := s.GuestClient(keyA)
	linkB := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "设备科-王五"})
	guestB := s.GuestClient(guestKeyOf(t, linkB))

	mine := mustUploadAttachment(t, s, guestA, number, "现场照片.png", []byte("photo"))
	if mine.State != "pending" {
		t.Fatalf("state = %q, 想 pending", mine.State)
	}

	// 成员与另一条链接都不能引用访客 A 上传的附件。
	for name, c := range map[string]*http.Client{"成员": web, "另一条链接": guestB} {
		res := claimByReply(t, s, c, number, map[string]any{"attachment_ids": []string{mine.ID}})
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("[%s] 状态码 = %d, 想 422", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "attachment_not_usable" {
			t.Errorf("[%s] 错误码 = %q, 想 attachment_not_usable", name, got.Error.Code)
		}
	}

	// 上传者本人（同一条链接）可以引用。
	if res := claimByReply(t, s, guestA, number,
		map[string]any{"body_md": "现场照片如下。", "attachment_ids": []string{mine.ID}, "author_name": "张三"}); res.StatusCode != http.StatusCreated {
		t.Errorf("上传者自己引用状态码 = %d, 想 201", res.StatusCode)
	}

	// 同一成员的 Agent 上传、本人回复：算同一方，可以引用。
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agentUploaded := mustUploadAttachment(t, s, s.AgentClient(token), number, "日志.txt", []byte("log"))
	res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{agentUploaded.ID}})
	if res.StatusCode != http.StatusCreated {
		t.Errorf("成员引用其 Agent 上传的附件状态码 = %d, 想 201（同一成员算同一方）", res.StatusCode)
	}
}

// 每条说明或回复的个数上限：附件（attachment_ids 与正文引用的非图片）与插图
// （正文插图语法引用的图片）分开计数，超过返回 413 attachment_count_exceeded，
// hint 提示改用 NAS 路径。
func Test每帖附件与插图的个数上限(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithAttachments(5, 2, 2, 50))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	upload := func(name string) attachmentUploadBody {
		return mustUploadAttachment(t, s, web, number, name, []byte(name))
	}

	// 附件超过 2 个（用 attachment_ids 显式挂）。
	files := []attachmentUploadBody{upload("a.zip"), upload("b.zip"), upload("c.zip")}
	res := claimByReply(t, s, web, number, map[string]any{
		"attachment_ids": []string{files[0].ID, files[1].ID, files[2].ID},
	})
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("3 个附件状态码 = %d, 想 413", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "attachment_count_exceeded" {
		t.Errorf("错误码 = %q, 想 attachment_count_exceeded", got.Error.Code)
	}
	if !strings.Contains(got.Error.Hint, "NAS") {
		t.Errorf("hint 应提示 NAS 路径：%q", got.Error.Hint)
	}

	// 插图超过 2 张（正文插图语法）。
	imgs := []attachmentUploadBody{upload("p1.png"), upload("p2.png"), upload("p3.png")}
	res = claimByReply(t, s, web, number, map[string]any{
		"body_md": "![1](att:" + imgs[0].ID + ") ![2](att:" + imgs[1].ID + ") ![3](att:" + imgs[2].ID + ")",
	})
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("3 张插图状态码 = %d, 想 413", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "attachment_count_exceeded" {
		t.Errorf("错误码 = %q, 想 attachment_count_exceeded", got.Error.Code)
	}

	// 2 个附件 + 2 张插图：各计各的，通过。
	okFiles := []attachmentUploadBody{upload("d.zip"), upload("e.zip")}
	okImgs := []attachmentUploadBody{upload("q1.png"), upload("q2.png")}
	res = claimByReply(t, s, web, number, map[string]any{
		"body_md":        "![1](att:" + okImgs[0].ID + ") ![2](att:" + okImgs[1].ID + ")",
		"attachment_ids": []string{okFiles[0].ID, okFiles[1].ID},
	})
	if res.StatusCode != http.StatusCreated {
		t.Errorf("2 附件 + 2 插图状态码 = %d, 想 201（分开计数）", res.StatusCode)
	}

	// 图片用 attachment_ids 挂算附件不算插图（插图 = 正文插图语法引用的图片）。
	pngFile := upload("r.png")
	oneZip := upload("f.zip")
	res = claimByReply(t, s, web, number, map[string]any{
		"attachment_ids": []string{pngFile.ID, oneZip.ID},
	})
	if res.StatusCode != http.StatusCreated {
		t.Errorf("图片 + 压缩包各一个（2 个附件）状态码 = %d, 想 201", res.StatusCode)
	}
	// 再挂一个就超过 2 个附件的上限（图片显式挂不计入插图的 2 张）。
	last := upload("g.zip")
	res = claimByReply(t, s, web, number, map[string]any{
		"attachment_ids": []string{last.ID, upload("h.zip").ID, upload("i.zip").ID},
	})
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("3 个 attachment_ids 状态码 = %d, 想 413", res.StatusCode)
	}
}

// 修订说明认领插图：PATCH 的正文 att: 引用把待用附件挂到「说明」上；已挂着的
// 引用可以重复出现（不报错、不重挂）；别人的待用附件不能挂。
func Test修订说明认领插图(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "整理 Q3 质控规则说明")

	mine := mustUploadAttachment(t, s, web, number, "示意图.png", []byte("img"))
	if res := revise(t, s, web, number, map[string]any{
		"brief_md":      "整理要点：\n\n![示意图](att:" + mine.ID + ")",
		"base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("修订说明认领插图状态码 = %d, 想 200", res.StatusCode)
	}

	var view attachmentsTicketBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Attachments) != 1 || view.Attachments[0].AttachedTo != "说明" {
		t.Fatalf("附件应挂在说明上：%+v", view.Attachments)
	}

	// 再修订时重复引用已挂上的插图：不报错。
	if res := revise(t, s, web, number, map[string]any{
		"brief_md":      "补充后仍引用同一张图\n\n![示意图](att:" + mine.ID + ")",
		"base_revision": 2,
	}); res.StatusCode != http.StatusOK {
		t.Errorf("重复引用已挂插图状态码 = %d, 想 200", res.StatusCode)
	}

	// 已挂上的附件可以在修订里重复引用（不报错、不重挂）；
	// 但别人的待用附件不能挂到说明上。
	key := guestKeyOf(t, mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三", "can_edit_brief": true}))
	if res := revise(t, s, s.GuestClient(key), number, map[string]any{
		"brief_md":      "补充后仍引用同一张图\n\n![示意图](att:" + mine.ID + ")",
		"base_revision": 3,
	}); res.StatusCode != http.StatusOK {
		t.Errorf("重复引用已挂插图状态码 = %d, 想 200", res.StatusCode)
	}
	other := mustUploadAttachment(t, s, web, number, "待用的图.png", []byte("pending"))
	res := revise(t, s, s.GuestClient(key), number, map[string]any{
		"brief_md":      "想挂成员的待用图\n\n![待用](att:" + other.ID + ")",
		"base_revision": 4,
	})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("访客挂成员的待用附件状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "attachment_not_usable" {
		t.Errorf("错误码 = %q, 想 attachment_not_usable", got.Error.Code)
	}
}
