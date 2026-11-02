// 瘦身标记的读取接口（#284）：成员、成员 Agent、访客读详情都带附件级与
// 工单级瘦身信息，列表项带瘦身状态；只通过 HTTP 观察外部行为。
package server_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type slimAttachmentBody struct {
	ID              string `json:"id"`
	Filename        string `json:"filename"`
	Size            int64  `json:"size"`
	URL             string `json:"url"`
	AttachedTo      string `json:"attached_to"`
	SlimRemovedAt   string `json:"slim_removed_at"`
	SlimPackageName string `json:"slim_package_name"`
}

type ticketSlimBody struct {
	RemovedCount  int      `json:"removed_count"`
	LastRemovedAt string   `json:"last_removed_at"`
	Packages      []string `json:"packages"`
	Status        string   `json:"status"`
}

type slimDetailBody struct {
	Number      string               `json:"number"`
	Attachments []slimAttachmentBody `json:"attachments"`
	Slim        ticketSlimBody       `json:"slim"`
}

type slimListBody struct {
	Items []struct {
		Number     string `json:"number"`
		SlimStatus string `json:"slim_status"`
	} `json:"items"`
}

func Test瘦身信息在详情与列表接口处处可见(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")
	_, agentToken := s.IssueAgentToken(t, member, "助理")
	memberAgent := s.AgentClient(agentToken)

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, member, "要标记瘦身的工单")
	log := mustUploadAttachment(t, s, member, number, "服务日志.log", []byte("slim-body"))
	if res := revise(t, s, member, number, map[string]any{
		"attachment_ids": []string{log.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, member, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")

	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}
	removeURL := "/api/v1/admin/slim/packages/" + pkg.Name + "/remove"
	var removed slimRemoveBody
	if res := s.Do(t, admin, http.MethodPost, removeURL, map[string]any{"saved": true}, &removed); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d", res.StatusCode)
	}

	link := mustCreateGuestLink(t, s, admin, number, map[string]any{"note": "现场", "valid_days": 7})
	guest := s.GuestClient(guestKeyOf(t, link))
	checkDetail := func(name string, c *http.Client) {
		t.Helper()
		var got slimDetailBody
		res := s.Do(t, c, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s 读取详情状态码 = %d", name, res.StatusCode)
		}
		if len(got.Attachments) != 1 {
			t.Fatalf("%s 附件列表 = %+v，想保留占位", name, got.Attachments)
		}
		att := got.Attachments[0]
		if att.ID != log.ID || att.Filename != "服务日志.log" || att.Size != 9 || att.AttachedTo != "说明" || att.URL == "" {
			t.Fatalf("%s 附件占位元数据 = %+v", name, att)
		}
		if att.SlimRemovedAt == "" || att.SlimPackageName != pkg.Name {
			t.Fatalf("%s 附件瘦身标记 = %+v，想包 %s", name, att, pkg.Name)
		}
		if got.Slim.RemovedCount != 1 || got.Slim.Status != "full" ||
			len(got.Slim.Packages) != 1 || got.Slim.Packages[0] != pkg.Name || got.Slim.LastRemovedAt == "" {
			t.Fatalf("%s 工单级瘦身汇总 = %+v", name, got.Slim)
		}
	}
	checkDetail("成员", member)
	checkDetail("成员 Agent", memberAgent)
	checkDetail("访客", guest)

	var list slimListBody
	res := s.Do(t, member, http.MethodGet, "/api/v1/tickets?status=all", nil, &list)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("列表状态码 = %d", res.StatusCode)
	}
	found := false
	for _, item := range list.Items {
		if item.Number != number {
			continue
		}
		found = true
		if item.SlimStatus != "full" {
			t.Fatalf("列表瘦身状态 = %q，想 full", item.SlimStatus)
		}
	}
	if !found {
		t.Fatalf("列表里没有 %s：%+v", number, list.Items)
	}
}

