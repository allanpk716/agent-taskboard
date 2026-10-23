//go:build windows

// Package desktop 提供 Windows 桌面适配层：托盘、Toast、凭据管理器、
// 系统浏览器和命名 mutex。图标编码有单元测试，系统行为靠真实 Windows 手工验收。
package desktop

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"unsafe"

	"fyne.io/systray"
	"git.sr.ht/~jackmordaunt/go-toast/v2"
	"github.com/allanpk716/agent-taskboard/internal/client"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	appAUMID         = "com.urithub.TaskboardClient"
	appGUID          = "{83A12D6C-CBB4-4BE6-9A1B-01E94734D782}"
	credentialTarget = "Taskboard Desktop Client"
	mutexName        = "Local\\TaskboardDesktopClient"
	readyEventName   = "Local\\TaskboardClientUpdateReady"
	quitEventName    = "Local\\TaskboardClientQuit"
	runValueName     = "TaskboardClient"
	clientConfigKey  = `Software\Taskboard\Client`
	aumidKey         = `SOFTWARE\Classes\AppUserModelId\` + appAUMID
	activatorKey     = `SOFTWARE\Classes\CLSID\{83A12D6C-CBB4-4BE6-9A1B-01E94734D782}\LocalServer32`
	activatorCLSID   = `SOFTWARE\Classes\CLSID\{83A12D6C-CBB4-4BE6-9A1B-01E94734D782}`
)

// System 是 Windows 适配层的一组实现。
type System struct {
	Tray      *WindowsTray
	Toaster   *WindowsToaster
	Store     *CredentialManager
	Browser   *ShellBrowser
	Lock      *NamedMutex
	Update    *WindowsUpdateManager
	Prompter  Win32ServerURLPrompter
	Lifecycle *client.LifecycleDependencies
}

// NewSystem 初始化 Windows 适配层。toastAppData 需要在进程启动早期注册一次。
func NewSystem() (*System, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := toast.SetAppData(toast.AppData{
		AppID: appAUMID, GUID: appGUID, ActivationExe: exe,
	}); err != nil {
		return nil, fmt.Errorf("注册 Toast 应用标识失败：%w", err)
	}
	lifecycle := &client.LifecycleDependencies{
		Config: RegistryConfig{}, Autostart: RunAutostart{}, Toast: ToastRegistration{},
		Files: UserFileSystem{}, Process: ProcessLauncher{}, InstallAlert: firstInstallNotifier{},
	}
	updates, err := NewWindowsUpdateManager()
	if err != nil {
		return nil, fmt.Errorf("创建更新就绪事件失败：%w", err)
	}
	return &System{
		Tray: &WindowsTray{}, Toaster: &WindowsToaster{appID: appAUMID},
		Store: &CredentialManager{}, Browser: &ShellBrowser{}, Lock: &NamedMutex{name: mutexName},
		Update: updates, Prompter: Win32ServerURLPrompter{}, Lifecycle: lifecycle,
	}, nil
}

// Dependencies 返回客户端核心需要的适配器集合。
func (s *System) Dependencies() client.Dependencies {
	return client.Dependencies{Tray: s.Tray, Toaster: s.Toaster, Store: s.Store, Browser: s.Browser,
		Lock: s.Lock, Update: s.Update, Prompter: s.Prompter, Lifecycle: s.Lifecycle}
}

// NewLifecycle 返回安装阶段使用的适配器；此时还没有创建托盘和 Toast。
func NewLifecycle() *client.LifecycleDependencies {
	return &client.LifecycleDependencies{
		Config: RegistryConfig{}, Autostart: RunAutostart{}, Toast: ToastRegistration{},
		Files: UserFileSystem{}, Process: ProcessLauncher{}, InstallAlert: firstInstallNotifier{},
	}
}

// NewLifecycleWithConfigKey 用指定 HKCU key 保存平台地址，仅供手工测试隔离。
func NewLifecycleWithConfigKey(key string) *client.LifecycleDependencies {
	deps := NewLifecycle()
	deps.Config = RegistryConfig{key: key}
	return deps
}

// NewServerURLPrompter 返回 Win32 平台地址输入框。
func NewServerURLPrompter() client.ServerURLPrompter { return Win32ServerURLPrompter{} }

// WindowsTray 用 systray 实现托盘。
type WindowsTray struct {
	mu     sync.Mutex
	ready  bool
	state  client.TrayState
	unread []client.Notification
	slots  []*systray.MenuItem
	update *systray.MenuItem
}

func (t *WindowsTray) Run(ctx context.Context, cb client.TrayCallbacks) error {
	go guard("托盘退出监听", func() {
		<-ctx.Done()
		systray.Quit()
	})
	systray.Run(func() {
		defer func() {
			if err := recover(); err != nil {
				NewFatalReporter().Fatalf(1, "托盘初始化发生内部错误：%v", err)
			}
		}()
		t.mu.Lock()
		t.ready = true
		t.mu.Unlock()
		t.buildMenu(cb)
		t.mu.Lock()
		t.applyLocked()
		t.mu.Unlock()
	}, func() {})
	return nil
}

func (t *WindowsTray) SetState(state client.TrayState) {
	guard("托盘状态更新", func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.state = state
		if t.ready {
			t.applyLocked()
		}
	})
}

func (t *WindowsTray) SetUnread(items []client.Notification) {
	guard("托盘未读更新", func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		t.unread = append([]client.Notification(nil), items...)
		if t.ready {
			t.applyLocked()
		}
	})
}

func (t *WindowsTray) buildMenu(cb client.TrayCallbacks) {
	t.mu.Lock()
	t.slots = make([]*systray.MenuItem, 10)
	for i := range t.slots {
		t.slots[i] = systray.AddMenuItem("", "")
		t.slots[i].Hide()
	}
	t.mu.Unlock()
	systray.AddSeparator()
	changeServer := systray.AddMenuItem("更改平台地址…", "连接另一个 Taskboard 平台")
	update := systray.AddMenuItem("", "")
	update.Hide()
	allRead := systray.AddMenuItem("全部标为已读", "保留更新的通知")
	viewAll := systray.AddMenuItem("查看全部", "在浏览器中打开通知")
	logout := systray.AddMenuItem("退出登录", "撤销这台电脑的凭证")
	uninstall := systray.AddMenuItem("卸载", "撤销凭证并删除这台电脑上的客户端")
	quit := systray.AddMenuItem("退出", "关闭桌面客户端")
	for _, item := range []*systray.MenuItem{allRead, viewAll, logout, uninstall, quit} {
		item.Disable()
	}
	t.mu.Lock()
	t.update = update
	t.mu.Unlock()
	allRead.Enable()
	viewAll.Enable()
	changeServer.Enable()
	logout.Enable()
	quit.Enable()
	t.watch(changeServer, cb.ChangeServerURL)
	t.watch(update, cb.Update)
	t.watch(allRead, cb.MarkAllRead)
	t.watch(viewAll, cb.ViewAll)
	t.watch(logout, cb.Logout)
	t.watch(uninstall, cb.Uninstall)
	t.watch(quit, cb.Quit)
	for i := range t.slots {
		i := i
		t.watch(t.slots[i], func() {
			t.mu.Lock()
			if i < len(t.unread) {
				n := t.unread[i]
				t.mu.Unlock()
				cb.OpenUnread(n)
				return
			}
			t.mu.Unlock()
		})
	}
}

func (t *WindowsTray) SetUpdateVersion(version string) {
	guard("托盘更新菜单更新", func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.update == nil {
			return
		}
		if version == "" {
			t.update.Hide()
			return
		}
		t.update.SetTitle("更新到 " + version)
		t.update.SetTooltip("下载并安装 Taskboard 客户端 " + version)
		t.update.Show()
	})
}

func (t *WindowsTray) watch(item *systray.MenuItem, onClick func()) {
	go guard("托盘菜单回调", func() {
		for range item.ClickedCh {
			guard("托盘菜单命令", onClick)
		}
	})
}

func (t *WindowsTray) applyLocked() {
	icon, tooltip := iconFor(t.state)
	systray.SetTooltip(tooltip)
	systray.SetIcon(icon)
	for i, item := range t.slots {
		if i < len(t.unread) {
			n := t.unread[i]
			item.SetTitle(n.Ticket.Number + " " + n.Ticket.Title)
			item.SetTooltip(n.Text)
			item.Show()
		} else {
			item.Hide()
		}
	}
}

func iconFor(state client.TrayState) ([]byte, string) {
	switch state {
	case client.TrayUnread:
		return ico(color.RGBA{R: 226, G: 66, B: 52, A: 255}), "Taskboard：有未读通知"
	case client.TrayNeedLogin:
		return ico(color.RGBA{R: 242, G: 148, B: 0, A: 255}), "Taskboard：需要登录"
	case client.TrayOutdated:
		return ico(color.RGBA{R: 220, G: 0, B: 107, A: 255}), "Taskboard：版本过旧"
	case client.TrayDisconnected:
		return ico(color.RGBA{R: 117, G: 117, B: 117, A: 255}), "Taskboard：连接中断"
	default:
		return ico(color.RGBA{R: 22, G: 128, B: 92, A: 255}), "Taskboard"
	}
}

func ico(c color.Color) []byte {
	const size = 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if x == 0 || y == 0 || x == size-1 || y == size-1 {
				img.Set(x, y, color.White)
			} else {
				img.Set(x, y, c)
			}
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, img); err != nil {
		panic(err)
	}
	out := make([]byte, 22, 22+pngBuf.Len())
	binary.LittleEndian.PutUint16(out[0:], 0)
	binary.LittleEndian.PutUint16(out[2:], 1)
	binary.LittleEndian.PutUint16(out[4:], 1)
	// ICONDIRENTRY 从第 6 字节开始：宽、高、调色板数、保留、色面、位深、数据长度、数据偏移。
	out[6] = size
	out[7] = size
	binary.LittleEndian.PutUint16(out[10:], 1)
	binary.LittleEndian.PutUint16(out[12:], 32)
	binary.LittleEndian.PutUint32(out[14:], uint32(pngBuf.Len()))
	binary.LittleEndian.PutUint32(out[18:], 22)
	return append(out, pngBuf.Bytes()...)
}

// WindowsToaster 用 go-toast 的前台激活回调实现系统通知。
type WindowsToaster struct {
	appID string
	mu    sync.Mutex
	items map[string]client.Notification
}

func (w *WindowsToaster) Run(ctx context.Context, onClick func(client.Notification)) error {
	toast.SetActivationCallback(func(args string, _ []toast.UserData) {
		guard("Toast 点击回调", func() {
			w.mu.Lock()
			n, ok := w.items[args]
			w.mu.Unlock()
			if ok {
				onClick(n)
			}
		})
	})
	<-ctx.Done()
	return nil
}

func (w *WindowsToaster) Show(n client.Notification) {
	key := strconv.FormatInt(n.ID, 10)
	if n.ID == 0 {
		key = n.Reason
	}
	w.mu.Lock()
	if w.items == nil {
		w.items = make(map[string]client.Notification)
	}
	w.items[key] = n
	w.mu.Unlock()
	title := n.Ticket.Number + " " + n.Ticket.Title
	body := n.Text
	if title == " " {
		title = n.Text
		body = ""
	} else if n.Summary != "" {
		body += "\n" + n.Summary
	}
	notification := toast.Notification{
		AppID: w.appID, Title: title, Body: body,
		ActivationType: toast.Foreground, ActivationArguments: key,
	}
	if n.Reason == "outdated" {
		notification.Duration = toast.Long
	}
	go guard("Toast 显示", func() { _ = notification.Push() })
}

// WindowsUpdateManager 用命名事件传递新进程就绪信号，并保留可结束的更新进程句柄。
type WindowsUpdateManager struct {
	mu          sync.Mutex
	event       windows.Handle
	quitEvent   windows.Handle
	ready       chan struct{}
	quit        chan struct{}
	waiting     bool
	quitWaiting bool
}

// createNamedEvent 打开已存在的同名事件时，x/sys 返回有效句柄加 ERROR_ALREADY_EXISTS，视为成功。
func createNamedEvent(name *uint16) (windows.Handle, error) {
	handle, err := windows.CreateEvent(nil, 0, 0, name)
	if handle != 0 && errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return handle, nil
	}
	return handle, err
}

func NewWindowsUpdateManager() (*WindowsUpdateManager, error) {
	name, err := windows.UTF16PtrFromString(readyEventName)
	if err != nil {
		return nil, err
	}
	event, err := createNamedEvent(name)
	if err != nil {
		return nil, err
	}
	quitName, err := windows.UTF16PtrFromString(quitEventName)
	if err != nil {
		return nil, err
	}
	quitEvent, err := createNamedEvent(quitName)
	if err != nil {
		return nil, err
	}
	return &WindowsUpdateManager{event: event, quitEvent: quitEvent}, nil
}

func (m *WindowsUpdateManager) Ready() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready == nil {
		m.ready = make(chan struct{})
	}
	if !m.waiting {
		m.waiting = true
		go guard("更新就绪事件监听", func() {
			if _, err := windows.WaitForSingleObject(m.event, windows.INFINITE); err == nil {
				close(m.ready)
			}
		})
	}
	return m.ready
}

func (m *WindowsUpdateManager) SendReady() error {
	name, err := windows.UTF16PtrFromString(readyEventName)
	if err != nil {
		return err
	}
	event, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	return windows.SetEvent(event)
}

func (m *WindowsUpdateManager) Quit() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.quit == nil {
		m.quit = make(chan struct{})
	}
	if !m.quitWaiting {
		m.quitWaiting = true
		go guard("更新退出事件监听", func() {
			if _, err := windows.WaitForSingleObject(m.quitEvent, windows.INFINITE); err == nil {
				close(m.quit)
			}
		})
	}
	return m.quit
}

func (m *WindowsUpdateManager) SendQuit() error { return windows.SetEvent(m.quitEvent) }

func (m *WindowsUpdateManager) ResetQuit() error { return windows.ResetEvent(m.quitEvent) }

func (m *WindowsUpdateManager) Start(path string, args []string) (client.UpdateProcess, error) {
	cmd := newProcessCommand(path, args)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		return nil, err
	}
	return WindowsUpdateProcess{handle: handle}, nil
}

type WindowsUpdateProcess struct{ handle windows.Handle }

func (p WindowsUpdateProcess) Stop() error {
	if err := windows.TerminateProcess(p.handle, 1); err != nil && !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return err
	}
	_, _ = windows.WaitForSingleObject(p.handle, 5000)
	return windows.CloseHandle(p.handle)
}

// CredentialManager 用 Windows Credential Manager 保存客户端凭证。
type CredentialManager struct{}

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

var (
	advapi32        = windows.NewLazySystemDLL("advapi32.dll")
	procCredWriteW  = advapi32.NewProc("CredWriteW")
	procCredReadW   = advapi32.NewProc("CredReadW")
	procCredDeleteW = advapi32.NewProc("CredDeleteW")
	procCredFree    = advapi32.NewProc("CredFree")
)

func (CredentialManager) Get() (string, bool, error) {
	target, err := windows.UTF16PtrFromString(credentialTarget)
	if err != nil {
		return "", false, err
	}
	var cred *windowsCredential
	ret, _, err := procCredReadW.Call(uintptr(unsafe.Pointer(target)), 1, 0, uintptr(unsafe.Pointer(&cred)))
	if ret == 0 {
		if errors.Is(err, windows.ERROR_NOT_FOUND) {
			return "", false, nil
		}
		return "", false, err
	}
	defer procCredFree.Call(uintptr(unsafe.Pointer(cred)))
	if cred.CredentialBlobSize == 0 {
		return "", false, nil
	}
	blob := unsafe.Slice(cred.CredentialBlob, cred.CredentialBlobSize)
	return string(blob), true, nil
}

func (CredentialManager) Save(token string) error {
	target, err := windows.UTF16PtrFromString(credentialTarget)
	if err != nil {
		return err
	}
	user, err := windows.UTF16PtrFromString("taskboard-client")
	if err != nil {
		return err
	}
	blob := []byte(token)
	cred := &windowsCredential{
		Type: 1, TargetName: target, UserName: user, Persist: 2,
		CredentialBlob: &blob[0], CredentialBlobSize: uint32(len(blob)),
	}
	ret, _, err := procCredWriteW.Call(uintptr(unsafe.Pointer(cred)), 0)
	if ret == 0 {
		return err
	}
	return nil
}

func (CredentialManager) Delete() error {
	target, err := windows.UTF16PtrFromString(credentialTarget)
	if err != nil {
		return err
	}
	ret, _, err := procCredDeleteW.Call(uintptr(unsafe.Pointer(target)), 1, 0)
	if ret == 0 && !errors.Is(err, windows.ERROR_NOT_FOUND) {
		return err
	}
	return nil
}

// ShellBrowser 用默认浏览器打开 URL。
type ShellBrowser struct{}

func (ShellBrowser) Open(url string) error {
	p, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, nil, p, nil, nil, windows.SW_SHOWNORMAL)
}

// NamedMutex 用命名 mutex 保证单实例。
type NamedMutex struct {
	name string
}

func (m *NamedMutex) Acquire() (func(), error) {
	name, err := windows.UTF16PtrFromString(m.name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, true, name)
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		if handle != 0 {
			_ = windows.CloseHandle(handle)
		}
		return nil, client.ErrAlreadyRunning
	}
	if err != nil {
		return nil, err
	}
	return func() { _ = windows.CloseHandle(handle) }, nil
}

// RegistryConfig 在 HKCU 保存客户端解析出的平台地址。
type RegistryConfig struct{ key string }

func (c RegistryConfig) configKey() string {
	if c.key != "" {
		return c.key
	}
	return clientConfigKey
}

func (c RegistryConfig) ServerURL() (string, bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, c.configKey(), registry.READ)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("ServerURL")
	if errors.Is(err, registry.ErrNotExist) || value == "" {
		return "", false, nil
	}
	return value, value != "", err
}

func (c RegistryConfig) SaveServerURL(rawURL string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, c.configKey(), registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue("ServerURL", rawURL)
}

func (c RegistryConfig) DeleteServerURL() error {
	if err := registry.DeleteKey(registry.CURRENT_USER, c.configKey()); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// RunAutostart 维护 HKCU Run 自启项。
type RunAutostart struct{}

func (RunAutostart) Enable(command string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(runValueName, command)
}

func (RunAutostart) Disable() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Run`, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer key.Close()
	if err := key.DeleteValue(runValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}

