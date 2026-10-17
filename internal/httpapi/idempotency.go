// Idempotency-Key 的 HTTP 侧（spec「幂等」）：所有会产生新内容的请求（建单、
// 回复、说明修订、建访客链接、指派、加标签/维度、建入口、反馈提交、附件上传）
// 都包一层——同一个（key、凭证、端点）的重放返回首次成功响应的快照，不再执行
// 写操作。执行前先原子预留 pending 记录（评审修正）：同键并发重放得到
// 409 idempotency_in_progress；快照保留 24 小时，超龄重放当作新请求。
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

// withIdempotency 执行 next：带 Idempotency-Key 时先预留执行权——已完成的
// 记录原样重放首次响应（含 201 状态码与响应体）；他人正在执行同键请求时回
// 409 idempotency_in_progress；拿到预留才执行，成功（2xx）补成快照，失败
// 释放预留（下次同键请求重新执行）。没带 key 就直接执行。
func (a *API) withIdempotency(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	key := strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader))
	if key == "" {
		next(w, r)
		return
	}
	credential := credentialFingerprint(r)
	endpoint := r.Method + " " + r.URL.Path
	reserved, snap, found, inProgress, err := a.idem.Reserve(r.Context(), key, credential, endpoint)
	if err != nil {
		panic(err)
	}
	if found {
		// 原样重放首次响应（含 201 状态码与响应体）。
		if snap.ContentType != "" {
			w.Header().Set("Content-Type", snap.ContentType)
		}
		w.WriteHeader(snap.Status)
		_, _ = w.Write(snap.Body)
		return
	}
	if inProgress {
		WriteError(w, http.StatusConflict, CodeIdempotencyInProgress,
			"同一个 Idempotency-Key 的请求正在执行",
			"稍等片刻再重试；首次请求完成后重放会直接返回首次结果")
		return
	}
	_ = reserved // Reserve 返回 reserved=false 只会配合 found / inProgress
	rec := &responseRecorder{header: make(http.Header)}
	next(rec, r)
	rec.flushTo(w)
	if rec.status >= 200 && rec.status < 300 {
		if err := a.idem.Complete(r.Context(), key, credential, endpoint, idempotency.Snapshot{
			Status:      rec.status,
			ContentType: rec.header.Get("Content-Type"),
			Body:        rec.buf.Bytes(),
		}); err != nil {
			panic(err)
		}
	} else if err := a.idem.Release(r.Context(), key, credential, endpoint); err != nil {
		panic(err)
	}
}

// credentialFingerprint 给当前请求上的凭证算指纹（sha256）：Agent token 取 Bearer，
// 网页会话取 cookie，将来访客链接密钥也是 Bearer。幂等记录按凭证隔离，
// 同一成员的不同凭证（本人会话与各 Agent）互不通用。匿名请求（入口提交）
// 按入口区分：intake:<slug>——不同入口互不共享，同入口内所有匿名提交者共用
// 一个范围（评审修正：避免所有匿名请求共享同一个 "anonymous" 指纹）。
func credentialFingerprint(r *http.Request) string {
	if token, ok := bearerToken(r); ok {
		return hashCredential(token)
	}
	if c, err := r.Cookie(SessionCookieName); err == nil && c.Value != "" {
		return hashCredential(c.Value)
	}
	if slug, ok := intakeSlugFromPath(r.URL.Path); ok {
		return "intake:" + slug
	}
	return "anonymous"
}

// intakeSlugFromPath 从匿名端点 /api/v1/intakes/{slug}/tickets 里取出 slug
// 作为匿名幂等记录的范围；其他路径没有可用的匿名范围。
func intakeSlugFromPath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/api/v1/intakes/")
	if !ok {
		return "", false
	}
	slug, sub, hasSub := strings.Cut(rest, "/")
	if !hasSub || sub != "tickets" || slug == "" {
		return "", false
	}
	return slug, true
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
