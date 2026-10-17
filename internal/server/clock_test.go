package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 时钟可注入：把假时钟拨过会话有效期后，会话失效；
// 差一秒没过期时会话仍然有效。
func Test时钟注入会话过期(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "secret-pass")
	client := s.Login("admin", "secret-pass")

	// 差一秒到期：仍可访问。
	s.Clock().Advance(identity.SessionTTL - time.Second)
	res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("到期前一秒状态码 = %d, 想 200", res.StatusCode)
	}

	// 拨过头：401。
	s.Clock().Advance(2 * time.Second)
	res = s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期后状态码 = %d, 想 401", res.StatusCode)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "unauthenticated" {
		t.Errorf("错误码 = %q, 想 unauthenticated", got.Error.Code)
	}
}

// 每个测试独占一个 data_dir：两个服务的数据库互不影响（同名管理员各自创建、各自密码生效）。
func Test每个测试独占数据目录(t *testing.T) {
	s1 := tbtest.Start(t)
	s1.CreateAdmin("admin", "pass-one")

	s2 := tbtest.Start(t, tbtest.WithClockStart(time.Date(2026, 11, 1, 8, 0, 0, 0, clock.Beijing)))
	s2.CreateAdmin("admin", "pass-two")

	// 各自的密码在自己实例上有效，在对方实例上无效。
	for _, s := range []*tbtest.Server{s1, s2} {
		want := "pass-one"
		if s == s2 {
			want = "pass-two"
		}
		res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session",
			map[string]string{"username": "admin", "password": want}, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("用 %s 登录状态码 = %d, 想 200", want, res.StatusCode)
		}
		bad := "pass-one"
		if s == s2 {
			bad = "pass-two"
		}
		res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session",
			map[string]string{"username": "admin", "password": bad + "-x"}, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("错误密码登录状态码 = %d, 想 401", res.StatusCode)
		}
	}
}
