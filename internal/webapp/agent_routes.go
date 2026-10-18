package webapp

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// AgentRoutes 在 SPA 回退前给偏好 Markdown 的 Agent 返回不查库的指引。
func AgentRoutes(publicURL string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && prefersMarkdown(r.Header.Get("Accept")) {
			if path, ok := agentRoutePath(r.URL.Path); ok {
				serveAgentRoute(w, r, publicURL, path)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func agentRoutePath(urlPath string) (string, bool) {
	for _, prefix := range []string{"/t/", "/g/"} {
		if !strings.HasPrefix(urlPath, prefix) {
			continue
		}
		value := strings.TrimPrefix(urlPath, prefix)
		if value != "" && !strings.Contains(value, "/") {
			return value, true
		}
	}
	return "", false
}

func prefersMarkdown(accept string) bool {
	markdown, markdownFound := acceptQuality(accept, "text/markdown")
	html, htmlFound := acceptQuality(accept, "text/html")
	if !markdownFound || markdown <= 0 {
		return false
	}
	if !htmlFound {
		return true
	}
	return markdown >= html
}

func acceptQuality(accept, mediaType string) (float64, bool) {
	for _, part := range strings.Split(accept, ",") {
		fields := strings.Split(part, ";")
		if strings.TrimSpace(strings.ToLower(fields[0])) != mediaType {
			continue
		}
		q := 1.0
		for _, field := range fields[1:] {
			key, value, found := strings.Cut(field, "=")
			if strings.EqualFold(strings.TrimSpace(key), "q") && found {
				if parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					q = parsed
				}
				break
			}
		}
		return q, true
	}
	return 0, false
}

func serveAgentRoute(w http.ResponseWriter, r *http.Request, publicURL, value string) {
	base := publicBaseURL(publicURL, r)
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	if strings.HasPrefix(r.URL.Path, "/t/") {
		fmt.Fprintf(w, `# 读工单 %s

这是 agent-taskboard 工单页面的 Agent 指引；本指引不判断工单是否存在，也不包含工单数据。

## 凭证

用主人自己在网页「我的 → Agent」签发的成员 Agent token，请求头写 `+"`Authorization: Bearer <token>`"+`；没有就先停下来请主人签发，不要用密码登录。

## 读工单

    TOKEN=<token>
    curl -H "Authorization: Bearer $TOKEN" "%s/api/v1/tickets/%s?format=markdown"

先读说明书：<%s/agent.md>
`, value, base, value, base)
		return
	}

	fmt.Fprintf(w, `# 使用访客链接

这个 /g/… 链接本身就是访客凭证：把链接里 /g/ 后面的密钥作为 Bearer token 使用。本指引不判断链接是否有效，凭证由 API 校验。

## 找到并读这张工单

    GUEST_TOKEN=<链接里的密钥>
    curl -H "Authorization: Bearer $GUEST_TOKEN" "%s/api/v1/session"
    TICKET_API=<session 响应里 ticket.api 的值>
    curl -H "Authorization: Bearer $GUEST_TOKEN" "$TICKET_API?format=markdown"

## 回复

    curl -X POST -H "Authorization: Bearer $GUEST_TOKEN" -H "Content-Type: application/json" -d '{"body_md":"…"}' "$TICKET_API/replies"

说明书：<%s/agent.md>
`, base, base)
}

func publicBaseURL(publicURL string, r *http.Request) string {
	if base := strings.TrimRight(publicURL, "/"); base != "" {
		return base
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
		scheme = forwarded
	}
	return scheme + "://" + r.Host
}
