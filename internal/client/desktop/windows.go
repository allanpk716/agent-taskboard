//go:build windows

// Package desktop 提供 Windows 桌面适配层：托盘、Toast、凭据管理器、
// 系统浏览器和命名 mutex。这里不做自动化测试，行为靠真实 Windows 手工验收。
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
		Files: UserFileSystem{}, Process: ProcessLauncher{},
	}
	return &System{
		Tray: &WindowsTray{}, Toaster: &WindowsToaster{appID: appAUMID},
		Store: &CredentialManager{}, Browser: &ShellBrowser{}, Lock: &NamedMutex{name: mutexName},
		Lifecycle: lifecycle,
	}, nil
}

// Dependencies 返回客户端核心需要的适配器集合。
func (s *System) Dependencies() client.Dependencies {
	return client.Dependencies{Tray: s.Tray, Toaster: s.Toaster, Store: s.Store, Browser: s.Browser,
		Lock: s.Lock, Lifecycle: s.Lifecycle}
}

// NewLifecycle 返回安装阶段使用的适配器；此时还没有创建托盘和 Toast。
func NewLifecycle() *client.LifecycleDependencies {
	return &client.LifecycleDependencies{
		Config: RegistryConfig{}, Autostart: RunAutostart{}, Toast: ToastRegistration{},
		Files: UserFileSystem{}, Process: ProcessLauncher{},
	}
}

// WindowsTray 用 systray 实现托盘。
type WindowsTray struct {
	mu     sync.Mutex
	ready  bool
	state  client.TrayState
	unread []client.Notification
	slots  []*systray.MenuItem
}

func (t *WindowsTray) Run(ctx context.Context, cb client.TrayCallbacks) error {
	go func() {
		<-ctx.Done()
		systray.Quit()
	}()
	systray.Run(func() {
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
	t.mu.Lock()
	defer t.mu.Unlock()
	t.state = state
	if t.ready {
		t.applyLocked()
	}
}

func (t *WindowsTray) SetUnread(items []client.Notification) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.unread = append([]client.Notification(nil), items...)
	if t.ready {
		t.applyLocked()
	}
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
	allRead := systray.AddMenuItem("全部标为已读", "保留更新的通知")
	viewAll := systray.AddMenuItem("查看全部", "在浏览器中打开通知")
	logout := systray.AddMenuItem("退出登录", "撤销这台电脑的凭证")
	uninstall := systray.AddMenuItem("卸载", "撤销凭证并删除这台电脑上的客户端")
	quit := systray.AddMenuItem("退出", "关闭桌面客户端")
	for _, item := range []*systray.MenuItem{allRead, viewAll, logout, uninstall, quit} {
		item.Disable()
	}
	allRead.Enable()
	viewAll.Enable()
	logout.Enable()
	quit.Enable()
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

func (t *WindowsTray) watch(item *systray.MenuItem, onClick func()) {
	go func() {
		for range item.ClickedCh {
			onClick()
		}
	}()
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
		return ico(color.RGBA{R: 242, G: 148, B: 0, A: 255}), "Taskboard：需要重新登录"
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
	out[5] = size
	out[6] = size
	binary.LittleEndian.PutUint16(out[12:], 1)
	binary.LittleEndian.PutUint16(out[14:], 32)
	binary.LittleEndian.PutUint32(out[16:], uint32(pngBuf.Len()))
	binary.LittleEndian.PutUint32(out[20:], 22)
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
		w.mu.Lock()
		n, ok := w.items[args]
		w.mu.Unlock()
		if ok {
			onClick(n)
		}
	})
	<-ctx.Done()
	return nil
}

func (w *WindowsToaster) Show(n client.Notification) {
	key := strconv.FormatInt(n.ID, 10)
	w.mu.Lock()
	if w.items == nil {
		w.items = make(map[string]client.Notification)
	}
	w.items[key] = n
	w.mu.Unlock()
	body := n.Text
	if n.Summary != "" {
		body += "\n" + n.Summary
	}
	notification := toast.Notification{
		AppID: w.appID, Title: n.Ticket.Number + " " + n.Ticket.Title, Body: body,
		ActivationType: toast.Foreground, ActivationArguments: key,
	}
	go func() { _ = notification.Push() }()
}

// CredentialManager 用 Windows Credential Manager 保存客户端凭证。
type CredentialManager struct{}

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlob     *byte
	CredentialBlobSize uint32
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
	if err != nil {
		return nil, err
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		_ = windows.CloseHandle(handle)
		return nil, errors.New("Taskboard 桌面客户端已经在运行")
	}
	return func() { _ = windows.CloseHandle(handle) }, nil
}

// RegistryConfig 在 HKCU 保存客户端解析出的平台地址。
type RegistryConfig struct{}

func (RegistryConfig) ServerURL() (string, bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, clientConfigKey, registry.READ)
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

func (RegistryConfig) SaveServerURL(rawURL string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, clientConfigKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue("ServerURL", rawURL)
}

func (RegistryConfig) DeleteServerURL() error {
	if err := registry.DeleteKey(registry.CURRENT_USER, clientConfigKey); err != nil && !errors.Is(err, registry.ErrNotExist) {
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

// ProcessLauncher 重启安装后的客户端，并安排卸载后的延迟删除。
type ProcessLauncher struct{}

func (ProcessLauncher) Start(path string, args []string) error {
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func (ProcessLauncher) DeleteLater(path string) error {
	cmd := exec.Command("cmd", "/c", `timeout /t 2 & del "`+path+`"`)
	cmd.SysProcAttr = &windows.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd.Start()
}
