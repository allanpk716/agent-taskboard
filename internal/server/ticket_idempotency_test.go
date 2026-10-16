package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// readAll 读取响应体（比较幂等重放是否逐字节相同时用）。
func readAll(t testing.TB, res *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应体失败：%v", err)
	}
	return b
}

// 带同一个 Idempotency-Key 重放回复：只产生一条回复，两次响应逐字节相同（验收点）。
func Test同一幂等键重放回复只产生一条(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "升级 UD-2300 固件")

	headers := map[string]string{"Idempotency-Key": "reply-once-001"}
	first := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		headers, map[string]string{"body_md": "收到，正在处理。"}, nil)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("首次回复状态码 = %d, 想 201", first.StatusCode)
	}
	firstBody := readAll(t, first)

	// 网络超时重试：同一个 key、同样的请求。
	second := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		headers, map[string]string{"body_md": "收到，正在处理。"}, nil)
	if second.StatusCode != http.StatusCreated {
		t.Fatalf("重放状态码 = %d, 想 201（快照含首次状态码）", second.StatusCode)
	}
	secondBody := readAll(t, second)
	if string(firstBody) != string(secondBody) {
		t.Errorf("两次响应不同：\n%s\n%s", firstBody, secondBody)
	}
	if ct1, ct2 := first.Header.Get("Content-Type"), second.Header.Get("Content-Type"); ct1 != ct2 || ct1 == "" {
		t.Errorf("两次响应的 Content-Type 不同：%q vs %q", ct1, ct2)
	}

	// 只产生一条回复。
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	replies := 0
	for _, ev := range got.Timeline {
		if ev.Type == "reply" {
			replies++
		}
	}
	if replies != 1 {
		t.Errorf("时间线上有 %d 条回复, 想 1", replies)
	}

	// 换一个 key 是新的请求：再产生一条回复。
	third := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"Idempotency-Key": "reply-once-002"}, map[string]string{"body_md": "收到，正在处理。"}, nil)
	if third.StatusCode != http.StatusCreated {
		t.Fatalf("换 key 回复状态码 = %d, 想 201", third.StatusCode)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	replies = 0
	for _, ev := range got.Timeline {
		if ev.Type == "reply" {
			replies++
		}
	}
	if replies != 2 {
		t.Errorf("换 key 后应有 2 条回复，实际 %d", replies)
	}
}

// 幂等记录按凭证隔离：同一个 key、不同成员（或成员的 Agent），各自产生新内容。
func Test幂等键按凭证隔离(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateAdmin("李工", "secret-pass")
	chen := s.Login("陈工", "secret-pass")
	li := s.Login("李工", "secret-pass")
	_, token := s.IssueAgentToken(t, chen, "笔记本")
	chenAgent := s.AgentClient(token)
	number := mustCreateTicket(t, s, chen, "升级 UD-2300 固件")

	key := map[string]string{"Idempotency-Key": "shared-key"}
	for i, c := range []*http.Client{chen, chenAgent, li} {
		res := s.DoWithHeaders(t, c, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
			key, map[string]string{"body_md": "各说各话。"}, nil)
		if res.StatusCode != http.StatusCreated {
			t.Fatalf("第 %d 个凭证回复状态码 = %d, 想 201", i+1, res.StatusCode)
		}
	}

	var got ticketResponseBody
	s.Do(t, chen, http.MethodGet, "/api/v1/tickets/"+number, nil, &got)
	replies := 0
	for _, ev := range got.Timeline {
		if ev.Type == "reply" {
			replies++
		}
	}
	if replies != 3 {
		t.Errorf("三个凭证各产生一条回复，实际 %d 条", replies)
	}
}

// 建单和说明修订同样支持 Idempotency-Key：重放不重复建单、不重复修订。
func Test建单与修订说明支持幂等键(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	key := map[string]string{"Idempotency-Key": "create-once"}
	body := createTicketBody{Title: "升级 UD-2300 固件", BriefMD: "初版", Labels: []string{"机型:UD-2300"}}
	first := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/tickets", key, body, nil)
	if first.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", first.StatusCode)
	}
	firstBody := readAll(t, first)
	second := s.DoWithHeaders(t, web, http.MethodPost, "/api/v1/tickets", key, body, nil)
	if second.StatusCode != http.StatusCreated || string(firstBody) != string(readAll(t, second)) {
		t.Fatalf("重放建单没有返回首次结果：%d vs %d", first.StatusCode, second.StatusCode)
	}
	var created createTicketResponse
	if err := json.Unmarshal(firstBody, &created); err != nil {
		t.Fatalf("建单响应不是合法 JSON：%s", firstBody)
	}

	// 库里只有一张：第二张的编号应当不存在。
	if res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/2", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("重放不应产生第二张工单：GET #2 状态码 = %d, 想 404", res.StatusCode)
	}

	// 说明修订同理。
	rkey := map[string]string{"Idempotency-Key": "revise-once"}
	rev := map[string]any{"brief_md": "第二版", "base_revision": 1}
	r1 := s.DoWithHeaders(t, web, http.MethodPatch, "/api/v1/tickets/"+created.Number, rkey, rev, nil)
	if r1.StatusCode != http.StatusOK {
		t.Fatalf("修订状态码 = %d", r1.StatusCode)
	}
	r1Body := readAll(t, r1)
	r2 := s.DoWithHeaders(t, web, http.MethodPatch, "/api/v1/tickets/"+created.Number, rkey, rev, nil)
	if r2.StatusCode != http.StatusOK || string(r1Body) != string(readAll(t, r2)) {
		t.Fatalf("重放修订没有返回首次结果：%d vs %d", r1.StatusCode, r2.StatusCode)
	}
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &got)
	revisions := 0
	for _, ev := range got.Timeline {
		if ev.Type == "brief_revised" {
			revisions++
		}
	}
	if revisions != 1 || got.Brief.Revision != 2 {
		t.Errorf("重放不应产生第二条修订：事件 %d 条、版本 %d", revisions, got.Brief.Revision)
	}
}
