package server_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func changeOwnPassword(s *tbtest.Server, t *testing.T, c *http.Client, oldPassword, newPassword string) (int, string) {
	t.Helper()
	res := s.Do(t, c, http.MethodPost, "/api/v1/me/password",
		map[string]string{"old_password": oldPassword, "new_password": newPassword}, nil)
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, body.Error.Code
}

// 自助改密保留当前网页会话、注销其他网页会话，Agent token 不受影响。
func Test自助改密注销其他网页会话(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	current := s.Login("wang", "member-pass")
	other := s.Login("wang", "member-pass")
	_, agentToken := s.IssueAgentToken(t, current, "笔记本")
	agent := s.AgentClient(agentToken)

	// 新旧相同要先校验旧密码，不能变成探测旧密码的渠道。
	status, code := changeOwnPassword(s, t, current, "wrong-pass", "member-pass")
	if status != http.StatusUnprocessableEntity || code != "invalid_old_password" {
		t.Fatalf("旧密码错误且新旧相同：状态码/错误码 = %d/%q，想 422/invalid_old_password",
			status, code)
	}

	status, code = changeOwnPassword(s, t, current, "member-pass", "member-pass")
	if status != http.StatusUnprocessableEntity || code != "password_unchanged" {
		t.Fatalf("新旧相同：状态码/错误码 = %d/%q，想 422/password_unchanged",
			status, code)
	}
	status, code = changeOwnPassword(s, t, current, "member-pass", "short")
	if status != http.StatusUnprocessableEntity || code != "password_too_short" {
		t.Fatalf("新密码太短：状态码/错误码 = %d/%q，想 422/password_too_short",
			status, code)
	}

	status, code = changeOwnPassword(s, t, current, "member-pass", "new-member-pass")
	if status != http.StatusNoContent || code != "" {
		t.Fatalf("自助改密状态码/错误码 = %d/%q，想 204/空", status, code)
	}
	if res := s.Do(t, current, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("改密后当前会话状态码 = %d，想 200", res.StatusCode)
	}
	if res := s.Do(t, other, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("改密后其他会话状态码 = %d，想 401", res.StatusCode)
	}
	if res := s.Do(t, agent, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("改密后 Agent token 状态码 = %d，想 200", res.StatusCode)
	}
}

// 成员 Agent 不能自助改密：这个接口只认本人网页会话（#99 评审修正）。
func Test成员Agent不能自助改密(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")

	status, code := changeOwnPassword(s, t, s.AgentClient(token), "member-pass", "new-member-pass")
	if status != http.StatusForbidden || code != "web_session_required" {
		t.Fatalf("Agent 自助改密状态码/错误码 = %d/%q，想 403/web_session_required", status, code)
	}
}
