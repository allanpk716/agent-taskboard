package outlet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
)

const (
	// DefaultTick 是部署级门铃派发循环间隔。
	DefaultTick = 30 * time.Second
	// MinPushInterval 是同一成员两响之间的最小间隔。
	MinPushInterval = time.Minute
	// MaxRetryDelay 是持续 5xx/超时后的重试退避上限。
	MaxRetryDelay = 30 * time.Minute
	// ClaimLease 是一次外呼从 claim 到结果落库的最长占用时间。发送前先把
	// retryable 行指到租约到期，进程中途死掉时重启后能按同键继续重试。
	ClaimLease = 2 * time.Minute
	// autoPauseFailureCount 是 4xx 连续失败多少次后自动暂停。
	autoPauseFailureCount = 2
	// stateWriteTimeout 是取消推送循环后仍要完成的 DB 状态写入时限。
	stateWriteTimeout = 5 * time.Second

	retryStateNone      = "none"
	retryStateRetryable = "retryable"
	retryStatePermanent = "permanent"
)

// OutletNotifier 把自动暂停写成成员本人的站内系统通知，并唤醒通知流。
type OutletNotifier interface {
	NotifyOutletPausedTx(ctx context.Context, tx *sql.Tx, memberID int64, at time.Time) error
	Wake(memberIDs []int64)
}

// PusherDeps 汇出门铃推送循环的系统依赖，生产装配和测试都走同一个入口。
type PusherDeps struct {
	DB       *sql.DB
	Clock    clock.Clock
	Cipher   *Cipher
	Webhook  config.Webhook
	Client   *Client
	Logger   *log.Logger
	Notifier OutletNotifier
}

// Pusher 是部署级门铃派发器：每个 outlet 一个有界 worker，游标与重试状态持久在 DB。
type Pusher struct {
	db       *sql.DB
	clk      clock.Clock
	cipher   *Cipher
	client   *Client
	logger   *log.Logger
	builder  *URLBuilder
	notifier OutletNotifier
	start    time.Time
	end      time.Time

	ctx      context.Context
	cancel   context.CancelFunc
	stopOnce sync.Once

	mu       sync.Mutex
	inFlight map[int64]struct{}
	lastLog  map[int64]string
}

// NewPusher 用 [webhook] 配置构造推送器；窗口沿用 config 的固定北京时间解析。
func NewPusher(deps PusherDeps) (*Pusher, error) {
	if deps.DB == nil || deps.Clock == nil {
		return nil, errors.New("门铃推送循环缺少数据库或时钟")
	}
	if deps.Cipher == nil {
		return nil, ErrMasterKeyMissing
	}
	if deps.Client == nil {
		deps.Client = NewClient()
	}
	if deps.Logger == nil {
		deps.Logger = log.New(log.Writer(), "", log.LstdFlags)
	}
	start, end, err := deps.Webhook.TimeWindow()
	if err != nil {
		return nil, err
	}
	builder, err := NewURLBuilder(deps.Webhook.AllowedBases)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Pusher{
		db: deps.DB, clk: deps.Clock, cipher: deps.Cipher, client: deps.Client,
		logger: deps.Logger, builder: builder, notifier: deps.Notifier, start: start, end: end,
		ctx: ctx, cancel: cancel,
		inFlight: make(map[int64]struct{}), lastLog: make(map[int64]string),
	}, nil
}

// SetNotifier 装配自动暂停通知；生产装配与测试都使用同一接口。
func (p *Pusher) SetNotifier(notifier OutletNotifier) { p.notifier = notifier }

type outletRow struct {
	status       string
	memberID     int64
	botID        string
	keyCipher    string
	lastPushedID int64
	generation   int64
	retryState   string
	failureCount int64
	pendingID    sql.NullInt64
	attemptCount int64
	nextAttempt  sql.NullString
	lastPushed   sql.NullString
	catchupAfter sql.NullString
}

type outletCounts struct {
	fromID   int64
	latestID int64
	turn     int64
	collab   int64
	fyi      int64
	system   int64
}

