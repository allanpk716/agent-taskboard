// Package client 实现平台无关的桌面客户端核心；托盘、Toast、凭据存储和
// 打开浏览器等系统行为都通过 Dependencies 注入。
package client

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

// TrayState 是托盘图标对用户可见的状态。
type TrayState string

const (
	TrayNormal       TrayState = "normal"
	TrayUnread       TrayState = "unread"
	TrayNeedLogin    TrayState = "need-login"
	TrayDisconnected TrayState = "disconnected"
	TrayOutdated     TrayState = "outdated"
)

// Notification 是客户端对外展示的一条通知。
type Notification struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Reason  string `json:"reason"`
	Text    string `json:"text"`
	Summary string `json:"summary"`
	Ticket  struct {
		Number string `json:"number"`
		Title  string `json:"title"`
		URL    string `json:"url"`
	} `json:"ticket"`
}

// Config 是客户端行为配置。
type Config struct {
	ServerURL string
	Version   string
	Hostname  string
	Nonces    func(int) ([]byte, error)
	RetryWait func(context.Context, time.Duration) error
	// AfterUpdate 表示本进程是自动更新启动的新进程；初始化后要通知旧进程退出。
	AfterUpdate bool
	// AfterInstall 表示本进程是覆盖安装后的新进程；旧实例可能还未释放单实例锁。
	AfterInstall bool
}

// TrayCallbacks 是托盘菜单命令回到核心的入口。
type TrayCallbacks struct {
	Reconnect   func()
	MarkAllRead func()
	ViewAll     func()
	Logout      func()
	Uninstall   func()
	Quit        func()
	OpenUnread  func(Notification)
}

// Tray 抽象托盘图标、菜单和菜单事件。
type Tray interface {
	Run(context.Context, TrayCallbacks) error
	SetState(TrayState)
	SetUnread([]Notification)
}

// Toaster 抽象系统通知及其点击事件。
type Toaster interface {
	Run(context.Context, func(Notification)) error
	Show(Notification)
}

// CredentialStore 抽象本机凭据存储。
type CredentialStore interface {
	Get() (string, bool, error)
	Save(string) error
	Delete() error
}

// Browser 抽象系统浏览器。
type Browser interface {
	Open(string) error
}

// InstanceLock 抽象“同一用户只运行一个客户端”的系统能力。
type InstanceLock interface {
	Acquire() (release func(), err error)
}

// Dependencies 是必须由装配层提供的桌面适配器。
type Dependencies struct {
	Tray      Tray
	Toaster   Toaster
	Store     CredentialStore
	Browser   Browser
	Lock      InstanceLock
	Lifecycle *LifecycleDependencies
	Update    UpdateProcessManager
	Clock     clock.Clock
}

// App 是客户端核心。
type App struct {
	cfg  Config
	deps Dependencies
	ctx  context.Context

	mu                sync.Mutex
	token             string
	lastID            int64
	unread            []Notification
	needLoginPrompted bool
	updating          bool
	initialUpdateDone bool
	streamCount       int
	failures          int
	loginRequired     bool
	loginSignal       chan struct{}
	tokenSignal       chan struct{}
	quitSignal        chan struct{}
	streamCancel      context.CancelFunc
	shutdown          context.CancelFunc
	httpClient        *http.Client
	streamClient      *http.Client
}

