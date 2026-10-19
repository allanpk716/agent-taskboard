package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test公开说明书和接口参考无需凭证可读(t *testing.T) {
	s := tbtest.Start(t)

	cases := []struct {
		path      string
		wantStart string
	}{
		{"/agent.md", "# agent-taskboard Agent 说明书"},
		{"/llms.txt", "# agent-taskboard Agent 说明书"},
		{"/api.md", "# HTTP API 约定与端点清单"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			res := s.Do(t, s.NewClient(), http.MethodGet, tc.path, nil, nil)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("状态码 = %d，想 200", res.StatusCode)
			}
			if got := res.Header.Get("Content-Type"); got != "text/markdown; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}
			body, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(string(body), tc.wantStart) {
				t.Fatalf("正文开头不对：%q", string(body))
			}
		})
	}
}

func Test说明书地址按public_url或请求地址生成(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://taskboard.example.com"))
	assertAgentDocContains(t, s, "https://taskboard.example.com/api/v1/tickets")
	assertAgentDocContains(t, s, "Authorization: Bearer tbg_<链接密钥>:<访问码>")

	s = tbtest.Start(t)
	assertAgentDocContains(t, s, s.URL()+"/api/v1/tickets")
}

func assertAgentDocContains(t *testing.T, s *tbtest.Server, want string) {
	t.Helper()
	res := s.Do(t, s.NewClient(), http.MethodGet, "/agent.md", nil, nil)
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), want) {
		t.Fatalf("说明书没有包含 %q", want)
	}
}

func TestAPI自描述和未知路径提示(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://taskboard.example.com"))

	for _, path := range []string{"/api/v1", "/api/v1/"} {
		res := s.Do(t, s.NewClient(), http.MethodGet, path, nil, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("%s 状态码 = %d，想 200", path, res.StatusCode)
		}
		var got struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			Docs    struct {
				Agent string `json:"agent"`
				API   string `json:"api"`
			} `json:"docs"`
			Auth        string            `json:"auth"`
			EntryPoints map[string]string `json:"entry_points"`
		}
		tbtest.DecodeJSON(t, res, &got)
		if got.Name != "agent-taskboard" || got.Version == "" {
			t.Fatalf("名称或版本不对：%+v", got)
		}
		if got.Docs.Agent != "https://taskboard.example.com/agent.md" ||
			got.Docs.API != "https://taskboard.example.com/api.md" {
			t.Fatalf("文档地址不对：%+v", got.Docs)
		}
		if !strings.Contains(got.Auth, "Authorization: Bearer") ||
			!strings.Contains(got.Auth, "我的 → Agent") {
			t.Fatalf("凭证说明不对：%q", got.Auth)
		}
		if got.EntryPoints["tickets"] != "https://taskboard.example.com/api/v1/tickets" ||
			got.EntryPoints["inbox"] != "https://taskboard.example.com/api/v1/inbox" {
			t.Fatalf("主要入口不对：%+v", got.EntryPoints)
		}
	}

	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/nope", nil, nil)
	var errBody struct {
		Error struct {
			Code string `json:"code"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &errBody)
	if res.StatusCode != http.StatusNotFound || errBody.Error.Code != "not_found" {
		t.Fatalf("未知路径状态 %d %+v", res.StatusCode, errBody)
	}
	if !strings.Contains(errBody.Error.Hint, "https://taskboard.example.com/agent.md") {
		t.Fatalf("未知路径 hint 没有说明书：%q", errBody.Error.Hint)
	}
	if strings.Contains(errBody.Error.Hint, "; ") {
		t.Fatalf("未知路径 hint 的中文句子不应使用半角分号：%q", errBody.Error.Hint)
	}
}

func Test未登录提示指向说明书而不是密码登录(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://taskboard.example.com"))

	res := s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/session", nil, nil)
	var got struct {
		Error struct {
			Code string `json:"code"`
			Hint string `json:"hint"`
		} `json:"error"`
	}
	tbtest.DecodeJSON(t, res, &got)
	if res.StatusCode != http.StatusUnauthorized || got.Error.Code != "unauthenticated" {
		t.Fatalf("状态 %d %+v", res.StatusCode, got)
	}
	if !strings.Contains(got.Error.Hint, "https://taskboard.example.com/agent.md") ||
		!strings.Contains(got.Error.Hint, "我的 → Agent") ||
		strings.Contains(got.Error.Hint, "POST /api/v1/session") {
		t.Fatalf("401 hint 不对：%q", got.Error.Hint)
	}
}
