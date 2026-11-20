package server_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
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
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type outletBody struct {
	Enabled bool `json:"enabled"`
	Outlet  *struct {
		BotID          string  `json:"bot_id"`
		KeyLast4       string  `json:"key_last4"`
		LastPushedID   int64   `json:"last_pushed_id"`
		Generation     int64   `json:"generation"`
		Status         string  `json:"status"`
		FailureCount   int64   `json:"failure_count"`
		PauseReason    *string `json:"pause_reason"`
		DeliveryState  string  `json:"delivery_state"`
		TransientSince *string `json:"transient_since"`
	} `json:"outlet"`
}

func Test网页会话自助维护通知出口且key只回末四位(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	s := startOutletServer(t, target, outlet.NewClient())
	s.CreateAdmin("admin", "secret-pass")
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")

	oldLog := log.Writer()
	logs := &strings.Builder{}
	log.SetOutput(logs)
	defer log.SetOutput(oldLog)

	var saved outletBody
	res := s.Do(t, web, http.MethodPut, "/api/v1/me/notification-outlet",
		map[string]string{"bot_id": "bot01", "key": "old-webhook-key-1234"}, &saved)
	if res.StatusCode != http.StatusOK || saved.Outlet == nil || saved.Outlet.BotID != "bot01" ||
		saved.Outlet.KeyLast4 != "1234" || saved.Outlet.Status != "active" || saved.Outlet.Generation != 1 {
		t.Fatalf("保存出口 = %d/%+v，想 200 且只回 key 末四位", res.StatusCode, saved)
	}

	var listed outletBody
	res = s.Do(t, web, http.MethodGet, "/api/v1/me/notification-outlet", nil, nil)
	raw, _ := io.ReadAll(res.Body)
	if err := json.Unmarshal(raw, &listed); err != nil {
		t.Fatalf("GET 响应不是合法 JSON：%s", raw)
	}
	if res.StatusCode != http.StatusOK || listed.Outlet == nil || listed.Outlet.BotID != "bot01" ||
		listed.Outlet.KeyLast4 != "1234" {
		t.Fatalf("读取出口 = %d/%+v，想 200 且只回 key 末四位", res.StatusCode, listed)
	}
	if strings.Contains(string(raw), "old-webhook-key-1234") || strings.Contains(logs.String(), "old-webhook-key-1234") {
		t.Fatalf("GET 响应或日志泄露 webhook key：response=%q logs=%q", raw, logs.String())
	}

	// 失败分级与自动暂停原因要回显给「我的」页标红，但仍不携带 key。
	if _, err := s.Store().DB.Exec(`
		UPDATE outlet SET status='paused',failure_count=2,pause_reason='outlet_paused',
			delivery_state='permanent',delivery_state_at='2026-11-20T09:00:01+08:00',
			transient_since=NULL WHERE bot_id='bot01'`); err != nil {
		t.Fatal(err)
	}
	var failed outletBody
	res = s.Do(t, web, http.MethodGet, "/api/v1/me/notification-outlet", nil, &failed)
	raw, _ = io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || failed.Outlet == nil || failed.Outlet.Status != "paused" ||
		failed.Outlet.FailureCount != 2 || failed.Outlet.PauseReason == nil ||
		*failed.Outlet.PauseReason != "outlet_paused" || failed.Outlet.DeliveryState != "permanent" {
		t.Fatalf("失败状态回显 = %d/%s，想暂停原因与确定性失败分级", res.StatusCode, raw)
	}
	if strings.Contains(string(raw), "old-webhook-key-1234") {
		t.Fatalf("失败状态回显泄露 webhook key：%s", raw)
	}

	// 改 bot_id 视同换配置，必须重新粘贴 key；旧 key 被新密文覆盖。
	res = s.Do(t, web, http.MethodPut, "/api/v1/me/notification-outlet",
		map[string]string{"bot_id": "bot02", "key": "new-webhook-key-5678"}, &saved)
	if res.StatusCode != http.StatusOK || saved.Outlet == nil || saved.Outlet.BotID != "bot02" ||
		saved.Outlet.KeyLast4 != "5678" || saved.Outlet.Generation != 2 {
		t.Fatalf("更换 bot = %d/%+v，想 200、新 key 末四位并递增代数", res.StatusCode, saved)
	}

	var paused outletBody
	res = s.Do(t, web, http.MethodPost, "/api/v1/me/notification-outlet/pause", nil, &paused)
	if res.StatusCode != http.StatusOK || paused.Outlet == nil || paused.Outlet.Status != "paused" ||
		paused.Outlet.LastPushedID != saved.Outlet.LastPushedID {
		t.Fatalf("暂停 = %d/%+v，想游标不动", res.StatusCode, paused)
	}
	var resumed outletBody
	res = s.Do(t, web, http.MethodPost, "/api/v1/me/notification-outlet/resume", nil, &resumed)
	if res.StatusCode != http.StatusOK || resumed.Outlet == nil || resumed.Outlet.Status != "active" ||
		resumed.Outlet.LastPushedID != saved.Outlet.LastPushedID {
		t.Fatalf("恢复 = %d/%+v，想游标仍不动", res.StatusCode, resumed)
	}

	if res := s.Do(t, web, http.MethodDelete, "/api/v1/me/notification-outlet", nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("删除状态码 = %d，想 204", res.StatusCode)
	}
	var empty outletBody
	res = s.Do(t, web, http.MethodGet, "/api/v1/me/notification-outlet", nil, &empty)
	if res.StatusCode != http.StatusOK || !empty.Enabled || empty.Outlet != nil {
		t.Fatalf("删除后读取 = %d/%+v，想 enabled=true 且 outlet=null", res.StatusCode, empty)
	}
}