// New 装配客户端核心。
func New(cfg Config, deps Dependencies) *App {
	if cfg.Version == "" {
		cfg.Version = "0.0.0"
	}
	if cfg.Hostname == "" {
		cfg.Hostname = "taskboard-desktop"
	}
	if cfg.Nonces == nil {
		cfg.Nonces = randomBytes
	}
	if cfg.RetryWait == nil {
		cfg.RetryWait = waitContext
	}
	cfg.ServerURL = strings.TrimRight(cfg.ServerURL, "/")
	if deps.Clock == nil {
		deps.Clock = clock.Real{}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &App{cfg: cfg, deps: deps, loginSignal: make(chan struct{}, 1), tokenSignal: make(chan struct{}, 1),
		httpClient: &http.Client{Timeout: 30 * time.Second}, streamClient: &http.Client{Transport: transport}}
}

// Run 阻塞运行客户端，直到 ctx 取消或收到另一实例的退出信号。
func (a *App) Run(ctx context.Context) error {
	runCtx, cancel := context.WithCancel(ctx)
	a.ctx, a.shutdown = runCtx, cancel
	defer cancel()
	if a.deps.Update != nil {
		_ = a.deps.Update.ResetQuit()
	}
	if a.cfg.AfterUpdate && a.deps.Update != nil {
		_ = a.deps.Update.SendQuit()
	}
	var releaseLock func()
	if a.deps.Lock != nil {
		if a.cfg.AfterUpdate || a.cfg.AfterInstall {
			var err error
			releaseLock, err = a.acquireLockAfterHandoff(runCtx)
			if err != nil {
				return err
			}
		} else {
			var err error
			releaseLock, err = a.deps.Lock.Acquire()
			if err != nil {
				return err
			}
		}
	}
	if releaseLock != nil {
		defer releaseLock()
	}
	a.deps.Tray.SetState(TrayDisconnected)
	trayDone := a.runAdapter(runCtx, func() error {
		return a.deps.Tray.Run(runCtx, TrayCallbacks{
			Reconnect:   a.reconnect,
			MarkAllRead: a.markAllRead,
			ViewAll:     a.viewAll,
			Logout:      a.logout,
			Uninstall:   a.uninstall,
			Quit:        cancel,
			OpenUnread:  a.openNotification,
		})
	})
	toastDone := a.runAdapter(runCtx, func() error {
		return a.deps.Toaster.Run(runCtx, a.openNotification)
	})
	if a.cfg.AfterUpdate && a.deps.Update != nil {
		_ = a.deps.Update.ResetQuit()
		_ = a.deps.Update.SendReady()
		a.deps.Toaster.Show(Notification{Reason: "updated", Text: "客户端已更新到 " + a.cfg.Version})
	}
	a.quitSignal = make(chan struct{})
	if a.deps.Update != nil {
		go func() {
			select {
			case <-runCtx.Done():
			case <-a.deps.Update.Quit():
				close(a.quitSignal)
				a.mu.Lock()
				updating := a.updating
				a.mu.Unlock()
				if !updating {
					cancel()
				}
			}
		}()
	}
	updateDone := make(chan struct{})
	if a.deps.Update != nil {
		go func() {
			defer close(updateDone)
			a.updateLoop(runCtx)
		}()
	} else {
		close(updateDone)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- a.serve(runCtx) }()
	var err error
	select {
	case err = <-serveDone:
	case <-a.quitSignal:
		err = <-serveDone
	}
	cancel()
	<-trayDone
	<-toastDone
	<-updateDone
	return err
}

func (a *App) acquireLockAfterHandoff(ctx context.Context) (func(), error) {
	deadline := a.deps.Clock.After(30 * time.Second)
	for {
		release, err := a.deps.Lock.Acquire()
		if err == nil {
			return release, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-deadline:
			return nil, err
		case <-a.deps.Clock.After(100 * time.Millisecond):
		}
	}
}

func (a *App) runAdapter(ctx context.Context, run func() error) <-chan error {
	done := make(chan error, 1)
	go func() { done <- run() }()
	return done
}

func (a *App) serve(ctx context.Context) error {
	for ctx.Err() == nil {
		if a.waitIfLoginRequired(ctx) {
			return nil
		}
		token, ok, err := a.deps.Store.Get()
		if err != nil {
			return err
		}
		if !ok {
			token, err = a.connect(ctx)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				a.deps.Tray.SetState(TrayNeedLogin)
				return err
			}
		}
		a.mu.Lock()
		a.token = token
		a.needLoginPrompted = false
		a.mu.Unlock()
		a.deps.Tray.SetState(TrayNormal)
		minVersion, err := a.serverMinVersion(ctx, token)
		if errors.Is(err, errInvalidCredential) {
			a.enterNeedLogin()
			a.deps.Tray.SetUnread(nil)
			a.deps.Tray.SetState(TrayNeedLogin)
			continue
		}
		if err != nil {
			if !a.retryAfterFailure(ctx, err) {
				return nil
			}
			continue
		}
		if version.Less(a.cfg.Version, minVersion) {
			a.deps.Tray.SetUnread(nil)
			a.deps.Tray.SetState(TrayOutdated)
			a.deps.Toaster.Show(Notification{
				Reason: "outdated", Text: "客户端版本过旧，请重新下载",
				Ticket: struct {
					Number string `json:"number"`
					Title  string `json:"title"`
					URL    string `json:"url"`
				}{URL: "/me#client"},
			})
			<-ctx.Done()
			return nil
		}
		if a.deps.Update != nil && !a.initialUpdateDone {
			a.initialUpdateDone = true
			if a.checkUpdate(ctx, token) {
				return nil
			}
		}
		select {
		case a.tokenSignal <- struct{}{}:
		default:
		}
		err = a.stream(ctx, token)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, errInvalidCredential) {
			a.enterNeedLogin()
			a.deps.Tray.SetUnread(nil)
			a.deps.Tray.SetState(TrayNeedLogin)
			continue
		}
		if err != nil {
			a.deps.Tray.SetState(TrayDisconnected)
			a.retryAfterFailure(ctx, err)
		}
	}
	return nil
}

