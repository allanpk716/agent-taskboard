package server_test

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

const testClientVerifier = "test-verifier-with-enough-characters-for-pkce-1234567890"

func testClientChallenge() string {
	sum := sha256.Sum256([]byte(testClientVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// 成员在网页确认后，客户端凭 PKCE code 换到只能读通知的客户端凭证；
// 使用痕迹、列表和撤销都从公开 HTTP 接口可观察。
func Test客户端凭证完整签发使用与撤销流程(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "secret-pass")
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")

	connect := s.StartClientConnect(t, web, "WORKSTATION-01")
	if connect.State != "client-state" {
		t.Fatalf("回调 state = %q, 想 client-state", connect.State)
	}
	if u, err := url.Parse(connect.RedirectURL); err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "/cb" {
		t.Fatalf("回调地址不合法：%q (%v)", connect.RedirectURL, err)
	}

	issued := s.ExchangeClientCode(t, connect, testClientVerifier)
	if issued.Token == "" || issued.Hostname != "WORKSTATION-01" {
		t.Fatalf("换取响应不对：%+v", issued)
	}
	client := s.ClientCredentialClient(issued.Token)

	var session sessionBody
	res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, &session)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("客户端查 session 状态码 = %d", res.StatusCode)
	}
	if session.Viewer.Kind != "member" || session.Viewer.Member.Username != "wang" || session.Viewer.Agent != nil {
		t.Fatalf("客户端 session 身份不对：%+v", session.Viewer)
	}
	if session.Viewer.Client == nil || session.Viewer.Client.ID != issued.ID || session.Viewer.Client.Hostname != "WORKSTATION-01" {
		t.Fatalf("客户端 session 缺 client：%+v", session.Viewer.Client)
	}

	if res := s.Do(t, client, http.MethodGet, "/api/v1/notifications", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("客户端读通知状态码 = %d", res.StatusCode)
	}

	s.Clock().Advance(2 * time.Hour)
	res = s.DoWithHeaders(t, client, http.MethodGet, "/api/v1/notifications/unread",
		map[string]string{"User-Agent": "taskboard-client/1.4.9"}, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("客户端读未读数状态码 = %d", res.StatusCode)
	}

	var list clientListBody
	s.Do(t, web, http.MethodGet, "/api/v1/me/clients", nil, &list)
	if len(list.Clients) != 1 {
		t.Fatalf("已连接电脑数量 = %d, 想 1：%+v", len(list.Clients), list.Clients)
	}
	got := list.Clients[0]
	if got.ID != issued.ID || got.Hostname != "WORKSTATION-01" || got.ClientVersion != "1.4.9" {
		t.Fatalf("电脑列表不对：%+v", got)
	}
	if got.LastUsedAt == "" || got.CreatedAt == "" {
		t.Fatalf("电脑列表缺时间：%+v", got)
	}

	if res := s.Do(t, web, http.MethodDelete, "/api/v1/me/clients/"+itoa(issued.ID), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("网页撤销客户端状态码 = %d", res.StatusCode)
	}
	res = s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后客户端状态码 = %d, 想 401", res.StatusCode)
	}
}

type clientListBody struct {
	Clients []struct {
		ID            int64  `json:"id"`
		Hostname      string `json:"hostname"`
		CreatedAt     string `json:"created_at"`
		LastUsedAt    string `json:"last_used_at"`
		ClientVersion string `json:"client_version"`
	} `json:"clients"`
}

