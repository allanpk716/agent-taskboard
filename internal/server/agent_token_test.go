package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// sessionBody 是 GET /api/v1/session 的响应结构（也是登录响应的结构）。
type sessionBody struct {
	Viewer struct {
		Kind   string `json:"kind"`
		Member struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Name     string `json:"name"`
			IsAdmin  bool   `json:"is_admin"`
		} `json:"member"`
		Agent *struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"agent"`
	} `json:"viewer"`
	Inbox string `json:"inbox"`
}

// 网页会话可以为自己签发具名 Agent token；GET /session 能区分本人与具名 Agent（ADR 0003）。
func Test网页会话签发具名AgentToken后session区分本人与Agent(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")

	// 本人的 session：agent 为 null。
	var me sessionBody
	res := s.Do(t, web, http.MethodGet, "/api/v1/session", nil, &me)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("本人查身份状态码 = %d, 想 200", res.StatusCode)
	}
	if me.Viewer.Kind != "member" {
		t.Errorf("kind = %q, 想 member", me.Viewer.Kind)
	}
	if me.Viewer.Member.Username != "chen" || me.Viewer.Member.Name != "chen" || !me.Viewer.Member.IsAdmin {
		t.Errorf("成员信息不对：%+v", me.Viewer.Member)
	}
	if me.Viewer.Agent != nil {
		t.Errorf("网页会话的 agent 应为 null，得到 %+v", me.Viewer.Agent)
	}

	// 签发一个具名 Agent token。
	var issued struct {
		ID        int64  `json:"id"`
		Name      string `json:"name"`
		Token     string `json:"token"`
		CreatedAt string `json:"created_at"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/me/agent-tokens",
		map[string]string{"name": "笔记本"}, &issued)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("签发状态码 = %d, 想 201", res.StatusCode)
	}
	if issued.ID == 0 || issued.Name != "笔记本" {
		t.Errorf("签发响应不对：%+v", issued)
	}
	if issued.Token == "" {
		t.Error("签发响应缺少 token")
	}
	if issued.CreatedAt == "" {
		t.Error("签发响应缺少 created_at")
	}

	// 用 Agent token 查 session：识别为该成员的具名 Agent，不是本人。
	agent := s.AgentClient(issued.Token)
	var agentMe sessionBody
	res = s.Do(t, agent, http.MethodGet, "/api/v1/session", nil, &agentMe)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Agent 查身份状态码 = %d, 想 200", res.StatusCode)
	}
	if agentMe.Viewer.Kind != "member" {
		t.Errorf("kind = %q, 想 member", agentMe.Viewer.Kind)
	}
	if agentMe.Viewer.Member.ID != me.Viewer.Member.ID {
		t.Errorf("Agent 的所属成员 = %d, 想 %d（本人成员）", agentMe.Viewer.Member.ID, me.Viewer.Member.ID)
	}
	if agentMe.Viewer.Agent == nil {
		t.Fatal("Agent token 的 session 里 agent 为 null")
	}
	if agentMe.Viewer.Agent.ID != issued.ID || agentMe.Viewer.Agent.Name != "笔记本" {
		t.Errorf("Agent 信息不对：%+v", agentMe.Viewer.Agent)
	}
}

// errorBody 是统一错误格式 {error:{code, message, hint?}} 的解码结构。
type errorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint"`
	} `json:"error"`
}

// 撤销 Agent token 后立即失效：再带着它请求任何接口都返回 401。
func Test撤销AgentToken后立即失效返回401(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")
	id, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	// 撤销前能用。
	if res := s.Do(t, agent, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("撤销前查身份状态码 = %d, 想 200", res.StatusCode)
	}

	// 网页会话撤销。
	res := s.Do(t, web, http.MethodDelete, "/api/v1/me/agent-tokens/"+strconv.FormatInt(id, 10), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销状态码 = %d, 想 204", res.StatusCode)
	}

	// 立即失效：查身份 → 401 invalid_token。
	var errBody errorBody
	res = s.Do(t, agent, http.MethodGet, "/api/v1/session", nil, &errBody)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后查身份状态码 = %d, 想 401", res.StatusCode)
	}
	if errBody.Error.Code != "invalid_token" {
		t.Errorf("错误码 = %q, 想 invalid_token", errBody.Error.Code)
	}

	// 失效凭证发起的任何请求都是 401，而不是 403。
	res = s.Do(t, agent, http.MethodPost, "/api/v1/me/agent-tokens", map[string]string{"name": "x"}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后签发状态码 = %d, 想 401", res.StatusCode)
	}
}

