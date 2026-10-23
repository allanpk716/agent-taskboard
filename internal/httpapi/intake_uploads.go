package httpapi

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// intakeUploadLimiter 是按（入口, IP）计数的进程内令牌桶；服务重启后清零。
type intakeUploadLimiter struct {
	mu      sync.Mutex
	buckets map[string]*intakeTokenBucket
	rate    int
	clk     clock.Clock
}

type intakeTokenBucket struct {
	tokens float64
	last   time.Time
}

func newIntakeUploadLimiter(rate int, clk clock.Clock) *intakeUploadLimiter {
	return &intakeUploadLimiter{buckets: map[string]*intakeTokenBucket{}, rate: rate, clk: clk}
}

func (l *intakeUploadLimiter) allow(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clk.Now()
	b := l.buckets[key]
	if b == nil {
		b = &intakeTokenBucket{tokens: float64(l.rate), last: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed > 0 {
		b.tokens = min(float64(l.rate), b.tokens+elapsed*float64(l.rate)/60)
		b.last = now
	}
	if b.tokens >= 1 {
		b.tokens--
		return 0
	}
	return time.Duration((1 - b.tokens) / (float64(l.rate) / 60) * float64(time.Second))
}

// handleIntakeAttachments 处理 POST /api/v1/intakes/{slug}/attachments。
// Idempotency-Key 在这里不是单响应幂等键，而是同一份草稿多个附件共同的提交归属键。
func (a *API) handleIntakeAttachments(w http.ResponseWriter, r *http.Request, slug string) {
	in, err := a.tickets.ActiveIntakeBySlug(r.Context(), slug)
	if err != nil {
		a.writeIntakesError(w, r, err)
		return
	}
	uploadKey := strings.TrimSpace(r.Header.Get(IdempotencyKeyHeader))
	if uploadKey == "" {
		WriteError(w, http.StatusBadRequest, CodeIdempotencyKeyRequired,
			"上传入口附件必须带 Idempotency-Key", "用本次提交使用的同一个幂等键")
		return
	}
	if retry := a.intakeUploads.allow(strconv.FormatInt(in.ID, 10) + "/" + clientIP(r)); retry > 0 {
		seconds := int(retry.Round(time.Second) / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(seconds))
		WriteErrorExtra(w, http.StatusTooManyRequests, CodeIntakeUploadRateLimited,
			"这个反馈入口的附件上传太频繁，请稍后再试",
			"同一入口按来源地址限流；稍后自动恢复", map[string]any{"retry_after_seconds": seconds})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.atts.Limits().MaxFileBytes+(1<<20))
	if err := r.ParseMultipartForm(uploadAttachmentMaxMemory); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentTooLarge,
				"单个文件不能超过 "+strconv.Itoa(a.atts.Limits().MaxFileMB())+" MB", guestLargeFileHint)
			return
		}
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "请求体不是合法的 multipart 表单",
			`用 multipart/form-data 上传，文件字段名 file`)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		WriteError(w, http.StatusUnprocessableEntity, CodeFileRequired, "缺少文件字段",
			`multipart 里要有名为 file 的文件字段`)
		return
	}
	defer file.Close()
	content, err := io.ReadAll(file)
	if err != nil {
		WriteError(w, http.StatusBadRequest, CodeInvalidJSON, "读文件内容失败："+err.Error(), "")
		return
	}
	filename := clientBaseName(header.Filename)
	info, markdown, err := a.atts.UploadToIntake(r.Context(), in.ID, uploadKey, filename, content)
	if err != nil {
		a.writeIntakeAttachmentError(w, err, filename)
		return
	}
	writeJSON(w, http.StatusCreated, attachmentUploadView{
		ID: info.Ref(), Filename: info.Filename, Size: info.Size, State: info.State,
		Markdown: markdown,
		Note:     "附件已暂存：提交反馈时自动挂到说明里；24 小时内未提交会被清除",
	})
}

func (a *API) writeIntakeAttachmentError(w http.ResponseWriter, err error, filename string) {
	var tooLarge *attachments.TooLargeError
	var quota *attachments.IntakeQuotaExceededError
	var ticketQuota *attachments.QuotaExceededError
	var count *attachments.CountExceededError
	switch {
	case errors.As(err, &tooLarge):
		WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentTooLarge, err.Error(), guestLargeFileHint)
	case errors.As(err, &quota):
		WriteErrorExtra(w, http.StatusRequestEntityTooLarge, CodeIntakePendingQuotaExceeded,
			err.Error(), "待提交附件过多时，请先提交已有反馈，或把大文件路径写在说明里",
			map[string]any{"used_mb": attachments.Limits{}.UsedMB(quota.Used), "limit_mb": int(quota.Limit >> 20)})
	case errors.As(err, &ticketQuota):
		WriteErrorExtra(w, http.StatusRequestEntityTooLarge, CodeTicketQuotaExceeded, err.Error(), "",
			map[string]any{"used_mb": 0, "ticket_limit_mb": a.atts.Limits().MaxTicketMB()})
	case errors.As(err, &count):
		WriteError(w, http.StatusRequestEntityTooLarge, CodeAttachmentCountExceeded, err.Error(), "")
	case errors.Is(err, attachments.ErrNotUsable):
		WriteError(w, http.StatusUnprocessableEntity, CodeAttachmentNotUsable, err.Error(), "")
	case errors.Is(err, attachments.ErrInvalidFilename):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidFilename, err.Error(), "")
	default:
		panic(err)
	}
}

const guestLargeFileHint = "请压缩或联系对接成员"

// clientIP 只信任回环反代带来的 X-Forwarded-For，并取最右侧一段（Caddy 追加
// 的客户端地址）；直连时忽略该头。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			parts := strings.Split(forwarded, ",")
			last := strings.TrimSpace(parts[len(parts)-1])
			if parsed := net.ParseIP(last); parsed != nil {
				return parsed.String()
			}
		}
	}
	if ip != nil {
		return ip.String()
	}
	return host
}