func Test下载已瘦身附件返回专用错误码与包名(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, member, "瘦身下载提示")
	log := mustUploadAttachment(t, s, member, number, "服务日志.log", []byte("slim-body"))
	if res := revise(t, s, member, number, map[string]any{
		"attachment_ids": []string{log.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, member, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")
	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}
	removeURL := "/api/v1/admin/slim/packages/" + pkg.Name + "/remove"
	if res := s.Do(t, admin, http.MethodPost, removeURL, map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d", res.StatusCode)
	}

	link := mustCreateGuestLink(t, s, admin, number, map[string]any{"note": "现场", "valid_days": 7})
	guest := s.GuestClient(guestKeyOf(t, link))
	for name, c := range map[string]*http.Client{"成员": member, "管理员": admin, "访客": guest} {
		res := downloadAttachment(t, s, c, "/api/v1/attachments/"+log.ID+"/"+url.PathEscape("服务日志.log"))
		if res.StatusCode != http.StatusGone {
			t.Fatalf("%s 下载已瘦身附件状态码 = %d，想 410", name, res.StatusCode)
		}
		var got errorBodyWithExtra
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "attachment_slimmed" {
			t.Fatalf("%s 下载错误码 = %s，想 attachment_slimmed", name, got.Error.Code)
		}
		if !strings.Contains(got.Error.Message, pkg.Name) {
			t.Fatalf("%s 下载错误说明 = %q，想包含包名 %s", name, got.Error.Message, pkg.Name)
		}
	}
}

func Test重开瘦身后上传新附件状态变部分且可再次瘦身(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, member, "重开后再瘦身")
	old := mustUploadAttachment(t, s, member, number, "旧日志.log", []byte("old"))
	if res := revise(t, s, member, number, map[string]any{
		"attachment_ids": []string{old.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, member, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")
	var first slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, &first); res.StatusCode != http.StatusCreated {
		t.Fatalf("第一次生成瘦身包状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+first.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("第一次确认瘦身状态码 = %d", res.StatusCode)
	}
	var afterFirst slimDetailBody
	s.Do(t, member, http.MethodGet, "/api/v1/tickets/"+number, nil, &afterFirst)
	if afterFirst.Slim.Status != "full" {
		t.Fatalf("第一次瘦身后状态 = %q，想 full", afterFirst.Slim.Status)
	}

	// 重开并上传新附件：新附件照常下载，工单标记变为「部分附件已瘦身」。
	if res := s.Do(t, member, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重开状态码 = %d", res.StatusCode)
	}
	fresh := mustUploadAttachment(t, s, member, number, "新日志.log", []byte("new-body"))
	if res := revise(t, s, member, number, map[string]any{
		"attachment_ids": []string{old.ID, fresh.ID}, "base_revision": 2,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂新附件状态码 = %d", res.StatusCode)
	}
	body := readAll(t, downloadAttachment(t, s, member,
		"/api/v1/attachments/"+fresh.ID+"/"+url.PathEscape("新日志.log")))
	if string(body) != "new-body" {
		t.Fatalf("新附件下载内容 = %q，想 new-body", body)
	}
	var afterReopen slimDetailBody
	s.Do(t, member, http.MethodGet, "/api/v1/tickets/"+number, nil, &afterReopen)
	if afterReopen.Slim.Status != "partial" || afterReopen.Slim.RemovedCount != 1 ||
		len(afterReopen.Attachments) != 2 {
		t.Fatalf("重开后瘦身汇总 = %+v，附件 = %+v", afterReopen.Slim, afterReopen.Attachments)
	}
	var list slimListBody
	s.Do(t, member, http.MethodGet, "/api/v1/tickets?status=all", nil, &list)
	for _, item := range list.Items {
		if item.Number == number && item.SlimStatus != "partial" {
			t.Fatalf("重开后列表瘦身状态 = %q，想 partial", item.SlimStatus)
		}
	}

	// 再次瘦身只处理新附件，之后标记回到「已瘦身」。
	if res := closeTicket(t, s, member, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("再次关闭状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.November, 16)
	admin = s.Login("陈工", "secret-pass")
	member = s.Login("王工", "member-pass")
	var second slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"closed_months": 1, "ticket_numbers": []string{number}}, &second); res.StatusCode != http.StatusCreated {
		t.Fatalf("第二次生成瘦身包状态码 = %d", res.StatusCode)
	}
	if second.Name == first.Name || second.AttachmentCount != 1 || second.AttachmentBytes != 8 {
		t.Fatalf("第二次瘦身包 = %+v，想只包含新附件", second)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+second.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("第二次确认瘦身状态码 = %d", res.StatusCode)
	}
	var afterSecond slimDetailBody
	s.Do(t, member, http.MethodGet, "/api/v1/tickets/"+number, nil, &afterSecond)
	if afterSecond.Slim.Status != "full" || afterSecond.Slim.RemovedCount != 2 ||
		len(afterSecond.Slim.Packages) != 2 {
		t.Fatalf("第二次瘦身后汇总 = %+v，想 2 个附件、2 个包、full", afterSecond.Slim)
	}
}

// S1：工单级瘦身状态的分母只算附件（不含插图）。插图永不参与瘦身，
// 不能因为它还留在服务器上就让已全部瘦身的工单永远显示 partial。
func Test瘦身状态分母只算附件不算插图(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, member, "带插图的瘦身工单")
	log := mustUploadAttachment(t, s, member, number, "服务日志.log", []byte("slim-body"))
	image := mustUploadAttachment(t, s, member, number, "现场图.png", []byte("image"))
	if res := revise(t, s, member, number, map[string]any{
		"brief_md":       "见 [服务日志](att:" + log.ID + ")。![现场图](att:" + image.ID + ")。",
		"attachment_ids": []string{log.ID},
		"base_revision":  1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, member, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")

	// 全部附件瘦身后：工单状态是 full，而不是被插图拖成 partial。
	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}
	if pkg.AttachmentCount != 1 {
		t.Fatalf("瘦身包只应包含 1 个附件，得到 %+v", pkg)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d", res.StatusCode)
	}
	checkStatus := func(wantDetail string) {
		t.Helper()
		var detail slimDetailBody
		if res := s.Do(t, member, http.MethodGet, "/api/v1/tickets/"+number, nil, &detail); res.StatusCode != http.StatusOK {
			t.Fatalf("读取详情状态码 = %d", res.StatusCode)
		}
		if detail.Slim.Status != wantDetail {
			t.Fatalf("详情瘦身状态 = %q，想 %q", detail.Slim.Status, wantDetail)
		}
		var list slimListBody
		if res := s.Do(t, member, http.MethodGet, "/api/v1/tickets?status=all", nil, &list); res.StatusCode != http.StatusOK {
			t.Fatalf("读取列表状态码 = %d", res.StatusCode)
		}
		for _, item := range list.Items {
			if item.Number == number && item.SlimStatus != wantDetail {
				t.Fatalf("列表瘦身状态 = %q，想 %q", item.SlimStatus, wantDetail)
			}
		}
	}
	checkStatus("full")

	// 重新打开并上传新附件：旧附件已瘦身、新附件在服务器上 → partial。
	if res := s.Do(t, member, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重开状态码 = %d", res.StatusCode)
	}
	fresh := mustUploadAttachment(t, s, member, number, "新日志.log", []byte("new-body"))
	if res := revise(t, s, member, number, map[string]any{
		"attachment_ids": []string{log.ID, fresh.ID}, "base_revision": 2,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂新附件状态码 = %d", res.StatusCode)
	}
	checkStatus("partial")

	// 恢复包内全部附件（只含旧附件；插图本来就不在包里）→ none，无任何标记。
	download := s.Do(t, admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if download.StatusCode != http.StatusOK {
		t.Fatalf("下载瘦身包状态码 = %d", download.StatusCode)
	}
	pkgBytes := readAll(t, download)
	res, got := restoreSlim(t, s, admin, pkgBytes, number)
	if res.StatusCode != http.StatusOK || len(got.Restored) != 1 || got.Restored[0].Number != number {
		t.Fatalf("恢复结果 = %d/%+v，想恢复 %s", res.StatusCode, got, number)
	}
	checkStatus("none")
	var detail slimDetailBody
	s.Do(t, member, http.MethodGet, "/api/v1/tickets/"+number, nil, &detail)
	if detail.Slim.RemovedCount != 0 || detail.Slim.LastRemovedAt != "" ||
		len(detail.Slim.Packages) != 0 {
		t.Fatalf("全部恢复后瘦身汇总应为空：%+v", detail.Slim)
	}
}

func TestMarkdown视图标注瘦身附件且不再给出下载链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, member, "瘦身 Markdown")
	log := mustUploadAttachment(t, s, member, number, "服务日志.log", []byte("slim-body"))
	if res := revise(t, s, member, number, map[string]any{
		"attachment_ids": []string{log.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, member, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	admin = s.Login("陈工", "secret-pass")
	var pkg slimPackageBody
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d", res.StatusCode)
	}

	res := s.Do(t, member, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Markdown 状态码 = %d", res.StatusCode)
	}
	md := string(readAll(t, res))
	if !strings.Contains(md, "已瘦身移出") || !strings.Contains(md, pkg.Name) || !strings.Contains(md, "服务日志.log") {
		t.Fatalf("Markdown 缺少瘦身标注：\n%s", md)
	}
	if strings.Contains(md, "/api/v1/attachments/"+log.ID+"/") {
		t.Fatalf("Markdown 不应再给已瘦身附件下载链接：\n%s", md)
	}
}