// 用 Agent token 请求签发或撤销 token 会被拒绝（ADR 0003：Agent 不能签发凭证）。
func Test用AgentToken签发或撤销token被拒绝(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")
	id, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	// 签发被拒：403 web_session_required。
	var errBody errorBody
	res := s.Do(t, agent, http.MethodPost, "/api/v1/me/agent-tokens", map[string]string{"name": "克隆"}, &errBody)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("Agent 签发状态码 = %d, 想 403", res.StatusCode)
	}
	if errBody.Error.Code != "web_session_required" {
		t.Errorf("错误码 = %q, 想 web_session_required", errBody.Error.Code)
	}
	if errBody.Error.Message == "" {
		t.Error("message 为空")
	}

	// 撤销同样被拒。
	res = s.Do(t, agent, http.MethodDelete, "/api/v1/me/agent-tokens/"+strconv.FormatInt(id, 10), nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("Agent 撤销状态码 = %d, 想 403", res.StatusCode)
	}

	// 被拒的请求没有真的签出 token：网页会话用同一个名字能签发成功。
	s.IssueAgentToken(t, web, "克隆")
}

// 带无效 Agent token 的请求返回 401 invalid_token，不会回退到 cookie。
func Test无效AgentToken返回401(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")

	var errBody errorBody
	res := s.Do(t, s.AgentClient("tbag_nope"), http.MethodGet, "/api/v1/session", nil, &errBody)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d, 想 401", res.StatusCode)
	}
	if errBody.Error.Code != "invalid_token" {
		t.Errorf("错误码 = %q, 想 invalid_token", errBody.Error.Code)
	}
}

// 一位成员名下的多个 Agent 各自具名，可以单独撤销（GLOSSARY：成员 Agent）。
func Test一位成员名下多个Agent各自具名且可单独撤销(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")

	laptopID, laptop := s.IssueAgentToken(t, web, "笔记本")
	_, desk := s.IssueAgentToken(t, web, "工位")

	// 各自查身份：各自署名，同属一位成员。
	for name, token := range map[string]string{"笔记本": laptop, "工位": desk} {
		var me sessionBody
		res := s.Do(t, s.AgentClient(token), http.MethodGet, "/api/v1/session", nil, &me)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s 查身份状态码 = %d, 想 200", name, res.StatusCode)
		}
		if me.Viewer.Agent == nil || me.Viewer.Agent.Name != name {
			t.Errorf("署名 = %+v, 想 %q", me.Viewer.Agent, name)
		}
		if me.Viewer.Member.Username != "chen" {
			t.Errorf("%s 的所属成员 = %q, 想 chen", name, me.Viewer.Member.Username)
		}
		if me.Viewer.Agent.ID == 0 {
			t.Errorf("%s 的 Agent id 为 0", name)
		}
	}

	// 单独撤销「笔记本」：它失效，「工位」不受影响。
	res := s.Do(t, web, http.MethodDelete, "/api/v1/me/agent-tokens/"+strconv.FormatInt(laptopID, 10), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销笔记本状态码 = %d, 想 204", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(laptop), http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后笔记本查身份状态码 = %d, 想 401", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(desk), http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("工位查身份状态码 = %d, 想 200（单独撤销不应影响别的 Agent）", res.StatusCode)
	}
}

// Agent 名字必填；未撤销的同名不能重复；撤销后名字可以复用。
func TestAgent名字必填未撤销时不能重复撤销后可复用(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")

	var errBody errorBody
	res := s.Do(t, web, http.MethodPost, "/api/v1/me/agent-tokens", map[string]string{"name": "  "}, &errBody)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("空名字状态码 = %d, 想 422", res.StatusCode)
	}
	if errBody.Error.Code != "name_required" {
		t.Errorf("错误码 = %q, 想 name_required", errBody.Error.Code)
	}

	id, _ := s.IssueAgentToken(t, web, "笔记本")
	res = s.Do(t, web, http.MethodPost, "/api/v1/me/agent-tokens", map[string]string{"name": "笔记本"}, &errBody)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("重名状态码 = %d, 想 409", res.StatusCode)
	}
	if errBody.Error.Code != "agent_name_taken" {
		t.Errorf("错误码 = %q, 想 agent_name_taken", errBody.Error.Code)
	}

	// 撤销后同名可再签（历史署名按 id 关联，不受名字复用影响）。
	if res := s.Do(t, web, http.MethodDelete, "/api/v1/me/agent-tokens/"+strconv.FormatInt(id, 10), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销状态码 = %d, 想 204", res.StatusCode)
	}
	s.IssueAgentToken(t, web, "笔记本")
}

