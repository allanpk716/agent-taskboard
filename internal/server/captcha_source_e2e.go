//go:build e2e_fixed_captcha

package server

import "github.com/allanpk716/agent-taskboard/internal/captcha"

// e2e 专用构建：固定验证码答案让 Playwright 不做 OCR，不是运行时配置。
const e2eFixedCaptchaAnswer = "TB123"

type fixedCaptchaSource struct{}

func (fixedCaptchaSource) Generate() (string, []byte, error) {
	pngBytes, err := captcha.Render(e2eFixedCaptchaAnswer)
	return e2eFixedCaptchaAnswer, pngBytes, err
}

// captchaSource 只在带 e2e_fixed_captcha build tag 编译时返回固定来源。
func captchaSource() captcha.Source { return fixedCaptchaSource{} }

// e2e 全套 Playwright 夹具都来自同一个回环地址；这里放宽限流避免测试互相影响。
// 429 行为由生产默认配置的 Go 黑盒测试覆盖。
func loginRateLimit() int { return 1000000 }