func (a *App) retryAfterFailure(ctx context.Context, err error) bool {
	_ = err
	a.deps.Tray.SetState(TrayDisconnected)
	a.mu.Lock()
	delay := time.Second << a.failures
	if delay <= 0 || delay > time.Minute {
		delay = time.Minute
	}
	a.failures++
	a.mu.Unlock()
	return a.cfg.RetryWait(ctx, delay) == nil
}

func (a *App) serverMinVersion(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.ServerURL+"/api/v1/session", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", a.userAgent())
	res, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return "", errInvalidCredential
	}
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return "", fmt.Errorf("session: status %d: %s", res.StatusCode, data)
	}
	var body struct {
		MinClientVersion string `json:"min_client_version"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.MinClientVersion, nil
}

func (a *App) updateLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.tokenSignal:
		}
		for ctx.Err() == nil {
			select {
			case <-ctx.Done():
				return
			case <-a.deps.Clock.After(6 * time.Hour):
			}
			a.mu.Lock()
			token := a.token
			a.mu.Unlock()
			if a.checkUpdate(ctx, token) {
				return
			}
		}
	}
}

func (a *App) waitIfLoginRequired(ctx context.Context) bool {
	a.mu.Lock()
	required := a.loginRequired
	a.mu.Unlock()
	if !required {
		return false
	}
	select {
	case <-ctx.Done():
		return true
	case <-a.loginSignal:
		a.mu.Lock()
		a.loginRequired = false
		a.mu.Unlock()
		return false
	}
}

func (a *App) connect(ctx context.Context) (string, error) {
	state, err := a.nonce(16)
	if err != nil {
		return "", err
	}
	verifier, err := a.nonce(43)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	mux := http.NewServeMux()
	callback := make(chan string, 1)
	serverError := make(chan error, 1)
	mux.HandleFunc("/cb", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		select {
		case callback <- code:
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, "callback already used", http.StatusBadRequest)
		}
	})
	srv := &http.Server{Handler: mux}
	go func() { serverError <- srv.Serve(ln) }()
	q := url.Values{}
	q.Set("port", strconv.Itoa(port))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("hostname", a.cfg.Hostname)
	if err := a.deps.Browser.Open(a.cfg.ServerURL + "/client/connect?" + q.Encode()); err != nil {
		_ = srv.Close()
		return "", err
	}
	select {
	case <-ctx.Done():
		_ = srv.Close()
		return "", ctx.Err()
	case err := <-serverError:
		if !errors.Is(err, http.ErrServerClosed) {
			return "", err
		}
	case code := <-callback:
		token, err := a.exchange(ctx, code, verifier)
		if err != nil {
			_ = srv.Close()
			return "", err
		}
		if err := a.deps.Store.Save(token); err != nil {
			_ = srv.Close()
			return "", err
		}
		_ = srv.Close()
		<-serverError
		return token, nil
	}
	return "", errors.New("connection closed")
}

func (a *App) exchange(ctx context.Context, code, verifier string) (string, error) {
	body, _ := json.Marshal(map[string]string{"code": code, "code_verifier": verifier})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.ServerURL+"/api/v1/client/token", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", a.userAgent())
	res, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return "", errInvalidCredential
	}
	if res.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return "", fmt.Errorf("exchange client code: status %d: %s", res.StatusCode, data)
	}
	var issued struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&issued); err != nil {
		return "", err
	}
	if issued.Token == "" {
		return "", errors.New("client token response is empty")
	}
	return issued.Token, nil
}

func (a *App) nonce(n int) (string, error) {
	b, err := a.cfg.Nonces(n)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func randomBytes(n int) ([]byte, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return b, nil
}

func (a *App) userAgent() string { return "taskboard-client/" + a.cfg.Version }

func (a *App) stream(ctx context.Context, token string) error {
	streamCtx, cancel := context.WithCancel(ctx)
	a.mu.Lock()
	a.streamCancel = cancel
	a.streamCount++
	streamNumber := a.streamCount
	a.mu.Unlock()
	defer func() {
		cancel()
		a.mu.Lock()
		a.streamCancel = nil
		a.mu.Unlock()
	}()
	req, err := http.NewRequestWithContext(streamCtx, http.MethodGet, a.cfg.ServerURL+"/api/v1/notifications/stream", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", a.userAgent())
	a.mu.Lock()
	last := a.lastID
	a.mu.Unlock()
	if last > 0 {
		req.Header.Set("Last-Event-ID", strconv.FormatInt(last, 10))
	}
	res, err := a.streamClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errInvalidCredential
	}
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("notification stream: status %d: %s", res.StatusCode, data)
	}
	lines := make(chan string)
	readDone := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			select {
			case <-streamCtx.Done():
				readDone <- streamCtx.Err()
				return
			case lines <- sc.Text():
			}
		}
		readDone <- sc.Err()
	}()
	var event string
	var data strings.Builder
	idle := a.deps.Clock.After(time.Minute)
	for {
		select {
		case <-idle:
			cancel()
			<-readDone
			return errors.New("notification stream idle timeout")
		case err := <-readDone:
			return err
		case line := <-lines:
			idle = a.deps.Clock.After(time.Minute)
			switch {
			case line == "":
				if event == "" && data.Len() == 0 {
					continue
				}
				if err := a.handleEvent(event, data.String(), streamNumber); err != nil {
					cancel()
					<-readDone
					return err
				}
				event, data = "", strings.Builder{}
			case strings.HasPrefix(line, "event:"):
				event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				if data.Len() > 0 {
					data.WriteByte('\n')
				}
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			}
		}
	}
}

func (a *App) handleEvent(event, raw string, streamNumber int) error {
	switch event {
	case "hello":
		var body struct {
			UnreadCount int   `json:"unread_count"`
			LatestID    int64 `json:"latest_id"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return err
		}
		if body.UnreadCount > 0 {
			a.mu.Lock()
			token := a.token
			a.mu.Unlock()
			if err := a.loadUnread(token, body.UnreadCount, body.LatestID); err != nil {
				return err
			}
		} else {
			a.syncUnread(nil, body.UnreadCount, body.LatestID)
		}
		a.mu.Lock()
		a.failures = 0
		a.mu.Unlock()
		if a.deps.Update != nil && streamNumber > 1 {
			a.mu.Lock()
			token := a.token
			a.mu.Unlock()
			if a.checkUpdate(a.ctx, token) {
				return nil
			}
		}
	case "notification":
		var n Notification
		if err := json.Unmarshal([]byte(raw), &n); err != nil {
			return err
		}
		a.mu.Lock()
		a.lastID = n.ID
		a.unread = append([]Notification{n}, a.unread...)
		if len(a.unread) > 10 {
			a.unread = a.unread[:10]
		}
		count := len(a.unread)
		a.mu.Unlock()
		a.deps.Toaster.Show(n)
		a.updateTray(count)
	case "read":
		var body struct {
			IDs         []int64 `json:"ids"`
			UnreadCount int     `json:"unread_count"`
		}
		if err := json.Unmarshal([]byte(raw), &body); err != nil {
			return err
		}
		a.syncUnread(body.IDs, body.UnreadCount, 0)
	}
	return nil
}

