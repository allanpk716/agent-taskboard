package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 未登录访问受保护接口时，返回统一的错误格式 {error:{code, message, hint?}}。
func Test未登录访问受保护接口返回统一错误格式(t *testing.T) {
	s := tbtest.Start(t)

	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d, 想 %d", res.StatusCode, http.StatusUnauthorized)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		} `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &body)
	if body.Error.Code != "unauthenticated" {
		t.Errorf("错误码 = %q, 想 %q", body.Error.Code, "unauthenticated")
	}
	if body.Error.Message == "" {
		t.Errorf("message 为空")
	}
}
