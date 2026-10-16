package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// fetchTimeline 读取时间线（可带 after），返回响应结构。
func fetchTimeline(t testing.TB, s *tbtest.Server, c *http.Client, number, after string) timelineBody {
	t.Helper()
	path := "/api/v1/tickets/" + number + "/timeline"
	if after != "" {
		path += "?after=" + after
	}
	var got timelineBody
	res := s.Do(t, c, http.MethodGet, path, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d, 想 200", path, res.StatusCode)
	}
	return got
}

// 时间线增量读取：after=N 只返回 seq>N 的事件，cursor 是当前最后一条事件的 seq；
// 响应还带 waiting_on、your_turn 和状态，客户端可以据此更新界面。
func Test时间线增量读取after(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级 UD-2300 固件")

	// 造 5 条事件：created + 回复 + 修订 + 关闭 + 重开。
	if res := reply(t, s, web, number, "收到。"); res.StatusCode != http.StatusCreated {
		t.Fatal("回复失败")
	}
	if res := revise(t, s, web, number, map[string]any{"brief_md": "修订后", "base_revision": 1}); res.StatusCode != http.StatusOK {
		t.Fatal("修订失败")
	}
	if res := closeTicket(t, s, web, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatal("关闭失败")
	}
	if res := s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatal("重开失败")
	}

	// 全量：5 条，cursor=5。
	all := fetchTimeline(t, s, web, number, "")
	if len(all.Events) != 5 || all.Cursor != 5 {
		t.Fatalf("全量时间线 = %d 条 cursor=%d, 想 5 条 cursor=5", len(all.Events), all.Cursor)
	}
	if all.Status != "open" || all.WaitingOn.Party != "department" {
		t.Errorf("状态/等待方不对：%q %+v", all.Status, all.WaitingOn)
	}
	if all.YourTurn {
		t.Error("未指派工单 your_turn 应为 false（轮到本部门）")
	}

	// 增量：after=2 只返回 3..5。
	inc := fetchTimeline(t, s, web, number, "2")
	if len(inc.Events) != 3 || inc.Events[0].Seq != 3 || inc.Events[2].Seq != 5 {
		t.Fatalf("after=2 的事件不对：%+v", inc.Events)
	}
	if inc.Events[0].Type != "brief_revised" || inc.Events[1].Type != "closed" || inc.Events[2].Type != "reopened" {
		t.Errorf("增量事件类型不对：%+v", inc.Events)
	}

	// 追平后再取增量：空列表，cursor 不变。
	caught := fetchTimeline(t, s, web, number, "5")
	if len(caught.Events) != 0 || caught.Cursor != 5 {
		t.Errorf("追平后 events/cursor = %d/%d, 想 0/5", len(caught.Events), caught.Cursor)
	}

	// after 不是整数或为负 → 422 invalid_cursor。
	for _, bad := range []string{"abc", "-1", "1.5"} {
		res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/timeline?after="+bad, nil, nil)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("after=%s 状态码 = %d, 想 422", bad, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "invalid_cursor" {
			t.Errorf("after=%s 错误码 = %q, 想 invalid_cursor", bad, got.Error.Code)
		}
	}

	// 不存在的工单 → 404。
	res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/20990101-001/timeline", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("状态码 = %d, 想 404", res.StatusCode)
	}
}

// 时间线事件只追加：旧事件在后续操作后原样保留；每条都记录作者和时间（验收点）。
func Test时间线只追加且每条带作者和时间(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	number := mustCreateTicket(t, s, web, "升级 UD-2300 固件")

	reply(t, s, agent, number, "Agent 第一条回复。")
	before := fetchTimeline(t, s, web, number, "")

	revise(t, s, web, number, map[string]any{"brief_md": "修订后", "base_revision": 1})
	reply(t, s, web, number, "本人追问。")
	closeTicket(t, s, web, number, closeBody{Reason: "wontfix"})
	after := fetchTimeline(t, s, web, number, "")

	assertTimelineAppendOnly(t, before.Events, after.Events)
	if len(after.Events) != len(before.Events)+3 {
		t.Fatalf("新增事件数不对：%d → %d", len(before.Events), len(after.Events))
	}
	// 新增的三条：brief_revised、reply、closed。
	wantTypes := []string{"brief_revised", "reply", "closed"}
	for i, want := range wantTypes {
		if got := after.Events[len(before.Events)+i].Type; got != want {
			t.Errorf("新增事件 %d 类型 = %q, 想 %q", i, got, want)
		}
	}
	// 事件作者与时间的具体形态：closed 事件由陈工本人发出。
	last := after.Events[len(after.Events)-1]
	if last.Actor.Member.Name != "陈工" || last.Actor.Agent != nil {
		t.Errorf("closed 事件作者不对：%+v", last.Actor)
	}
	if last.At == "" {
		t.Error("closed 事件缺时间")
	}
}
