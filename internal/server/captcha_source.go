//go:build !e2e_fixed_captcha

package server

import "github.com/allanpk716/agent-taskboard/internal/captcha"

// captchaSource 返回服务使用的验证码来源。生产构建始终是随机实现。
func captchaSource() captcha.Source { return captcha.Random{} }

// loginRateLimit 返回生产登录限流。
func loginRateLimit() int { return 20 }