// code 是一次性的；过期和 verifier 不匹配都换不到凭证；非法端口不能生成回调。
func Test客户端PKCEcode一次性过期与verifier校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")

	connect := s.StartClientConnect(t, web, "LAB-PC")
	s.ExchangeClientCode(t, connect, testClientVerifier)
	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/client/token",
		map[string]string{"code": connect.Code, "code_verifier": testClientVerifier}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("code 重复使用状态码 = %d, 想 401", res.StatusCode)
	}

	connect = s.StartClientConnect(t, web, "LAB-PC")
	s.Clock().Advance(11 * time.Minute)
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/client/token",
		map[string]string{"code": connect.Code, "code_verifier": testClientVerifier}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期 code 状态码 = %d, 想 401", res.StatusCode)
	}

	connect = s.StartClientConnect(t, web, "LAB-PC")
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/client/token",
		map[string]string{"code": connect.Code, "code_verifier": "wrong-verifier-with-enough-length-1234567890"}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("verifier 不匹配状态码 = %d, 想 401", res.StatusCode)
	}

	for _, port := range []string{"0", "65536", "-1", "example.com:1234"} {
		res := s.Do(t, web, http.MethodPost, "/api/v1/client/connect", map[string]any{
			"port": port, "state": "s", "code_challenge": testClientChallenge(), "hostname": "LAB-PC",
		}, nil)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("非法 port %q 状态码 = %d, 想 422", port, res.StatusCode)
		}
	}
}

// 客户端凭证越过通知面时统一 403 client_scope；它自己可以撤销自己。
func Test客户端凭证权限边界与自撤销(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")
	issued := s.IssueClientCredential(t, web, "WORKSTATION-01")
	client := s.ClientCredentialClient(issued.Token)

	var errBody errorBody
	res := s.Do(t, client, http.MethodGet, "/api/v1/tickets", nil, &errBody)
	if res.StatusCode != http.StatusForbidden || errBody.Error.Code != "client_scope" {
		t.Fatalf("越权状态码/错误码 = %d/%q, 想 403/client_scope", res.StatusCode, errBody.Error.Code)
	}
	if errBody.Error.Hint != "请用浏览器打开工单处理；如需其它操作，登录网页后操作" {
		t.Fatalf("client_scope hint = %q", errBody.Error.Hint)
	}

	if res := s.Do(t, client, http.MethodDelete, "/api/v1/me/clients/self", nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("客户端自撤销状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("自撤销后状态码 = %d, 想 401", res.StatusCode)
	}
}

// 停用账号、管理员重置密码撤销全部客户端凭证；自助改密不撤销。
func Test账号事件对客户端凭证的撤销规则(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "secret-pass")
	s.CreateMember("wang", "member-pass")
	admin := s.Login("admin", "secret-pass")
	web := s.Login("wang", "member-pass")
	issued := s.IssueClientCredential(t, web, "WORKSTATION-01")
	client := s.ClientCredentialClient(issued.Token)
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatal("前置：客户端凭证应可用")
	}

	res := s.Do(t, web, http.MethodPost, "/api/v1/me/password",
		map[string]string{"old_password": "member-pass", "new_password": "new-member-pass"}, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("自助改密状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("自助改密后客户端状态码 = %d, 想 200", res.StatusCode)
	}

	var session sessionBody
	s.Do(t, web, http.MethodGet, "/api/v1/session", nil, &session)
	res = s.Do(t, admin, http.MethodPost,
		"/api/v1/members/"+itoa(session.Viewer.Member.ID)+"/reset-password", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("管理员重置密码状态码 = %d", res.StatusCode)
	}
	var reset struct {
		InitialPassword string `json:"initial_password"`
	}
	tbtest.DecodeJSON(t, res, &reset)
	if len(reset.InitialPassword) < 8 {
		t.Fatalf("重置密码太短：%q", reset.InitialPassword)
	}
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("管理员重置后客户端状态码 = %d, 想 401", res.StatusCode)
	}

	web = s.Login("wang", reset.InitialPassword)
	issued = s.IssueClientCredential(t, web, "WORKSTATION-02")
	client = s.ClientCredentialClient(issued.Token)
	s.Do(t, admin, http.MethodPost,
		"/api/v1/members/"+itoa(session.Viewer.Member.ID)+"/deactivate", nil, nil)
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("停用后客户端状态码 = %d, 想 401", res.StatusCode)
	}
}
