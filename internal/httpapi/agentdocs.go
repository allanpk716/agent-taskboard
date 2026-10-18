package httpapi

import (
	"net/http"

	"github.com/allanpk716/agent-taskboard/internal/version"
)

type apiRootView struct {
	Name        string            `json:"name"`
	Version     string            `json:"version"`
	Docs        apiRootDocs       `json:"docs"`
	Auth        string            `json:"auth"`
	EntryPoints map[string]string `json:"entry_points"`
}

type apiRootDocs struct {
	Agent string `json:"agent"`
	API   string `json:"api"`
}

func (a *API) handleAPIRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	base := a.requestBaseURL(r)
	writeJSON(w, http.StatusOK, apiRootView{
		Name:    "agent-taskboard",
		Version: version.Server,
		Docs:    apiRootDocs{Agent: base + "/agent.md", API: base + "/api.md"},
		Auth: "成员 Agent 用主人在网页「我的 → Agent」签发的 token：Authorization: Bearer <token>；" +
			"访客用访客链接里的凭证",
		EntryPoints: map[string]string{
			"tickets":   base + "/api/v1/tickets",
			"inbox":     base + "/api/v1/inbox",
			"session":   base + "/api/v1/session",
			"labels":    base + "/api/v1/labels",
			"templates": base + "/api/v1/templates",
		},
	})
}
