//go:build windows

package desktop

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procTestFindWindow    = user32.NewProc("FindWindowW")
	procTestGetDlgItem    = user32.NewProc("GetDlgItem")
	procTestPostMessage   = user32.NewProc("PostMessageW")
	procTestSendMessage   = user32.NewProc("SendMessageW")
	procTestGetWindowText = user32.NewProc("GetWindowTextW")
)

const (
	testWMSettext     = 0x000C
	testWMKeydown     = 0x0100
	testBMClick       = 0x00F5
	testVKReturn      = 0x0D
	testVKTab         = 0x09
	testVKEscape      = 0x1B
	testWMClose       = 0x0010
	promptWindowTitle = "Taskboard 平台地址"
	promptWindowClass = "TaskboardServerURLPrompt"
	promptProbeError  = "这个地址不是 taskboard 平台"
	promptTestTimeout = 5 * time.Second
)

func TestServerURLPromptOK(t *testing.T) {
	result := startPrompt(t, "https://old.example.com", errors.New(promptProbeError))
	hwnd := waitForPromptWindow(t)
	edit := dialogItem(t, hwnd, idcEdit)
	if got := windowTextForTest(edit); got != "https://old.example.com" {
		t.Fatalf("预填地址 = %q", got)
	}
	errorCtl := dialogItem(t, hwnd, idcError)
	if got := windowTextForTest(errorCtl); !strings.Contains(got, promptProbeError) {
		t.Fatalf("错误文本 = %q，想包含 %q", got, promptProbeError)
	}
	setWindowText(t, edit, "https://new.example.com")
	clickDialogItem(t, hwnd, idOK)
	assertPromptResult(t, result, promptTest{value: "https://new.example.com", ok: true})
}

func TestServerURLPromptCancel(t *testing.T) {
	result := startPrompt(t, "https://old.example.com", errors.New(promptProbeError))
	hwnd := waitForPromptWindow(t)
	clickDialogItem(t, hwnd, idCancel)
	assertPromptResult(t, result, promptTest{value: "", ok: false})
}

func TestServerURLPromptClose(t *testing.T) {
	result := startPrompt(t, "https://old.example.com", errors.New(promptProbeError))
	hwnd := waitForPromptWindow(t)
	postWindowMessage(t, hwnd, testWMClose, 0, 0)
	assertPromptResult(t, result, promptTest{value: "", ok: false})
}

func TestServerURLPromptEscapeUsesDialogMessage(t *testing.T) {
	result := startPrompt(t, "https://old.example.com", errors.New(promptProbeError))
	hwnd := waitForPromptWindow(t)
	edit := dialogItem(t, hwnd, idcEdit)
	postWindowMessage(t, edit, testWMKeydown, testVKEscape, 0)
	assertPromptResult(t, result, promptTest{value: "", ok: false})
}

func TestServerURLPromptTabAndEnterUseDialogMessage(t *testing.T) {
	result := startPrompt(t, "https://old.example.com", errors.New(promptProbeError))
	hwnd := waitForPromptWindow(t)
	edit := dialogItem(t, hwnd, idcEdit)
	setWindowText(t, edit, "https://new.example.com")
	postWindowMessage(t, edit, testWMKeydown, testVKTab, 0)
	postWindowMessage(t, edit, testWMKeydown, testVKReturn, 0)
	assertPromptResult(t, result, promptTest{value: "https://new.example.com", ok: true})
}

func TestServerURLPromptSequentialCalls(t *testing.T) {
	for i := 0; i < 2; i++ {
		t.Run(fmt.Sprint(i+1), func(t *testing.T) {
			result := startPrompt(t, fmt.Sprintf("https://old%d.example.com", i+1), errors.New(promptProbeError))
			hwnd := waitForPromptWindow(t)
			setWindowText(t, dialogItem(t, hwnd, idcEdit), fmt.Sprintf("https://new%d.example.com", i+1))
			clickDialogItem(t, hwnd, idOK)
			assertPromptResult(t, result, promptTest{
				value: fmt.Sprintf("https://new%d.example.com", i+1),
				ok:    true,
			})
		})
	}
}