func (a *App) syncUnread(remove []int64, count int, latest int64) {
	a.mu.Lock()
	if len(remove) > 0 {
		removed := make(map[int64]struct{}, len(remove))
		for _, id := range remove {
			removed[id] = struct{}{}
		}
		kept := a.unread[:0]
		for _, n := range a.unread {
			if _, ok := removed[n.ID]; !ok {
				kept = append(kept, n)
			}
		}
		a.unread = kept
	}
	if count < len(a.unread) && count >= 0 {
		a.unread = a.unread[:count]
	}
	if latest > a.lastID {
		a.lastID = latest
	}
	a.mu.Unlock()
	a.updateTray(count)
}

func (a *App) updateTray(unread int) {
	if unread > 0 {
		a.deps.Tray.SetState(TrayUnread)
	} else {
		a.deps.Tray.SetState(TrayNormal)
	}
	a.mu.Lock()
	items := append([]Notification(nil), a.unread...)
	a.mu.Unlock()
	a.deps.Tray.SetUnread(items)
}

func waitContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (a *App) reconnect() {
	a.mu.Lock()
	required := a.loginRequired
	cancel := a.streamCancel
	a.mu.Unlock()
	if !required {
		if cancel != nil {
			cancel()
		}
		return
	}
	a.loginRequired = false
	select {
	case a.loginSignal <- struct{}{}:
	default:
	}
}

