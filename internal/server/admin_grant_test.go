package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 管理员身份（GLOSSARY「管理员」）：任何在职管理员可在网页上授予/撤销其他
// 成员的管理员身份；不能撤销自己的。授予后对方随即能做管理操作，撤销后
// 立即失去管理权限。
func Test管理员可授予与撤销其他成员的管理员身份(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	s.CreateMember("wang", "member-pass")
	chen := s.Login("chen", "secret-pass")
	wang := s.Login("wang", "member-pass")
	id := memberID(t, s, chen, "wang")

	// 授予前 wang 是普通成员：做管理操作（设置初筛人）403。
	if res := s.Do(t, wang, http.MethodPost, "/api/v1/members/"+itoa(id)+"/triager",
		map[string]bool{"triager": true}, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("授予前 wang 做管理操作状态码 = %d, 想 403", res.StatusCode)
	}

	// 授予：幂等，响应回 {id, admin}。
	var body struct {
		ID    int64 `json:"id"`
		Admin bool  `json:"admin"`
	}
	res := s.Do(t, chen, http.MethodPost, "/api/v1/members/"+itoa(id)+"/admin",
		map[string]bool{"admin": true}, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("授予管理员状态码 = %d, 想 200", res.StatusCode)
	}
	if body.ID != id || !body.Admin {
		t.Errorf("授予响应 = %+v, 想 {id:%d admin:true}", body, id)
	}

	// 被授予者随即能做管理操作；再次授予幂等成功。
	mustSetTriager(t, s, wang, id, true)
	mustSetAdmin(t, s, chen, id, true)

	// 撤销后再做管理操作得 403；撤销同样幂等。
	mustSetAdmin(t, s, chen, id, false)
	mustSetAdmin(t, s, chen, id, false)
	if res := s.Do(t, wang, http.MethodPost, "/api/v1/members/"+itoa(id)+"/triager",
		map[string]bool{"triager": true}, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("撤销后 wang 做管理操作状态码 = %d, 想 403", res.StatusCode)
	}

	// 成员名单回显 is_admin：chen 是管理员，wang 已撤销。
	admins := map[string]bool{}
	var list struct {
		Members []struct {
			Username string `json:"username"`
			IsAdmin  bool   `json:"is_admin"`
		} `json:"members"`
	}
	s.Do(t, chen, http.MethodGet, "/api/v1/members", nil, &list)
	for _, m := range list.Members {
		admins[m.Username] = m.IsAdmin
	}
	if !admins["chen"] || admins["wang"] {
		t.Errorf("名单 is_admin = %v, 想 chen:true wang:false", admins)
	}
}

// 不能撤销自己的管理员身份：422 cannot_revoke_self_admin，hint 提示找
// 另一位管理员；授予自己（本来就是管理员）幂等成功。
func Test不能撤销自己的管理员身份(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	chen := s.Login("chen", "secret-pass")
	id := memberID(t, s, chen, "chen")

	res := s.Do(t, chen, http.MethodPost, "/api/v1/members/"+itoa(id)+"/admin",
		map[string]bool{"admin": false}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("撤销自己状态码 = %d, 想 422", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "cannot_revoke_self_admin" {
		t.Errorf("错误码 = %q, 想 cannot_revoke_self_admin", errBody.Error.Code)
	}
	if errBody.Error.Hint != "请另一位管理员来撤销" {
		t.Errorf("hint = %q, 想「请另一位管理员来撤销」", errBody.Error.Hint)
	}

	if res := s.Do(t, chen, http.MethodPost, "/api/v1/members/"+itoa(id)+"/admin",
		map[string]bool{"admin": true}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("授予自己状态码 = %d, 想 200（幂等成功）", res.StatusCode)
	}
}

// 已停用的成员不能被授予管理员（422 member_inactive）；撤销停用成员的
// 管理员身份允许——停用不清除 is_admin，恢复后仍是管理员。
func Test停用成员不能被授予管理员(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	s.CreateAdmin("li", "secret-pass")
	chen := s.Login("chen", "secret-pass")
	id := memberID(t, s, chen, "li")

	if res := s.Do(t, chen, http.MethodPost, "/api/v1/members/"+itoa(id)+"/deactivate",
		nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("停用 li 状态码 = %d, 想 200", res.StatusCode)
	}
	res := s.Do(t, chen, http.MethodPost, "/api/v1/members/"+itoa(id)+"/admin",
		map[string]bool{"admin": true}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("授予停用成员状态码 = %d, 想 422", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "member_inactive" {
		t.Errorf("错误码 = %q, 想 member_inactive", errBody.Error.Code)
	}

	// 撤销停用成员的管理员身份允许；恢复后不再是管理员。
	if res := s.Do(t, chen, http.MethodPost, "/api/v1/members/"+itoa(id)+"/admin",
		map[string]bool{"admin": false}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("撤销停用成员管理员状态码 = %d, 想 200", res.StatusCode)
	}
	if res := s.Do(t, chen, http.MethodPost, "/api/v1/members/"+itoa(id)+"/reactivate",
		nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("恢复 li 状态码 = %d, 想 200", res.StatusCode)
	}
	var list struct {
		Members []struct {
			Username string `json:"username"`
			IsAdmin  bool   `json:"is_admin"`
		} `json:"members"`
	}
	s.Do(t, chen, http.MethodGet, "/api/v1/members", nil, &list)
	for _, m := range list.Members {
		if m.Username == "li" && m.IsAdmin {
			t.Errorf("li 已被撤销管理员，恢复后不应仍是管理员")
		}
	}
}

// 设置管理员身份只接受管理员本人的网页会话：普通成员 403 admin_required，
// 管理员 Agent token 403 web_session_required。
func Test设置管理员只接受管理员网页会话(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	s.CreateMember("wang", "member-pass")
	admin := s.Login("chen", "secret-pass")
	member := s.Login("wang", "member-pass")
	_, token := s.IssueAgentToken(t, admin, "秘书")
	id := memberID(t, s, admin, "wang")

	for _, tc := range []struct {
		name string
		c    *http.Client
		code string
	}{
		{"普通成员", member, "admin_required"},
		{"管理员Agent", s.AgentClient(token), "web_session_required"},
	} {
		res := s.Do(t, tc.c, http.MethodPost, "/api/v1/members/"+itoa(id)+"/admin",
			map[string]bool{"admin": true}, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s 状态码 = %d, 想 403", tc.name, res.StatusCode)
			continue
		}
		var errBody errorBody
		tbtest.DecodeJSON(t, res, &errBody)
		if errBody.Error.Code != tc.code {
			t.Errorf("%s 错误码 = %q, 想 %q", tc.name, errBody.Error.Code, tc.code)
		}
	}
}

func mustSetAdmin(t testing.TB, s *tbtest.Server, c *http.Client, id int64, admin bool) {
	t.Helper()
	res := s.Do(t, c, http.MethodPost, "/api/v1/members/"+itoa(id)+"/admin",
		map[string]bool{"admin": admin}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("设置管理员状态码 = %d, 想 200", res.StatusCode)
	}
}
