package server_test

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type loginLockError struct {
	Error struct {
		Code             string `json:"code"`
		Message          string `json:"message"`
		Hint             string `json:"hint"`
		RemainingMinutes int    `json:"remaining_minutes"`
	} `json:"error"`
}

func loginFromIP(t testing.TB, s *tbtest.Server, ip, username, password string) (*http.Response, string) {
	t.Helper()
	var captcha struct {
		ID string `json:"id"`
	}
	client := &http.Client{Transport: fixedRemoteTransport{remote: "127.0.0.1:12345"}}
	s.DoWithHeaders(t, client, http.MethodGet, "/api/v1/login-captcha",
		map[string]string{"X-Forwarded-For": ip}, nil, &captcha)
	res := s.DoWithHeaders(t, client, http.MethodPost, "/api/v1/session",
		map[string]string{"X-Forwarded-For": ip}, map[string]string{
			"username": username, "password": password,
			"captcha_id": captcha.ID, "captcha_answer": s.CaptchaAnswer(),
		}, nil)
	return res, captcha.ID
}

func mustLoginLockError(t testing.TB, res *http.Response) loginLockError {
	t.Helper()
	var got loginLockError
	tbtest.DecodeJSON(t, res, &got)
	return got
}

func Test连续密码失败按账号和IP锁定并可解锁(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "admin-pass")
	s.CreateMember("wang", "member-pass")
	const ip = "203.0.113.10"

	for i := 0; i < 9; i++ {
		res, _ := loginFromIP(t, s, ip, "wang", "wrong-pass")
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次失败状态码 = %d, 想 401", i+1, res.StatusCode)
		}
	}
	tenth, _ := loginFromIP(t, s, ip, "wang", "wrong-pass")
	got := mustLoginLockError(t, tenth)
	if tenth.StatusCode != http.StatusForbidden || got.Error.Code != "login_locked" {
		t.Fatalf("第 10 次失败 = %d/%s, 想 403/login_locked", tenth.StatusCode, got.Error.Code)
	}
	if got.Error.RemainingMinutes != 15 || !strings.Contains(got.Error.Message+got.Error.Hint, "已锁定") {
		t.Fatalf("锁定提示 = %+v, 想剩余 15 分钟并说明已锁定", got.Error)
	}

	locked, _ := loginFromIP(t, s, ip, "wang", "member-pass")
	lockedErr := mustLoginLockError(t, locked)
	if locked.StatusCode != http.StatusForbidden || lockedErr.Error.Code != "login_locked" {
		t.Fatalf("锁定期正确密码 = %d/%s, 想 403/login_locked", locked.StatusCode, lockedErr.Error.Code)
	}

	other, _ := loginFromIP(t, s, "203.0.113.11", "wang", "member-pass")
	if other.StatusCode != http.StatusOK {
		t.Fatalf("另一个 IP 的正确密码 = %d, 想 200", other.StatusCode)
	}

	admin := s.Login("admin", "admin-pass")
	var locks struct {
		Locks []struct {
			Username         string `json:"username"`
			Name             string `json:"name"`
			IP               string `json:"ip"`
			RemainingMinutes int    `json:"remaining_minutes"`
		} `json:"locks"`
	}
	list := s.Do(t, admin, http.MethodGet, "/api/v1/admin/login-locks", nil, &locks)
	if list.StatusCode != http.StatusOK || len(locks.Locks) != 1 {
		t.Fatalf("管理员列锁定 = %d/%+v, 想 1 条", list.StatusCode, locks.Locks)
	}
	lock := locks.Locks[0]
	if lock.Username != "wang" || lock.IP != ip || lock.RemainingMinutes != 15 {
		t.Fatalf("锁定条目 = %+v", lock)
	}

	unlocked := s.Do(t, admin, http.MethodPost, "/api/v1/admin/login-locks/unlock",
		map[string]string{"username": "wang", "ip": ip}, nil)
	if unlocked.StatusCode != http.StatusOK {
		t.Fatalf("管理员解锁 = %d, 想 200", unlocked.StatusCode)
	}
	afterUnlock, _ := loginFromIP(t, s, ip, "wang", "member-pass")
	if afterUnlock.StatusCode != http.StatusOK {
		t.Fatalf("解锁后正确密码 = %d, 想 200", afterUnlock.StatusCode)
	}
}

func Test登录锁定十五分钟后自动恢复且成功清零(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	const ip = "203.0.113.20"

	for i := 0; i < 9; i++ {
		loginFromIP(t, s, ip, "wang", "wrong-pass")
	}
	if res, _ := loginFromIP(t, s, ip, "wang", "member-pass"); res.StatusCode != http.StatusOK {
		t.Fatalf("9 次失败后的正确密码 = %d, 想 200", res.StatusCode)
	}
	for i := 0; i < 9; i++ {
		loginFromIP(t, s, ip, "wang", "wrong-pass")
	}
	if res, _ := loginFromIP(t, s, ip, "wang", "member-pass"); res.StatusCode != http.StatusOK {
		t.Fatalf("成功清零后又 9 次失败不应锁定，状态码 = %d", res.StatusCode)
	}

	for i := 0; i < 10; i++ {
		loginFromIP(t, s, ip, "wang", "wrong-pass")
	}
	s.Clock().Advance(15 * time.Minute)
	res, _ := loginFromIP(t, s, ip, "wang", "member-pass")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("15 分钟后正确密码 = %d, 想 200", res.StatusCode)
	}
}