func (a *App) logout() {
	a.mu.Lock()
	token := a.token
	cancel := a.streamCancel
	a.mu.Unlock()
	_ = a.deleteJSON(context.Background(), token, "/api/v1/me/clients/self")
	_ = a.deps.Store.Delete()
	a.enterNeedLogin()
	if cancel != nil {
		cancel()
	}
	a.deps.Tray.SetUnread(nil)
	a.deps.Tray.SetState(TrayNeedLogin)
}

func (a *App) enterNeedLogin() {
	_ = a.deps.Store.Delete()
	a.mu.Lock()
	a.token = ""
	a.lastID = 0
	a.unread = nil
	a.loginRequired = true
	prompt := !a.needLoginPrompted
	a.needLoginPrompted = true
	a.mu.Unlock()
	if prompt {
		a.deps.Toaster.Show(Notification{Reason: "relogin", Text: "需要重新登录 Taskboard"})
	}
}

func (a *App) markAllRead() {
	a.mu.Lock()
	upTo := a.lastID
	token := a.token
	a.mu.Unlock()
	if upTo == 0 {
		return
	}
	if err := a.postJSON(a.ctx, token, "/api/v1/notifications/read", map[string]any{"up_to": upTo}, nil); errors.Is(err, errInvalidCredential) {
		a.enterNeedLogin()
		a.deps.Tray.SetUnread(nil)
		a.deps.Tray.SetState(TrayNeedLogin)
	}
}

func (a *App) viewAll() {
	_ = a.deps.Browser.Open(a.cfg.ServerURL + "/")
}

func (a *App) openNotification(n Notification) {
	switch n.Reason {
	case "outdated":
		_ = a.deps.Browser.Open(a.absolute(n.Ticket.URL))
		return
	case "updated":
		return
	}
	if n.ID == 0 {
		a.reconnect()
		return
	}
	a.mu.Lock()
	token := a.token
	a.mu.Unlock()
	if err := a.postJSON(a.ctx, token, "/api/v1/notifications/read", map[string]any{"ids": []int64{n.ID}}, nil); errors.Is(err, errInvalidCredential) {
		a.enterNeedLogin()
		a.deps.Tray.SetUnread(nil)
		a.deps.Tray.SetState(TrayNeedLogin)
		return
	}
	a.syncUnread([]int64{n.ID}, len(a.currentUnread())-1, 0)
	_ = a.deps.Browser.Open(a.absolute(n.Ticket.URL))
}

func (a *App) loadUnread(token string, count int, latest int64) error {
	req, err := http.NewRequestWithContext(a.ctx, http.MethodGet, a.cfg.ServerURL+"/api/v1/notifications?unread=1&limit=10", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", a.userAgent())
	res, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errInvalidCredential
	}
	if res.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("load unread: status %d: %s", res.StatusCode, data)
	}
	var body struct {
		Items []Notification `json:"items"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	a.mu.Lock()
	a.unread = body.Items
	if latest > a.lastID {
		a.lastID = latest
	}
	a.mu.Unlock()
	a.updateTray(count)
	return nil
}

func (a *App) deleteJSON(ctx context.Context, token, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, a.cfg.ServerURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", a.userAgent())
	res, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%s: status %d: %s", path, res.StatusCode, data)
	}
	return nil
}

func (a *App) currentUnread() []Notification {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]Notification(nil), a.unread...)
}

func (a *App) postJSON(ctx context.Context, token, path string, in, out any) error {
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.ServerURL+path, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", a.userAgent())
	res, err := a.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return errInvalidCredential
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("%s: status %d: %s", path, res.StatusCode, data)
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}

func (a *App) absolute(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	if !strings.HasPrefix(path, "/") {
		return a.cfg.ServerURL + "/" + path
	}
	return a.cfg.ServerURL + path
}

var errInvalidCredential = errors.New("invalid client credential")
