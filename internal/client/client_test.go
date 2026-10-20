package client_test

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/client"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test首次连接后凭证存入桌面凭据存储(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	web := s.Login("李工", "secret-pass")
	fake.browser.web = web
	fake.browser.serverURL = s.URL()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())

	select {
	case token := <-fake.store.saved:
		if !strings.HasPrefix(token, "tbc_") {
			t.Fatalf("保存的凭证 = %q, 想以 tbc_ 开头", token)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待凭证保存超时")
	}
	fake.waitState(t, client.TrayNormal)
	if got := fake.browser.opened(t); got == "" {
		t.Fatal("客户端没有打开连接确认页")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("客户端退出出错：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待客户端退出超时")
	}
}

func Test单实例锁已被持有时Run返回已在运行错误(t *testing.T) {
	fake := newFakeDesktop(t)
	fake.lock.acquireErr = client.ErrAlreadyRunning
	app := client.New(client.Config{ServerURL: "http://localhost"}, fake.dependencies())

	err := app.Run(context.Background())
	if !errors.Is(err, client.ErrAlreadyRunning) {
		t.Fatalf("Run 错误 = %v，想 ErrAlreadyRunning", err)
	}
}

func Test新通知弹出Toast点击后标已读并打开工单(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)

	created := createAssignedTicket(t, s, s.Login("王工", "secret-pass"), "打印机无法开机", "李工")
	var shown client.Notification
	deadline := time.Now().Add(2 * time.Second)
	for {
		toasts := fake.toaster.shown(t)
		if len(toasts) == 1 {
			shown = toasts[0]
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-time.After(time.Until(deadline)):
			t.Fatalf("Toast 数量 = %d, 想 1：%+v", len(fake.toaster.shown(t)), fake.toaster.shown(t))
		}
	}
	if shown.ID == 0 || shown.Ticket.Number != created.Number || shown.Ticket.Title != "打印机无法开机" ||
		shown.Reason != "assigned" || shown.Summary != "" {
		t.Fatalf("Toast 内容 = %+v", shown)
	}
	fake.toaster.click(t, shown)

	var unread struct {
		UnreadCount int `json:"unread_count"`
	}
	deadline = time.Now().Add(2 * time.Second)
	for {
		res := s.Do(t, webLi, http.MethodGet, "/api/v1/notifications/unread", nil, &unread)
		if res.StatusCode == http.StatusOK && unread.UnreadCount == 0 {
			break
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-time.After(2 * time.Second):
			t.Fatalf("点击 Toast 后未读数 = %+v, 想 0", unread)
		}
	}
	if got := fake.browser.opened(t); got != s.URL()+"/t/"+created.Number {
		t.Fatalf("点击 Toast 打开 = %q, 想 %q", got, s.URL()+"/t/"+created.Number)
	}
	if items := fake.tray.menuUnread(); len(items) != 0 {
		t.Fatalf("点击后托盘未读菜单 = %+v, 想为空", items)
	}

	cancel()
	waitClient(t, done)
}

func Test别处标已读后托盘状态和菜单同步(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)
	webWang := s.Login("王工", "secret-pass")
	first := createAssignedTicket(t, s, webWang, "第一张", "李工")
	second := createAssignedTicket(t, s, webWang, "第二张", "李工")
	fake.toaster.wait(t, 2)
	fake.waitState(t, client.TrayUnread)
	if items := fake.tray.menuUnread(); len(items) != 2 || items[0].Ticket.Number != second.Number || items[1].Ticket.Number != first.Number {
		t.Fatalf("未读菜单 = %+v, 想第二张在前", items)
	}

	var listed struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	s.Do(t, webLi, http.MethodGet, "/api/v1/notifications?unread=1", nil, &listed)
	if len(listed.Items) != 2 {
		t.Fatalf("服务端未读 = %+v", listed)
	}
	res := s.Do(t, webLi, http.MethodPost, "/api/v1/notifications/read",
		map[string]any{"ids": []int64{listed.Items[1].ID}}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("网页标已读状态码 = %d", res.StatusCode)
	}
	fake.waitState(t, client.TrayUnread)
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if len(fake.tray.menuUnread()) == 1 && fake.tray.menuUnread()[0].Ticket.Number == second.Number {
			fake.waitState(t, client.TrayUnread)
			cancel()
			waitClient(t, done)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("read 后托盘菜单 = %+v, 想只剩第二张", fake.tray.menuUnread())
}

