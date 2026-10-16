package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 账号或密码错误时返回统一错误格式。
func Test账号或密码错误返回统一错误格式(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "secret-pass")

	for name, body := range map[string]map[string]string{
		"密码错误": {"username": "admin", "password": "wrong-pass"},
		"用户名不存在": {"username": "nobody", "password": "secret-pass"},
	} {
		t.Run(name, func(t *testing.T) {
			res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session", body, nil)
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("状态码 = %d, 想 %d", res.StatusCode, http.StatusUnauthorized)
			}
			var got struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "invalid_credentials" {
				t.Errorf("错误码 = %q, 想 %q", got.Error.Code, "invalid_credentials")
			}
			if got.Error.Message == "" {
				t.Errorf("message 为空")
			}
		})
	}
}

// 请求体不是合法 JSON 时返回 400 invalid_json。
func Test登录请求体不是JSON时返回400(t *testing.T) {
	s := tbtest.Start(t)

	req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/session", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := s.NewClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("状态码 = %d, 想 %d", res.StatusCode, http.StatusBadRequest)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "invalid_json" {
		t.Errorf("错误码 = %q, 想 %q", got.Error.Code, "invalid_json")
	}
}

// 管理员创建后可以在网页上登录：拿到会话 cookie、查到本人身份，登出后失效。
func Test创建管理员后可以登录查身份并登出(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "secret-pass")

	// 数据库是 WAL 模式：写入后 data_dir 里能看到 -wal 文件。
	if _, err := os.Stat(filepath.Join(s.DataDir(), "taskboard.db-wal")); err != nil {
		t.Errorf("没看到 taskboard.db-wal（WAL 未生效？）：%v", err)
	}

	// 登录：换到会话 cookie。
	loginRes := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session",
		map[string]string{"username": "admin", "password": "secret-pass"}, nil)
	if loginRes.StatusCode != http.StatusOK {
		t.Fatalf("登录状态码 = %d, 想 200", loginRes.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range loginRes.Cookies() {
		if c.Name == "tb_session" {
			cookie = c
		}
	}
	if cookie == nil || cookie.Value == "" {
		t.Fatal("登录成功但没有下发 tb_session cookie")
	}
	if !cookie.HttpOnly {
		t.Error("会话 cookie 缺 HttpOnly")
	}
	if !cookie.Secure {
		t.Error("会话 cookie 缺 Secure")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("会话 cookie SameSite = %v, 想 Lax", cookie.SameSite)
	}
	if cookie.Path != "/" {
		t.Errorf("会话 cookie Path = %q, 想 \"/\"", cookie.Path)
	}

	// 带会话查本人身份。
	client := s.Login("admin", "secret-pass")
	var me struct {
		Kind   string `json:"kind"`
		Member struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Name     string `json:"name"`
			IsAdmin  bool   `json:"is_admin"`
		} `json:"member"`
	}
	res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, &me)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("查身份状态码 = %d, 想 200", res.StatusCode)
	}
	if me.Kind != "member" {
		t.Errorf("kind = %q, 想 member", me.Kind)
	}
	if me.Member.Username != "admin" || me.Member.Name != "admin" || !me.Member.IsAdmin {
		t.Errorf("成员信息不对：%+v", me.Member)
	}

	// 登出后再查身份 → 401。
	logoutRes := s.Do(t, client, http.MethodDelete, "/api/v1/session", nil, nil)
	if logoutRes.StatusCode != http.StatusNoContent {
		t.Fatalf("登出状态码 = %d, 想 204", logoutRes.StatusCode)
	}
	res = s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("登出后查身份状态码 = %d, 想 401", res.StatusCode)
	}
}
