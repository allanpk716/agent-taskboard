package server_test

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type guestResetPreviewBody struct {
	GuestName    string `json:"guest_name"`
	DeviceCount  int    `json:"device_count"`
	TicketCount  int    `json:"ticket_count"`
	AccessLocked bool   `json:"access_code_locked"`
}

type guestResetAccessCodeBody struct {
	AccessCode string `json:"access_code"`
	HomeURL    string `json:"home_url"`
	ResetText  string `json:"reset_text"`
}

type guestHomeResetBody struct {
	HomeURL   string `json:"home_url"`
	ShareText string `json:"share_text"`
}

type guestHomeClaimBody struct {
	DeviceToken string `json:"device_token"`
	AccessCode  string `json:"access_code"`
}

func claimGuestHome(t testing.TB, s *tbtest.Server, homeKey, name string) guestHomeClaimBody {
	t.Helper()
	var got guestHomeClaimBody
	res := s.Do(t, s.AgentClient(homeKey), http.MethodPost, "/api/v1/guest-home/claim",
		map[string]string{"author_name": name}, &got)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领访客主页状态码 = %d，想 201", res.StatusCode)
	}
	return got
}

func enterGuestHome(t testing.TB, s *tbtest.Server, homeKey, code string) *http.Response {
	t.Helper()
	return s.Do(t, s.AgentClient(homeKey), http.MethodPost, "/api/v1/guest-home/enter",
		map[string]string{"access_code": code}, nil)
}

// 访客级重置访问码：作废该访客全部设备（主页与所有链接）、清除锁定、生成
// 新码一次性返回；影响预览的设备数与工单数正确；名下每张工单都记录事件。
func Test访客级重置访问码作废全部设备并清除锁定(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	first := mustCreateTicket(t, s, web, "第一张共享访问码")
	firstLink := mustCreateGuestLink(t, s, web, first,
		map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三"})
	second := mustCreateTicket(t, s, web, "第二张共享访问码")
	mustCreateGuestLink(t, s, web, second, map[string]any{"note": "追问日志", "guest_id": firstLink.GuestID})
	mustCreateGuestLink(t, s, web, second, map[string]any{"note": "再次追问", "guest_id": firstLink.GuestID})

	home := copyGuestHomeText(t, s, web, firstLink.GuestID)
	homeKey := strings.TrimPrefix(home.URL, "/h/")
	claim := claimGuestHome(t, s, homeKey, "检验科 张三")
	var renewed struct {
		DeviceToken string `json:"device_token"`
	}
	if res := s.Do(t, s.AgentClient(homeKey), http.MethodPost, "/api/v1/guest-home/enter",
		map[string]string{"access_code": claim.AccessCode}, &renewed); res.StatusCode != http.StatusCreated {
		t.Fatalf("凭访问码进入状态码 = %d，想 201", res.StatusCode)
	}
	// 连续输错 5 次后锁定，正确访问码也会被拒绝。
	for i := 0; i < 5; i++ {
		enterGuestHome(t, s, homeKey, "000000")
	}
	if res := enterGuestHome(t, s, homeKey, claim.AccessCode); res.StatusCode != http.StatusForbidden {
		t.Fatalf("锁定后正确访问码状态码 = %d，想 403", res.StatusCode)
	}

	var preview guestResetPreviewBody
	res := s.Do(t, web, http.MethodGet,
		"/api/v1/guests/"+strconv.FormatInt(firstLink.GuestID, 10)+"/reset-access-code/preview", nil, &preview)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重置访问码预览状态码 = %d，想 200", res.StatusCode)
	}
	if preview.GuestName != "检验科-张三" || preview.DeviceCount != 2 || preview.TicketCount != 2 || !preview.AccessLocked {
		t.Fatalf("重置访问码预览 = %+v，想 检验科-张三/2 台设备/2 张工单/已锁定", preview)
	}

	var reset guestResetAccessCodeBody
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/guests/"+strconv.FormatInt(firstLink.GuestID, 10)+"/reset-access-code", nil, &reset)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客级重置访问码状态码 = %d，想 200", res.StatusCode)
	}
	if reset.AccessCode == claim.AccessCode || !regexp.MustCompile(`^\d{6}$`).MatchString(reset.AccessCode) {
		t.Fatalf("应返回新的 6 位访问码：old %q new %q", claim.AccessCode, reset.AccessCode)
	}
	if !strings.HasPrefix(reset.HomeURL, "/h/tbh_") || reset.HomeURL != home.URL {
		t.Fatalf("重置访问码应返回当前主页地址：got %q want %q", reset.HomeURL, home.URL)
	}
	for _, want := range []string{
		"检验科-张三：",
		reset.HomeURL,
		"新访问码：" + reset.AccessCode,
		"访问码请勿转发",
		"也请把新访问码告诉它",
	} {
		if !strings.Contains(reset.ResetText, want) {
			t.Errorf("重置文案缺少 %q：%q", want, reset.ResetText)
		}
	}

	for _, number := range []string{first, second} {
		if res := s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet,
			"/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("[%s] 认领设备状态码 = %d，想 401", number, res.StatusCode)
		}
		if res := s.Do(t, s.AgentClient(renewed.DeviceToken), http.MethodGet,
			"/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("[%s] 凭码设备状态码 = %d，想 401", number, res.StatusCode)
		}
	}
	if res := enterGuestHome(t, s, homeKey, claim.AccessCode); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("旧访问码状态码 = %d，想 401（且不应再锁定）", res.StatusCode)
	}
	var entered struct {
		DeviceToken string `json:"device_token"`
	}
	if res := s.Do(t, s.AgentClient(homeKey), http.MethodPost, "/api/v1/guest-home/enter",
		map[string]string{"access_code": reset.AccessCode}, &entered); res.StatusCode != http.StatusCreated {
		t.Fatalf("新访问码进入状态码 = %d，想 201（锁定应已清除）", res.StatusCode)
	}
	for _, number := range []string{first, second} {
		if res := s.Do(t, s.AgentClient(entered.DeviceToken), http.MethodGet,
			"/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("[%s] 新设备读取状态码 = %d，想 200", number, res.StatusCode)
		}
	}

	for _, number := range []string{first, second} {
		var view ticketResponseBody
		s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
		found := false
		for _, ev := range view.Timeline {
			if ev.Type != "guest_access_code_reset" {
				continue
			}
			found = true
			if ev.Note != "检验科-张三" {
				t.Errorf("[%s] 访客级重置事件访客名 = %q，想 检验科-张三", number, ev.Note)
			}
			if ev.Actor.Member == nil || ev.Actor.Member.Name != "陈工" {
				t.Errorf("[%s] 访客级重置事件操作者不对：%+v", number, ev.Actor)
			}
		}
		if !found {
			t.Fatalf("[%s] 时间线缺少访客级重置事件：%+v", number, view.Timeline)
		}
	}
}