// Dispatch 扫描 active outlet 并派发本轮工作；慢 endpoint 只占用自己的 worker。
func (p *Pusher) Dispatch(ctx context.Context) error {
	outlets := []outletRow{}
	rows, err := p.db.QueryContext(ctx, `
		SELECT status, member_id, bot_id, key_cipher, last_pushed_id, generation,
			failure_count, retry_state, pending_latest_id, attempt_count, next_attempt_at, last_pushed_at,
			catchup_after
		FROM outlet ORDER BY member_id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row outletRow
		if err := rows.Scan(&row.status, &row.memberID, &row.botID, &row.keyCipher, &row.lastPushedID,
			&row.generation, &row.failureCount, &row.retryState, &row.pendingID, &row.attemptCount,
			&row.nextAttempt, &row.lastPushed, &row.catchupAfter); err != nil {
			return err
		}
		outlets = append(outlets, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, row := range outlets {
		if !p.beginWorker(row.memberID) {
			continue
		}
		go func(row outletRow) {
			defer p.finishWorker(row.memberID)
			p.dispatchRow(ctx, row)
		}(row)
	}
	return nil
}

func (p *Pusher) dispatchRow(ctx context.Context, row outletRow) {
	if row.status == StatusPaused {
		p.logResult(row.memberID, "paused", outletCounts{
			fromID: row.lastPushedID, latestID: row.pendingID.Int64,
		}, 0, 0)
		return
	}
	if row.retryState == retryStatePermanent {
		current, err := p.count(ctx, row.memberID, row.lastPushedID, sql.NullInt64{}, retryStateNone, row.catchupAfter)
		if err != nil {
			p.logError(row.memberID, err)
			return
		}
		if !row.pendingID.Valid || current.latestID <= row.pendingID.Int64 {
			p.logResult(row.memberID, "failed", outletCounts{fromID: row.lastPushedID, latestID: row.pendingID.Int64}, 0, 0)
			return
		}
		// 4xx 不重试旧键；新的 latest_id 是下一响，也换新的幂等键。
		row.retryState = retryStateNone
		row.pendingID = sql.NullInt64{}
		row.attemptCount = 0
	}
	if row.retryState == retryStateRetryable {
		if row.nextAttempt.Valid {
			next, err := time.Parse(time.RFC3339Nano, row.nextAttempt.String)
			if err != nil {
				p.logError(row.memberID, fmt.Errorf("解析门铃重试时间失败：%w", err))
				return
			}
			if p.clk.Now().Before(next) {
				return
			}
		}
		if !row.pendingID.Valid || row.pendingID.Int64 <= row.lastPushedID {
			p.logError(row.memberID, errors.New("门铃重试状态缺少有效 latest_id"))
			return
		}
	}

	counts, err := p.count(ctx, row.memberID, row.lastPushedID, row.pendingID, row.retryState, row.catchupAfter)
	if err != nil {
		p.logError(row.memberID, err)
		return
	}
	if counts.latestID <= row.lastPushedID {
		return
	}
	if counts.turn == 0 && counts.collab == 0 && counts.fyi == 0 && counts.system == 0 {
		if err := p.advanceWithoutPush(ctx, row, counts.latestID); err != nil {
			p.logError(row.memberID, err)
			return
		}
		p.logResult(row.memberID, "skipped_all_read", counts, 0, 0)
		return
	}
	if row.lastPushed.Valid && row.lastPushed.String != "" {
		last, err := time.Parse(time.RFC3339Nano, row.lastPushed.String)
		if err != nil {
			p.logError(row.memberID, fmt.Errorf("解析门铃上次推送时间失败：%w", err))
			return
		}
		if p.clk.Now().Sub(last) < MinPushInterval {
			p.logResult(row.memberID, "skipped_throttled", counts, 0, 0)
			return
		}
	}
	if !p.inWindow() && counts.system == 0 {
		p.logResult(row.memberID, "skipped_window", counts, 0, 0)
		return
	}

	webhookKey, err := p.cipher.Decrypt(row.keyCipher)
	if err != nil {
		p.logError(row.memberID, err)
		return
	}
	endpoint, err := p.builder.Build(row.botID)
	if err != nil {
		p.logError(row.memberID, err)
		return
	}
	payload, err := json.Marshal(outletPayload{
		Event: "taskboard.notify", FromID: counts.fromID, LatestID: counts.latestID,
		Turn: counts.turn, Collab: counts.collab, FYI: counts.fyi, System: counts.system,
	})
	if err != nil {
		p.logError(row.memberID, err)
		return
	}
	if err := p.claim(ctx, row, counts.latestID); err != nil {
		p.logError(row.memberID, err)
		return
	}
	p.work(row, endpoint, webhookKey, payload, counts)
}

func (p *Pusher) beginWorker(memberID int64) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, busy := p.inFlight[memberID]; busy {
		return false
	}
	p.inFlight[memberID] = struct{}{}
	return true
}

func (p *Pusher) finishWorker(memberID int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.inFlight, memberID)
}

type outletPayload struct {
	Event    string `json:"event"`
	FromID   int64  `json:"from_id"`
	LatestID int64  `json:"latest_id"`
	Turn     int64  `json:"turn"`
	Collab   int64  `json:"collab"`
	FYI      int64  `json:"fyi"`
	System   int64  `json:"system"`
}

func (p *Pusher) count(ctx context.Context, memberID, fromID int64, pending sql.NullInt64,
	state string, catchupAfter sql.NullString) (outletCounts, error) {
	counts := outletCounts{fromID: fromID}
	var latest int64
	if state == retryStateRetryable && pending.Valid {
		latest = pending.Int64
	} else if err := p.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(id),0) FROM notifications
		WHERE member_id=? AND kind<>'draft'`, memberID).Scan(&latest); err != nil {
		return outletCounts{}, err
	}
	counts.latestID = latest
	if latest <= fromID {
		return counts, nil
	}
	var catchup any
	if catchupAfter.Valid {
		catchup = catchupAfter.String
	}
	rows, err := p.db.QueryContext(ctx, `
		SELECT bucket,COUNT(*) FROM (
			SELECT CASE
				WHEN kind='turn' THEN 'turn'
				WHEN reason IN ('collaborator_added','collaborator_removed','collaborator_replied') THEN 'collab'
				WHEN ticket_seq IS NULL THEN 'system'
				ELSE 'fyi'
			END AS bucket
			FROM notifications
			WHERE member_id=? AND id>? AND id<=? AND read_at IS NULL AND kind<>'draft'
				AND (? IS NULL OR created_at>=?)
		) WHERE bucket IS NOT NULL GROUP BY bucket`, memberID, fromID, latest, catchup, catchup)
	if err != nil {
		return outletCounts{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var bucket string
		var count int64
		if err := rows.Scan(&bucket, &count); err != nil {
			return outletCounts{}, err
		}
		switch bucket {
		case "turn":
			counts.turn = count
		case "collab":
			counts.collab = count
		case "fyi":
			counts.fyi = count
		case "system":
			counts.system = count
		}
	}
	if err := rows.Err(); err != nil {
		return outletCounts{}, err
	}
	return counts, nil
}

func (p *Pusher) advanceWithoutPush(ctx context.Context, row outletRow, latest int64) error {
	now := p.clk.Now().Format(time.RFC3339Nano)
	_, err := p.db.ExecContext(ctx, `
		UPDATE outlet SET last_pushed_id=?, `+PendingStateResetSQL+`,
			catchup_after=NULL, updated_at=?
		WHERE member_id=? AND generation=? AND last_pushed_id=? AND status=?`,
		latest, now, row.memberID, row.generation, row.lastPushedID, StatusActive)
	return err
}

func (p *Pusher) claim(ctx context.Context, row outletRow, latest int64) error {
	now := p.clk.Now()
	leaseUntil := now.Add(ClaimLease).Format(time.RFC3339Nano)
	res, err := p.db.ExecContext(ctx, `
		UPDATE outlet SET retry_state='retryable', pending_latest_id=?, attempt_count=?,
			next_attempt_at=?, updated_at=?
		WHERE member_id=? AND generation=? AND last_pushed_id=? AND status=?`,
		latest, row.attemptCount, leaseUntil, now.Format(time.RFC3339Nano),
		row.memberID, row.generation, row.lastPushedID, StatusActive)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return errors.New("门铃 outlet 已在派发后变化，放弃本响")
	}
	return nil
}

