package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test访客Agent用链接密钥加访问码访问工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "访客 Agent 可以处理")
	att := mustUploadAttachment(t, s, web, number, "日志.txt", []byte("ticket log"))
	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	key := guestKeyOf(t, link)

	var claimed struct {
		AccessCode string `json:"access_code"`
	}
	res := s.Do(t, s.AgentClient(key), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "检验科 张三"}, &claimed)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 认领状态码 = %d, 想 201", res.StatusCode)
	}
	agentCredential := key + ":" + claimed.AccessCode
	agent := s.AgentClient(agentCredential)

	var view ticketResponseBody
	res = s.Do(t, agent, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Agent 读取状态码 = %d, 想 200", res.StatusCode)
	}
	if view.Title != "访客 Agent 可以处理" {
		t.Fatalf("Agent 读到标题 = %q", view.Title)
	}
	var attachments attachmentsTicketBody
	s.Do(t, agent, http.MethodGet, "/api/v1/tickets/"+number, nil, &attachments)

	res = s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "Agent 已处理", "via_agent": true}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 回复状态码 = %d, 想 201", res.StatusCode)
	}

	if len(attachments.Attachments) != 1 {
		t.Fatalf("Agent 读到的附件 = %+v", attachments.Attachments)
	}
	if res := downloadAttachment(t, s, agent, attachments.Attachments[0].URL); res.StatusCode != http.StatusOK {
		t.Fatalf("Agent 下载状态码 = %d, 想 200", res.StatusCode)
	}

	records := readAccessRecords(t, s, web, number)
	for _, want := range []struct{ action, entry string }{
		{"view", "access_code"},
		{"download", "access_code"},
	} {
		found := false
		for _, r := range records.Records {
			if r.Action == want.action && r.Actor.Kind == "guest" && r.Actor.EntryMethod == want.entry {
				found = true
			}
		}
		if !found {
			t.Fatalf("缺少 %s 凭访问码的访问记录：%+v", want.action, records.Records)
		}
	}
}

func Test访客Agent访问码错误与浏览器共用锁定计数(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "访客 Agent 输码锁定")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	key := guestKeyOf(t, link)

	var claimed struct {
		AccessCode string `json:"access_code"`
	}
	res := s.Do(t, s.AgentClient(key), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "检验科 张三"}, &claimed)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领状态码 = %d, 想 201", res.StatusCode)
	}

	for i := 0; i < 3; i++ {
		res = s.Do(t, s.AgentClient(key), http.MethodPost, "/api/v1/guest-links/enter",
			map[string]string{"access_code": "000000"}, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("浏览器第 %d 次输错状态码 = %d, 想 401", i+1, res.StatusCode)
		}
	}
	for i := 0; i < 2; i++ {
		credential := key + ":000000"
		want := http.StatusUnauthorized
		if i == 1 {
			want = http.StatusForbidden
		}
		res = s.Do(t, s.AgentClient(credential), http.MethodGet, "/api/v1/tickets/"+number, nil, nil)
		if res.StatusCode != want {
			t.Fatalf("Agent 第 %d 次输错状态码 = %d, 想 %d", i+1, res.StatusCode, want)
		}
	}

	locked := s.AgentClient(key + ":" + claimed.AccessCode)
	res = s.Do(t, locked, http.MethodGet, "/api/v1/tickets/"+number, nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("锁定后正确访问码状态码 = %d, 想 403", res.StatusCode)
	}
	res = s.Do(t, s.AgentClient(key), http.MethodPost, "/api/v1/guest-links/enter",
		map[string]string{"access_code": claimed.AccessCode}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("锁定后浏览器输码状态码 = %d, 想 403", res.StatusCode)
	}
}
