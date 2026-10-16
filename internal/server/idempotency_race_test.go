package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 匿名幂等记录按入口隔离（评审修正 #1）：同一个 Idempotency-Key 提交到
// 两个不同的入口，各自建单——匿名凭证的指纹是 intake:<slug>，不是全局 anonymous，
// 一个入口里的重放不会串到另一个入口的快照。
func Test匿名幂等按入口隔离(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	createIntake(t, s, web, map[string]any{"slug": "advice", "name": "建议反馈", "template": "bug-report"})

	key := map[string]string{"Idempotency-Key": "same-key-two-intakes"}
	first := submitToIntakeHeaders(t, s, "bug", key)
	second := submitToIntakeHeaders(t, s, "advice", key)
	if first.Ticket.Number == "" || second.Ticket.Number == "" {
		t.Fatalf("两次提交都应 201 建单：%q / %q", first.Ticket.Number, second.Ticket.Number)
	}
	if first.Ticket.Number == second.Ticket.Number {
		t.Errorf("不同入口各建一张工单，编号不应相同：%s", first.Ticket.Number)
	}
	if first.GuestLink.URL == second.GuestLink.URL {
		t.Errorf("不同入口不应拿到同一条访客链接：%s", first.GuestLink.URL)
	}
}

// 幂等记录 24 小时后过期（评审修正 #1）：超龄的重放当作新请求执行（再建一张单，
// 拿一条新访客链接），过期记录由清除任务删除——快照里的访客链接密钥至多保留 24 小时。
func Test幂等记录24小时后过期并被清除(t *testing.T) {
	s := tbtest.Start(t) // 假时钟起点：2026-10-16 09:00（北京时间）
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	key := map[string]string{"Idempotency-Key": "expire-after-24h"}
	first := submitToIntakeHeaders(t, s, "bug", key)
	if first.Ticket.Number != "20261016-001" {
		t.Fatalf("首次提交编号 = %q, 想 20261016-001", first.Ticket.Number)
	}

	// 24 小时内重放：返回首次快照。
	replay := submitToIntakeHeaders(t, s, "bug", key)
	if replay.Ticket.Number != first.Ticket.Number {
		t.Fatalf("24 小时内的重放应返回首次结果：%q vs %q", replay.Ticket.Number, first.Ticket.Number)
	}

	// 过 24 小时：重放当作新请求（编号进入第二天），访客链接也是新的一条。
	s.Clock().Advance(25 * time.Hour)
	fresh := submitToIntakeHeaders(t, s, "bug", key)
	if fresh.Ticket.Number != "20261017-001" {
		t.Fatalf("过期后的重放应当作新请求建单 20261017-001，实际 %q", fresh.Ticket.Number)
	}
	if fresh.GuestLink.URL == first.GuestLink.URL {
		t.Errorf("过期后的重放应拿到新的访客链接，仍是 %s", first.GuestLink.URL)
	}

	// 清除任务删掉过期记录（created_at 来自注入时钟）。
	s.Clock().Advance(24 * time.Hour)
	if n, err := s.Idempotency().PurgeExpired(context.Background()); err != nil || n != 1 {
		t.Fatalf("清除过期幂等记录 = %d/%v, 想 1 条", n, err)
	}
}

// 同一个 Idempotency-Key 的并发重放：原子预留下恰好一张工单（评审修正 #1）。
// 先到者拿到 pending 占位行并执行；并发的那份得到 409 idempotency_in_progress。
func Test并发同键入口提交只建一张单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	type result struct {
		status int
		body   string
	}
	do := func() result {
		payload, err := json.Marshal(map[string]any{"title": "并发提交", "fields": bugFields(), "author_name": "赵六"})
		if err != nil {
			return result{status: -1, body: err.Error()}
		}
		req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/intakes/bug/tickets", bytes.NewReader(payload))
		if err != nil {
			return result{status: -1, body: err.Error()}
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", "concurrent-001")
		res, err := s.NewClient().Do(req)
		if err != nil {
			return result{status: -1, body: err.Error()}
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return result{status: res.StatusCode, body: string(b)}
	}

	const workers = 2
	results := make(chan result, workers)
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			start.Wait() // 尽量同时发出
			results <- do()
		}()
	}
	start.Done()
	wg.Wait()
	close(results)

	created, inProgress := 0, 0
	for res := range results {
		switch {
		case res.status == http.StatusCreated:
			created++
		case res.status == http.StatusConflict && strings.Contains(res.body, "idempotency_in_progress"):
			inProgress++
		default:
			t.Errorf("意外的响应：状态码 %d，响应体 %s", res.status, res.body)
		}
	}
	if created != 1 {
		t.Errorf("并发重放应恰好一份 201，实际 %d", created)
	}
	if inProgress != workers-1 {
		t.Errorf("并发重放其余各得一个 409 idempotency_in_progress，实际 %d", inProgress)
	}

	// 只建了一张工单（#2 不存在）。
	if res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/2", nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("并发重放不应建第二张工单（/tickets/2 = %d, 想 404）", res.StatusCode)
	}
}

// submitToIntakeHeaders 带自定义请求头匿名向入口提交（要求 201），返回响应结构。
func submitToIntakeHeaders(t testing.TB, s *tbtest.Server, slug string, headers map[string]string) intakeSubmitBody {
	t.Helper()
	res := s.DoWithHeaders(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/"+slug+"/tickets",
		headers, map[string]any{"title": "网络抖动重试", "fields": bugFields(), "author_name": "赵六"}, nil)
	if res.StatusCode != http.StatusCreated {
		body := readAll(t, res)
		t.Fatalf("入口提交状态码 = %d, 想 201：%s", res.StatusCode, body)
	}
	var out intakeSubmitBody
	tbtest.DecodeJSON(t, res, &out)
	return out
}