func (p *Pusher) work(row outletRow, endpoint, webhookKey string, payload []byte, counts outletCounts) {
	defer func() {
		p.mu.Lock()
		delete(p.inFlight, row.memberID)
		p.mu.Unlock()
	}()
	started := time.Now()
	idempotencyKey := fmt.Sprintf("taskboard:%d:g%d:%d", row.memberID, row.generation, counts.latestID)
	response, err := p.client.Post(p.ctx, endpoint, http.Header{
		"X-Webhook-Key":     []string{webhookKey},
		"X-Idempotency-Key": []string{idempotencyKey},
	}, payload)
	duration := time.Since(started)
	if err == nil && response.IsSuccess() {
		ignored := responseIgnored(response.Body)
		if err := p.markSuccess(row, counts.latestID, ignored); err != nil {
			if markErr := p.markFailure(row, counts.latestID, false); markErr != nil {
				err = fmt.Errorf("%v；登记重试也失败：%w", err, markErr)
			} else {
				p.logResult(row.memberID, "failed", counts, response.StatusCode, duration)
				return
			}
			p.logError(row.memberID, err)
			return
		}
		result := "pushed"
		if ignored {
			result = "ignored"
		}
		p.logResult(row.memberID, result, counts, response.StatusCode, duration)
		return
	}

	status := response.StatusCode
	permanent := err == nil && status >= 300 && status < 500
	if err := p.markFailure(row, counts.latestID, permanent); err != nil {
		p.logError(row.memberID, err)
		return
	}
	p.logResult(row.memberID, "failed", counts, status, duration)
}

