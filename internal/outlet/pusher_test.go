package outlet_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/outlet"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

func Test门铃按未读四桶计数推送并推进持久游标(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)

	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 30, 0, 0, clock.Beijing))
	cipher, err := outlet.NewCipher([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	manager := outlet.NewManager(s.DB, clk, cipher)
	if _, err := manager.Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)
	insertOutletNotification(t, s.DB, 1, "fyi", "collaborator_added", nil)
	insertOutletNotification(t, s.DB, 1, "fyi", "closed", nil)
	insertOutletNotification(t, s.DB, 1, "fyi", "backup_failing", nil)
	insertOutletNotification(t, s.DB, 1, "draft", "draft_pending", nil)
	insertOutletNotification(t, s.DB, 1, "fyi", "reopened", clk.Now())

	var (
		mu       sync.Mutex
		requests []capturedOutletRequest
		release  = make(chan struct{})
	)
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("解析门铃 payload 失败：%v", err)
		}
		mu.Lock()
		requests = append(requests, capturedOutletRequest{
			Path:        r.URL.Path,
			WebhookKey:  r.Header.Get("X-Webhook-Key"),
			Idempotency: r.Header.Get("X-Idempotency-Key"),
			Payload:     payload,
			ContentType: r.Header.Get("Content-Type"),
		})
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		<-release
	}))
	defer target.Close()
	close(release)

	pushClient := outlet.NewClientWithTimeout(time.Second)
	pushClient.HTTP = target.Client()
	pushClient.HTTP.Timeout = time.Second
	pushClient.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	logs := newOutletTestLogs()
	pusher, err := outlet.NewPusher(outlet.PusherDeps{
		DB:     s.DB,
		Clock:  clk,
		Cipher: cipher,
		Webhook: config.Webhook{
			Enabled:      true,
			AllowedBases: []string{target.URL},
			WindowStart:  "08:00",
			WindowEnd:    "20:00",
		},
		Client: pushClient,
		Logger: log.New(logs, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		count := len(requests)
		mu.Unlock()
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("门铃请求数 = %d，想 1", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
	waitForOutletCondition(t, func() bool {
		var cursor int64
		if err := s.DB.QueryRow(`SELECT last_pushed_id FROM outlet WHERE member_id=1`).Scan(&cursor); err != nil {
			return false
		}
		return cursor == 6
	})

	mu.Lock()
	request := requests[0]
	mu.Unlock()
	if request.Path != "/api/v1/bots/bot01/webhook" || request.WebhookKey != "webhook-secret" ||
		request.Idempotency != "taskboard:1:g1:6" || request.ContentType != "application/json" {
		t.Fatalf("请求地址/鉴权/幂等键不符：%+v", request)
	}
	want := map[string]any{
		"event":     "taskboard.notify",
		"from_id":   float64(0),
		"latest_id": float64(6),
		"turn":      float64(1),
		"collab":    float64(1),
		"fyi":       float64(1),
		"system":    float64(1),
	}
	if len(request.Payload) != len(want) {
		t.Fatalf("payload 字段 = %v，想零业务内容四桶信号 %v", request.Payload, want)
	}
	for key, value := range want {
		got, ok := request.Payload[key]
		if !ok || got != value {
			t.Fatalf("payload[%s] = %v(%T)，想 %v", key, got, got, value)
		}
	}
}

func Test窗外只记不推且重启后按持久游标补推(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 7, 59, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)

	var mu sync.Mutex
	requests := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	logs := newOutletTestLogs()
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", logs)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool { return strings.Contains(logs.String(), "result=skipped_window") })
	mu.Lock()
	outsideRequests := requests
	mu.Unlock()
	if outsideRequests != 0 {
		t.Fatalf("窗外不应 POST，实际 %d 次", outsideRequests)
	}
	if cursor := queryOutletCursor(t, s.DB, 1); cursor != 0 {
		t.Fatalf("窗外游标 = %d，想保持 0 等窗口开口", cursor)
	}
	if out := logs.String(); !strings.Contains(out, "result=skipped_window") || strings.Contains(out, "webhook-secret") {
		t.Fatalf("窗外日志应有 result 枚举且不含 key：%q", out)
	}
	if !strings.Contains(logs.String(), "at=2026-11-20T07:59:00+08:00") {
		t.Fatalf("日志必须带时间戳：%q", logs.String())
	}
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if got := strings.Count(logs.String(), "result=skipped_window"); got != 1 {
		t.Fatalf("重复窗外状态应去重，日志 %d 条：%q", got, logs.String())
	}

	// 新推送器代表进程重启；基线只能来自 DB，不能在启动时重建。
	clk.Set(time.Date(2026, 11, 20, 8, 0, 0, 0, clock.Beijing))
	restarted := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", newOutletTestLogs())
	if err := restarted.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return requests == 1
	})
	waitForOutletCondition(t, func() bool { return queryOutletCursor(t, s.DB, 1) == 1 })
}

