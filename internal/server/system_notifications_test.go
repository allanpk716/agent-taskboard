package server_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/notifications"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test系统通知网页和客户端可见而Agent不可见(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")
	_, agentToken := s.IssueAgentToken(t, admin, "备份Agent")
	issued := s.IssueClientCredential(t, admin, "WORKSTATION-01")
	if err := s.Notifications().NotifySystem(context.Background(),
		notifications.ReasonBackupFailing, "备份失败：测试原因"); err != nil {
		t.Fatal(err)
	}

	type item struct {
		ID     int64  `json:"id"`
		URL    string `json:"url"`
		Reason string `json:"reason"`
		Ticket *struct {
			Number string `json:"number"`
		} `json:"ticket"`
	}
	get := func(c *http.Client) []item {
		t.Helper()
		var body struct {
			Items []item `json:"items"`
		}
		s.Do(t, c, http.MethodGet, "/api/v1/notifications?limit=1", nil, &body)
		return body.Items
	}
	web := get(admin)
	if len(web) != 1 || web[0].URL != "/admin#backup" || web[0].Ticket != nil || web[0].Reason != notifications.ReasonBackupFailing {
		t.Fatalf("网页系统通知 = %+v", web)
	}
	client := get(s.ClientCredentialClient(issued.Token))
	if len(client) != 1 || client[0].URL != "/admin#backup" || client[0].Ticket != nil {
		t.Fatalf("客户端系统通知 = %+v", client)
	}
	if got := get(s.AgentClient(agentToken)); len(got) != 0 {
		t.Fatalf("Agent 不应看到系统通知：%+v", got)
	}
	var unread struct {
		Count int `json:"unread_count"`
	}
	s.Do(t, s.AgentClient(agentToken), http.MethodGet, "/api/v1/notifications/unread", nil, &unread)
	if unread.Count != 0 {
		t.Fatalf("Agent 未读数应过滤系统通知：%d", unread.Count)
	}
}
