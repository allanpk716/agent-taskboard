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
}

// Real 是真实时钟。
type Real struct{}

func (Real) Now() time.Time { return time.Now().In(Beijing) }

// Fake 是可手动推进的时钟，用于测试跨天、到期等行为。
type Fake struct {
	mu  sync.Mutex
	now time.Time
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
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}