func responseIgnored(body []byte) bool {
	var decoded struct {
		Ignored bool `json:"ignored"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return false
	}
	return decoded.Ignored
}

func (p *Pusher) markSuccess(row outletRow, latest int64, ignored bool) error {
	now := p.clk.Now().Format(time.RFC3339Nano)
	deliveryState := DeliveryStateOK
	if ignored {
		deliveryState = DeliveryStateIgnored
	}
	ctx, cancel := p.stateContext()
	defer cancel()
	res, err := p.db.ExecContext(ctx, `
		UPDATE outlet SET last_pushed_id=?, `+PendingStateResetSQL+`,
			failure_count=CASE WHEN ? THEN failure_count ELSE 0 END,
			delivery_state=?, delivery_state_at=?, transient_since=NULL, catchup_after=NULL,
			last_pushed_at=?, updated_at=?
		WHERE member_id=? AND generation=? AND last_pushed_id=? AND pending_latest_id=? AND status=?`,
		latest, ignored, deliveryState, now, now, now,
		row.memberID, row.generation, row.lastPushedID, latest, StatusActive)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return errors.New("门铃成功后的游标推进未生效，保留同键待重试")
	}
	return nil
}

func (p *Pusher) markFailure(row outletRow, latest int64, permanent bool) error {
	now := p.clk.Now()
	nowText := now.Format(time.RFC3339Nano)
	state := retryStateRetryable
	var next any
	if permanent {
		state = retryStatePermanent
	} else {
		next = now.Add(retryDelay(row.attemptCount + 1)).Format(time.RFC3339Nano)
	}
	pause := permanent && row.failureCount+1 >= autoPauseFailureCount
	ctx, cancel := p.stateContext()
	defer cancel()
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `
		UPDATE outlet SET
			retry_state=?, pending_latest_id=?, attempt_count=attempt_count+1,
			next_attempt_at=?, failure_count=failure_count+?,
				status=CASE WHEN ? THEN ? ELSE ? END,
				pause_reason=CASE WHEN ? THEN ? ELSE NULL END,
			delivery_state=?, delivery_state_at=?,
			transient_since=CASE WHEN ? THEN NULL ELSE COALESCE(transient_since, ?) END,
			updated_at=?
		WHERE member_id=? AND generation=? AND last_pushed_id=? AND pending_latest_id=? AND status=?`,
		state, latest, next, permanentFailureIncrement(permanent),
		pause, StatusPaused, StatusActive, pause, PauseReasonOutletPaused,
		deliveryStateName(permanent), nowText, permanent, nowText, nowText,
		row.memberID, row.generation, row.lastPushedID, latest, StatusActive)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return errors.New("门铃失败状态未写入，保留同键待重试")
	}
	if pause && p.notifier != nil {
		if err := p.notifier.NotifyOutletPausedTx(ctx, tx, row.memberID, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if pause && p.notifier != nil {
		p.notifier.Wake([]int64{row.memberID})
	}
	return nil
}

func (p *Pusher) stateContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(p.ctx), stateWriteTimeout)
}

func deliveryStateName(permanent bool) string {
	if permanent {
		return DeliveryStatePermanent
	}
	return DeliveryStateTransient
}

func permanentFailureIncrement(permanent bool) int64 {
	if permanent {
		return 1
	}
	return 0
}

func retryDelay(attempt int64) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := MinPushInterval
	for i := int64(1); i < attempt; i++ {
		delay *= 2
		if delay >= MaxRetryDelay {
			return MaxRetryDelay
		}
	}
	return delay
}

func (p *Pusher) inWindow() bool {
	now := p.clk.Now()
	nowMinutes := now.Hour()*60 + now.Minute()
	return nowMinutes >= p.start.Hour()*60+p.start.Minute() &&
		nowMinutes < p.end.Hour()*60+p.end.Minute()
}

func (p *Pusher) logResult(memberID int64, result string, counts outletCounts, status int, duration time.Duration) {
	key := fmt.Sprintf("%s:%d", result, status)
	if (result == "pushed" || result == "ignored" || result == "failed") && counts.latestID > 0 {
		key = fmt.Sprintf("%s:%d:%d:%d", result, status, counts.fromID, counts.latestID)
	}
	p.mu.Lock()
	if p.lastLog[memberID] == key {
		p.mu.Unlock()
		return
	}
	p.lastLog[memberID] = key
	p.mu.Unlock()
	p.logger.Printf("at=%s result=%s member_id=%d from_id=%d latest_id=%d turn=%d collab=%d fyi=%d system=%d status=%d duration_ms=%d",
		p.clk.Now().Format(time.RFC3339Nano), result, memberID,
		counts.fromID, counts.latestID, counts.turn, counts.collab, counts.fyi, counts.system,
		status, duration.Milliseconds())
}

func (p *Pusher) logError(memberID int64, err error) {
	p.mu.Lock()
	if p.lastLog[memberID] == "error" {
		p.mu.Unlock()
		return
	}
	p.lastLog[memberID] = "error"
	p.mu.Unlock()
	p.logger.Printf("at=%s result=error member_id=%d error=%v",
		p.clk.Now().Format(time.RFC3339Nano), memberID, err)
}

// Run 每 tick 派发一次，直到 done 关闭或 Stop 取消在途请求。
func (p *Pusher) Run(done <-chan struct{}, tick time.Duration) {
	if tick <= 0 {
		tick = DefaultTick
	}
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if err := p.Dispatch(p.ctx); err != nil {
				p.logger.Printf("at=%s result=error error=%v", p.clk.Now().Format(time.RFC3339Nano), err)
			}
		}
	}
}

// Stop 停止循环并取消在途 HTTP 请求；调用方传入停机超时，返回前等待 worker 退出。
func (p *Pusher) Stop(ctx context.Context) error {
	var waitErr error
	p.stopOnce.Do(func() {
		p.cancel()
		waitErr = func() error {
			p.mu.Lock()
			busy := len(p.inFlight)
			p.mu.Unlock()
			if busy == 0 {
				return nil
			}
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
					p.mu.Lock()
					busy = len(p.inFlight)
					p.mu.Unlock()
					if busy == 0 {
						return nil
					}
				}
			}
		}()
	})
	return waitErr
}
