package server_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
	"gopkg.in/yaml.v3"
)

type openAPIDocument struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Version string `yaml:"version"`
	} `yaml:"info"`
	Servers []struct {
		URL string `yaml:"url"`
	} `yaml:"servers"`
	Paths map[string]map[string]struct {
		OperationID string `yaml:"operationId"`
	} `yaml:"paths"`
}

func Test公开OpenAPI描述免凭证返回并替换请求地址(t *testing.T) {
	s := tbtest.Start(t)

	res := s.Do(t, s.NewClient(), http.MethodGet, "/openapi.yaml", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != "application/yaml" {
		t.Fatalf("Content-Type = %q, 想 application/yaml", got)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	raw := string(body)
	if strings.Contains(raw, "{{BASE}}") {
		t.Fatal("响应仍残留 {{BASE}}")
	}

	var doc openAPIDocument
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("解析 OpenAPI YAML 失败：%v", err)
	}
	if len(doc.Servers) != 1 || doc.Servers[0].URL != s.URL() {
		t.Fatalf("servers 没有使用请求方地址 %s", s.URL())
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		t.Fatalf("OpenAPI 版本 = %q, 想 3.x", doc.OpenAPI)
	}
	if doc.Info.Version != "v1" {
		t.Fatalf("info.version = %q, 想 v1", doc.Info.Version)
	}
}

func TestOpenAPI描述恰好二十个真实操作且守住红线(t *testing.T) {
	s := tbtest.Start(t)
	res := s.Do(t, s.NewClient(), http.MethodGet, "/openapi.yaml", nil, nil)
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	var doc openAPIDocument
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("解析 OpenAPI YAML 失败：%v", err)
	}

	ids := make([]string, 0, 20)
	seen := map[string]bool{}
	for path, operations := range doc.Paths {
		for method, operation := range operations {
			if operation.OperationID == "" {
				t.Fatalf("%s %s 缺少 operationId", method, path)
			}
			if seen[operation.OperationID] {
				t.Fatalf("operationId 重复：%s", operation.OperationID)
			}
			seen[operation.OperationID] = true
			ids = append(ids, operation.OperationID)
			assertOpenAPIRouteExists(t, s, method, path)
		}
	}
	if len(ids) != 20 {
		t.Fatalf("operation 数量 = %d (%v), 想 20", len(ids), ids)
	}

	raw := string(body)
	for _, forbidden := range []string{
		"guest-links", "guest_link", "rotate-key", "/api/v1/me/", "/api/v1/members",
		"/api/v1/admin", "/api/v1/notifications", "/api/v1/intakes", "feedback",
	} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("OpenAPI 描述包含红线索 %q", forbidden)
		}
	}
}

func assertOpenAPIRouteExists(t *testing.T, s *tbtest.Server, method, path string) {
	t.Helper()
	path = strings.ReplaceAll(path, "{id}", "1")
	path = strings.ReplaceAll(path, "{key}", "bug-report")
	path = strings.ReplaceAll(path, "{att}", "att_1")
	res := s.Do(t, s.NewClient(), strings.ToUpper(method), path, nil, nil)
	if res.StatusCode == http.StatusNotFound || res.StatusCode == http.StatusMethodNotAllowed {
		body, _ := io.ReadAll(res.Body)
		t.Fatalf("%s %s 路由不存在：状态码 %d %s %s",
			res.Request.Method, res.Request.URL.String(), res.StatusCode, res.Header.Get("Allow"), body)
	}
	if res.StatusCode >= 500 {
		t.Fatalf("%s %s 返回服务端错误：%d", method, path, res.StatusCode)
	}
}
