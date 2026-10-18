package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

type clientConnectRequest struct {
	Port          any    `json:"port"`
	State         string `json:"state"`
	CodeChallenge string `json:"code_challenge"`
	Hostname      string `json:"hostname"`
}

func (a *API) handleClientConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	var req clientConnectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "请求体不是合法的 JSON",
			"请求体应为 {\"port\": 49161, \"state\": \"…\", \"code_challenge\": \"…\", \"hostname\": \"…\"}")
		return
	}
	port, err := clientPort(req.Port)
	if err != nil {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidClientPort, "回调端口必须是 1-65535",
			"客户端只能监听本机回环地址；port 传监听端口号")
		return
	}
	code, err := a.ident.CreateClientConnect(r.Context(), viewer.Member, port,
		req.State, req.CodeChallenge, req.Hostname)
	if errors.Is(err, identity.ErrInvalidClientPort) {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidClientPort, "回调端口必须是 1-65535", "")
		return
	}
	if errors.Is(err, identity.ErrInvalidClientConnect) {
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidClientConnect,
			"客户端连接参数不合法", "state、hostname 和 S256 code_challenge 都不能为空；hostname 最长 255 字符")
		return
	}
	if err != nil {
		panic(err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/cb?code=%s&state=%s",
		port, url.QueryEscape(code), url.QueryEscape(req.State))
	writeJSON(w, http.StatusCreated, map[string]string{"redirect_url": redirect})
}

func (a *API) handleClientToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	var req struct {
		Code         string `json:"code"`
		CodeVerifier string `json:"code_verifier"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "请求体不是合法的 JSON",
			"请求体应为 {\"code\": \"…\", \"code_verifier\": \"…\"}")
		return
	}
	cred, err := a.ident.ExchangeClientCode(r.Context(), req.Code, req.CodeVerifier)
	if errors.Is(err, identity.ErrInvalidClientCode) {
		WriteError(w, http.StatusUnauthorized, CodeInvalidClientCode,
			"客户端连接 code 无效、已过期或 verifier 不匹配", "请重新发起连接")
		return
	}
	if err != nil {
		panic(err)
	}
	writeJSON(w, http.StatusCreated, cred)
}

func (a *API) handleClients(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	list, err := a.ident.ListClientCredentials(r.Context(), viewer.Member)
	if err != nil {
		panic(err)
	}
	rows := make([]clientCredentialRow, 0, len(list))
	for _, c := range list {
		row := clientCredentialRow{ID: c.ID, Hostname: c.Hostname,
			CreatedAt: formatTime(c.CreatedAt), LastUsedAt: formatTime(c.LastUsedAt),
			ClientVersion: c.ClientVersion}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, struct {
		Clients []clientCredentialRow `json:"clients"`
	}{Clients: rows})
}

type clientCredentialRow struct {
	ID            int64  `json:"id"`
	Hostname      string `json:"hostname"`
	CreatedAt     string `json:"created_at"`
	LastUsedAt    string `json:"last_used_at"`
	ClientVersion string `json:"client_version"`
}

func (a *API) handleClient(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/me/clients/self":
		a.revokeCurrentClient(w, r)
	case r.Method == http.MethodDelete:
		a.revokeClient(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

func (a *API) revokeCurrentClient(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.viewerFromRequest(w, r)
	if !ok || viewer.Client == nil {
		if ok && viewer.Client == nil {
			WriteError(w, http.StatusForbidden, CodeClientScope, "只有客户端凭证能撤销自己", "")
		}
		return
	}
	if err := a.ident.RevokeCurrentClientCredential(r.Context(), *viewer.Client); err != nil {
		if errors.Is(err, identity.ErrClientCredentialNotFound) {
			WriteError(w, http.StatusNotFound, CodeClientCredentialNotFound, "没有这个客户端凭证", "")
			return
		}
		panic(err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *API) revokeClient(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	idStr := strings.TrimPrefix(r.URL.Path, "/api/v1/me/clients/")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusNotFound, CodeClientCredentialNotFound, "没有这个客户端凭证", "")
		return
	}
	if err := a.ident.RevokeClientCredential(r.Context(), viewer.Member, id); err != nil {
		if errors.Is(err, identity.ErrClientCredentialNotFound) {
			WriteError(w, http.StatusNotFound, CodeClientCredentialNotFound, "没有这个客户端凭证", "")
			return
		}
		panic(err)
	}
	w.WriteHeader(http.StatusNoContent)
}

func clientPort(raw any) (int, error) {
	switch v := raw.(type) {
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("invalid port")
		}
		return strconv.Atoi(strconv.FormatFloat(v, 'f', 0, 64))
	case string:
		return strconv.Atoi(v)
	default:
		return 0, fmt.Errorf("invalid port")
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339Nano)
}
