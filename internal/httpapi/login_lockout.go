package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	loginFailureLimit     = 10
	loginLockDuration     = 15 * time.Minute
	loginRatePerMinute    = 20
	loginFailureRetention = loginLockDuration
)

type loginLockKey struct {
	username string
	ip       string
}

type loginLockRecord struct {
	failures    int
	lastFailure time.Time
	lockedUntil time.Time
}

type loginLockStore struct {
	mu      sync.Mutex
	records map[loginLockKey]*loginLockRecord
}

func newLoginLockStore() *loginLockStore {
	return &loginLockStore{records: map[loginLockKey]*loginLockRecord{}}
}

// locked returns the remaining lock duration for an account/IP pair.
func (l *loginLockStore) locked(username, ip string, now time.Time) (time.Duration, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	record := l.records[loginLockKey{username, ip}]
	if record == nil {
		return 0, false
	}
	if !now.Before(record.lockedUntil) {
		l.purgeExpiredLocked(now)
		return 0, false
	}
	return record.lockedUntil.Sub(now), true
}

func (l *loginLockStore) recordFailure(username, ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.purgeExpiredLocked(now)
	key := loginLockKey{username, ip}
	record := l.records[key]
	if record == nil {
		record = &loginLockRecord{}
		l.records[key] = record
	}
	record.failures++
	record.lastFailure = now
	if record.failures >= loginFailureLimit {
		record.lockedUntil = now.Add(loginLockDuration)
		return true
	}
	return false
}

func (l *loginLockStore) reset(username, ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.records, loginLockKey{username, ip})
}

func (l *loginLockStore) list(now time.Time) []loginLockView {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.purgeExpiredLocked(now)
	views := make([]loginLockView, 0, len(l.records))
	for key, record := range l.records {
		if !now.Before(record.lockedUntil) {
			continue
		}
		views = append(views, loginLockView{
			Username: key.username, IP: key.ip,
			RemainingMinutes: remainingMinutes(record.lockedUntil.Sub(now)),
		})
	}
	return views
}

func (l *loginLockStore) unlock(username, ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	key := loginLockKey{username, ip}
	_, exists := l.records[key]
	delete(l.records, key)
	return exists
}

// purgeExpiredLocked clears expired lock records and stale unconfirmed counters.
// The stale cutoff keeps invented usernames from growing the process map forever.
func (l *loginLockStore) purgeExpiredLocked(now time.Time) {
	for key, record := range l.records {
		if record.lockedUntil.IsZero() && now.Sub(record.lastFailure) > loginFailureRetention {
			delete(l.records, key)
		}
		if !record.lockedUntil.IsZero() && !now.Before(record.lockedUntil) {
			delete(l.records, key)
		}
	}
}

func remainingMinutes(remaining time.Duration) int {
	minutes := int(remaining / time.Minute)
	if remaining%time.Minute != 0 {
		minutes++
	}
	return minutes
}

func retryAfterSeconds(retry time.Duration) int {
	seconds := int(retry.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return seconds
}

type loginLockView struct {
	Username         string `json:"username"`
	Name             string `json:"name"`
	IP               string `json:"ip"`
	RemainingMinutes int    `json:"remaining_minutes"`
}

func (a *API) writeLoginRateLimited(w http.ResponseWriter, retry time.Duration) {
	seconds := retryAfterSeconds(retry)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	WriteErrorExtra(w, http.StatusTooManyRequests, CodeLoginRateLimited,
		"登录请求太频繁，请稍后再试", "同一来源地址每分钟最多 20 次登录请求",
		map[string]any{"retry_after_seconds": seconds})
}

func (a *API) handleLoginLocks(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.adminWebSessionFromRequest(w, r, "登录锁定只能由管理员管理"); !ok {
		return
	}
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "锁定列表用 GET 读取")
		return
	}
	rows, err := a.ident.ListMembersDetailed(r.Context())
	if err != nil {
		panic(err)
	}
	names := make(map[string]string, len(rows))
	for _, row := range rows {
		names[row.Username] = row.Name
	}
	locks := a.loginLocks.list(a.clk.Now())
	for i := range locks {
		locks[i].Name = names[locks[i].Username]
	}
	writeJSON(w, http.StatusOK, struct {
		Locks []loginLockView `json:"locks"`
	}{Locks: locks})
}

func (a *API) handleLoginUnlock(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.adminWebSessionFromRequest(w, r, "登录锁定只能由管理员解除"); !ok {
		return
	}
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "解锁用 POST")
		return
	}
	var req struct {
		Username string `json:"username"`
		IP       string `json:"ip"`
	}
	if !decodeJSON(w, r, &req, `{"username": "zhao", "ip": "203.0.113.10"}`) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.IP = strings.TrimSpace(req.IP)
	if req.Username == "" || req.IP == "" {
		WriteError(w, http.StatusUnprocessableEntity, CodeLoginLockRequired, "登录名和来源 IP 都不能为空", "")
		return
	}
	if !a.loginLocks.unlock(req.Username, req.IP) {
		WriteError(w, http.StatusNotFound, CodeLoginLockNotFound, "这条登录锁定不存在或已过期", "")
		return
	}
	rows, err := a.ident.ListMembersDetailed(r.Context())
	if err != nil {
		panic(err)
	}
	for _, row := range rows {
		if row.Username == req.Username {
			a.wakeMemberStreams(row.ID)
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": req.Username, "ip": req.IP, "unlocked": true})
}
