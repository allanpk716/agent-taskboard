package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test客户端在线状态只统计客户端凭证SSE连接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")
	_, agentToken := s.IssueAgentToken(t, web, "通知Agent")
	issued := s.IssueClientCredential(t, web, "WORKSTATION-01")
	client := s.ClientCredentialClient(issued.Token)

	online := func() bool {
		t.Helper()
		var body struct {
			ClientOnline bool `json:"client_online"`
		}
		res := s.Do(t, web, http.MethodGet, "/api/v1/notifications/unread", nil, &body)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("读未读数状态码 = %d", res.StatusCode)
		}
		return body.ClientOnline
	}

	if online() {
		t.Fatal("客户端 SSE 建立前 client_online 不应为 true")
	}
	stream := s.OpenSSE(t, client, "")
	if ev := stream.Event(t); ev.Name != "hello" {
		t.Fatalf("客户端 SSE 首帧 = %q, 想 hello", ev.Name)
	}
	if !online() {
		t.Fatal("客户端 SSE 建立后 client_online 应为 true")
	}

	webStream := s.OpenSSE(t, web, "")
	agentStream := s.OpenSSE(t, s.AgentClient(agentToken), "")
	for i, opened := range []*tbtest.SSEStream{webStream, agentStream} {
		if ev := opened.Event(t); ev.Name != "hello" {
			t.Fatalf("第 %d 条对照 SSE 首帧 = %q, 想 hello", i+1, ev.Name)
		}
	}
	if !online() {
		t.Fatal("网页会话和 Agent 的 SSE 连接不应影响 client_online")
	}

	stream.Close()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !online() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("客户端 SSE 断开后 client_online 应为 false")
}
