//go:build windows

package desktop

import (
	"bytes"
	"encoding/binary"
	"image/png"
	"testing"
	"unsafe"

	"github.com/allanpk716/agent-taskboard/internal/client"
)

func Test托盘状态图标是合法ICO(t *testing.T) {
	states := []client.TrayState{
		client.TrayNormal, client.TrayUnread, client.TrayNeedLogin,
		client.TrayDisconnected, client.TrayOutdated,
	}
	for _, state := range states {
		t.Run(string(state), func(t *testing.T) {
			data, _ := iconFor(state)
			if len(data) < 22 {
				t.Fatalf("ICO 长度 = %d，小于目录头", len(data))
			}
			if got := binary.LittleEndian.Uint16(data[0:]); got != 0 {
				t.Fatalf("reserved = %d，想 0", got)
			}
			if got := binary.LittleEndian.Uint16(data[2:]); got != 1 {
				t.Fatalf("type = %d，想 1", got)
			}
			if got := binary.LittleEndian.Uint16(data[4:]); got != 1 {
				t.Fatalf("count = %d，想 1", got)
			}
			if data[6] != 16 || data[7] != 16 {
				t.Fatalf("目录项尺寸 = %dx%d，想 16x16", data[6], data[7])
			}
			if got := binary.LittleEndian.Uint16(data[10:]); got != 1 {
				t.Fatalf("planes = %d，想 1", got)
			}
			if got := binary.LittleEndian.Uint16(data[12:]); got != 32 {
				t.Fatalf("bpp = %d，想 32", got)
			}
			size := binary.LittleEndian.Uint32(data[14:])
			offset := binary.LittleEndian.Uint32(data[18:])
			if int(size) != len(data)-22 {
				t.Fatalf("数据长度 = %d，想 %d", size, len(data)-22)
			}
			if offset != 22 {
				t.Fatalf("数据偏移 = %d，想 22", offset)
			}
			img, err := png.Decode(bytes.NewReader(data[offset:]))
			if err != nil {
				t.Fatalf("解码 PNG：%v", err)
			}
			bounds := img.Bounds()
			if bounds.Dx() != 16 || bounds.Dy() != 16 {
				t.Fatalf("PNG 尺寸 = %dx%d，想 16x16", bounds.Dx(), bounds.Dy())
			}
		})
	}
}

// 字段顺序错了 CredWriteW 会返回 ERROR_INVALID_PARAMETER（0.1.2 授权后崩溃）。
func Test凭据结构体与CREDENTIALW布局一致(t *testing.T) {
	var c windowsCredential
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("只校验 64 位布局")
	}
	got := []uintptr{unsafe.Offsetof(c.LastWritten), unsafe.Offsetof(c.CredentialBlobSize),
		unsafe.Offsetof(c.CredentialBlob), unsafe.Offsetof(c.Persist), unsafe.Offsetof(c.UserName), unsafe.Sizeof(c)}
	want := []uintptr{24, 32, 40, 48, 72, 80}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("偏移 = %v，想 %v", got, want)
		}
	}
}
