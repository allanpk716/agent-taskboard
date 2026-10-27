// Package agentdocs 提供给 Agent 看的公开 Markdown 文档。
package agentdocs

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/allanpk716/agent-taskboard/docs"
	"github.com/allanpk716/agent-taskboard/internal/publicbase"
)

//go:embed agent.md
var agentMarkdown string

//go:embed skill.md
var skillMarkdown string

// Handler 挂公开文档路由，其余路径交给 next（SPA 回退仍在文档之后）。
func Handler(publicURL string, next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/agent.md", serveMarkdown(publicURL, func(base string) string {
		return strings.ReplaceAll(agentMarkdown, "{{BASE}}", base)
	}))
	mux.HandleFunc("/llms.txt", serveMarkdown(publicURL, func(base string) string {
		return strings.ReplaceAll(agentMarkdown, "{{BASE}}", base)
	}))
	mux.HandleFunc("/api.md", serveMarkdown(publicURL, func(string) string { return docs.APIMarkdown }))
	mux.HandleFunc("/skills/agent-taskboard/SKILL.md", serveMarkdown(publicURL, func(base string) string {
		return strings.ReplaceAll(skillMarkdown, "{{BASE}}", base)
	}))
	mux.Handle("/", next)
	return mux
}

func serveMarkdown(publicURL string, render func(base string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		base := publicbase.FromRequest(publicURL, r)
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(render(base)))
	}
}
