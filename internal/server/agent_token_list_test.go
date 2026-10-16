package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// #42 个人设置页要列出成员名下的 Agent token（撤销的标注「已停用」）：
// GET /api/v1/me/agent-tokens 只接受网页会话，不回显明文 token。
func Test列出成员名下的AgentToken(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")

	laptopID, laptopToken := s.IssueAgentToken(t, web, "笔记本")
	s.IssueAgentToken(t, web, "巡检")
	// 撤销「笔记本」：列表里保留并标注，历史署名不受影响。
	if res := s.Do(t, web, http.MethodDelete, "/api/v1/me/agent-tokens/"+itoa(laptopID), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销状态码 = %d, 想 204", res.StatusCode)
	}

	type tokenRow struct {
		ID        int64   `json:"id"`
		Name      string  `json:"name"`
		CreatedAt string  `json:"created_at"`
		RevokedAt *string `json:"revoked_at"`
		Token     string  `json:"token"`
	}
	var got struct {
		Tokens []tokenRow `json:"tokens"`
	}
	res := s.Do(t, web, http.MethodGet, "/api/v1/me/agent-tokens", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("列表状态码 = %d, 想 200", res.StatusCode)
	}
	if len(got.Tokens) != 2 {
		t.Fatalf("token 数 = %d, 想 2（撤销的保留）：%+v", len(got.Tokens), got.Tokens)
	}
	byName := map[string]tokenRow{}
	for _, tk := range got.Tokens {
		byName[tk.Name] = tk
	}
	if byName["笔记本"].RevokedAt == nil {
		t.Errorf("「笔记本」已撤销，revoked_at 应非空：%+v", byName["笔记本"])
	}
	if byName["巡检"].RevokedAt != nil {
		t.Errorf("「巡检」未撤销，revoked_at 应为空：%+v", byName["巡检"])
	}
	if byName["巡检"].CreatedAt == "" {
		t.Errorf("created_at 不应为空")
	}
	// 明文 token 只在签发响应出现一次，列表不回显。
	raw, _ := io.ReadAll(s.Do(t, web, http.MethodGet, "/api/v1/me/agent-tokens", nil, nil).Body)
	if body := string(raw); strings.Contains(body, laptopToken) || strings.Contains(body, "tbag_") {
		t.Errorf("列表不应回显明文 token：%s", raw)
	}

	// 只接受网页会话：Agent token 不能读列表（ADR 0003）。
	_, otherToken := s.IssueAgentToken(t, web, "巡检二号")
	agent := s.AgentClient(otherToken)
	res = s.Do(t, agent, http.MethodGet, "/api/v1/me/agent-tokens", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("Agent token 读列表状态码 = %d, 想 403", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "web_session_required" {
		t.Errorf("错误码 = %q, 想 web_session_required", errBody.Error.Code)
	}
}