func Test全零已读不POST只推进游标(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", clk.Now().Format(time.RFC3339Nano))
	insertOutletNotification(t, s.DB, 1, "draft", "draft_pending", nil)

	var mu sync.Mutex
	requests := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
	}))
	t.Cleanup(target.Close)
	logs := newOutletTestLogs()
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", logs)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return queryOutletCursor(t, s.DB, 1) == 1 && requests == 0
	})
	mu.Lock()
	defer mu.Unlock()
	if requests != 0 {
		t.Fatalf("已读通知不应 POST，实际 %d 次", requests)
	}
	if out := logs.String(); !strings.Contains(out, "result=skipped_all_read") {
		t.Fatalf("全零应记录 skipped_all_read：%q", out)
	}
}

func Test系统通知豁免推送窗口(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	clk := clock.NewFake(time.Date(2026, 11, 20, 7, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "fyi", "backup_failing", nil)

	var mu sync.Mutex
	idempotency := ""
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		idempotency = r.Header.Get("X-Idempotency-Key")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", newOutletTestLogs())
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool { return queryOutletCursor(t, s.DB, 1) == 1 })
	mu.Lock()
	defer mu.Unlock()
	if idempotency != "taskboard:1:g1:1" {
		t.Fatalf("系统通知幂等键 = %q，想 taskboard:1:g1:1", idempotency)
	}
}

func Test同一成员两响最小间隔六十秒(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)

	var mu sync.Mutex
	requests := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	logs := newOutletTestLogs()
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", logs)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool { return queryOutletCursor(t, s.DB, 1) == 1 })

	insertOutletNotification(t, s.DB, 1, "turn", "returned", nil)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool { return strings.Contains(logs.String(), "result=skipped_throttled") })
	clk.Advance(30 * time.Second)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if requests != 1 {
		mu.Unlock()
		t.Fatalf("30 秒内不应有第二响，请求 = %d", requests)
	}
	mu.Unlock()
	clk.Advance(31 * time.Second)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return requests == 2
	})
	waitForOutletCondition(t, func() bool { return queryOutletCursor(t, s.DB, 1) == 2 })
}

func Test暂停出口只记录paused不POST(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)
	if _, err := s.DB.Exec(`UPDATE outlet SET status='paused',pause_reason='member' WHERE member_id=1`); err != nil {
		t.Fatal(err)
	}

	var requests int
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
	}))
	t.Cleanup(target.Close)
	logs := newOutletTestLogs()
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", logs)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool { return strings.Contains(logs.String(), "result=paused") })
	if requests != 0 || queryOutletCursor(t, s.DB, 1) != 0 {
		t.Fatalf("暂停出口不应 POST 或推进：requests=%d cursor=%d", requests, queryOutletCursor(t, s.DB, 1))
	}
}

func Test服务端五XX失败用同键退避重试三次(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)

	var mu sync.Mutex
	var keys []string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(target.Close)
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", newOutletTestLogs())

	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		state, attempts := queryOutletRetryState(t, s.DB, 1)
		mu.Lock()
		count := len(keys)
		mu.Unlock()
		return state == "retryable" && attempts == 1 && count == 1
	})
	for i, delay := range []time.Duration{60 * time.Second, 120 * time.Second, 240 * time.Second} {
		clk.Advance(delay)
		if err := pusher.Dispatch(ctx); err != nil {
			t.Fatal(err)
		}
		wantAttempts := i + 2
		waitForOutletCondition(t, func() bool {
			state, attempts := queryOutletRetryState(t, s.DB, 1)
			mu.Lock()
			count := len(keys)
			mu.Unlock()
			return state == "retryable" && attempts == int64(wantAttempts) && count == wantAttempts
		})
	}
	mu.Lock()
	defer mu.Unlock()
	if len(keys) != 4 {
		t.Fatalf("总尝试次数 = %d，想初次加 3 次重试", len(keys))
	}
	for _, key := range keys {
		if key != "taskboard:1:g1:1" {
			t.Fatalf("重试必须复用同键，得到 %v", keys)
		}
	}
	if cursor := queryOutletCursor(t, s.DB, 1); cursor != 0 {
		t.Fatalf("持续失败不能推进游标，实际 %d", cursor)
	}
}

