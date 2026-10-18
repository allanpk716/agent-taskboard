// Package clock 提供可注入的时钟，时区固定为北京时间（UTC+8）。
// 工单编号、访客链接到期等所有时间语义都经由 Clock 取得。
package clock

import (
	"sync"
	"time"
)

// Beijing 是平台固定的时区：北京时间（UTC+8，无夏令时）。
var Beijing = time.FixedZone("UTC+8", 8*60*60)

// Clock 返回当前北京时间。生产用 Real，测试注入 Fake。
type Clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
}

// Real 是真实时钟。
type Real struct{}

func (Real) Now() time.Time { return time.Now().In(Beijing) }

func (Real) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Fake 是可手动推进的时钟，用于测试跨天、到期等行为。
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []fakeTimer
}

type fakeTimer struct {
	when time.Time
	ch   chan time.Time
}

// NewFake 以 t（会先转成北京时间）为起点构造假时钟。
func NewFake(t time.Time) *Fake {
	return &Fake{now: t.In(Beijing)}
}

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// Set 把时钟拨到 t。
func (f *Fake) Set(t time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = t.In(Beijing)
}

// Advance 把时钟向前拨 d。
func (f *Fake) Advance(d time.Duration) {
	f.advance(d)
}

func (f *Fake) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	f.timers = append(f.timers, fakeTimer{when: f.now.Add(d), ch: ch})
	return ch
}

func (f *Fake) advance(d time.Duration) {
	f.mu.Lock()
	now := f.now.Add(d)
	f.now = now
	due := make([]fakeTimer, 0, len(f.timers))
	remaining := make([]fakeTimer, 0, len(f.timers))
	for _, timer := range f.timers {
		if !timer.when.After(now) {
			due = append(due, timer)
		} else {
			remaining = append(remaining, timer)
		}
	}
	f.timers = remaining
	f.mu.Unlock()
	for _, timer := range due {
		select {
		case timer.ch <- now:
		default:
		}
	}
}
