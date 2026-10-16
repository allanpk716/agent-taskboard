// Idempotency-Key 的 HTTP 侧（spec「幂等」）：所有会产生新内容的请求（建单、
// 回复、说明修订；反馈提交与附件上传随后续工单接入）都包一层——同一个
// （key、凭证、端点）的重放返回首次成功响应的快照，不再执行写操作。
package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/idempotency"
)

// IdempotencyKeyHeader 是幂等请求头。
const IdempotencyKeyHeader = "Idempotency-Key"

// withIdempotency 执行 next：带 Idempotency-Key 时先查快照，命中则原样重放；
// 未命中则执行并把成功的响应（2xx）存成快照。没带 key 就直接执行。
func (a *API) withIdempotency(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	key := strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader))
	if key == "" {
		next(w, r)
		return
	}
	credential := credentialFingerprint(r)
	endpoint := r.Method + " " + r.URL.Path
	if snap, found, err := a.idem.Lookup(r.Context(), key, credential, endpoint); err != nil {
		panic(err)
	} else if found {
		// 原样重放首次响应（含 201 状态码与响应体）。
		if snap.ContentType != "" {
			w.Header().Set("Content-Type", snap.ContentType)
		}
		w.WriteHeader(snap.Status)
		_, _ = w.Write(snap.Body)
		return
	}
	rec := &responseRecorder{header: make(http.Header)}
	next(rec, r)
	rec.flushTo(w)
	if rec.status >= 200 && rec.status < 300 {
		if err := a.idem.Save(r.Context(), key, credential, endpoint, idempotency.Snapshot{
			Status:      rec.status,
			ContentType: rec.header.Get("Content-Type"),
			Body:        rec.buf.Bytes(),
		}); err != nil {
			panic(err)
		}
	}
}

// credentialFingerprint 给当前请求上的凭证算指纹（sha256）：Agent token 取 Bearer，
// 网页会话取 cookie，将来访客链接密钥也是 Bearer。幂等记录按凭证隔离，
// 同一成员的不同凭证（本人会话与各 Agent）互不通用。
func credentialFingerprint(r *http.Request) string {
	if token, ok := bearerToken(r); ok {
		return hashCredential(token)
	}
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		return hashCredential(c.Value)
	}
	return "anonymous"
}

func hashCredential(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// responseRecorder 把 handler 写出的响应录下来，再原样发给客户端。
type responseRecorder struct {
	header http.Header
	buf    bytes.Buffer
	status int
}

func (rec *responseRecorder) Header() http.Header { return rec.header }

func (rec *responseRecorder) WriteHeader(status int) {
	if rec.status == 0 {
		rec.status = status
	}
}

func (rec *responseRecorder) Write(p []byte) (int, error) {
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	return rec.buf.Write(p)
}

// flushTo 把录下的响应（响应头、状态码、响应体）写给真实客户端。
func (rec *responseRecorder) flushTo(w http.ResponseWriter) {
	for k, vv := range rec.header {
		w.Header()[k] = vv
	}
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	w.WriteHeader(rec.status)
	_, _ = w.Write(rec.buf.Bytes())
}
