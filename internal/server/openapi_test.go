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
		OperationID string                     `yaml:"operationId"`
		Responses   map[string]openAPIResponse `yaml:"responses"`
	} `yaml:"paths"`
}

type openAPIResponse struct {
	Description string `yaml:"description"`
	Content     map[string]struct {
		Schema openAPISchema `yaml:"schema"`
	} `yaml:"content"`
}

type openAPISchema struct {
	AllOf      []openAPISchema          `yaml:"allOf"`
	Properties map[string]openAPISchema `yaml:"properties"`
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

func TestOpenAPI描述恰好二十一个真实操作且守住红线(t *testing.T) {
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

	ids := make([]string, 0, 21)
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
	if len(ids) != 21 {
		t.Fatalf("operation 数量 = %d (%v), 想 21", len(ids), ids)
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

func Test附件短链接OpenAPI专门描述已瘦身410(t *testing.T) {
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

	gone := doc.Paths["/api/v1/attachment-links/{att}"]["get"].Responses["410"]
	for _, want := range []string{"附件已瘦身移出", "attachment_slimmed", "slim_package_name", "slim_removed_at"} {
		if !strings.Contains(gone.Description, want) {
			t.Fatalf("短链接 410 描述 = %q, 想包含 %q", gone.Description, want)
		}
	}
	if strings.Contains(gone.Description, "工单已归档移除") {
		t.Fatalf("短链接 410 描述不应沿用归档文案：%q", gone.Description)
	}
	jsonContent, ok := gone.Content["application/json"]
	if !ok {
		t.Fatal("短链接 410 缺少 application/json 响应体")
	}
	for _, field := range []string{"slim_package_name", "slim_removed_at"} {
		if !openAPISchemaHasProperty(jsonContent.Schema, "error", field) {
			t.Fatalf("短链接 410 错误 schema 缺少 error.%s", field)
		}
	}
}

func openAPISchemaHasProperty(schema openAPISchema, path ...string) bool {
	if len(path) == 0 {
		return true
	}
	if child, ok := schema.Properties[path[0]]; ok && openAPISchemaHasProperty(child, path[1:]...) {
		return true
	}
	for _, child := range schema.AllOf {
		if openAPISchemaHasProperty(child, path...) {
			return true
		}
	}
	return false
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
