// Package outletstate 存放通知出口状态的受限词汇和跨包 SQL 片段。它保持
// 零依赖，identity 与 outlet 都能复用而不会形成导入环。
package outletstate

// 通知出口的生命周期与投递分级是 API、SQL 和界面共同遵守的受限词汇。
const (
	StatusActive = "active"
	StatusPaused = "paused"

	DeliveryStateOK        = "ok"
	DeliveryStateTransient = "transient"
	DeliveryStateIgnored   = "ignored"
	DeliveryStatePermanent = "permanent"

	PauseReasonMember            = "member"
	PauseReasonOutletPaused      = "outlet_paused"
	PauseReasonMemberDeactivated = "member_deactivated"
)

// 这些 SQL 片段只清空持久投递状态，不推进基线；跨包生命周期事务也复用同一份，
// 避免各处手抄字段后漏掉新增列。
const (
	// PendingStateResetSQL 清掉在途重试，供暂停、恢复、重建基线等状态迁移使用。
	PendingStateResetSQL = `pending_latest_id=NULL,retry_state='none',attempt_count=0,next_attempt_at=NULL`
	// DeliveryStateResetSQL 恢复“无故障分级”；占位符是分级时间。
	DeliveryStateResetSQL = `delivery_state='ok',delivery_state_at=?,transient_since=NULL,catchup_after=NULL`
	// LastPushedAtResetSQL 只在重建基线时使用，保留 last_pushed_id 本身。
	LastPushedAtResetSQL = `last_pushed_at=NULL`
)
