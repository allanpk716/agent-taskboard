package server_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 停用成员后，成员读到工单里的历史署名与指派人都带 deactivated；访客视图
// 不变；Markdown 给成员加「（已停用）」；恢复后标记消失（#115）。
func Test工单视图标记已停用成员(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	wang := s.Login("王工", "member-pass")

	number := mustCreateTicket(t, s, wang, "王工负责的停用标记工单")
	adminNumber := mustCreateTicket(t, s, admin, "管理员发给王工的停用标记工单")
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]string{"assignee": "王工"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("指派给王工状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+adminNumber+"/assign",
		map[string]string{"assignee": "王工"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("指派给王工状态码 = %d, 想 200", res.StatusCode)
	}
	if res := reply(t, s, wang, adminNumber, "我收到这张单。"); res.StatusCode != http.StatusCreated {
		t.Fatalf("王工回复管理员工单状态码 = %d, 想 201", res.StatusCode)
	}
	if res := reply(t, s, wang, number, "我先看日志。"); res.StatusCode != http.StatusCreated {
		t.Fatalf("王工回复状态码 = %d, 想 201", res.StatusCode)
	}
	link := s.CreateGuestLink(t, admin, number, map[string]any{"note": "检验科-张三"})

	var wangSession sessionBody
	s.Do(t, wang, http.MethodGet, "/api/v1/session", nil, &wangSession)
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/members/"+itoa(wangSession.Viewer.Member.ID)+"/deactivate", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("停用王工状态码 = %d, 想 200", res.StatusCode)
	}

	var full ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &full)
	if full.Creator.Member == nil || full.Creator.Member.Deactivated == nil || !*full.Creator.Member.Deactivated {
		t.Errorf("发起人未标记已停用：%+v", full.Creator.Member)
	}
	if full.Assignee == nil || full.Assignee.Member == nil || full.Assignee.Member.Deactivated == nil || !*full.Assignee.Member.Deactivated {
		t.Errorf("指派人未标记已停用：%+v", full.Assignee)
	}
	replySeen := false
	for _, ev := range full.Timeline {
		if ev.Type == "reply" {
			replySeen = true
			if ev.Actor.Member == nil || ev.Actor.Member.Deactivated == nil || !*ev.Actor.Member.Deactivated {
				t.Errorf("回复署名未标记已停用：%+v", ev.Actor.Member)
			}
		}
	}
	if !replySeen {
		t.Fatalf("时间线里没有王工的回复：%+v", full.Timeline)
	}

	listRaw, _ := io.ReadAll(s.Do(t, admin, http.MethodGet,
		"/api/v1/tickets?status=all&q="+url.QueryEscape("管理员发给王工的停用标记工单"), nil, nil).Body)
	if !strings.Contains(string(listRaw), `"assignee":{"id":2,"name":"王工","deactivated":true}`) {
		t.Errorf("列表摘要未标记已停用指派人：%s", listRaw)
	}
	var inbox inboxBody
	s.Do(t, admin, http.MethodGet, "/api/v1/inbox", nil, &inbox)
	adminInbox := inbox.itemByNumber(adminNumber)
	if adminInbox == nil || adminInbox.Assignee == nil || adminInbox.Assignee.Member == nil ||
		adminInbox.Assignee.Member.Deactivated == nil || !*adminInbox.Assignee.Member.Deactivated {
		t.Errorf("收件箱未标记已停用指派人：%+v", adminInbox)
	}

	raw, _ := io.ReadAll(s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil).Body)
	for _, want := range []string{"发起人：王工（已停用）", "指派人：王工（已停用）", "王工（已停用） 回复"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("Markdown 缺少 %q：%s", want, raw)
		}
	}

	guestRaw, _ := io.ReadAll(s.Do(t, s.GuestClient(link.Key(t)), http.MethodGet, "/api/v1/tickets/"+number, nil, nil).Body)
	if strings.Contains(string(guestRaw), `"deactivated"`) || strings.Contains(string(guestRaw), "（已停用）") {
		t.Errorf("访客视图不应暴露账号状态：%s", guestRaw)
	}

	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/members/"+itoa(wangSession.Viewer.Member.ID)+"/reactivate", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("恢复王工状态码 = %d, 想 200", res.StatusCode)
	}
	full = ticketResponseBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/"+number, nil, &full)
	if full.Creator.Member != nil && full.Creator.Member.Deactivated != nil {
		t.Errorf("恢复后发起人仍有停用标记：%+v", full.Creator.Member)
	}
	if full.Assignee != nil && full.Assignee.Member != nil && full.Assignee.Member.Deactivated != nil {
		t.Errorf("恢复后指派人仍有停用标记：%+v", full.Assignee.Member)
	}
	for _, ev := range full.Timeline {
		if ev.Actor.Member != nil && ev.Actor.Member.Deactivated != nil {
			t.Errorf("恢复后事件署名仍有停用标记：%+v", ev.Actor.Member)
		}
	}
}
