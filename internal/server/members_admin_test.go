package server_test

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// 成员网页（#42）的管理员功能：在网页上创建、停用成员账号（spec 用户故事 88，
// 只接受网页会话发起，ADR 0003）。停用不是删除：已有的回复和事件保留，
// 但不能再登录，已登录的会话与其 Agent token 立即失效。
func Test管理员在网页上创建成员账号(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	// 创建：服务器生成初始密码，只在创建响应里出现一次。
	var created struct {
		Member struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Name     string `json:"name"`
			IsAdmin  bool   `json:"is_admin"`
			Active   bool   `json:"active"`
		} `json:"member"`
		InitialPassword string `json:"initial_password"`
	}
	res := s.Do(t, admin, http.MethodPost, "/api/v1/members",
		map[string]string{"username": "zhao", "name": "赵工"}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("创建成员状态码 = %d, 想 201", res.StatusCode)
	}
	if created.Member.Username != "zhao" || created.Member.Name != "赵工" {
		t.Errorf("创建的成员不对：%+v", created.Member)
	}
	if created.Member.IsAdmin {
		t.Errorf("网页创建的应是普通成员，不是管理员")
	}
	if !created.Member.Active {
		t.Errorf("新成员应是启用状态")
	}
	if len(created.InitialPassword) < 8 {
		t.Fatalf("初始密码太短：%q", created.InitialPassword)
	}

	// 新成员用初始密码能登录（登录成功即算账号可用）。
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session",
		map[string]string{"username": "zhao", "password": created.InitialPassword}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("新成员用初始密码登录状态码 = %d, 想 200", res.StatusCode)
	}

	// 初始密码只出现一次：成员名单与再次创建的失败响应里都不回显。
	var list struct {
		Members []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Name     string `json:"name"`
			Active   bool   `json:"active"`
		} `json:"members"`
	}
	s.Do(t, admin, http.MethodGet, "/api/v1/members", nil, &list)
	found := false
	for _, m := range list.Members {
		if m.Username == "zhao" {
			found = true
			if !m.Active {
				t.Errorf("名单里 zhao 应为启用")
			}
		}
	}
	if !found {
		t.Fatalf("成员名单里没有 zhao：%+v", list.Members)
	}
	raw, _ := io.ReadAll(s.Do(t, admin, http.MethodGet, "/api/v1/members", nil, nil).Body)
	if strings.Contains(string(raw), "initial_password") || strings.Contains(string(raw), created.InitialPassword) {
		t.Errorf("成员名单不应回显初始密码：%s", raw)
	}

	// 登录名重复 → 409 username_taken。
	res = s.Do(t, admin, http.MethodPost, "/api/v1/members",
		map[string]string{"username": "zhao", "name": "另一个赵工"}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("重复登录名状态码 = %d, 想 409", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "username_taken" {
		t.Errorf("错误码 = %q, 想 username_taken", errBody.Error.Code)
	}

	// 缺登录名 → 422 username_required；名字为空时用登录名兜底（与 create-admin 一致）。
	res = s.Do(t, admin, http.MethodPost, "/api/v1/members", map[string]string{"name": "没写登录名"}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("缺登录名状态码 = %d, 想 422", res.StatusCode)
	}
}

// 创建与停用成员账号只接受管理员的网页会话：非管理员 403 admin_required，
// Agent token 403 web_session_required（Agent 不能创建账号，ADR 0003），
// 访客 403 members_only，匿名 401。
func Test成员账号管理只接受管理员网页会话(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")
	_, token := s.IssueAgentToken(t, admin, "秘书")
	agent := s.AgentClient(token)

	cases := []struct {
		name string
		c    *http.Client
		code string
		want int
	}{
		{"非管理员成员", member, "admin_required", http.StatusForbidden},
		{"管理员Agent", agent, "web_session_required", http.StatusForbidden},
	}
	for _, tc := range cases {
		res := s.Do(t, tc.c, http.MethodPost, "/api/v1/members",
			map[string]string{"username": "x-" + tc.name, "name": tc.name}, nil)
		if res.StatusCode != tc.want {
			t.Errorf("%s 创建成员状态码 = %d, 想 %d", tc.name, res.StatusCode, tc.want)
			continue
		}
		var errBody errorBody
		tbtest.DecodeJSON(t, res, &errBody)
		if errBody.Error.Code != tc.code {
			t.Errorf("%s 错误码 = %q, 想 %q", tc.name, errBody.Error.Code, tc.code)
		}
	}

	// 匿名 401。
	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/members",
		map[string]string{"username": "anon", "name": "匿名"}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("匿名创建成员状态码 = %d, 想 401", res.StatusCode)
	}

	// 访客 403 members_only：先造一张工单开链接。
	var first struct {
		Number string `json:"number"`
	}
	s.Do(t, member, http.MethodPost, "/api/v1/tickets",
		map[string]any{"title": "拿编号用", "labels": []string{"机型:US-1680"}}, &first)
	link := s.CreateGuestLink(t, member, first.Number, map[string]any{"note": "检验科-张三"})
	res = s.Do(t, s.GuestClient(link.Key(t)), http.MethodPost, "/api/v1/members",
		map[string]string{"username": "guest-made", "name": "访客建号"}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("访客创建成员状态码 = %d, 想 403", res.StatusCode)
	}

	// 停用同样只接受管理员网页会话（对同一目标的越权请求逐一核对）。
	var created struct {
		Member struct {
			ID int64 `json:"id"`
		} `json:"member"`
	}
	s.Do(t, admin, http.MethodPost, "/api/v1/members",
		map[string]string{"username": "target", "name": "停用练习"}, &created)
	for _, tc := range cases {
		res := s.Do(t, tc.c, http.MethodPost,
			"/api/v1/members/"+itoa(created.Member.ID)+"/deactivate", nil, nil)
		if res.StatusCode != tc.want {
			t.Errorf("%s 停用成员状态码 = %d, 想 %d", tc.name, res.StatusCode, tc.want)
		}
	}
}

