// 内置定时调度（#43）：每天 [backup].daily_at（默认 03:00，北京时间）执行一次
// 备份；服务在那个时刻没在跑时，下次检查补做错过的那一次；目标连不上等失败
// 之后每小时重试一次，直到成功。备份时刻由注入时钟判定，RunSchedule 只负责
// 周期检查，测试里可以直接调 CheckSchedule。
package ops

import (
	"context"
	"time"
)

// CheckSchedule 做一次调度检查：到点（或需要补做/重试）就执行备份并返回 true。
// 首次调用会按最近一次成功备份初始化调度状态。备份种类按实际执行那天的日期
// 定（每月 1 日是 monthly）：补做或重试跨了天，也按当天算。
func (b *Backups) CheckSchedule(ctx context.Context) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.started {
		b.initScheduleLocked(ctx)
		b.started = true
	}
	now := b.clk.Now()
	if now.Before(b.nextDue) {
		return false
	}
	rec, err := b.BackupNow(ctx, KindForDate(now))
	if err == nil && rec.Success() {
		// 成功：下一次是严格晚于现在的第一个每日时刻。
		b.nextDue = b.nextSlotAfter(now)
	} else {
		// 失败：一小时后重试（“之后每小时重试一次，直到成功”）。
		b.nextDue = now.Add(RetryEvery)
	}
	return true
}

// RunSchedule 周期检查直到 done 关闭。tick 是真实时间的检查间隔（生产用
// DefaultScheduleTick；测试里可以调小），备份是否到点由注入时钟决定。
func (b *Backups) RunSchedule(done <-chan struct{}, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_ = b.CheckSchedule(context.Background())
		}
	}
}

// initScheduleLocked 初始化调度状态（b.mu 已持有）：最近一个每日时刻若还没有
// 成功的定时备份（服务当时没在跑），立刻补做；否则等到下一个每日时刻。
// 更新前备份（preupdate）不算定时槽位的完成。
func (b *Backups) initScheduleLocked(ctx context.Context) {
	now := b.clk.Now()
	if b.slotSatisfied(ctx, b.lastSlotAt(now)) {
		b.nextDue = b.nextSlotAfter(now)
		return
	}
	b.nextDue = now // 补做错过的那一次
}

// slotSatisfied 报告 slot 这个每日时刻是否已有成功的定时备份（daily/monthly）。
func (b *Backups) slotSatisfied(ctx context.Context, slot time.Time) bool {
	var n int
	if err := b.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM backup_records
		WHERE error IS NULL AND integrity_ok = 1 AND kind IN ('daily','monthly')
		  AND at >= ?`, slot.Format(time.RFC3339Nano)).Scan(&n); err != nil {
		return false // 查不了库就当作没做过，宁可多备一次
	}
	return n > 0
}

// lastSlotAt 返回不晚于 now 的最近一个每日时刻。
func (b *Backups) lastSlotAt(now time.Time) time.Time {
	slot := time.Date(now.Year(), now.Month(), now.Day(), b.atHour, b.atMin, 0, 0, now.Location())
	if slot.After(now) {
		return slot.AddDate(0, 0, -1)
	}
	return slot
}

// nextSlotAfter 返回严格晚于 now 的第一个每日时刻。
func (b *Backups) nextSlotAfter(now time.Time) time.Time {
	slot := time.Date(now.Year(), now.Month(), now.Day(), b.atHour, b.atMin, 0, 0, now.Location())
	if !slot.After(now) {
		slot = slot.AddDate(0, 0, 1)
	}
	return slot
}
