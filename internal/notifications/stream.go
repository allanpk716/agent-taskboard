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

const (
	EventKindNotify     = "notify"
	EventKindRead       = "read"
	EventKindDisconnect = "disconnect"
)

type subscriber struct {
	mu      sync.Mutex
	pending []string
	read    StreamEvent
	wake    chan struct{}
	done    chan struct{}
	out     chan StreamEvent
}

type streamBroker struct {
	mu     sync.Mutex
	subs   map[int64]map[*subscriber]struct{}
	closed bool
}

func newStreamBroker() *streamBroker {
	return &streamBroker{subs: make(map[int64]map[*subscriber]struct{})}
}

func (b *streamBroker) subscribe(memberID int64) (<-chan StreamEvent, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	sub := &subscriber{
		wake: make(chan struct{}, 1), done: make(chan struct{}), out: make(chan StreamEvent, 1),
	}
	if b.closed {
		close(sub.out)
		return sub.out, func() {}
	}
	if b.subs[memberID] == nil {
		b.subs[memberID] = make(map[*subscriber]struct{})
	}
	b.subs[memberID][sub] = struct{}{}
	go sub.run()
	return sub.out, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		if _, ok := b.subs[memberID][sub]; ok {
			delete(b.subs[memberID], sub)
			close(sub.done)
		}
	}
}

func (b *streamBroker) publish(memberID int64, ev StreamEvent, kind string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	for sub := range b.subs[memberID] {
		sub.enqueue(ev, kind)
		select {
		case sub.wake <- struct{}{}:
		default:
		}
	}
}

func (s *subscriber) enqueue(ev StreamEvent, kind string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if kind == EventKindRead {
		s.read = ev
		for _, pending := range s.pending {
			if pending == kind {
				return
			}
		}
		s.pending = append(s.pending, kind)
		return
	}
	if len(s.pending) == 0 || s.pending[len(s.pending)-1] != kind {
		s.pending = append(s.pending, kind)
	}
}

func (s *subscriber) run() {
	for {
		select {
		case <-s.done:
			close(s.out)
			return
		case <-s.wake:
			s.mu.Lock()
			pending := s.pending
			read := s.read
			s.pending = nil
			s.mu.Unlock()
			for _, kind := range pending {
				ev := StreamEvent{Kind: kind}
				if kind == EventKindRead {
					ev = read
				}
				select {
				case s.out <- ev:
				case <-s.done:
					close(s.out)
					return
				}
			}
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
		for sub := range memberSubs {
			close(sub.done)
		}
	}
	b.subs = make(map[int64]map[*subscriber]struct{})
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
		m.streams.publish(id, StreamEvent{Kind: EventKindNotify}, EventKindNotify)
	}
}

// WakeRead 在已读事务提交后广播具体读状态。
func (m *Manager) WakeRead(memberID int64, result MarkReadResult) {
	m.streams.publish(memberID,
		StreamEvent{Kind: EventKindRead, ReadIDs: result.IDs, UnreadCount: result.UnreadCount}, EventKindRead)
}

// WakeDisconnect 要求该成员的连接立刻重新校验凭证。
func (m *Manager) WakeDisconnect(memberID int64) {
	m.streams.publish(memberID, StreamEvent{Kind: EventKindDisconnect}, EventKindDisconnect)
}

// Close 结束所有订阅，用于服务停机。
func (m *Manager) Close() { m.streams.close() }

// After 暴露注入时钟的定时唤醒，供 SSE 保活使用。
func (m *Manager) After(d time.Duration) <-chan time.Time { return m.clk.After(d) }
