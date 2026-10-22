package httpapi

import (
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestClientBaseName支持GBK文件名(t *testing.T) {
	gbk := func(s string) string {
		b, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(s))
		if err != nil {
			t.Fatalf("编码 %q 为 GB18030 失败：%v", s, err)
		}
		return string(b)
	}
	backslashSecondByte := rune(0)
	for r := rune(0x4e00); r < 0xa000; r++ {
		b, err := simplifiedchinese.GB18030.NewEncoder().Bytes([]byte(string(r)))
		if err == nil && len(b) == 2 && b[1] == 0x5c {
			backslashSecondByte = r
			break
		}
	}
	if backslashSecondByte == 0 {
		t.Fatal("GB18030 编码表中找不到第二字节为 0x5C 的汉字")
	}
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"UTF8原样", "R4-01-广于环保科技.html", "R4-01-广于环保科技.html"},
		{"GBK解码", "R4-01-" + gbk("广于环保科技") + ".html", "R4-01-广于环保科技.html"},
		{"GBK路径先解码后截取", `C:\dir\` + gbk("广于环保科技.html"), "广于环保科技.html"},
		{"GBK第二字节是反斜杠", gbk(string(backslashSecondByte)) + ".txt", string(backslashSecondByte) + ".txt"},
		{"非法UTF8保持原值", "\xff\xfe.txt", "\xff\xfe.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := clientBaseName(tt.in)
			if got != tt.want {
				t.Fatalf("clientBaseName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
