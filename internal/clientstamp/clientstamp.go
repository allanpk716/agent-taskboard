// Package clientstamp 在发布的桌面客户端 exe 尾部附加平台地址。服务端
// 下载接口负责写入，客户端启动时只读文件末尾解析，双方共用这里的格式。
package clientstamp

import (
	"encoding/binary"
	"errors"
	"io"
	"net/url"
	"os"
	"strings"
)

const (
	// Magic 是尾部格式的版本标识，固定为 ASCII "TBSTAMP1"。
	Magic = "TBSTAMP1"
	// MaxURLLength 限制尾部地址长度，避免异常文件导致大范围读盘。
	MaxURLLength = 4096
	trailerLen   = len(Magic) + 4
)

// Append 返回原始 exe 字节加尾部后的完整下载内容。
func Append(exe []byte, rawURL string) ([]byte, error) {
	if err := Validate(rawURL); err != nil {
		return nil, err
	}
	out := make([]byte, len(exe), len(exe)+trailerLen+len(rawURL))
	copy(out, exe)
	var length [4]byte
	binary.LittleEndian.PutUint32(length[:], uint32(len(rawURL)))
	out = append(out, rawURL...)
	out = append(out, length[:]...)
	return append(out, Magic...), nil
}

// AppendFile 在文件末尾追加平台地址。
func AppendFile(path, rawURL string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	data, err := Append(nil, rawURL)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	return err
}

// Read 只读取文件末尾并解析平台地址；文件没有合法尾部时 ok 为 false。
func Read(path string) (rawURL string, ok bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer f.Close()
	tLen := int64(trailerLen)
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return "", false, err
	}
	if end < tLen {
		return "", false, nil
	}
	trailer := make([]byte, trailerLen)
	if _, err := f.ReadAt(trailer, end-tLen); err != nil {
		return "", false, err
	}
	if string(trailer[4:]) != Magic {
		return "", false, nil
	}
	length := binary.LittleEndian.Uint32(trailer[:4])
	if length == 0 || length > MaxURLLength || int64(length) > end-tLen {
		return "", false, nil
	}
	data := make([]byte, length)
	if _, err := f.ReadAt(data, end-tLen-int64(length)); err != nil {
		return "", false, err
	}
	rawURL = string(data)
	if err := Validate(rawURL); err != nil {
		return "", false, nil
	}
	return rawURL, true, nil
}

// ReadFromBytes 从完整 exe 内容末尾解析平台地址。
func ReadFromBytes(exe []byte) (rawURL string, ok bool, err error) {
	if len(exe) < trailerLen {
		return "", false, nil
	}
	trailer := exe[len(exe)-trailerLen:]
	if string(trailer[4:]) != Magic {
		return "", false, nil
	}
	length := binary.LittleEndian.Uint32(trailer[:4])
	if length == 0 || length > MaxURLLength || int(length) > len(exe)-trailerLen {
		return "", false, nil
	}
	rawURL = string(exe[len(exe)-trailerLen-int(length) : len(exe)-trailerLen])
	if err := Validate(rawURL); err != nil {
		return "", false, nil
	}
	return rawURL, true, nil
}

// Validate 校验平台地址是可以安全写进尾部的 http/https 根地址。
func Validate(rawURL string) error {
	if len(rawURL) == 0 || len(rawURL) > MaxURLLength {
		return errors.New("平台地址长度必须在 1 到 4096 字节之间")
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("平台地址必须是 http 或 https 的绝对地址")
	}
	if strings.TrimSpace(rawURL) != rawURL {
		return errors.New("平台地址首尾不能有空白")
	}
	return nil
}