func Test游标写入失败后下轮同键重推(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)
	if _, err := s.DB.Exec(`CREATE TRIGGER block_outlet_success BEFORE UPDATE ON outlet
		WHEN NEW.retry_state='none' AND NEW.last_pushed_id<>OLD.last_pushed_id
		BEGIN SELECT RAISE(ABORT, 'block cursor'); END`); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var keys []string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", newOutletTestLogs())
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(keys) == 1
	})
	if cursor := queryOutletCursor(t, s.DB, 1); cursor != 0 {
		t.Fatalf("游标写入失败时不能推进，实际 %d", cursor)
	}

	if _, err := s.DB.Exec(`DROP TRIGGER block_outlet_success`); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Minute)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(keys) == 2 && queryOutletCursor(t, s.DB, 1) == 1
	})
	mu.Lock()
	defer mu.Unlock()
	if keys[0] != "taskboard:1:g1:1" || keys[1] != keys[0] {
		t.Fatalf("游标失败后的两次请求应同键：%v", keys)
	}
}

func Test发送途中新增通知不被游标跳过(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)

	var mu sync.Mutex
	var keys []string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		mu.Unlock()
		if len(keys) == 1 {
			insertOutletNotification(t, s.DB, 1, "turn", "returned", nil)
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", newOutletTestLogs())
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(keys) == 1 && queryOutletCursor(t, s.DB, 1) == 1
	})

	clk.Advance(time.Minute)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(keys) == 2 && queryOutletCursor(t, s.DB, 1) == 2
	})
	mu.Lock()
	defer mu.Unlock()
	if keys[0] != "taskboard:1:g1:1" || keys[1] != "taskboard:1:g1:2" {
		t.Fatalf("发送途中新增的通知下一响应换 latest：%v", keys)
	}
}

func Test重建基线递增代数后不复用旧幂等键(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	manager := outlet.NewManager(s.DB, clk, cipher)
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)
	if _, err := manager.Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "returned", nil)

	var mu sync.Mutex
	var keys []string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", newOutletTestLogs())
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(keys) == 1 && queryOutletCursor(t, s.DB, 1) == 2
	})

	if _, err := manager.Save(ctx, 1, "bot02", "new-webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "delivered", nil)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(keys) == 2 && queryOutletCursor(t, s.DB, 1) == 3
	})
	mu.Lock()
	defer mu.Unlock()
	if keys[0] != "taskboard:1:g1:2" || keys[1] != "taskboard:1:g2:3" {
		t.Fatalf("重建基线后的幂等键 = %v，想 generation +1", keys)
	}
}

func Test四XX确定性失败不重试(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)

	var mu sync.Mutex
	var keys []string
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("X-Idempotency-Key"))
		mu.Unlock()
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(target.Close)
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", newOutletTestLogs())
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		state, attempts := queryOutletRetryState(t, s.DB, 1)
		mu.Lock()
		defer mu.Unlock()
		return state == "permanent" && attempts == 1 && len(keys) == 1
	})

	clk.Advance(5 * time.Minute)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	mu.Lock()
	fourXXAttempts := len(keys)
	mu.Unlock()
	if fourXXAttempts != 1 {
		t.Fatalf("4xx 不应重试，请求幂等键 = %v", keys)
	}

	insertOutletNotification(t, s.DB, 1, "turn", "returned", nil)
	clk.Advance(time.Minute)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(keys) == 2
	})
	mu.Lock()
	defer mu.Unlock()
	if keys[1] != "taskboard:1:g1:2" {
		t.Fatalf("新 latest 是新幂等键，不该复用 4xx 旧键：%v", keys)
	}
}

func Test空响ignored单独记录且日志不含响应内容(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	if _, err := outlet.NewManager(s.DB, clk, cipher).Save(ctx, 1, "bot01", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)

	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ignored":true,"body":"response-secret"}`))
	}))
	t.Cleanup(target.Close)
	logs := newOutletTestLogs()
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", logs)
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	waitForOutletCondition(t, func() bool { return strings.Contains(logs.String(), "result=ignored") })
	if out := logs.String(); strings.Contains(out, "response-secret") || strings.Contains(out, "webhook-secret") {
		t.Fatalf("ignored 日志不得包含响应 body 或 key：%q", out)
	}
}