func Test断线后带LastEventID重连并补上新通知(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	proxy := newBreakingStreamProxy(t, s.URL())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, proxy.URL)
	fake.waitState(t, client.TrayNormal)

	webWang := s.Login("王工", "secret-pass")
	first := createAssignedTicket(t, s, webWang, "断线前", "李工")
	fake.toaster.wait(t, 1)
	reconnected := false
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if proxy.streams() >= 2 {
			reconnected = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !reconnected {
		select {
		case err := <-done:
			t.Fatalf("客户端提前退出：%v；SSE 连接数 = %d", err, proxy.streams())
		default:
		}
		t.Fatalf("等待重连超时；SSE 连接数 = %d，主动断开 = %d", proxy.streams(), proxy.broken())
	}
	select {
	case err := <-done:
		t.Logf("客户端提前退出：%v", err)
	default:
	}
	second := createAssignedTicket(t, s, webWang, "断线后", "李工")
	fake.toaster.wait(t, 2)
	if got := proxy.lastEventID(2); got != notificationIDOf(t, s, webLi, first.Number) {
		t.Fatalf("第二次 SSE Last-Event-ID = %q, 想第一条通知 id", got)
	}
	if shown := fake.toaster.shown(t); len(shown) != 2 || shown[1].Ticket.Number != second.Number {
		t.Fatalf("重连后的 Toast = %+v", shown)
	}

	cancel()
	waitClient(t, done)
}

func Test服务短暂不可用时客户端不退出并在恢复后重连(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	issued := s.IssueClientCredential(t, webLi, "TEST-PC")
	fake.store.token = issued.Token
	proxy := newOutageProxy(t, s.URL())
	proxy.setDown(true)
	t.Cleanup(proxy.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, proxy.URL)
	waitCondition(t, func() bool { return proxy.sessionChecks() >= 1 })
	select {
	case err := <-done:
		t.Fatalf("服务短暂不可用时客户端提前退出：%v", err)
	default:
	}

	proxy.setDown(false)
	fake.waitState(t, client.TrayNormal)
	cancel()
	waitClient(t, done)
}

func Test连续断线时按指数退避等待(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	issued := s.IssueClientCredential(t, webLi, "TEST-PC")
	fake.store.token = issued.Token
	var waits []time.Duration
	var mu sync.Mutex
	proxy := newFailingStreamProxy(t, s.URL(), 3)
	t.Cleanup(proxy.Close)
	fake.waitOverride = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, proxy.URL)
	waitCondition(t, func() bool { return proxy.streams() == 4 })
	got := append([]time.Duration(nil), waits...)
	if len(got) != 3 || got[0] != time.Second || got[1] != 2*time.Second || got[2] != 4*time.Second {
		t.Fatalf("退避间隔 = %v, 想 [1s 2s 4s]", got)
	}
	cancel()
	waitClient(t, done)
}

func TestSSE空闲超过六十秒时断开重连(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	issued := s.IssueClientCredential(t, webLi, "TEST-PC")
	fake.store.token = issued.Token
	proxy := newSilentStreamProxy(t, s.URL())
	t.Cleanup(proxy.Close)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, proxy.URL)
	waitCondition(t, func() bool { return proxy.streams() == 1 })
	waitForFakeTimer(t, fake.clock, time.Minute)
	fake.clock.Advance(61 * time.Second)
	waitCondition(t, func() bool { return proxy.streams() >= 2 })
	select {
	case err := <-done:
		t.Fatalf("空闲看门狗不应结束客户端：%v", err)
	default:
	}
	cancel()
	waitClient(t, done)
}

func Test凭证撤销后需要登录点击托盘后重新连接(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	ctx, cancel := context.WithCancel(context.Background())
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)

	revokeOnlyClient(t, s, webLi)
	cancel()
	waitClient(t, done)
	drainChannel(fake.store.saved)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done2 := fake.startClient(t, ctx2, s.URL())
	fake.waitState(t, client.TrayNeedLogin)
	toasts := fake.toaster.shown(t)
	if len(toasts) != 1 || toasts[0].Reason != "relogin" || toasts[0].Text != "需要登录 Taskboard" {
		t.Fatalf("需要登录 Toast = %+v", toasts)
	}
	if fake.store.current(t) != "" {
		t.Fatal("凭证失效后仍留在本机存储")
	}
	select {
	case <-fake.store.saved:
		t.Fatal("没有用户点击重新登录时不应自动重连")
	case <-time.After(100 * time.Millisecond):
	}

	fake.tray.click(t, "reconnect")
	fake.waitState(t, client.TrayNormal)
	if fake.store.current(t) == "" {
		t.Fatal("重新登录后没有保存新凭证")
	}
	if clients := listClients(t, s, webLi); len(clients) != 1 {
		t.Fatalf("重新登录后的电脑数 = %d, 想 1", len(clients))
	}
	cancel2()
	waitClient(t, done2)
}

