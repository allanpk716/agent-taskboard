//go:build windows

package desktop

import (
	"fmt"
	"runtime"
	"sync"
	"unsafe"

	"github.com/allanpk716/agent-taskboard/internal/client"
	"golang.org/x/sys/windows"
)

var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	gdi32                    = windows.NewLazySystemDLL("gdi32.dll")
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procRegisterClassEx      = user32.NewProc("RegisterClassExW")
	procCreateWindowEx       = user32.NewProc("CreateWindowExW")
	procDefWindowProc        = user32.NewProc("DefWindowProcW")
	procShowWindow           = user32.NewProc("ShowWindow")
	procUpdateWindow         = user32.NewProc("UpdateWindow")
	procGetMessage           = user32.NewProc("GetMessageW")
	procIsDialogMessage      = user32.NewProc("IsDialogMessageW")
	procTranslateMessage     = user32.NewProc("TranslateMessage")
	procDispatchMessage      = user32.NewProc("DispatchMessageW")
	procPostQuitMessage      = user32.NewProc("PostQuitMessage")
	procSendMessage          = user32.NewProc("SendMessageW")
	procGetWindowText        = user32.NewProc("GetWindowTextW")
	procDestroyWindow        = user32.NewProc("DestroyWindow")
	procGetModuleHandle      = kernel32.NewProc("GetModuleHandleW")
	procSetForegroundWindow  = user32.NewProc("SetForegroundWindow")
	procGetStockObject       = gdi32.NewProc("GetStockObject")
	procLoadCursor           = user32.NewProc("LoadCursorW")
	procAdjustWindowRectEx   = user32.NewProc("AdjustWindowRectEx")
	procSystemParametersInfo = user32.NewProc("SystemParametersInfoW")
	procSetFocus             = user32.NewProc("SetFocus")

	promptClassOnce sync.Once
	promptClassAtom uint16
	promptClassErr  error
	promptWndProc   = windows.NewCallback(serverURLWndProc)
)

const (
	wsChild           = 0x40000000
	wsVisible         = 0x10000000
	wsBorder          = 0x00800000
	wsTabStop         = 0x00010000
	wsCaption         = 0x00C00000
	wsSysMenu         = 0x00080000
	wsExDlgModalFrame = 0x00000001
	wsExControlParent = 0x00010000
	bsDefPushButton   = 0x00000001
	esAutoHScroll     = 0x0080
	ssEditControl     = 0x00002000
	swShow            = 5
	wmDestroy         = 2
	wmClose           = 0x0010
	wmCommand         = 0x0111
	wmSetFont         = 0x0030
	emSetSel          = 0x00B1
	bnClicked         = 0
	idOK              = 1
	idCancel          = 2
	idcEdit           = 2000
	idcError          = 2001
	idcInstruction    = 2002
	defaultGuiFont    = 17
	idcArrow          = 32512
	colorBtnFace      = 15
	spiGetWorkArea    = 48
)

type promptDialog struct {
	hwnd   uintptr
	edit   uintptr
	errCtl uintptr
	ok     bool
	value  string
}

// promptMu serializes prompt creation. A second prompt is rejected rather than
// queued so tray callbacks can never leave two competing dialogs.
var (
	promptMu      sync.Mutex
	promptStateMu sync.Mutex
	activePrompts = map[uintptr]*promptDialog{}
)

// Win32ServerURLPrompter 用 Win32 控件实现平台地址输入框。
type Win32ServerURLPrompter struct{}

