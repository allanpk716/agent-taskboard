package server_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

var gbkDrill = []byte{0xd1, 0xdd, 0xc1, 0xb7}

func postRaw(t testing.TB, s *tbtest.Server, c *http.Client, method, path string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, s.URL()+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func countRows(t testing.TB, s *tbtest.Server, table string) int {
	t.Helper()
	var n int
	if err := s.Store().DB.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("统计 %s 失败：%v", table, err)
	}
	return n
}

func assertInvalidUTF8(t testing.TB, res *http.Response) {
	t.Helper()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, 想 400：%s", res.StatusCode, readAll(t, res))
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "invalid_utf8" {
		t.Fatalf("错误码 = %q, 想 invalid_utf8", got.Error.Code)
	}
	if got.Error.Hint == "" {
		t.Fatal("hint 为空：应告诉 Windows Agent 如何发送 UTF-8 JSON")
	}
}

func Test非法UTF8请求体被拒绝且不写入(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "member-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "GBK 回归用工单")

	// 「演练」的 GBK 字节是 D1 DD C1 B7；这里保留原始字节，不做字符串转换。
	jsonBytes := func(prefix, suffix string) []byte {
		return append(append([]byte(prefix), gbkDrill...), suffix...)
	}

	checks := []struct {
		name  string
		path  string
		body  []byte
		table string
	}{
		{
			name:  "建单",
			path:  "/api/v1/tickets",
			body:  jsonBytes(`{"title":"`, `","labels":["机型:UD-2300"]}`),
			table: "tickets",
		},
		{
			name:  "回复",
			path:  "/api/v1/tickets/" + number + "/replies",
			body:  jsonBytes(`{"body_md":"`, `"}`),
			table: "timeline_events",
		},
		{
			name:  "创建成员",
			path:  "/api/v1/members",
			body:  jsonBytes(`{"username":"zhao","name":"`, `"}`),
			table: "members",
		},
		{
			name:  "签发AgentToken",
			path:  "/api/v1/me/agent-tokens",
			body:  jsonBytes(`{"name":"`, `"}`),
			table: "agent_tokens",
		},
		{
			name:  "登录",
			path:  "/api/v1/session",
			body:  jsonBytes(`{"username":"`, `","password":"secret-pass"}`),
			table: "sessions",
		},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			before := countRows(t, s, check.table)
			assertInvalidUTF8(t, postRaw(t, s, web, http.MethodPost, check.path, check.body))
			if after := countRows(t, s, check.table); after != before {
				t.Fatalf("%s 表记录 %d → %d，被拒绝的请求不应写入", check.table, before, after)
			}
		})
	}
}

func Test合法UTF8中文请求照常读写(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	var created createTicketResponse
	res := s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "演练接收人", "brief_md": "说明：演练。", "labels": []string{"机型:UD-2300"},
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201：%s", res.StatusCode, readAll(t, res))
	}
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	if got.Title != "演练接收人" || got.Brief.Markdown != "说明：演练。" {
		t.Fatalf("中文回读不一致：title=%q brief=%q", got.Title, got.Brief.Markdown)
	}
}