func Test慢出口只占用本成员有界worker(t *testing.T) {
	s := openOutletTestStore(t)
	ctx := context.Background()
	insertOutletMember(t, s.DB, 1, "wang")
	insertOutletMember(t, s.DB, 2, "li")
	insertOutletTicket(t, s.DB)
	clk := clock.NewFake(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing))
	cipher := newOutletTestCipher(t)
	manager := outlet.NewManager(s.DB, clk, cipher)
	if _, err := manager.Save(ctx, 1, "slowbot", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Save(ctx, 2, "fastbot", "webhook-secret"); err != nil {
		t.Fatal(err)
	}
	insertOutletNotification(t, s.DB, 1, "turn", "assigned", nil)
	insertOutletNotification(t, s.DB, 2, "turn", "assigned", nil)

	slowStarted := make(chan struct{})
	releaseSlow := make(chan struct{})
	var mu sync.Mutex
	fastPaths := []string{}
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/slowbot/webhook") {
			close(slowStarted)
			<-releaseSlow
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		fastPaths = append(fastPaths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(target.Close)
	pusher := newOutletTestPusher(t, s, clk, cipher, target, "08:00", "20:00", newOutletTestLogs())
	if err := pusher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	<-slowStarted
	waitForOutletCondition(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(fastPaths) == 1
	})
	close(releaseSlow)
	waitForOutletCondition(t, func() bool { return queryOutletCursor(t, s.DB, 1) == 1 })
}

type capturedOutletRequest struct {
	Path        string
	WebhookKey  string
	Idempotency string
	Payload     map[string]any
	ContentType string
}

func newOutletTestCipher(t *testing.T) *outlet.Cipher {
	t.Helper()
	cipher, err := outlet.NewCipher([]byte(strings.Repeat("k", 32)))
	if err != nil {
		t.Fatal(err)
	}
	return cipher
}

type outletTestLogs struct {
	mu sync.Mutex
	b  strings.Builder
}

func newOutletTestLogs() *outletTestLogs { return &outletTestLogs{} }

func (l *outletTestLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *outletTestLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func newOutletTestPusher(t *testing.T, s *store.Store, clk clock.Clock, cipher *outlet.Cipher, target *httptest.Server, start, end string, logs *outletTestLogs) *outlet.Pusher {
	t.Helper()
	pushClient := outlet.NewClientWithTimeout(time.Second)
	pushClient.HTTP = target.Client()
	pushClient.HTTP.Timeout = time.Second
	pushClient.HTTP.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	pusher, err := outlet.NewPusher(outlet.PusherDeps{
		DB: s.DB, Clock: clk, Cipher: cipher,
		Webhook: config.Webhook{
			Enabled: true, AllowedBases: []string{target.URL},
			WindowStart: start, WindowEnd: end,
		},
		Client: pushClient, Logger: log.New(logs, "", 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	return pusher
}

func queryOutletCursor(t *testing.T, db *sql.DB, memberID int64) int64 {
	t.Helper()
	var cursor int64
	if err := db.QueryRow(`SELECT last_pushed_id FROM outlet WHERE member_id=?`, memberID).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	return cursor
}

func queryOutletRetryState(t *testing.T, db *sql.DB, memberID int64) (string, int64) {
	t.Helper()
	var state string
	var attempts int64
	if err := db.QueryRow(`SELECT retry_state,attempt_count FROM outlet WHERE member_id=?`, memberID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	return state, attempts
}

func insertOutletTicket(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO tickets
		(seq, number, kind, title, status, brief_md, brief_revision, creator_member_id, assignee_id, created_at, last_change)
		VALUES (1, 'T-0001', 'task', '内部工单标题', 'open', '', 1, 1, 1,
		'2026-11-20T09:00:00+08:00', 1)`); err != nil {
		t.Fatal(err)
	}
}

func insertOutletNotification(t *testing.T, db *sql.DB, memberID int64, kind, reason string, readAt any) {
	t.Helper()
	if reason == "backup_failing" || reason == "backup_recovered" || reason == "intake_paused" {
		if _, err := db.Exec(`INSERT INTO notifications
			(member_id, kind, reason, text, url, created_at, read_at)
			VALUES (?, ?, ?, '通知内容', '', '2026-11-20T09:10:00+08:00', ?)`,
			memberID, kind, reason, readAt); err != nil {
			t.Fatal(err)
		}
		return
	}
	if _, err := db.Exec(`INSERT INTO notifications
		(member_id, ticket_seq, ticket_number, ticket_title, kind, reason, text, url,
		 trigger_seq, trigger_type, actor_kind, actor_name, created_at, read_at)
		VALUES (?, 1, 'T-0001', '内部工单标题', ?, ?, '通知内容', '',
		 1, ?, 'member', '王工', '2026-11-20T09:10:00+08:00', ?)`,
		memberID, kind, reason, reason, readAt); err != nil {
		t.Fatal(err)
	}
}

func waitForOutletCondition(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("等待门铃结果超时")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