func Test退出登录撤销服务端凭证并删除本机凭证(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)

	fake.tray.click(t, "logout")
	select {
	case <-fake.store.deleted:
	case <-time.After(2 * time.Second):
		t.Fatal("退出登录没有删除本机凭证")
	}
	fake.waitState(t, client.TrayNeedLogin)
	if clients := listClients(t, s, webLi); len(clients) != 0 {
		t.Fatalf("退出登录后的电脑数 = %d, 想 0", len(clients))
	}
	if fake.store.current(t) != "" {
		t.Fatal("退出登录后本机仍有凭证")
	}

	cancel()
	waitClient(t, done)
}

func Test全部标为已读使用已见最大通知(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	fake.browser.web = webLi
	fake.browser.serverURL = s.URL()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)
	webWang := s.Login("王工", "secret-pass")
	createAssignedTicket(t, s, webWang, "已读一", "李工")
	createAssignedTicket(t, s, webWang, "已读二", "李工")
	fake.toaster.wait(t, 2)

	fake.tray.click(t, "all-read")
	var unread struct {
		UnreadCount int `json:"unread_count"`
	}
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		s.Do(t, webLi, http.MethodGet, "/api/v1/notifications/unread", nil, &unread)
		if unread.UnreadCount == 0 && len(fake.tray.menuUnread()) == 0 {
			cancel()
			waitClient(t, done)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("全部已读后服务端/托盘 = %+v/%+v", unread, fake.tray.menuUnread())
}

func Test查看全部打开成员收件箱(t *testing.T) {
	fake := newFakeDesktop(t)
	s := fake.startService(t)
	webLi := s.Login("李工", "secret-pass")
	issued := s.IssueClientCredential(t, webLi, "TEST-PC")
	fake.store.token = issued.Token
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := fake.startClient(t, ctx, s.URL())
	fake.waitState(t, client.TrayNormal)

	fake.tray.click(t, "view-all")
	if got := fake.browser.opened(t); got != s.URL()+"/" {
		t.Fatalf("查看全部打开 = %q, 想成员收件箱 %q", got, s.URL()+"/")
	}
	cancel()
	waitClient(t, done)
}

type fakeDesktop struct {
	t            testing.TB
	service      *tbtest.Server
	browser      *fakeBrowser
	store        *fakeCredentialStore
	tray         *fakeTray
	toaster      *fakeToaster
	lifecycle    client.LifecycleDependencies
	updater      *fakeUpdateAdapter
	clock        *clock.Fake
	lock         *fakeInstanceLock
	waitOverride func(context.Context, time.Duration) error
}

func newFakeDesktop(t testing.TB) *fakeDesktop {
	return &fakeDesktop{
		t:       t,
		browser: &fakeBrowser{},
		store:   newFakeCredentialStore(),
		tray:    newFakeTray(),
		toaster: newFakeToaster(),
		clock:   clock.NewFake(time.Now()),
		lock:    newFakeInstanceLock(),
	}
}

