package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// closeResult 是 POST …/close 的响应结构。
type closeResult struct {
	Status      string  `json:"status"`
	CloseReason string  `json:"close_reason"`
	DuplicateOf *string `json:"duplicate_of"`
}

// reopenResult 是 POST …/reopen 的响应结构。
type reopenResult struct {
	Status string `json:"status"`
}

// 关闭必须给原因：缺失或非法都返回 422 invalid_close_reason。
func Test关闭不给原因或原因非法返回422(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级固件")

	cases := map[string]closeBody{
		"缺失":  {Reason: ""},
		"非法值": {Reason: "finished"},
		"数字":  {Reason: "1"},
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			res := closeTicket(t, s, web, number, body)
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "invalid_close_reason" {
				t.Errorf("错误码 = %q, 想 invalid_close_reason", got.Error.Code)
			}
		})
	}
	// 校验失败的关闭不落地。
	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.Status != "open" {
		t.Errorf("被拒的关闭不应落地：status = %q", after.Status)
	}
}

// 任何成员（含成员 Agent）都能关闭与重开；关闭记 closed 事件，重开记 reopened 事件，
// 重开后可以继续回复。
func Test关闭重开后可以继续回复(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateAdmin("李工", "secret-pass")
	chen := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	_, token := s.IssueAgentToken(t, li, "工位")
	liAgent := s.AgentClient(token)
	number := mustCreateTicket(t, s, chen, "升级 UD-2300 固件")

	// 非创建者的成员也能关闭。
	res := closeTicket(t, s, li, number, closeBody{Reason: "done"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("成员关闭状态码 = %d, 想 200（任何成员都能关闭）", res.StatusCode)
	}
	var closed closeResult
	tbtest.DecodeJSON(t, res, &closed)
	if closed.Status != "closed" || closed.CloseReason != "done" {
		t.Errorf("关闭响应不对：%+v", closed)
	}

	// 成员 Agent 也能关闭（署名按 ADR 0003 记入事件）。
	number2 := mustCreateTicket(t, s, chen, "另一件事")
	res = closeTicket(t, s, liAgent, number2, closeBody{Reason: "wontfix"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("成员 Agent 关闭状态码 = %d, 想 200", res.StatusCode)
	}

	var got ticketResponseBody
	s.Do(t, chen, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	if got.Status != "closed" {
		t.Fatalf("关闭后 status = %q", got.Status)
	}
	if got.CloseReason == nil || *got.CloseReason != "done" {
		t.Errorf("close_reason = %v, 想 done", got.CloseReason)
	}
	// viewer.can 随状态翻转：关闭后不能关、能重开。
	if got.Viewer.Can.Close || !got.Viewer.Can.Reopen {
		t.Errorf("关闭后的 viewer.can 不对：%+v", got.Viewer.Can)
	}
	if got.WaitingOn.Party != "none" {
		t.Errorf("关闭后 waiting_on.party = %q, 想 none", got.WaitingOn.Party)
	}
	if len(got.Timeline) != 2 || got.Timeline[1].Type != "closed" || got.Timeline[1].Reason != "done" {
		t.Fatalf("closed 事件不对：%+v", got.Timeline)
	}

	// 重复关闭 → 409 already_closed。
	res = closeTicket(t, s, li, number, closeBody{Reason: "wontfix"})
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("重复关闭状态码 = %d, 想 409", res.StatusCode)
	}
	var conflict errorBody
	tbtest.DecodeJSON(t, res, &conflict)
	if conflict.Error.Code != "already_closed" {
		t.Errorf("错误码 = %q, 想 already_closed", conflict.Error.Code)
	}

	// 重开：任何成员；记 reopened 事件。
	res = s.Do(t, li, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重开状态码 = %d, 想 200", res.StatusCode)
	}
	var reopened reopenResult
	tbtest.DecodeJSON(t, res, &reopened)
	if reopened.Status != "open" {
		t.Errorf("重开响应 status = %q, 想 open", reopened.Status)
	}

	// 未关闭时重开 → 409 not_closed。
	res = s.Do(t, li, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("未关闭时重开状态码 = %d, 想 409", res.StatusCode)
	}
	var notClosed errorBody
	tbtest.DecodeJSON(t, res, &notClosed)
	if notClosed.Error.Code != "not_closed" {
		t.Errorf("错误码 = %q, 想 not_closed", notClosed.Error.Code)
	}

	// 重开后可以继续回复（验收点）。
	if res := reply(t, s, chen, number, "重开后补充：新固件已到货。"); res.StatusCode != http.StatusCreated {
		t.Fatalf("重开后回复状态码 = %d, 想 201", res.StatusCode)
	}
	var after ticketResponseBody
	s.Do(t, chen, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if after.Status != "open" || len(after.Timeline) != 4 {
		t.Fatalf("重开后状态/时间线不对：%q %d 条", after.Status, len(after.Timeline))
	}
	types := []string{after.Timeline[1].Type, after.Timeline[2].Type, after.Timeline[3].Type}
	if types[0] != "closed" || types[1] != "reopened" || types[2] != "reply" {
		t.Errorf("事件顺序不对：%v", types)
	}
	// 重开后 close_reason 清空。
	if after.CloseReason != nil {
		t.Errorf("重开后 close_reason 应为 null：%v", after.CloseReason)
	}
}

// 原因为 duplicate 时必须带 duplicate_of（可写编号或序号）；事件和响应回显
// 重复的那张工单；指向不存在的工单返回 404。
func Test重复关闭要带duplicateOf(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	first := mustCreateTicket(t, s, web, "LIS 导出报错")
	second := mustCreateTicket(t, s, web, "LIS 导出报错（再次出现）")

	// 不带 duplicate_of → 422 duplicate_of_required。
	res := closeTicket(t, s, web, second, closeBody{Reason: "duplicate"})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "duplicate_of_required" {
		t.Errorf("错误码 = %q, 想 duplicate_of_required", got.Error.Code)
	}

	// 用编号关闭 → 事件与响应回显。
	res = closeTicket(t, s, web, second, closeBody{Reason: "duplicate", DuplicateOf: first})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重复关闭状态码 = %d, 想 200", res.StatusCode)
	}
	var closed closeResult
	tbtest.DecodeJSON(t, res, &closed)
	if closed.DuplicateOf == nil || *closed.DuplicateOf != first {
		t.Errorf("duplicate_of = %v, 想 %q", closed.DuplicateOf, first)
	}
	var view ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+second, nil, &view)
	last := view.Timeline[len(view.Timeline)-1]
	if last.Type != "closed" || last.Reason != "duplicate" || last.DuplicateOf != first {
		t.Errorf("closed 事件不对：%+v", last)
	}

	// 指向不存在的工单 → 404 ticket_not_found。
	third := mustCreateTicket(t, s, web, "LIS 导出报错（第三次）")
	res = closeTicket(t, s, web, third, closeBody{Reason: "duplicate", DuplicateOf: "20990101-001"})
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("状态码 = %d, 想 404", res.StatusCode)
	}
	var missing errorBody
	tbtest.DecodeJSON(t, res, &missing)
	if missing.Error.Code != "ticket_not_found" {
		t.Errorf("错误码 = %q, 想 ticket_not_found", missing.Error.Code)
	}

	// 用序号（数字）也可以。
	res = closeTicket(t, s, web, third, closeBody{Reason: "duplicate", DuplicateOf: 1})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("用序号关闭状态码 = %d, 想 200", res.StatusCode)
	}
}
