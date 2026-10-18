// Package agentdocs serves the public Markdown documentation used by agents.
package agentdocs

import (
	_ "embed"
	"net/http"
	"strings"

	"github.com/allanpk716/agent-taskboard/docs"
)

//go:embed agent.md
var agentMarkdown string

// Handler returns the public documentation routes.
func Handler(publicURL string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/agent.md", serveMarkdown(publicURL, func(base string) string {
		return strings.ReplaceAll(agentMarkdown, "{{BASE}}", base)
	}))
	mux.HandleFunc("/llms.txt", serveMarkdown(publicURL, func(base string) string {
		return strings.ReplaceAll(agentMarkdown, "{{BASE}}", base)
	}))
	mux.HandleFunc("/api.md", serveMarkdown(publicURL, func(string) string { return docs.APIMarkdown }))
	return mux
}

func serveMarkdown(publicURL string, render func(base string) string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		base := strings.TrimRight(publicURL, "/")
		if base == "" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			if forwarded := r.Header.Get("X-Forwarded-Proto"); forwarded != "" {
				scheme = forwarded
			}
			base = scheme + "://" + r.Host
		}
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(render(base)))
	}
}