func Test非管理员不能列出或解除登录锁定(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "admin-pass")
	s.CreateMember("wang", "member-pass")
	for i := 0; i < 10; i++ {
		loginFromIP(t, s, "203.0.113.30", "wang", "wrong-pass")
	}
	member := s.Login("wang", "member-pass")

	list := s.Do(t, member, http.MethodGet, "/api/v1/admin/login-locks", nil, nil)
	if list.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员列锁定 = %d, 想 403", list.StatusCode)
	}
	unlock := s.Do(t, member, http.MethodPost, "/api/v1/admin/login-locks/unlock",
		map[string]string{"username": "wang", "ip": "203.0.113.30"}, nil)
	if unlock.StatusCode != http.StatusForbidden {
		t.Fatalf("普通成员解锁 = %d, 想 403", unlock.StatusCode)
	}
}

func Test登录IP限流每分钟二十次(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	const ip = "203.0.113.40"
	client := &http.Client{Transport: fixedRemoteTransport{remote: "127.0.0.1:12345"}}

	for i := 0; i < 20; i++ {
		res := s.DoWithHeaders(t, client, http.MethodPost, "/api/v1/session",
			map[string]string{"X-Forwarded-For": ip}, map[string]string{"username": "wang", "password": "nope"}, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次登录 = %d, 想 401", i+1, res.StatusCode)
		}
	}
	limited := s.DoWithHeaders(t, client, http.MethodPost, "/api/v1/session",
		map[string]string{"X-Forwarded-For": ip}, map[string]string{"username": "wang", "password": "nope"}, nil)
	got := mustLoginLockError(t, limited)
	if limited.StatusCode != http.StatusTooManyRequests || got.Error.Code != "login_rate_limited" {
		t.Fatalf("第 21 次登录 = %d/%s, 想 429/login_rate_limited", limited.StatusCode, got.Error.Code)
	}
	if limited.Header.Get("Retry-After") == "" {
		t.Fatal("429 缺 Retry-After")
	}

	other, _ := loginFromIP(t, s, "203.0.113.41", "wang", "member-pass")
	if other.StatusCode != http.StatusOK {
		t.Fatalf("另一个 IP 登录 = %d, 想 200", other.StatusCode)
	}
	s.Clock().Advance(time.Minute)
	res, _ := loginFromIP(t, s, ip, "wang", "member-pass")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("一分钟后同 IP 登录 = %d, 想 200", res.StatusCode)
	}
}

func Test登录来源IP只信任回环反代的转发头(t *testing.T) {
	sourceIP, ok := nonLoopbackLocalIP(t)
	if !ok {
		t.Skip("本机没有可用非回环地址，无法构造非回环来源")
	}
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	for i := 0; i < 10; i++ {
		loginFromIP(t, s, "203.0.113.50", "wang", "wrong-pass")
	}

	// 同一套服务代码另听真实非回环地址：该来源自带 XFF 时应按来源地址分桶。
	direct := tbtest.Start(t, tbtest.WithListenHost(sourceIP.String()))
	direct.CreateMember("wang", "member-pass")
	res := direct.DoWithHeaders(t, direct.NewClient(), http.MethodPost, "/api/v1/session",
		map[string]string{"X-Forwarded-For": "203.0.113.50"}, map[string]string{
			"username": "wang", "password": "member-pass",
			"captcha_id":     newLoginCaptcha(t, direct, direct.NewClient()),
			"captcha_answer": direct.CaptchaAnswer(),
		}, nil)
	if res.StatusCode != http.StatusOK {
		errBody := mustLoginLockError(t, res)
		t.Fatalf("非回环来源忽略 XFF 登录 = %d/%+v, 想 200", res.StatusCode, errBody.Error)
	}
}

func newLoginCaptcha(t testing.TB, s *tbtest.Server, client *http.Client) string {
	t.Helper()
	var captcha struct {
		ID string `json:"id"`
	}
	s.Do(t, client, http.MethodGet, "/api/v1/login-captcha", nil, &captcha)
	return captcha.ID
}

func nonLoopbackLocalIP(t testing.TB) (net.IP, bool) {
	t.Helper()
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addresses {
			if ipNet, ok := addr.(*net.IPNet); ok {
				if ip := ipNet.IP.To4(); ip != nil && !ip.IsLoopback() {
					return ip, true
				}
			}
		}
	}
	return nil, false
}