func Test建单与改派accept数字成员id并拒绝非法assignee(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("李工", "member-pass")
	web := s.Login("陈工", "secret-pass")

	var members struct {
		Members []struct {
			ID   int64
			Name string
		} `json:"members"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/members", nil, &members)
	var memberID int64
	for _, m := range members.Members {
		if m.Name == "李工" {
			memberID = m.ID
		}
	}
	if memberID == 0 {
		t.Fatal("成员名单里没有李工")
	}

	create := func(name string, assignee any) createAssignedResponse {
		t.Helper()
		var created createAssignedResponse
		res := s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
			"title": name, "labels": []string{"机型:UD-2300"}, "assignee": assignee,
		}, &created)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("建单 %s 状态码 = %d, 想 201：%s", name, res.StatusCode, readAll(t, res))
		}
		return created
	}
	if got := create("数字id建单", memberID); got.Assignee == nil || got.Assignee.ID != memberID || got.Assignee.Name != "李工" {
		t.Fatalf("数字 id 建单 assignee = %+v, 想李工 #%d", got.Assignee, memberID)
	}
	if got := create("字符串id建单", itoa(memberID)); got.Assignee == nil || got.Assignee.Name != "李工" {
		t.Fatalf("字符串 id 建单 assignee = %+v, 想李工", got.Assignee)
	}
	if got := create("姓名建单", "李工"); got.Assignee == nil || got.Assignee.Name != "李工" {
		t.Fatalf("姓名建单 assignee = %+v, 想李工", got.Assignee)
	}

	assertInvalidAssignee := func(res *http.Response, note string) {
		t.Helper()
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s 状态码 = %d, 想 422：%s", note, res.StatusCode, readAll(t, res))
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "invalid_assignee" {
			t.Fatalf("%s 错误码 = %q, 想 invalid_assignee", note, got.Error.Code)
		}
	}
	beforeTickets := countRows(t, s, "tickets")
	assertInvalidAssignee(s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "布尔assignee", "labels": []string{"机型:UD-2300"}, "assignee": true,
	}, nil), "建单布尔 assignee")
	assertInvalidAssignee(s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "对象assignee", "labels": []string{"机型:UD-2300"}, "assignee": map[string]any{},
	}, nil), "建单对象 assignee")
	if afterTickets := countRows(t, s, "tickets"); afterTickets != beforeTickets {
		t.Fatalf("非法 assignee 建单写入 tickets：%d → %d", beforeTickets, afterTickets)
	}

	number := mustCreateTicket(t, s, web, "改派回归用工单")
	assign := func(assignee any) *http.Response {
		t.Helper()
		return s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
			map[string]any{"assignee": assignee}, nil)
	}
	assertAssignee := func(wantID int64, wantName string) {
		t.Helper()
		var got ticketResponseBody
		s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
		if got.Assignee == nil || got.Assignee.Member == nil ||
			got.Assignee.Member.ID != wantID || got.Assignee.Member.Name != wantName {
			t.Fatalf("工单 assignee = %+v, 想 #%d %s", got.Assignee, wantID, wantName)
		}
	}
	if res := assign(memberID); res.StatusCode != http.StatusOK {
		t.Fatalf("数字 id 改派状态码 = %d, 想 200：%s", res.StatusCode, readAll(t, res))
	}
	assertAssignee(memberID, "李工")

	if res := assign("李工"); res.StatusCode != http.StatusUnprocessableEntity || !isCode(t, res, "already_assigned") {
		t.Fatalf("姓名改派当前指派人响应不对：%d %s", res.StatusCode, readAll(t, res))
	}
	// 再用字符串 id 覆盖一次（当前是李工，改回陈工），证明字符串路径不变。
	var me struct {
		Members []struct {
			ID   int64
			Name string
		} `json:"members"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/members", nil, &me)
	var adminID int64
	for _, m := range me.Members {
		if m.Name == "陈工" {
			adminID = m.ID
		}
	}
	if res := assign(itoa(adminID)); res.StatusCode != http.StatusOK {
		t.Fatalf("字符串 id 改派状态码 = %d, 想 200：%s", res.StatusCode, readAll(t, res))
	}
	assertAssignee(adminID, "陈工")

	before := countRows(t, s, "timeline_events")
	assertInvalidAssignee(assign(true), "改派布尔 assignee")
	assertInvalidAssignee(assign(map[string]any{}), "改派对象 assignee")
	assertAssignee(adminID, "陈工")
	if after := countRows(t, s, "timeline_events"); after != before {
		t.Fatalf("非法 assignee 改派写入事件：%d → %d", before, after)
	}
}

func Test建单与改派非法JSON提示是合法示例(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "非法 JSON 提示用工单")

	for name, path := range map[string]string{
		"建单": "/api/v1/tickets",
		"改派": "/api/v1/tickets/" + number + "/assign",
	} {
		t.Run(name, func(t *testing.T) {
			res := postRaw(t, s, web, http.MethodPost, path, []byte(`{`))
			if res.StatusCode != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, 想 400：%s", res.StatusCode, readAll(t, res))
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			const prefix = "请求体应为 "
			if !strings.HasPrefix(got.Error.Hint, prefix) {
				t.Fatalf("hint = %q, 想以 %q 开头", got.Error.Hint, prefix)
			}
			example := strings.TrimPrefix(got.Error.Hint, prefix)
			if !json.Valid([]byte(example)) {
				t.Fatalf("hint 示例不是合法 JSON：%q", example)
			}
		})
	}
}

func isCode(t testing.TB, res *http.Response, code string) bool {
	t.Helper()
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	return got.Error.Code == code
}