// 撤销不存在或不属于自己 的 Agent token 返回 404；重复撤销幂等成功。
func Test撤销不存在的AgentToken返回404重复撤销幂等(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")

	var errBody errorBody
	res := s.Do(t, web, http.MethodDelete, "/api/v1/me/agent-tokens/999999", nil, &errBody)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("撤销不存在 token 状态码 = %d, 想 404", res.StatusCode)
	}
	if errBody.Error.Code != "agent_token_not_found" {
		t.Errorf("错误码 = %q, 想 agent_token_not_found", errBody.Error.Code)
	}
	// 路径不是数字同样按 404 处理。
	if res := s.Do(t, web, http.MethodDelete, "/api/v1/me/agent-tokens/abc", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("非数字 id 状态码 = %d, 想 404", res.StatusCode)
	}

	id, _ := s.IssueAgentToken(t, web, "笔记本")
	path := "/api/v1/me/agent-tokens/" + strconv.FormatInt(id, 10)
	if res := s.Do(t, web, http.MethodDelete, path, nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("第一次撤销状态码 = %d, 想 204", res.StatusCode)
	}
	if res := s.Do(t, web, http.MethodDelete, path, nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("重复撤销状态码 = %d, 想 204（幂等）", res.StatusCode)
	}
}

// 未登录时签发/撤销 token 返回 401。
func Test未登录签发AgentToken返回401(t *testing.T) {
	s := tbtest.Start(t)

	for method, path := range map[string]string{
		http.MethodPost:   "/api/v1/me/agent-tokens",
		http.MethodDelete: "/api/v1/me/agent-tokens/1",
	} {
		if res := s.Do(t, s.NewClient(), method, path, map[string]string{"name": "x"}, nil); res.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s 状态码 = %d, 想 401", method, path, res.StatusCode)
		}
	}
}

// 撤销的 Agent 在历史署名里标注「已停用」（评审 P2-10，#28）：时间线与创建者
// 视图带 agent.revoked: true（增量字段），署名文案与 Markdown 追加（已停用）。
func Test撤销的Agent历史署名标注已停用(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	web := s.Login("chen", "secret-pass")
	id, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	// Agent 建单并回复，然后撤销它。
	var created createTicketResponse
	res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets",
		createTicketBody{Title: "Agent 建的工单", Labels: []string{"机型:UD-2300"}}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 建单状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+created.Number+"/replies",
		map[string]string{"body_md": "Agent 的回复"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 回复状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, web, http.MethodDelete, "/api/v1/me/agent-tokens/"+strconv.FormatInt(id, 10), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销 Agent token 状态码 = %d", res.StatusCode)
	}

	// 成员视角：创建者与回复事件的 agent 带 revoked: true。
	var got struct {
		Creator struct {
			Agent struct {
				Revoked bool `json:"revoked"`
			} `json:"agent"`
		} `json:"creator"`
		Timeline []struct {
			Type   string `json:"type"`
			Actor  struct {
				Agent struct {
					Revoked bool `json:"revoked"`
				} `json:"agent"`
			} `json:"actor"`
		} `json:"timeline"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if !got.Creator.Agent.Revoked {
		t.Errorf("创建者的 agent.revoked 应为 true")
	}
	replyFound := false
	for _, ev := range got.Timeline {
		if ev.Type == "reply" {
			replyFound = true
			if !ev.Actor.Agent.Revoked {
				t.Errorf("回复事件的 agent.revoked 应为 true")
			}
		}
	}
	if !replyFound {
		t.Fatal("时间线上没有回复事件")
	}

	// Markdown 视图同样标注（历史事件不可变，展示照实反映状态）。
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number+"?format=markdown", nil, nil)
	body := string(readAll(t, res))
	if !strings.Contains(body, "（已停用）") {
		t.Errorf("Markdown 里应出现「（已停用）」署名：\n%s", truncateForTest(body))
	}
}

func truncateForTest(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
