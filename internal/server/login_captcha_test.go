package server_test

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/captcha"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type fixedCaptcha struct{}

func (fixedCaptcha) Generate() (string, []byte, error) {
	pngBytes, err := captcha.Render("AB23")
	return "AB23", pngBytes, err
}

func Test登录验证码必需且一次性(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithCaptchaSource(fixedCaptcha{}, "AB23"))
	s.CreateAdmin("admin", "secret-pass")

	var issued struct {
		ID    string `json:"id"`
		Image string `json:"image"`
	}
	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/login-captcha", nil, &issued)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("获取验证码状态码 = %d, 想 200", res.StatusCode)
	}
	if issued.ID == "" {
		t.Fatal("验证码响应缺 id")
	}
	img, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(issued.Image, "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("验证码 image 不是 base64：%v", err)
	}
	if len(img) < 8 || !bytes.Equal(img[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}) ||
		!strings.HasPrefix(issued.Image, "data:image/png;base64,") {
		t.Fatalf("验证码图片响应不合法：%q %q", issued.Image, res.Header.Get("Content-Type"))
	}

	cases := []struct {
		name string
		body map[string]string
		code string
	}{
		{"缺少验证码", map[string]string{"username": "admin", "password": "secret-pass"}, "captcha_required"},
		{"答案错误", map[string]string{"username": "admin", "password": "secret-pass", "captcha_id": issued.ID, "captcha_answer": "wrong"}, "captcha_invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session", tc.body, nil)
			if res.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s 状态码 = %d, 想 401", tc.name, res.StatusCode)
			}
			var got struct {
				Error struct {
					Code string `json:"code"`
				}
			}
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != tc.code {
				t.Fatalf("%s 错误码 = %q, 想 %q", tc.name, got.Error.Code, tc.code)
			}
		})
	}

	issued.Image = ""
	s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/login-captcha", nil, &issued)
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session", map[string]string{
		"username": "admin", "password": "wrong-pass",
		"captcha_id": issued.ID, "captcha_answer": "ab23",
	}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("正确验证码加错误密码状态码 = %d, 想 401", res.StatusCode)
	}
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session", map[string]string{
		"username": "admin", "password": "secret-pass",
		"captcha_id": issued.ID, "captcha_answer": "AB23",
	}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("重复使用验证码状态码 = %d, 想 401", res.StatusCode)
	}
	var replay struct {
		Error struct {
			Code string `json:"code"`
		}
	}
	tbtest.DecodeJSON(t, res, &replay)
	if replay.Error.Code != "captcha_used" {
		t.Fatalf("重复使用验证码错误码 = %q, 想 captcha_used", replay.Error.Code)
	}

	issued.Image = ""
	s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/login-captcha", nil, &issued)
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session", map[string]string{
		"username": "admin", "password": "secret-pass",
		"captcha_id": issued.ID, "captcha_answer": "AB23",
	}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("正确验证码和密码登录状态码 = %d, 想 200", res.StatusCode)
	}
}

func Test登录验证码约五分钟过期后作废(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithCaptchaSource(fixedCaptcha{}, "AB23"))
	s.CreateAdmin("admin", "secret-pass")

	var issued struct {
		ID string `json:"id"`
	}
	s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/login-captcha", nil, &issued)
	s.Clock().Advance(5*time.Minute + time.Second)
	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/session", map[string]string{
		"username": "admin", "password": "secret-pass",
		"captcha_id": issued.ID, "captcha_answer": "AB23",
	}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期验证码登录状态码 = %d, 想 401", res.StatusCode)
	}
	var got struct {
		Error struct {
			Code string `json:"code"`
		}
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "captcha_expired" {
		t.Fatalf("过期验证码错误码 = %q, 想 captcha_expired", got.Error.Code)
	}
}
