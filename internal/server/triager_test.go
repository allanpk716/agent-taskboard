package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 初筛人（#75）：管理员可指定；未指定时所有管理员兜底。初筛人的收件箱
// 包含轮到本部门的未指派开启工单，原因统一写「待初筛」。
func Test管理员指定初筛人并影响收件箱(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("chen", "secret-pass")
	s.CreateAdmin("li", "secret-pass")
	s.CreateMember("wang", "member-pass")
	chen := s.Login("chen", "secret-pass")
	li := s.Login("li", "secret-pass")
	wang := s.Login("wang", "member-pass")

	var created struct {
		Number string `json:"number"`
	}
	s.Do(t, chen, http.MethodPost, "/api/v1/tickets",
		map[string]any{"title": "未指派的反馈", "labels": []string{"机型:UD-2300"}}, &created)

	// 一个初筛人都没设：所有管理员默认是初筛人，普通成员不变。
	for _, c := range []*inboxClient{{name: "chen", client: chen, want: true}, {name: "li", client: li, want: true}, {name: "wang", client: wang}} {
		inbox := triagerInboxOf(t, s, c.client)
		if has := inbox.reasonByNumber()[created.Number]; c.want && has != "待初筛" {
			t.Errorf("%s 默认初筛人收件箱原因 = %q, 想 待初筛", c.name, has)
		} else if !c.want && has != "" {
			t.Errorf("%s 不是初筛人，收件箱却出现 %s（%s）", c.name, created.Number, has)
		}
	}

	// 指定wang后，只有wang是初筛人；取消后回到管理员默认。
	mustSetTriager(t, s, chen, memberID(t, s, chen, "wang"), true)
	for _, c := range []*inboxClient{{name: "chen", client: chen}, {name: "li", client: li}, {name: "wang", client: wang, want: true}} {
		inbox := triagerInboxOf(t, s, c.client)
		if has := inbox.reasonByNumber()[created.Number]; c.want && has != "待初筛" {
			t.Errorf("指定后 %s 收件箱原因 = %q, 想 待初筛", c.name, has)
		} else if !c.want && has != "" {
			t.Errorf("指定后 %s 不是初筛人，收件箱却出现 %s", c.name, created.Number)
		}
	}
	mustSetTriager(t, s, chen, memberID(t, s, chen, "wang"), false)
	if has := triagerInboxOf(t, s, li).reasonByNumber()[created.Number]; has != "待初筛" {
		t.Fatalf("取消指定后管理员应收回到收件箱，原因 = %q", has)
	}

	// 停用的显式初筛人不生效：回到所有管理员兜底。
	mustSetTriager(t, s, chen, memberID(t, s, chen, "wang"), true)
	if res := s.Do(t, chen, http.MethodPost,
		"/api/v1/members/"+itoa(memberID(t, s, chen, "wang"))+"/deactivate", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("停用wang状态码 = %d, 想 200", res.StatusCode)
	}
	if has := triagerInboxOf(t, s, chen).reasonByNumber()[created.Number]; has != "待初筛" {
		t.Fatalf("初筛人停用后管理员应收回到收件箱，原因 = %q", has)
	}
}

// 设置初筛人只接受管理员网页会话：普通成员 403，管理员 Agent 403。
func Test设置初筛人只接受管理员网页会话(t *testing.T) {
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
		res := s.Do(t, tc.c, http.MethodPost, "/api/v1/members/"+itoa(id)+"/triager",
			map[string]bool{"triager": true}, nil)
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

type inboxClient struct {
	name   string
	client *http.Client
	want   bool
}

func triagerInboxOf(t testing.TB, s *tbtest.Server, c *http.Client) inboxBody {
	t.Helper()
	var body inboxBody
	res := s.Do(t, c, http.MethodGet, "/api/v1/inbox", nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读取 %s 收件箱状态码 = %d, 想 200", body.Member, res.StatusCode)
	}
	return body
}

func memberID(t testing.TB, s *tbtest.Server, c *http.Client, username string) int64 {
	t.Helper()
	var list struct {
		Members []struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
		} `json:"members"`
	}
	s.Do(t, c, http.MethodGet, "/api/v1/members", nil, &list)
	for _, m := range list.Members {
		if m.Username == username {
			return m.ID
		}
	}
	t.Fatalf("成员名单里没有 %s", username)
	return 0
}

func mustSetTriager(t testing.TB, s *tbtest.Server, c *http.Client, id int64, triager bool) {
	t.Helper()
	res := s.Do(t, c, http.MethodPost, "/api/v1/members/"+itoa(id)+"/triager",
		map[string]bool{"triager": triager}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("设置初筛人状态码 = %d, 想 200", res.StatusCode)
	}
}
