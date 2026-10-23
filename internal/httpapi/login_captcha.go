package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"
)

const captchaTTL = 5 * time.Minute

var (
	errCaptchaRequired = errors.New("captcha required")
	errCaptchaInvalid  = errors.New("captcha invalid")
	errCaptchaExpired  = errors.New("captcha expired")
	errCaptchaUsed     = errors.New("captcha used")
)

func (a *API) handleLoginCaptcha(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	answer, image, err := a.captchaSource.Generate()
	if err != nil {
		panic(err)
	}
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		panic(err)
	}
	id := hex.EncodeToString(idBytes)
	a.captchaMu.Lock()
	if a.captchas == nil {
		a.captchas = make(map[string]captchaRecord)
	}
	now := a.clk.Now()
	a.purgeExpiredCaptchasLocked(now)
	a.captchas[id] = captchaRecord{answer: strings.ToUpper(answer), expiresAt: now.Add(captchaTTL)}
	a.captchaMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{
		"id":    id,
		"image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(image),
	})
}

func (a *API) consumeCaptcha(id, answer string) error {
	if id == "" || strings.TrimSpace(answer) == "" {
		return errCaptchaRequired
	}
	a.captchaMu.Lock()
	defer a.captchaMu.Unlock()
	record, ok := a.captchas[id]
	if !ok {
		return errCaptchaInvalid
	}
	now := a.clk.Now()
	if !now.Before(record.expiresAt) {
		delete(a.captchas, id)
		return errCaptchaExpired
	}
	if record.used {
		return errCaptchaUsed
	}
	got := strings.ToUpper(strings.TrimSpace(answer))
	if subtle.ConstantTimeCompare([]byte(got), []byte(record.answer)) != 1 {
		delete(a.captchas, id)
		return errCaptchaInvalid
	}
	record.used = true
	a.captchas[id] = record
	return nil
}

func (a *API) purgeExpiredCaptchasLocked(now time.Time) {
	for id, record := range a.captchas {
		if !now.Before(record.expiresAt) {
			delete(a.captchas, id)
		}
	}
}

func (a *API) writeCaptchaError(w http.ResponseWriter, err error) {
	switch err {
	case errCaptchaRequired:
		WriteError(w, http.StatusUnauthorized, CodeCaptchaRequired, "请输入验证码", "")
	case errCaptchaInvalid:
		WriteError(w, http.StatusUnauthorized, CodeCaptchaInvalid, "验证码错误", "请查看新验证码后重试")
	case errCaptchaExpired:
		WriteError(w, http.StatusUnauthorized, CodeCaptchaExpired, "验证码已过期", "请查看新验证码后重试")
	case errCaptchaUsed:
		WriteError(w, http.StatusUnauthorized, CodeCaptchaUsed, "验证码已使用", "请查看新验证码后重试")
	default:
		panic(err)
	}
}