func (Win32ServerURLPrompter) PromptServerURL(current string, probeErr error) (string, bool, error) {
	// Class registration, window creation, focus, and the message loop must all
	// run on one OS thread. This also makes tray callback goroutines safe.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	promptStateMu.Lock()
	for _, existing := range activePrompts {
		if existing.hwnd != 0 {
			_, _, _ = procSetForegroundWindow.Call(existing.hwnd)
		}
		promptStateMu.Unlock()
		return "", false, nil
	}
	promptStateMu.Unlock()

	promptMu.Lock()
	defer promptMu.Unlock()

	if err := registerServerURLClass(); err != nil {
		return "", false, err
	}
	promptStateMu.Lock()
	for _, existing := range activePrompts {
		if existing.hwnd != 0 {
			_, _, _ = procSetForegroundWindow.Call(existing.hwnd)
		}
		promptStateMu.Unlock()
		return "", false, nil
	}
	promptStateMu.Unlock()

	title, err := windows.UTF16PtrFromString("Taskboard 平台地址")
	if err != nil {
		return "", false, err
	}
	instruction, err := windows.UTF16PtrFromString("请输入平台网址，例如 https://taskboard.example.com")
	if err != nil {
		return "", false, err
	}
	errorText := " "
	if probeErr != nil {
		errorText = probeErr.Error()
	}
	errorPtr, err := windows.UTF16PtrFromString(errorText)
	if err != nil {
		return "", false, err
	}
	dialog := &promptDialog{}
	font := getStockFont()
	module, _, moduleErr := procGetModuleHandle.Call(0)
	if module == 0 {
		return "", false, fmt.Errorf("读取程序模块失败：%w", moduleErr)
	}

	outerRect := rect{Left: 24, Top: 24, Right: 24 + 456, Bottom: 24 + 188}
	var exStyle uint32 = wsExDlgModalFrame | wsExControlParent
	var style uint32 = wsCaption | wsSysMenu
	ret, _, _ := procAdjustWindowRectEx.Call(
		uintptr(unsafe.Pointer(&outerRect)), uintptr(style), 0, uintptr(exStyle))
	if ret == 0 {
		return "", false, fmt.Errorf("计算平台地址对话框尺寸失败")
	}
	workArea := rect{}
	ret, _, _ = procSystemParametersInfo.Call(
		uintptr(spiGetWorkArea), 0, uintptr(unsafe.Pointer(&workArea)), 0)
	if ret == 0 {
		return "", false, fmt.Errorf("读取工作区大小失败")
	}
	outerWidth := outerRect.Right - outerRect.Left
	outerHeight := outerRect.Bottom - outerRect.Top
	x := workArea.Left + (workArea.Right-workArea.Left-outerWidth)/2
	y := workArea.Top + (workArea.Bottom-workArea.Top-outerHeight)/2

	dialog.hwnd = createWindow(exStyle, promptClassAtom, title, style,
		x, y, outerWidth, outerHeight, 0, 0, module)
	if dialog.hwnd == 0 {
		return "", false, fmt.Errorf("创建平台地址对话框失败")
	}
	promptStateMu.Lock()
	activePrompts[dialog.hwnd] = dialog
	promptStateMu.Unlock()

	dialog.edit = createChild(dialog.hwnd, "EDIT", windows.StringToUTF16Ptr(current),
		wsChild|wsVisible|wsBorder|wsTabStop|esAutoHScroll, 24, 50, 408, 26, idcEdit, module)
	dialog.errCtl = createChild(dialog.hwnd, "STATIC", errorPtr,
		wsChild|wsVisible|ssEditControl, 24, 86, 408, 44, idcError, module)
	okButton := createChild(dialog.hwnd, "BUTTON", mustUTF16("确定"),
		wsChild|wsVisible|wsTabStop|bsDefPushButton, 228, 142, 92, 28, idOK, module)
	cancelButton := createChild(dialog.hwnd, "BUTTON", mustUTF16("取消"),
		wsChild|wsVisible|wsTabStop, 330, 142, 92, 28, idCancel, module)
	instructionCtl := createChild(dialog.hwnd, "STATIC", instruction,
		wsChild|wsVisible, 24, 20, 408, 22, idcInstruction, module)
	if dialog.edit == 0 || dialog.errCtl == 0 || okButton == 0 || cancelButton == 0 || instructionCtl == 0 {
		clearActivePrompt(dialog)
		_, _, _ = procDestroyWindow.Call(dialog.hwnd)
		return "", false, fmt.Errorf("创建平台地址对话框控件失败")
	}
	for _, ctl := range []uintptr{dialog.edit, dialog.errCtl, okButton, cancelButton, instructionCtl} {
		sendMessage(ctl, wmSetFont, font, 1)
	}
	_, _, _ = procShowWindow.Call(dialog.hwnd, swShow)
	_, _, _ = procUpdateWindow.Call(dialog.hwnd)
	_, _, _ = procSetForegroundWindow.Call(dialog.hwnd)
	_, _, _ = procSetFocus.Call(dialog.edit)
	sendMessage(dialog.edit, emSetSel, 0, ^uintptr(0))

	runMessageLoop(dialog.hwnd)

	promptStateMu.Lock()
	delete(activePrompts, dialog.hwnd)
	promptStateMu.Unlock()
	return dialog.value, dialog.ok, nil
}

type rect struct {
	Left, Top, Right, Bottom int32
}