func TestServerURLPromptSecondCallRejected(t *testing.T) {
	first := startPrompt(t, "https://old.example.com", errors.New(promptProbeError))
	hwnd := waitForPromptWindow(t)
	second := startPrompt(t, "https://ignored.example.com", errors.New(promptProbeError))
	assertPromptResult(t, second, promptTest{value: "", ok: false})
	clickDialogItem(t, hwnd, idCancel)
	assertPromptResult(t, first, promptTest{value: "", ok: false})
}

type promptTest struct {
	value string
	ok    bool
}

type promptResult struct {
	value string
	ok    bool
	err   error
	done  chan struct{}
}

func startPrompt(t *testing.T, current string, probeErr error) *promptResult {
	t.Helper()
	result := &promptResult{done: make(chan struct{})}
	go func() {
		defer close(result.done)
		result.value, result.ok, result.err = Win32ServerURLPrompter{}.PromptServerURL(current, probeErr)
	}()
	t.Cleanup(func() {
		select {
		case <-result.done:
		case <-time.After(promptTestTimeout):
			t.Errorf("提示器超过 %v 未退出", promptTestTimeout)
		}
	})
	return result
}

func waitForPromptWindow(t *testing.T) uintptr {
	t.Helper()
	deadline := time.Now().Add(promptTestTimeout)
	for time.Now().Before(deadline) {
		hwnd, _, _ := procTestFindWindow.Call(
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(promptWindowClass))),
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(promptWindowTitle))))
		if hwnd != 0 {
			time.Sleep(50 * time.Millisecond)
			return hwnd
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("超过 %v 未找到平台地址对话框", promptTestTimeout)
	return 0
}

func dialogItem(t *testing.T, hwnd uintptr, id int16) uintptr {
	t.Helper()
	item, _, _ := procTestGetDlgItem.Call(hwnd, uintptr(uint16(id)))
	if item == 0 {
		t.Fatalf("GetDlgItem(%d) 失败", id)
	}
	return item
}

func windowTextForTest(hwnd uintptr) string {
	length := int(callWindowMessage(hwnd, 0x000E, 0, 0))
	if length <= 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	_, _, _ = procTestGetWindowText.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(length+1))
	return windows.UTF16ToString(buf)
}

func setWindowText(t *testing.T, hwnd uintptr, text string) {
	t.Helper()
	callWindowMessage(hwnd, testWMSettext, 0, uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(text))))
}

func clickDialogItem(t *testing.T, hwnd uintptr, id int16) {
	t.Helper()
	callWindowMessage(dialogItem(t, hwnd, id), testBMClick, 0, 0)
}

func postWindowMessage(t *testing.T, hwnd uintptr, message uint32, wParam, lParam uintptr) {
	t.Helper()
	ret, _, _ := procTestPostMessage.Call(hwnd, uintptr(message), wParam, lParam)
	if ret == 0 {
		t.Fatalf("PostMessage(0x%x) 失败", message)
	}
}

func callWindowMessage(hwnd uintptr, message uint32, wParam, lParam uintptr) uintptr {
	ret, _, _ := procTestSendMessage.Call(hwnd, uintptr(message), wParam, lParam)
	return ret
}

func assertPromptResult(t *testing.T, result *promptResult, want promptTest) {
	t.Helper()
	select {
	case <-result.done:
	case <-time.After(promptTestTimeout):
		t.Fatalf("提示器超过 %v 未返回", promptTestTimeout)
	}
	if result.err != nil {
		t.Fatalf("PromptServerURL 错误：%v", result.err)
	}
	if result.value != want.value || result.ok != want.ok {
		t.Fatalf("PromptServerURL = (%q, %v)，想 (%q, %v)", result.value, result.ok, want.value, want.ok)
	}
}
