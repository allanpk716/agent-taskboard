// Package captcha 生成网页登录用的图片验证码。来源可以注入，生产固定使用随机实现。
package captcha

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Source 是验证码来源：返回答案和 PNG 图片字节。
type Source interface {
	Generate() (string, []byte, error)
}

// SourceFunc 把函数适配成 Source（测试注入用）。
type SourceFunc func() (string, []byte, error)

func (f SourceFunc) Generate() (string, []byte, error) { return f() }

// readableCharset 去掉 I、O、0、1 这些容易混淆的字符。
const readableCharset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// Random 是生产验证码来源：4-6 个易辨认的大写字母和数字。
type Random struct{}

func (Random) Generate() (string, []byte, error) {
	length := 4 + randomInt(3)
	raw := make([]byte, length)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, fmt.Errorf("生成验证码随机数失败：%w", err)
	}
	text := make([]byte, length)
	for i, b := range raw {
		text[i] = readableCharset[int(b)%len(readableCharset)]
	}
	pngBytes, err := Render(string(text))
	if err != nil {
		return "", nil, err
	}
	return string(text), pngBytes, nil
}

func randomInt(max int) int {
	b := make([]byte, 1)
	if _, err := rand.Read(b); err != nil {
		return 0
	}
	return int(b[0]) % max
}

// Render 用固定字库和少量干扰线渲染验证码，无外部字体依赖，便于交叉编译。
func Render(text string) ([]byte, error) {
	if len(text) < 4 || len(text) > 6 {
		return nil, errors.New("验证码长度必须是 4-6 个字符")
	}
	width := 24 + 22*len(text)
	img := image.NewRGBA(image.Rect(0, 0, width, 48))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	for i := 0; i < 3; i++ {
		y := 8 + i*13
		for x := 4; x < width-4; x += 3 {
			img.Set(x, (y+x/2)%42+2, color.Gray{Y: 170})
		}
	}
	drawer := &font.Drawer{Dst: img, Face: basicfont.Face7x13, Src: image.NewUniform(color.Black)}
	for i, r := range text {
		drawer.Dot = fixed.P(12+i*22, 30)
		drawer.DrawString(string(r))
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("编码验证码 PNG 失败：%w", err)
	}
	return buf.Bytes(), nil
}
