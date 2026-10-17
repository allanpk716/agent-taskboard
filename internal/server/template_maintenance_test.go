package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 模板维护请求同样支持 Idempotency-Key：同 key 重放返回首次响应，不重复建模板。
func Test模板维护请求支持幂等键(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	body := map[string]string{"key": "check", "markdown": checkTemplate}
	res1 := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/templates",
		map[string]string{"Idempotency-Key": "tpl-1"}, body, nil)
	if res1.StatusCode != http.StatusCreated {
		t.Fatalf("首次状态码 = %d, 想 201", res1.StatusCode)
	}
	res2 := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/templates",
		map[string]string{"Idempotency-Key": "tpl-1"}, body, nil)
	if res2.StatusCode != http.StatusCreated {
		t.Fatalf("重放状态码 = %d, 想 201（返回首次结果）", res2.StatusCode)
	}

	// 重放没有产生第二份内容：模板只有一个 check。
	var list struct {
		Templates []templateSummary `json:"templates"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/templates", nil, &list)
	count := 0
	for _, tpl := range list.Templates {
		if tpl.Key == "check" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("check 模板出现 %d 次, 想 1", count)
	}
}

// 成员 Agent 也能维护模板（任何成员都能新建和修改）；版本记录具名 Agent。
func Test成员Agent可以维护模板(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "秘书")
	agent := s.AgentClient(token)

	res := s.Do(t, agent, http.MethodPost, "/api/v1/templates",
		map[string]string{"key": "agent-made", "markdown": checkTemplate}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("Agent 新建状态码 = %d, 想 201", res.StatusCode)
	}
	var got templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/agent-made", nil, &got)
	if got.UpdatedBy == nil || got.UpdatedBy.Agent == nil || got.UpdatedBy.Agent.Name != "秘书" {
		t.Errorf("修改人应为具名 Agent：%+v", got.UpdatedBy)
	}
}