// 停用成员账号：停用后不能登录；已登录的会话与其 Agent token 立即失效；
// 再停用幂等成功；不能停用自己；名单标记为已停用。
func Test停用成员账号立即生效(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	var created struct {
		Member struct {
			ID int64 `json:"id"`
		} `json:"member"`
		InitialPassword string `json:"initial_password"`
	}
	s.Do(t, admin, http.MethodPost, "/api/v1/members",
		map[string]string{"username": "zhao", "name": "赵工"}, &created)
	zhao := s.Login("zhao", created.InitialPassword)
	_, agentToken := s.IssueAgentToken(t, zhao, "笔记本")
	zhaoAgent := s.AgentClient(agentToken)

	// 停用前一切正常。
	res := s.Do(t, zhao, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("停用前本人会话状态码 = %d, 想 200", res.StatusCode)
	}

	// 停用：200 {id, active:false}，幂等（再停用仍 200）。
	res = s.Do(t, admin, http.MethodPost,
		"/api/v1/members/"+itoa(created.Member.ID)+"/deactivate", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("停用状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, admin, http.MethodPost,
		"/api/v1/members/"+itoa(created.Member.ID)+"/deactivate", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重复停用状态码 = %d, 想 200（幂等）", res.StatusCode)
	}

	// 已登录的会话立即失效。
	res = s.Do(t, zhao, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("停用后本人会话状态码 = %d, 想 401", res.StatusCode)
	}
	// 其 Agent token 也立即失效（账号停用，名下 Agent 一并停用）。
	res = s.Do(t, zhaoAgent, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("停用后 Agent token 状态码 = %d, 想 401", res.StatusCode)
	}
	// 不能再登录（明确告知账号已停用）。
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session",
		map[string]string{"username": "zhao", "password": created.InitialPassword}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("停用后登录状态码 = %d, 想 401", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "member_deactivated" {
		t.Errorf("停用后登录错误码 = %q, 想 member_deactivated", errBody.Error.Code)
	}

	// 名单标记为已停用（成员仍列出：历史署名可查）。
	var list struct {
		Members []struct {
			Username string `json:"username"`
			Active   bool   `json:"active"`
		} `json:"members"`
	}
	s.Do(t, admin, http.MethodGet, "/api/v1/members", nil, &list)
	for _, m := range list.Members {
		if m.Username == "zhao" && m.Active {
			t.Errorf("名单里 zhao 应为已停用")
		}
	}

	// 管理员不能停用自己（防止把自己锁在门外）。
	var me struct {
		ID int64 `json:"id"`
	}
	var sess struct {
		Viewer struct {
			Member struct {
				ID int64 `json:"id"`
			} `json:"member"`
		} `json:"viewer"`
	}
	s.Do(t, admin, http.MethodGet, "/api/v1/session", nil, &sess)
	me.ID = sess.Viewer.Member.ID
	res = s.Do(t, admin, http.MethodPost, "/api/v1/members/"+itoa(me.ID)+"/deactivate", nil, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("停用自己状态码 = %d, 想 422", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "cannot_deactivate_self" {
		t.Errorf("停用自己错误码 = %q, 想 cannot_deactivate_self", errBody.Error.Code)
	}

	// 不存在的成员 → 404 member_not_found。
	res = s.Do(t, admin, http.MethodPost, "/api/v1/members/99999/deactivate", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("停用不存在成员状态码 = %d, 想 404", res.StatusCode)
	}
}
