package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test可见范围外成员与Agent写入工单得到一致的404无副作用并留下被拒记录(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	s.CreateMember("创建者", "secret-pass")
	s.CreateMember("指派人", "secret-pass")
	s.CreateMember("旁观者", "secret-pass")
	admin := s.Login("管理员", "secret-pass")
	creator := s.Login("创建者", "secret-pass")
	outsider := s.Login("旁观者", "secret-pass")
	_, token := s.IssueAgentToken(t, outsider, "旁观的 Agent")
	agent := s.AgentClient(token)

	var created createTicketResponse
	res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "成员写不入的商业敏感工单", "labels": []string{"机型:UD-2300"}, "assignee": "指派人",
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	number := created.Number
	for credential, client := range map[string]*http.Client{"成员": outsider, "成员Agent": agent} {
		hidden := s.Do(t, client, http.MethodPost, "/api/v1/tickets/"+number+"/replies", map[string]any{"body_md": "越权回复"}, nil)
		if hidden.StatusCode != http.StatusNotFound {
			t.Fatalf("%s回复隐藏工单状态码 = %d, 想 404", credential, hidden.StatusCode)
		}
		missing := s.Do(t, client, http.MethodPost, "/api/v1/tickets/20990101-001/replies", map[string]any{"body_md": "越权回复"}, nil)
		if missing.StatusCode != http.StatusNotFound {
			t.Fatalf("%s回复不存在工单状态码 = %d, 想 404", credential, missing.StatusCode)
		}
		if got, want := responseBody(t, hidden), responseBody(t, missing); got != want {
			t.Fatalf("%s回复的隐藏/不存在工单 404 响应不一致：\n%q\n%q", credential, got, want)
		}
	}

	// 无副作用：被拒的回复不能写进时间线。
	var ticket ticketResponseBody
	if res := s.Do(t, creator, http.MethodGet, "/api/v1/tickets/"+number, nil, &ticket); res.StatusCode != http.StatusOK {
		t.Fatalf("创建者读工单 = %d, 想 200", res.StatusCode)
	}
	for _, ev := range ticket.Timeline {
		if ev.Type == "reply" {
			t.Fatalf("被拒回复写进了时间线：%+v", ev)
		}
	}

	records := readAccessRecords(t, s, admin, number)
	var denied []accessRecordBody
	for _, record := range records.Records {
		if record.Action != "denied" {
			continue
		}
		denied = append(denied, record)
		if record.Count != 1 {
			t.Errorf("被拒记录次数 = %+v, 想 1", record)
		}
	}
	if len(denied) != 2 {
		t.Fatalf("被拒记录有 %d 条, 想 2：%+v", len(denied), denied)
	}
	sawMember, sawAgent := false, false
	for _, record := range denied {
		switch {
		case record.Actor.Kind == "member" && record.Actor.Name == "旁观者":
			sawMember = true
		case record.Actor.Kind == "agent" && record.Actor.Name == "旁观者 的 Agent · 旁观的 Agent":
			sawAgent = true
		default:
			t.Errorf("被拒记录署名不对：%+v", record)
		}
	}
	if !sawMember || !sawAgent {
		t.Fatalf("被拒记录应同时留下成员与成员 Agent 署名：%+v", denied)
	}
}

func Test离开可见范围成员只收到最后一条不带摘要的通知(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("管理员", "secret-pass")
	for _, name := range []string{"创建者", "旧指派人", "新指派人"} {
		s.CreateMember(name, "secret-pass")
	}
	creator := s.Login("创建者", "secret-pass")
	old := s.Login("旧指派人", "secret-pass")
	number := mustCreateTicket(t, s, creator, "改派后的旧指派人")
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "旧指派人"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("初次指派状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]any{"assignee": "新指派人"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("改派状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "旧指派人看不到的后续内容"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("后续回复状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, creator, http.MethodPost, "/api/v1/tickets/"+number+"/close",
		map[string]any{"reason": "done"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭状态码 = %d", res.StatusCode)
	}

	var notes notificationsBody
	if res := s.Do(t, old, http.MethodGet, "/api/v1/notifications", nil, &notes); res.StatusCode != http.StatusOK {
		t.Fatalf("通知状态码 = %d", res.StatusCode)
	}
	if len(notes.Items) != 2 {
		t.Fatalf("旧指派人通知 = %+v, 想 2 条", notes.Items)
	}
	last := notes.Items[0]
	if last.Reason != "unassigned" || last.Ticket.Title != "改派后的旧指派人" || last.Summary != "" {
		t.Fatalf("离开可见范围通知不对：%+v", last)
	}
	if !strings.Contains(last.Text, "改派后的旧指派人") {
		t.Fatalf("离开可见范围通知应带标题：%+v", last)
	}
	if res := s.Do(t, old, http.MethodGet, "/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("旧指派人读取状态码 = %d, 想 404", res.StatusCode)
	}
}
