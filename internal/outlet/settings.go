package outlet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// ErrOutletNotFound 表示这位成员还没有通知出口配置。
var ErrOutletNotFound = errors.New("还没有通知出口配置")

// TestResult 是测试按钮的结果分类；只含状态码，不携带目标响应 body。
type TestResult struct {
	Kind       string
	HTTPStatus int
}

// ValidBotID 供 HTTP 层在进入存储前重复同一白名单校验。
func ValidBotID(botID string) bool {
	return botIDPattern.MatchString(botID)
}

// Get 读取这位成员的非敏感配置。webhook key 只解出末四位用于界面识别。
func (m *Manager) Get(ctx context.Context, memberID int64) (Configuration, error) {
	if memberID <= 0 {
		return Configuration{}, ErrOutletNotFound
	}
	if m.cipher == nil {
		return Configuration{}, ErrMasterKeyMissing
	}
	var (
		out             Configuration
		keyCipher       string
		pauseReason     sql.NullString
		deliveryStateAt sql.NullString
		transientSince  sql.NullString
	)
	out.MemberID = memberID
	err := m.db.QueryRowContext(ctx, `
		SELECT bot_id,key_cipher,last_pushed_id,generation,status,failure_count,pause_reason,
			delivery_state,delivery_state_at,transient_since
		FROM outlet WHERE member_id=?`, memberID,
	).Scan(&out.BotID, &keyCipher, &out.LastPushedID, &out.Generation, &out.Status,
		&out.FailureCount, &pauseReason, &out.DeliveryState, &deliveryStateAt, &transientSince)
	if errors.Is(err, sql.ErrNoRows) {
		return Configuration{}, ErrOutletNotFound
	}
	if err != nil {
		return Configuration{}, err
	}
	key, err := m.cipher.Decrypt(keyCipher)
	if err != nil {
		return Configuration{}, err
	}
	out.KeyLast4 = keyLast4(key)
	if pauseReason.Valid {
		reason := pauseReason.String
		out.PauseReason = &reason
	}
	if deliveryStateAt.Valid {
		at := deliveryStateAt.String
		out.DeliveryStateAt = &at
	}
	if transientSince.Valid {
		since := transientSince.String
		out.TransientSince = &since
	}
	return out, nil
}

// Pause 暂停推送；游标与代数保持不动，恢复后由推送循环决定补推口径。
func (m *Manager) Pause(ctx context.Context, memberID int64) (Configuration, error) {
	return m.setStatus(ctx, memberID, StatusPaused, PauseReasonMember)
}

// Resume 恢复推送；不重建基线，last_pushed_id 原样保留。
func (m *Manager) Resume(ctx context.Context, memberID int64) (Configuration, error) {
	return m.setStatus(ctx, memberID, StatusActive, "")
}

func (m *Manager) setStatus(ctx context.Context, memberID int64, status, pauseReason string) (Configuration, error) {
	if memberID <= 0 {
		return Configuration{}, ErrOutletNotFound
	}
	now := m.clk.Now().Format(time.RFC3339Nano)
	resume := status == StatusActive
	var catchupAfter any
	if resume {
		catchupAfter = m.clk.Now().Add(-ResumeCatchupWindow).Format(time.RFC3339Nano)
	}
	result, err := m.db.ExecContext(ctx, `
		UPDATE outlet
		SET status=?,failure_count=0,pause_reason=?,
			`+PendingStateResetSQL+`,
			delivery_state=CASE WHEN ? THEN ? ELSE delivery_state END,
			delivery_state_at=CASE WHEN ? THEN ? ELSE delivery_state_at END,
			transient_since=CASE WHEN ? THEN NULL ELSE transient_since END,
			catchup_after=?,updated_at=?
		WHERE member_id=?`,
		status, nullString(pauseReason), resume, DeliveryStateOK, resume, now, resume, catchupAfter, now, memberID)
	if err != nil {
		return Configuration{}, err
	}
	if rows, err := result.RowsAffected(); err != nil || rows == 0 {
		if err != nil {
			return Configuration{}, err
		}
		return Configuration{}, ErrOutletNotFound
	}
	return m.Get(ctx, memberID)
}

// Delete 删除这位成员的 outlet；删除后不会继续推送，重建配置时重新建基线。
func (m *Manager) Delete(ctx context.Context, memberID int64) error {
	if memberID <= 0 {
		return ErrOutletNotFound
	}
	result, err := m.db.ExecContext(ctx, `DELETE FROM outlet WHERE member_id=?`, memberID)
	if err != nil {
		return err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return err
	} else if rows == 0 {
		return ErrOutletNotFound
	}
	return nil
}

// SendTest 立即发送一次 taskboard.test。它不重试；每次由调用方传入当前时间，
// 幂等键按成员和 epoch-ms 生成，保证同一毫秒后的每次确认都是新键。
func (m *Manager) SendTest(ctx context.Context, memberID int64, builder *URLBuilder,
	client *Client, now time.Time) (TestResult, error) {
	if memberID <= 0 || m == nil || builder == nil || client == nil {
		return TestResult{}, ErrOutletNotFound
	}
	var botID, keyCipher string
	err := m.db.QueryRowContext(ctx,
		`SELECT bot_id,key_cipher FROM outlet WHERE member_id=?`, memberID,
	).Scan(&botID, &keyCipher)
	if errors.Is(err, sql.ErrNoRows) {
		return TestResult{}, ErrOutletNotFound
	}
	if err != nil {
		return TestResult{}, err
	}
	if m.cipher == nil {
		return TestResult{}, ErrMasterKeyMissing
	}
	key, err := m.cipher.Decrypt(keyCipher)
	if err != nil {
		return TestResult{}, err
	}
	endpoint, err := builder.Build(botID)
	if err != nil {
		return TestResult{}, err
	}
	idempotencyKey := fmt.Sprintf("taskboard-test:%d:%d", memberID, now.UnixMilli())
	response, err := client.Post(ctx, endpoint, http.Header{
		"X-Webhook-Key":     []string{key},
		"Authorization":     []string{"Bearer " + key},
		"X-Idempotency-Key": []string{idempotencyKey},
	}, []byte(`{"event":"taskboard.test"}`))
	if err != nil {
		return TestResult{Kind: "timeout"}, nil
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized:
		return TestResult{Kind: "unauthorized", HTTPStatus: response.StatusCode}, nil
	case response.IsSuccess():
		var body struct {
			Ignored bool `json:"ignored"`
		}
		_ = json.Unmarshal(response.Body, &body)
		if body.Ignored {
			return TestResult{Kind: "ignored", HTTPStatus: response.StatusCode}, nil
		}
		return TestResult{Kind: "ok", HTTPStatus: response.StatusCode}, nil
	default:
		return TestResult{Kind: "http_error", HTTPStatus: response.StatusCode}, nil
	}
}

func keyLast4(key string) string {
	runes := []rune(key)
	if len(runes) <= 4 {
		return string(runes)
	}
	return string(runes[len(runes)-4:])
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