func Test通知出口保存校验botID和必填key(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	s := startOutletServer(t, target, outlet.NewClient())
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")

	cases := []struct {
		name string
		body map[string]string
		code string
	}{
		{name: "botID大小写非法", body: map[string]string{"bot_id": "Bot01", "key": "key"}, code: "invalid_outlet_bot_id"},
		{name: "botID路径非法", body: map[string]string{"bot_id": "../bot", "key": "key"}, code: "invalid_outlet_bot_id"},
		{name: "key缺失", body: map[string]string{"bot_id": "bot01", "key": ""}, code: "outlet_webhook_key_required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var errBody errorBody
			res := s.Do(t, web, http.MethodPut, "/api/v1/me/notification-outlet", tc.body, &errBody)
			if res.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != tc.code {
				t.Fatalf("状态码/错误码 = %d/%q，想 422/%q", res.StatusCode, errBody.Error.Code, tc.code)
			}
		})
	}
}

func Test通知出口API只接受网页会话(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	s := startOutletServer(t, target, outlet.NewClient())
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")
	_, agentToken := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(agentToken)
	issued := s.IssueClientCredential(t, web, "WORKSTATION-01")
	client := s.ClientCredentialClient(issued.Token)

	requests := []struct {
		name   string
		method string
		path   string
		body   map[string]string
	}{
		{name: "读取", method: http.MethodGet, path: "/api/v1/me/notification-outlet"},
		{name: "保存", method: http.MethodPut, path: "/api/v1/me/notification-outlet",
			body: map[string]string{"bot_id": "bot01", "key": "secret"}},
		{name: "暂停", method: http.MethodPost, path: "/api/v1/me/notification-outlet/pause"},
		{name: "测试", method: http.MethodPost, path: "/api/v1/me/notification-outlet/test"},
	}
	for name, c := range map[string]*http.Client{"成员Agent": agent, "客户端凭证": client} {
		for _, req := range requests {
			var errBody errorBody
			res := s.Do(t, c, req.method, req.path, req.body, &errBody)
			wantCode := "web_session_required"
			if name == "客户端凭证" {
				wantCode = "client_scope"
			}
			if res.StatusCode != http.StatusForbidden || errBody.Error.Code != wantCode {
				t.Fatalf("%s %s = %d/%q，想 403/%s", name, req.name, res.StatusCode, errBody.Error.Code, wantCode)
			}
		}
	}
}