// ToastRegistration 删除 go-toast 写入 HKCU 的 AUMID 和激活 COM 注册。
type ToastRegistration struct{}

func (ToastRegistration) Unregister() error {
	if err := registry.DeleteKey(registry.CURRENT_USER, aumidKey); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, activatorKey); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	_ = registry.DeleteKey(registry.CURRENT_USER, activatorCLSID)
	return nil
}

// UserFileSystem 复制自身到 %LOCALAPPDATA%\Programs\taskboard-client。
type UserFileSystem struct{}

func (UserFileSystem) InstallDir() (string, error) {
	root := os.Getenv("LOCALAPPDATA")
	if root == "" {
		return "", errors.New("没有 LOCALAPPDATA 环境变量")
	}
	return filepath.Join(root, "Programs", "taskboard-client"), nil
}

func (UserFileSystem) Executable() (string, error) { return os.Executable() }

func (UserFileSystem) Exists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func (UserFileSystem) CopyExecutable(dest string) error {
	source, err := os.Executable()
	if err != nil {
		return err
	}
	from, err := os.Open(source)
	if err != nil {
		return err
	}
	defer from.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	to, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(to, from); err != nil {
		_ = to.Close()
		return err
	}
	return to.Close()
}

func (UserFileSystem) RenameExecutable(old, dest string) error { return os.Rename(old, dest) }

func (UserFileSystem) RemoveExecutable(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// ProcessLauncher 重启安装后的客户端，并安排卸载后的延迟删除。
type ProcessLauncher struct{}

func (ProcessLauncher) Start(path string, args []string) error {
	cmd := newProcessCommand(path, args)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func newProcessCommand(path string, args []string) *exec.Cmd {
	cmd := exec.Command(path, args...)
	cmd.Dir = filepath.Dir(path)
	return cmd
}

func (ProcessLauncher) DeleteLater(path string) error {
	cmd := exec.Command("cmd", "/c", `timeout /t 2 & del "`+path+`"`)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Start()
}

func (ProcessLauncher) QuitExisting() error {
	name, err := windows.UTF16PtrFromString(quitEventName)
	if err != nil {
		return err
	}
	event, err := createNamedEvent(name)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(event)
	return windows.SetEvent(event)
}