func (d *fakeDesktop) startService(t testing.TB) *tbtest.Server {
	t.Helper()
	s := tbtest.Start(t)
	s.CreateAdmin("王工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	d.service = s
	return s
}

func (d *fakeDesktop) startClient(t testing.TB, ctx context.Context, serverURL string) <-chan error {
	t.Helper()
	app := client.New(client.Config{
		ServerURL: serverURL,
		Version:   "0.2.0",
		Hostname:  "TEST-PC",
		Nonces:    fixedNonces,
		RetryWait: d.waitRetry,
	}, d.dependencies())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()
	return done
}

func (d *fakeDesktop) waitRetry(ctx context.Context, delay time.Duration) error {
	if d.waitOverride != nil {
		return d.waitOverride(ctx, delay)
	}
	return ctx.Err()
}

func (d *fakeDesktop) dependencies() client.Dependencies {
	deps := client.Dependencies{
		Tray: d.tray, Toaster: d.toaster, Store: d.store, Browser: d.browser,
		Lock: d.lock, Lifecycle: &d.lifecycle, Clock: d.clock,
	}
	if d.updater != nil {
		deps.Update = d.updater
	}
	return deps
}

func (d *fakeDesktop) waitState(t testing.TB, want client.TrayState) {
	t.Helper()
	for {
		if got := d.tray.state(); got == want {
			return
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-time.After(2 * time.Second):
			t.Fatalf("托盘状态 = %q, 想 %q", d.tray.state(), want)
		}
	}
}

type fakeBrowser struct {
	mu        sync.Mutex
	urls      []string
	web       *http.Client
	serverURL string
}

func (b *fakeBrowser) Open(u string) error {
	b.mu.Lock()
	b.urls = append(b.urls, u)
	b.mu.Unlock()
	if strings.Contains(u, "/client/connect?") {
		return b.confirm(u)
	}
	return nil
}

func (b *fakeBrowser) confirm(connectURL string) error {
	u, err := url.Parse(connectURL)
	if err != nil {
		return err
	}
	q := u.Query()
	sum := sha256.Sum256([]byte(fakeVerifier()))
	body, err := json.Marshal(map[string]any{
		"port":           q.Get("port"),
		"state":          q.Get("state"),
		"code_challenge": base64.RawURLEncoding.EncodeToString(sum[:]),
		"hostname":       q.Get("hostname"),
	})
	if err != nil {
		return err
	}
	res, err := b.web.Post(b.serverURL+"/api/v1/client/connect", "application/json", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(res.Body)
		return fmt.Errorf("确认连接状态码 %d：%s", res.StatusCode, data)
	}
	var redirect struct {
		RedirectURL string `json:"redirect_url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&redirect); err != nil {
		return err
	}
	cb, err := http.Get(redirect.RedirectURL)
	if err != nil {
		return err
	}
	defer cb.Body.Close()
	if cb.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(cb.Body)
		return fmt.Errorf("客户端回调状态码 %d：%s", cb.StatusCode, data)
	}
	return nil
}

func (b *fakeBrowser) opened(t testing.TB) string {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.urls) == 0 {
		return ""
	}
	return b.urls[len(b.urls)-1]
}

type fakeCredentialStore struct {
	mu      sync.Mutex
	token   string
	saved   chan string
	deleted chan struct{}
}

func newFakeCredentialStore() *fakeCredentialStore {
	return &fakeCredentialStore{saved: make(chan string, 10), deleted: make(chan struct{}, 10)}
}

func (s *fakeCredentialStore) Get() (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token, s.token != "", nil
}

func (s *fakeCredentialStore) Save(token string) error {
	s.mu.Lock()
	s.token = token
	s.mu.Unlock()
	s.saved <- token
	return nil
}

func (s *fakeCredentialStore) Delete() error {
	s.mu.Lock()
	s.token = ""
	s.mu.Unlock()
	s.deleted <- struct{}{}
	return nil
}

func (s *fakeCredentialStore) current(t testing.TB) string {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

type fakeTray struct {
	mu        sync.Mutex
	current   client.TrayState
	unread    []client.Notification
	callbacks client.TrayCallbacks
}

func newFakeTray() *fakeTray {
	return &fakeTray{current: client.TrayDisconnected}
}

func (tr *fakeTray) Run(ctx context.Context, cb client.TrayCallbacks) error {
	tr.mu.Lock()
	tr.callbacks = cb
	tr.mu.Unlock()
	<-ctx.Done()
	return nil
}

func (tr *fakeTray) SetState(state client.TrayState) {
	tr.mu.Lock()
	tr.current = state
	tr.mu.Unlock()
}

func (tr *fakeTray) SetUnread(items []client.Notification) {
	tr.mu.Lock()
	tr.unread = append([]client.Notification(nil), items...)
	tr.mu.Unlock()
}

func (tr *fakeTray) state() client.TrayState {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.current
}

func (tr *fakeTray) menuUnread() []client.Notification {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]client.Notification(nil), tr.unread...)
}

func (tr *fakeTray) click(t testing.TB, name string) {
	t.Helper()
	tr.mu.Lock()
	cb := tr.callbacks
	tr.mu.Unlock()
	switch name {
	case "reconnect":
		cb.Reconnect()
	case "all-read":
		cb.MarkAllRead()
	case "view-all":
		cb.ViewAll()
	case "logout":
		cb.Logout()
	case "uninstall":
		cb.Uninstall()
	}
}

type fakeToaster struct {
	mu      sync.Mutex
	toasts  []client.Notification
	onClick func(client.Notification)
}

func newFakeToaster() *fakeToaster {
	return &fakeToaster{}
}

func (to *fakeToaster) Run(ctx context.Context, onClick func(client.Notification)) error {
	to.mu.Lock()
	to.onClick = onClick
	to.mu.Unlock()
	<-ctx.Done()
	return nil
}

func (to *fakeToaster) Show(n client.Notification) {
	to.mu.Lock()
	to.toasts = append(to.toasts, n)
	to.mu.Unlock()
}

func (to *fakeToaster) click(t testing.TB, n client.Notification) {
	t.Helper()
	to.mu.Lock()
	onClick := to.onClick
	to.mu.Unlock()
	if onClick == nil {
		t.Fatal("Toast 点击回调尚未注册")
	}
	onClick(n)
}

func (to *fakeToaster) shown(t testing.TB) []client.Notification {
	to.mu.Lock()
	defer to.mu.Unlock()
	return append([]client.Notification(nil), to.toasts...)
}

func (to *fakeToaster) wait(t testing.TB, want int) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if len(to.shown(t)) == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("等待 %d 条 Toast 超时，当前 %+v", want, to.shown(t))
}

func fixedNonces(n int) ([]byte, error) {
	return []byte(strings.Repeat("7", n)), nil
}

func fakeVerifier() string {
	return base64.RawURLEncoding.EncodeToString(bytesFixed(43))
}

func bytesFixed(n int) []byte { return []byte(strings.Repeat("7", n)) }

type createdTicket struct {
	Number string `json:"number"`
}

func createAssignedTicket(t testing.TB, s *tbtest.Server, c *http.Client, title, assignee string) createdTicket {
	t.Helper()
	var created createdTicket
	res := s.Do(t, c, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": title, "brief_md": "背景：……", "labels": []string{"机型:UD-2300"}, "assignee": assignee,
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	return created
}

func waitClient(t testing.TB, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("客户端退出出错：%v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待客户端退出超时")
	}
}

func waitCondition(t testing.TB, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("等待条件超时")
}

func waitForFakeTimer(t testing.TB, fake *clock.Fake, d time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !fake.WaitForTimer(ctx, d) {
		t.Fatalf("等待 %v 的假时钟计时器注册超时", d)
	}
}

func drainChannel[T any](ch <-chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

type breakingStreamProxy struct {
	*httptest.Server
	mu         sync.Mutex
	ids        []string
	breakFirst bool
	breaks     int
}

type outageProxy struct {
	*httptest.Server
	mu       sync.Mutex
	down     bool
	sessions int
}

func newOutageProxy(t testing.TB, backend string) *outageProxy {
	t.Helper()
	target, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	p := &outageProxy{down: true}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/session" {
			p.mu.Lock()
			p.sessions++
			down := p.down
			p.mu.Unlock()
			if down {
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	return p
}

func (p *outageProxy) setDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
}

func (p *outageProxy) sessionChecks() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessions
}

type failingStreamProxy struct {
	*httptest.Server
	mu       sync.Mutex
	count    int
	failures int
}

func newFailingStreamProxy(t testing.TB, backend string, failures int) *failingStreamProxy {
	t.Helper()
	target, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	p := &failingStreamProxy{failures: failures}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notifications/stream" {
			proxy.ServeHTTP(w, r)
			return
		}
		p.mu.Lock()
		p.count++
		fail := p.count <= p.failures
		p.mu.Unlock()
		if fail {
			http.Error(w, "stream unavailable", http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	return p
}

func (p *failingStreamProxy) streams() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

type silentStreamProxy struct {
	*httptest.Server
	mu  sync.Mutex
	sse int
}

func newSilentStreamProxy(t testing.TB, backend string) *silentStreamProxy {
	t.Helper()
	target, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	p := &silentStreamProxy{}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notifications/stream" {
			proxy.ServeHTTP(w, r)
			return
		}
		p.mu.Lock()
		p.sse++
		p.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
	}))
	return p
}

func (p *silentStreamProxy) streams() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sse
}

type fakeInstanceLock struct {
	mu         sync.Mutex
	held       bool
	acquired   int
	acquireErr error
}

func newFakeInstanceLock() *fakeInstanceLock { return &fakeInstanceLock{} }

func (l *fakeInstanceLock) Acquire() (func(), error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.acquireErr != nil {
		return nil, l.acquireErr
	}
	if l.held {
		return nil, errors.New("already running")
	}
	l.held = true
	l.acquired++
	return func() {
		l.mu.Lock()
		l.held = false
		l.mu.Unlock()
	}, nil
}

func (l *fakeInstanceLock) AcquireMust(t testing.TB) func() {
	t.Helper()
	release, err := l.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func (l *fakeInstanceLock) acquiredCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.acquired
}

func (l *fakeInstanceLock) heldByUpdate() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

func newBreakingStreamProxy(t testing.TB, backend string) *breakingStreamProxy {
	t.Helper()
	target, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	normal := httputil.NewSingleHostReverseProxy(target)
	p := &breakingStreamProxy{breakFirst: true}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/notifications/stream" {
			normal.ServeHTTP(w, r)
			return
		}
		p.serveStream(w, r, backend)
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *breakingStreamProxy) serveStream(w http.ResponseWriter, r *http.Request, backend string) {
	p.mu.Lock()
	p.ids = append(p.ids, r.Header.Get("Last-Event-ID"))
	p.mu.Unlock()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, backend+"/api/v1/notifications/stream", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header = r.Header.Clone()
	res, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer res.Body.Close()
	p.mu.Lock()
	closeConnection := p.breakFirst
	p.mu.Unlock()
	if closeConnection {
		w.Header().Set("Connection", "close")
	}
	for k, vs := range res.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	flusher := w.(http.Flusher)
	sc := bufio.NewScanner(res.Body)
	var event string
	for sc.Scan() {
		line := sc.Text()
		if _, err := fmt.Fprintln(w, line); err != nil {
			return
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if line == "" {
			flusher.Flush()
			p.mu.Lock()
			shouldBreak := p.breakFirst
			p.mu.Unlock()
			if shouldBreak && event == "notification" {
				p.mu.Lock()
				p.breakFirst = false
				p.breaks++
				p.mu.Unlock()
				if conn, _, err := http.NewResponseController(w).Hijack(); err == nil {
					_ = conn.Close()
				}
				panic(http.ErrAbortHandler)
			}
			event = ""
		}
	}
}

func (p *breakingStreamProxy) broken() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.breaks
}

func (p *breakingStreamProxy) streams() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.ids)
}

func (p *breakingStreamProxy) lastEventID(n int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 || n > len(p.ids) {
		return ""
	}
	return p.ids[n-1]
}

func notificationIDOf(t testing.TB, s *tbtest.Server, c *http.Client, number string) string {
	t.Helper()
	var body struct {
		Items []struct {
			ID     int64 `json:"id"`
			Ticket struct {
				Number string `json:"number"`
			} `json:"ticket"`
		} `json:"items"`
	}
	s.Do(t, c, http.MethodGet, "/api/v1/notifications?after=0", nil, &body)
	for _, n := range body.Items {
		if n.Ticket.Number == number {
			return strconv.FormatInt(n.ID, 10)
		}
	}
	t.Fatalf("找不到 %s 的通知", number)
	return ""
}

func revokeOnlyClient(t testing.TB, s *tbtest.Server, web *http.Client) {
	t.Helper()
	clients := listClients(t, s, web)
	if len(clients) != 1 {
		t.Fatalf("撤销前电脑数 = %d, 想 1", len(clients))
	}
	res := s.Do(t, web, http.MethodDelete, "/api/v1/me/clients/"+strconv.FormatInt(clients[0], 10), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销客户端凭证状态码 = %d, 想 204", res.StatusCode)
	}
}

func listClients(t testing.TB, s *tbtest.Server, web *http.Client) []int64 {
	t.Helper()
	var body struct {
		Clients []struct {
			ID int64 `json:"id"`
		} `json:"clients"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/me/clients", nil, &body)
	ids := make([]int64, 0, len(body.Clients))
	for _, c := range body.Clients {
		ids = append(ids, c.ID)
	}
	return ids
}
