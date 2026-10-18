package tbtest

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// SSEEvent 是从 SSE 流读出的一帧；ping 注释用 Comment 表示。
type SSEEvent struct {
	ID      string
	Name    string
	Data    json.RawMessage
	Comment string
}

// SSEStream 是一条已打开的通知流。
type SSEStream struct {
	res    *http.Response
	cancel context.CancelFunc
	events chan SSEEvent
	errs   chan error
}

// OpenSSE 打开 /api/v1/notifications/stream；lastEventID 非空时带 Last-Event-ID。
func (s *Server) OpenSSE(t testing.TB, c *http.Client, lastEventID string) *SSEStream {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, s.url+"/api/v1/notifications/stream", nil)
	if err != nil {
		t.Fatalf("构造 SSE 请求失败：%v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	req = req.WithContext(ctx)
	if lastEventID != "" {
		req.Header.Set("Last-Event-ID", lastEventID)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("打开 SSE 流失败：%v", err)
	}
	if res.StatusCode != http.StatusOK {
		defer res.Body.Close()
		t.Fatalf("打开 SSE 流状态码 = %d, 想 200", res.StatusCode)
	}
	stream := &SSEStream{res: res, cancel: cancel, events: make(chan SSEEvent, 1), errs: make(chan error, 1)}
	t.Cleanup(stream.Close)
	go stream.read()
	return stream
}

// Response 返回原始响应，供测试检查头和状态码。
func (s *SSEStream) Response() *http.Response { return s.res }

// Event 等待下一帧，超过测试超时算失败。
func (s *SSEStream) Event(t testing.TB) SSEEvent {
	t.Helper()
	select {
	case ev := <-s.events:
		return ev
	case err := <-s.errs:
		t.Fatalf("读 SSE 流失败：%v", err)
		return SSEEvent{}
	case <-time.After(2 * time.Second):
		t.Fatal("等待 SSE 事件超时")
		return SSEEvent{}
	}
}

// ExpectNoEvent 在 timeout 内断言流上没有新帧。
func (s *SSEStream) ExpectNoEvent(t testing.TB, timeout time.Duration) {
	t.Helper()
	select {
	case ev, ok := <-s.events:
		if ok {
			t.Fatalf("SSE 流上不应有事件，收到 %+v", ev)
		}
	case err := <-s.errs:
		t.Fatalf("读 SSE 流失败：%v", err)
	case <-time.After(timeout):
	}
}

// Closed 等待服务端结束这条流。
func (s *SSEStream) Closed(t testing.TB) {
	t.Helper()
	select {
	case _, ok := <-s.events:
		if !ok {
			return
		}
	case err := <-s.errs:
		if err == nil {
			return
		}
		t.Fatalf("SSE 流异常结束：%v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("等待 SSE 流结束超时")
	}
}

// Close 关闭流和底层请求上下文。
func (s *SSEStream) Close() {
	s.cancel()
	_ = s.res.Body.Close()
}

func (s *SSEStream) read() {
	defer close(s.events)
	sc := bufio.NewScanner(s.res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var ev SSEEvent
	hasEvent := false
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if hasEvent {
				s.events <- ev
				ev = SSEEvent{}
				hasEvent = false
			}
		case strings.HasPrefix(line, "id:"):
			ev.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
			hasEvent = true
		case strings.HasPrefix(line, "event:"):
			ev.Name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			hasEvent = true
		case strings.HasPrefix(line, "data:"):
			ev.Data = append(ev.Data, strings.TrimSpace(strings.TrimPrefix(line, "data:"))...)
			hasEvent = true
		case strings.HasPrefix(line, ":"):
			ev.Comment = strings.TrimSpace(strings.TrimPrefix(line, ":"))
			hasEvent = true
		}
	}
	if err := sc.Err(); err != nil {
		s.errs <- err
	}
}
