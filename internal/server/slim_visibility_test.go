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