func mustUTF16(text string) *uint16 {
	ptr, err := windows.UTF16PtrFromString(text)
	if err != nil {
		panic(err)
	}
	return ptr
}

func registerServerURLClass() error {
	promptClassOnce.Do(func() {
		name := mustUTF16("TaskboardServerURLPrompt")
		module, _, classErr := procGetModuleHandle.Call(0)
		if module == 0 {
			promptClassErr = fmt.Errorf("读取程序模块失败：%w", classErr)
			return
		}
		cursor, _, _ := procLoadCursor.Call(0, uintptr(idcArrow))
		if cursor == 0 {
			promptClassErr = fmt.Errorf("读取箭头光标失败")
			return
		}
		wc := struct {
			Size       uint32
			Style      uint32
			WndProc    uintptr
			ClsExtra   int32
			WndExtra   int32
			Instance   uintptr
			Icon       uintptr
			Cursor     uintptr
			Background uintptr
			MenuName   *uint16
			ClassName  *uint16
			IconSm     uintptr
		}{
			WndProc:    promptWndProc,
			Instance:   module,
			Cursor:     cursor,
			Background: colorBtnFace + 1,
			ClassName:  name,
		}
		wc.Size = uint32(unsafe.Sizeof(wc))
		ret, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&wc)))
		if ret == 0 {
			promptClassErr = fmt.Errorf("注册平台地址对话框失败：%w", err)
			return
		}
		promptClassAtom = uint16(ret)
	})
	return promptClassErr
}

func serverURLWndProc(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	dialog := activePromptForHwnd(hwnd)
	switch msg {
	case wmCommand:
		id, notification := uint16(wParam), uint16(wParam>>16)
		if dialog != nil && notification == bnClicked {
			switch id {
			case idOK:
				dialog.value = windowText(dialog.edit)
				dialog.ok = true
				_, _, _ = procDestroyWindow.Call(hwnd)
				return 0
			case idCancel:
				dialog.ok = false
				_, _, _ = procDestroyWindow.Call(hwnd)
				return 0
			}
		}
	case wmClose:
		if dialog != nil {
			dialog.ok = false
		}
		_, _, _ = procDestroyWindow.Call(hwnd)
		return 0
	case wmDestroy:
		_, _, _ = procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProc.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func activePromptForHwnd(hwnd uintptr) *promptDialog {
	promptStateMu.Lock()
	defer promptStateMu.Unlock()
	return activePrompts[hwnd]
}

func clearActivePrompt(dialog *promptDialog) {
	promptStateMu.Lock()
	delete(activePrompts, dialog.hwnd)
	promptStateMu.Unlock()
}

type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Pt      struct{ X, Y int32 }
	Private uintptr
}

func runMessageLoop(hwnd uintptr) {
	var msg winMsg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			return
		}
		isDialog, _, _ := procIsDialogMessage.Call(hwnd, uintptr(unsafe.Pointer(&msg)))
		if isDialog != 0 {
			continue
		}
		_, _, _ = procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		_, _, _ = procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}
}

func createWindow(exStyle uint32, class uint16, title *uint16, style uint32, x, y, width, height int32, parent, menu, instance uintptr) uintptr {
	ret, _, _ := procCreateWindowEx.Call(
		uintptr(exStyle), uintptr(class), uintptr(unsafe.Pointer(title)), uintptr(style),
		uintptr(x), uintptr(y), uintptr(width), uintptr(height), parent, menu, instance, 0)
	return ret
}

func createChild(parent uintptr, class string, text *uint16, style uint32, x, y, width, height int32, id int16, instance uintptr) uintptr {
	ret, _, _ := procCreateWindowEx.Call(
		0, uintptr(unsafe.Pointer(mustUTF16(class))), uintptr(unsafe.Pointer(text)), uintptr(style),
		uintptr(x), uintptr(y), uintptr(width), uintptr(height), parent, uintptr(uint16(id)), instance, 0)
	return ret
}

func sendMessage(hwnd uintptr, msg uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procSendMessage.Call(hwnd, uintptr(msg), wParam, lParam)
	return ret
}

func getStockFont() uintptr {
	ret, _, _ := procGetStockObject.Call(defaultGuiFont)
	return ret
}

func windowText(hwnd uintptr) string {
	length := int(sendMessage(hwnd, 0x000E, 0, 0))
	if length <= 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	_, _, _ = procGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
	return windows.UTF16ToString(buf)
}

var _ client.ServerURLPrompter = Win32ServerURLPrompter{}
