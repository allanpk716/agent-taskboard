package notifications

import (
	"sync"
	"time"
)

// StreamEvent 是唤醒某成员 SSE 连接的进程内信号。
type StreamEvent struct {
	Kind        string
	ReadIDs     []int64
	UnreadCount int
}

type streamBroker struct {
	mu     sync.Mutex
	subs   map[int64]map[chan StreamEvent]struct{}
	closed bool
}

func newStreamBroker() *streamBroker {
	return &streamBroker{subs: make(map[int64]map[chan StreamEvent]struct{})}
}

func (b *streamBroker) subscribe(memberID int64) (<-chan StreamEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan StreamEvent, 1)
	if b.closed {
		close(ch)
		return ch, func() {}
	}
	if b.subs[memberID] == nil {
		b.subs[memberID] = make(map[chan StreamEvent]struct{})
	}
	b.subs[memberID][ch] = struct{}{}
	return ch, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[memberID][ch]; ok {
			delete(b.subs[memberID], ch)
			close(ch)
		}
	}
}

func (b *streamBroker) publish(memberID int64, ev StreamEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for ch := range b.subs[memberID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

func (b *streamBroker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for _, memberSubs := range b.subs {
		for ch := range memberSubs {
			close(ch)
		}
	}
	b.subs = make(map[int64]map[chan StreamEvent]struct{})
}

// Subscribe 订阅该成员的唤醒信号；返回的取消函数由 SSE 连接结束时调用。
func (m *Manager) Subscribe(memberID int64) (<-chan StreamEvent, func()) {
	return m.streams.subscribe(memberID)
}

// Wake 在通知事务提交后唤醒成员的全部连接；信号可合并，连接会按 id 增量查库。
func (m *Manager) Wake(memberIDs []int64) {
	seen := make(map[int64]struct{}, len(memberIDs))
	for _, id := range memberIDs {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		m.streams.publish(id, StreamEvent{Kind: "notify"})
	}
}

// WakeRead 在已读事务提交后广播具体读状态。
func (m *Manager) WakeRead(memberID int64, result MarkReadResult) {
	m.streams.publish(memberID, StreamEvent{Kind: "read", ReadIDs: result.IDs, UnreadCount: result.UnreadCount})
}

// Close 结束所有订阅，用于服务停机。
func (m *Manager) Close() { m.streams.close() }

// After 暴露注入时钟的定时唤醒，供 SSE 保活使用。
func (m *Manager) After(d time.Duration) <-chan time.Time { return m.clk.After(d) }