// 重置主页地址只换主页凭证：旧主页地址失效，访问码、设备与单条链接都不受影响，
// 也不写工单时间线。
func Test重置主页地址旧地址失效且链接设备访问码不变(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "主页地址单独重置")
	link := mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三"})
	home := copyGuestHomeText(t, s, web, link.GuestID)
	oldKey := strings.TrimPrefix(home.URL, "/h/")
	claim := claimGuestHome(t, s, oldKey, "检验科 张三")

	var before ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &before)

	var reset guestHomeResetBody
	res := s.Do(t, web, http.MethodPost,
		"/api/v1/guests/"+strconv.FormatInt(link.GuestID, 10)+"/reset-home-key", nil, &reset)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重置主页地址状态码 = %d，想 200", res.StatusCode)
	}
	if reset.HomeURL == home.URL || !strings.HasPrefix(reset.HomeURL, "/h/tbh_") {
		t.Fatalf("应返回新的主页地址：old %q new %q", home.URL, reset.HomeURL)
	}
	if !strings.Contains(reset.ShareText, reset.HomeURL) || strings.Contains(reset.ShareText, claim.AccessCode) {
		t.Fatalf("主页重置文案应包含新地址且不含访问码：%q", reset.ShareText)
	}

	if res := s.Do(t, s.AgentClient(oldKey), http.MethodGet, "/api/v1/guest-home", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("旧主页地址状态码 = %d，想 401", res.StatusCode)
	}
	var info guestHomeBody
	if res := s.Do(t, s.AgentClient(strings.TrimPrefix(reset.HomeURL, "/h/")), http.MethodGet,
		"/api/v1/guest-home", nil, &info); res.StatusCode != http.StatusOK || info.State != "claimed" {
		t.Fatalf("新主页地址状态码/状态 = %d/%s，想 200/claimed", res.StatusCode, info.State)
	}
	if res := s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet,
		"/api/v1/tickets/"+number, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重置主页后原设备读取状态码 = %d，想 200", res.StatusCode)
	}
	var entered struct {
		DeviceToken string `json:"device_token"`
	}
	if res := s.Do(t, s.AgentClient(strings.TrimPrefix(reset.HomeURL, "/h/")), http.MethodPost,
		"/api/v1/guest-home/enter", map[string]string{"access_code": claim.AccessCode}, &entered); res.StatusCode != http.StatusCreated {
		t.Fatalf("原访问码状态码 = %d，想 201", res.StatusCode)
	}

	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	if len(after.Timeline) != len(before.Timeline) {
		t.Fatalf("重置主页地址不应写工单时间线：before %d after %d", len(before.Timeline), len(after.Timeline))
	}
}

// 重置访问码与重置主页地址只接受成员本人的网页会话；成员 Agent 一律拒绝。
// 有主链接调用链接级重置访问码时，错误应明确指向访客级接口。
func Test成员Agent不能重置访客访问码或主页地址(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "Agent 不能重置")
	link := mustCreateGuestLink(t, s, web, number,
		map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三"})
	home := copyGuestHomeText(t, s, web, link.GuestID)
	claim := claimGuestHome(t, s, strings.TrimPrefix(home.URL, "/h/"), "检验科 张三")
	_ = claim
	_, token := s.IssueAgentToken(t, web, "助手")
	agent := s.AgentClient(token)

	guestPath := "/api/v1/guests/" + strconv.FormatInt(link.GuestID, 10)
	for name, method := range map[string]string{
		"重置访问码预览": http.MethodGet + " " + guestPath + "/reset-access-code/preview",
		"重置访问码":   http.MethodPost + " " + guestPath + "/reset-access-code",
		"重置主页地址":  http.MethodPost + " " + guestPath + "/reset-home-key",
	} {
		parts := strings.SplitN(method, " ", 2)
		res := s.Do(t, agent, parts[0], parts[1], nil, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("[%s] Agent 状态码 = %d，想 403", name, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "web_session_required" {
			t.Errorf("[%s] Agent 错误码 = %q，想 web_session_required", name, got.Error.Code)
		}
	}

	res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/reset-access-code", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("有主链接重置访问码状态码 = %d，想 409", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "guest_access_code_guest_level" {
		t.Fatalf("有主链接错误码 = %q，想 guest_access_code_guest_level", got.Error.Code)
	}
	want := "/api/v1/guests/" + strconv.FormatInt(link.GuestID, 10) + "/reset-access-code"
	if !strings.Contains(got.Error.Hint, want) {
		t.Errorf("错误 hint 应指向访客级重置 %q：%q", want, got.Error.Hint)
	}
}