func Test通知出口测试按钮受限发送唯一幂等键并限频(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		delay      time.Duration
		wantResult string
	}{
		{name: "成功", status: http.StatusOK, body: `{"ok":true}`, wantResult: "ok"},
		{name: "空响", status: http.StatusOK, body: `{"ignored":true}`, wantResult: "ignored"},
		{name: "未授权", status: http.StatusUnauthorized, body: `{"message":"bad key"}`, wantResult: "unauthorized"},
		{name: "超时", delay: 120 * time.Millisecond, wantResult: "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []http.Request
			var bodies []string
			target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				buf, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, *r.Clone(r.Context()))
				bodies = append(bodies, string(buf))
				mu.Unlock()
				if tc.delay != 0 {
					time.Sleep(tc.delay)
				}
				if tc.status == 0 {
					tc.status = http.StatusOK
				}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer target.Close()
			s := startOutletServer(t, target, outletTestClient(target, 30*time.Millisecond))
			s.CreateMember("wang", "member-pass")
			web := s.Login("wang", "member-pass")
			s.Do(t, web, http.MethodPut, "/api/v1/me/notification-outlet",
				map[string]string{"bot_id": "bot01", "key": "webhook-secret-1234"}, nil)

			var first struct {
				Result     string `json:"result"`
				HTTPStatus int    `json:"http_status"`
			}
			res := s.Do(t, web, http.MethodPost, "/api/v1/me/notification-outlet/test", nil, &first)
			if res.StatusCode != http.StatusOK || first.Result != tc.wantResult {
				t.Fatalf("测试结果 = %d/%+v，想 200/%s", res.StatusCode, first, tc.wantResult)
			}

			var limited errorBody
			res = s.Do(t, web, http.MethodPost, "/api/v1/me/notification-outlet/test", nil, &limited)
			if res.StatusCode != http.StatusTooManyRequests || limited.Error.Code != "outlet_test_rate_limited" {
				t.Fatalf("限频 = %d/%q，想 429/outlet_test_rate_limited", res.StatusCode, limited.Error.Code)
			}
			s.Clock().Set(s.Clock().Now().Add(61 * time.Second))
			res = s.Do(t, web, http.MethodPost, "/api/v1/me/notification-outlet/test", nil, &first)
			if res.StatusCode != http.StatusOK || first.Result != tc.wantResult {
				t.Fatalf("限频恢复后 = %d/%+v，想 200/%s", res.StatusCode, first, tc.wantResult)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 || bodies[0] != `{"event":"taskboard.test"}` || bodies[1] != bodies[0] {
				t.Fatalf("目标平台请求数/body = %d/%q，想两次且 payload 固定", len(requests), bodies)
			}
			keys := make([]string, 2)
			for i, req := range requests {
				if req.Method != http.MethodPost || req.URL.Path != "/api/v1/bots/bot01/webhook" ||
					req.Header.Get("X-Webhook-Key") != "webhook-secret-1234" {
					t.Fatalf("第 %d 次请求不是受限出口请求：%+v", i+1, req)
				}
				keys[i] = req.Header.Get("X-Idempotency-Key")
				if !strings.HasPrefix(keys[i], "taskboard-test:1:") ||
					!isDigits(keys[i][len("taskboard-test:1:"):]) || keys[i] == keys[0] && i == 1 {
					t.Fatalf("第 %d 次幂等键 = %q，想唯一且符合成员/epoch 格式", i+1, keys[i])
				}
			}
		})
	}
}

func startOutletServer(t *testing.T, target *httptest.Server, client *outlet.Client) *tbtest.Server {
	t.Helper()
	t.Setenv("TASKBOARD_OUTLET_KEY", base64.StdEncoding.EncodeToString([]byte(strings.Repeat("o", 32))))
	return tbtest.Start(t,
		tbtest.WithWebhook(config.Webhook{
			Enabled: true, AllowedBases: []string{target.URL},
			WindowStart: "08:00", WindowEnd: "20:00",
		}),
		tbtest.WithOutletClient(client),
		tbtest.WithClockStart(time.Date(2026, 11, 20, 9, 0, 0, 0, clock.Beijing)),
	)
}

func outletTestClient(target *httptest.Server, timeout time.Duration) *outlet.Client {
	pool := x509.NewCertPool()
	pool.AddCert(target.Certificate())
	client := outlet.NewClientWithTimeout(timeout)
	client.HTTP.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
	}
	return client
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
