// Package fslink 把 web/ 做成指向 web-<version>/ 的目录链接：Windows 上用
// junction（不需要管理员权限，也不需要开发者模式），linux 上用符号链接。
//
// 前端切换就是重建这个链接：链接本身没有打开的句柄（服务打开的是它指向的
// 目标目录），所以服务运行中也能切换；切换后新请求经由链接进入新版本目录，
// 老版本目录保持原样，正在进行的响应不受影响。
package fslink

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

// ioReparseTagMountPoint 是目录联接（junction）的 reparse tag。
const ioReparseTagMountPoint = 0xA0000003

// Make 把 link 创建为指向 target 的目录链接（junction）。target 应为绝对路径。
// link 已存在时会先被移除（只对链接本身，不动 target）。
func Make(link, target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	link16, err := windows.UTF16PtrFromString(link)
	if err != nil {
		return err
	}
	if err := windows.CreateDirectory(link16, nil); err != nil && err != windows.ERROR_ALREADY_EXISTS {
		return fmt.Errorf("创建链接目录 %s：%w", link, err)
	}
	// REPARSE_DATA_BUFFER（MountPoint）：长度按字节计，不含结尾 NUL。
	substitute := []rune(`\??\` + filepath.Clean(abs))
	printName := []rune("")
	subBytes, printBytes := len(substitute)*2, len(printName)*2
	dataLen := 8 + subBytes + 2 + printBytes + 2
	buf := make([]byte, 8+dataLen)
	binary.LittleEndian.PutUint32(buf[0:], ioReparseTagMountPoint)
	binary.LittleEndian.PutUint16(buf[4:], uint16(dataLen))
	binary.LittleEndian.PutUint16(buf[6:], 0)
	d := buf[8:]
	binary.LittleEndian.PutUint16(d[0:], 0) // SubstituteNameOffset
	binary.LittleEndian.PutUint16(d[2:], uint16(subBytes))
	binary.LittleEndian.PutUint16(d[4:], uint16(subBytes+2)) // PrintNameOffset
	binary.LittleEndian.PutUint16(d[6:], uint16(printBytes))
	for i, r := range substitute {
		binary.LittleEndian.PutUint16(d[8+i*2:], uint16(r))
	}
	for i, r := range printName {
		binary.LittleEndian.PutUint16(d[8+subBytes+2+i*2:], uint16(r))
	}
	fh, err := windows.CreateFile(link16, windows.GENERIC_WRITE, 0, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		os.Remove(link)
		return fmt.Errorf("打开链接 %s 写 reparse point：%w", link, err)
	}
	defer windows.CloseHandle(fh)
	var returned uint32
	if err := windows.DeviceIoControl(fh, windows.FSCTL_SET_REPARSE_POINT,
		&buf[0], uint32(len(buf)), nil, 0, &returned, nil); err != nil {
		os.Remove(link)
		return fmt.Errorf("设置 junction（%s → %s）：%w", link, abs, err)
	}
	return nil
}
