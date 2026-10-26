package server_test

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test再次复制新访客链接得到同一条可打开链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	created := createWithGuestLink(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 质控时报警 E-203",
		"fields":   map[string]string{},
		"labels":   []string{},
		"guest_link": map[string]any{
			"note":           "检验科-张三",
			"can_edit_brief": true,
		},
	})
	var first feedbackCreateResponse
	tbtest.DecodeJSON(t, created, &first)

	res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+first.Number+"/guest-links/"+strconv.FormatInt(first.GuestLink.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("再次复制转发文案状态码 = %d, 想 200", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, 想 no-store", got)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if copied.ShareText != first.GuestLink.ShareText {
		t.Fatalf("再次复制的文案不一致：\ngot  %q\nwant %q", copied.ShareText, first.GuestLink.ShareText)
	}
	if !strings.Contains(copied.ShareText, first.GuestLink.URL) {
		t.Fatalf("文案里应是同一条链接：\n%q\nwant %q", copied.ShareText, first.GuestLink.URL)
	}

	linkKey := strings.TrimPrefix(first.GuestLink.URL, "/g/")
	res = s.Do(t, s.AgentClient(linkKey), http.MethodGet, "/api/v1/guest-link", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("文案里的链接能打开认领页：状态码 = %d, 想 200", res.StatusCode)
	}

	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+first.Number+"/guest-links", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读访客链接列表状态码 = %d, 想 200", res.StatusCode)
	}
	var raw struct {
		Links []map[string]any `json:"links"`
	}
	tbtest.DecodeJSON(t, res, &raw)
	if len(raw.Links) != 1 {
		t.Fatalf("列表有 %d 条链接, 想 1", len(raw.Links))
	}
	if got, ok := raw.Links[0]["share_text_available"].(bool); !ok || !got {
		t.Errorf("share_text_available = %v, 想 true", raw.Links[0]["share_text_available"])
	}
	encoded, _ := json.Marshal(raw.Links[0])
	if strings.Contains(string(encoded), linkKey) ||
		strings.Contains(string(encoded), first.GuestLink.URL) {
		t.Errorf("访客链接列表不应回显密钥或完整地址：%s", encoded)
	}
}

func Test再次复制转发文案按当前标题和有效期生成(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	created := createWithGuestLink(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 报警",
		"fields":   map[string]string{},
		"labels":   []string{},
		"guest_link": map[string]any{
			"note":           "检验科-张三",
			"can_edit_brief": true,
		},
	})
	var first feedbackCreateResponse
	tbtest.DecodeJSON(t, created, &first)

	res := revise(t, s, web, first.Number, map[string]any{
		"title": "BH-7600CS 打印异常", "base_revision": 1,
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("修改标题状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+first.Number+"/guest-links/"+strconv.FormatInt(first.GuestLink.ID, 10)+"/extend",
		map[string]any{"valid_days": 90}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("延期状态码 = %d, 想 200", res.StatusCode)
	}

	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+first.Number+"/guest-links/"+strconv.FormatInt(first.GuestLink.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("再次复制状态码 = %d, 想 200", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if !strings.Contains(copied.ShareText, "BH-7600CS 打印异常") {
		t.Errorf("文案应使用当前标题：%q", copied.ShareText)
	}
	if !strings.Contains(copied.ShareText, "2027-01-14") {
		t.Errorf("文案应使用延期后的有效期 2027-01-14：%q", copied.ShareText)
	}
	if !strings.Contains(copied.ShareText, first.GuestLink.URL) {
		t.Errorf("延期与改标题不应换链接：%q", copied.ShareText)
	}
}

func Test认领后再次复制转发文案提醒访问码(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "换设备需要访问码")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})

	res := s.Do(t, s.AgentClient(guestKeyOf(t, link)), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "张三"}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领状态码 = %d, 想 201", res.StatusCode)
	}

	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("已认领链接再次复制状态码 = %d, 想 200", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if !strings.Contains(copied.ShareText, "换了设备需要输入当初的访问码，忘了请找我重置") {
		t.Errorf("已认领文案应提醒访问码：%q", copied.ShareText)
	}
	if !strings.Contains(copied.ShareText, link.URL) {
		t.Errorf("已认领文案仍应包含原链接：%q", copied.ShareText)
	}
}

func Test反馈入口生成的链接也能再次复制并提醒访问码(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	submitted := submitToIntake(t, s, "bug", map[string]any{
		"title": "UD-2300 报警", "fields": bugFields(), "author_name": "赵六",
	})
	var list struct {
		Links []struct {
			ID                 int64 `json:"id"`
			ShareTextAvailable bool  `json:"share_text_available"`
		} `json:"links"`
	}
	res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number+"/guest-links", nil, &list)
	if res.StatusCode != http.StatusOK || len(list.Links) != 1 {
		t.Fatalf("读入口生成的链接列表 = %d/%d 条, 想 200/1 条", res.StatusCode, len(list.Links))
	}
	if !list.Links[0].ShareTextAvailable {
		t.Fatal("入口生成的链接应可直接复制转发文案")
	}

	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+submitted.Ticket.Number+"/guest-links/"+strconv.FormatInt(list.Links[0].ID, 10)+"/share-text",
		nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("再次复制入口链接状态码 = %d, 想 200", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if !strings.Contains(copied.ShareText, submitted.GuestLink.URL) {
		t.Errorf("文案应是提交者拿到的同一条链接：%q", copied.ShareText)
	}
	if !strings.Contains(copied.ShareText, "换了设备需要输入当初的访问码，忘了请找我重置") {
		t.Errorf("入口链接提交时已认领，应提醒访问码：%q", copied.ShareText)
	}
}

func Test失效链接不能复制且访客无管理权限(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "失效链接不能复制")
	revoked := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "已撤销", "valid_days": 90})
	expiring := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "将过期", "valid_days": 7})
	active := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "有效", "valid_days": 90})

	res := s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(revoked.ID, 10), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销状态码 = %d, 想 204", res.StatusCode)
	}
	s.Clock().Advance(8 * 24 * time.Hour)
	web = s.Login("陈工", "secret-pass") // 假时钟推进后会话已过期，重新登录再测 API。

	for name, id := range map[string]int64{"已撤销": revoked.ID, "已过期": expiring.ID} {
		res := s.Do(t, web, http.MethodPost,
			"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(id, 10)+"/share-text", nil, nil)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("[%s] 复制状态码 = %d, 想 409", name, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "link_not_extendable" {
			t.Errorf("[%s] 错误码 = %q, 想 link_not_extendable", name, got.Error.Code)
		}
	}

	var list struct {
		Links []struct {
			ID                 int64 `json:"id"`
			ShareTextAvailable bool  `json:"share_text_available"`
		} `json:"links"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, &list)
	byID := map[int64]bool{}
	for _, link := range list.Links {
		byID[link.ID] = link.ShareTextAvailable
	}
	if byID[revoked.ID] || byID[expiring.ID] {
		t.Errorf("失效链接不应可复制：%v", byID)
	}
	if !byID[active.ID] {
		t.Errorf("有效链接应可复制：%v", byID)
	}

	var claimed struct {
		DeviceToken string `json:"device_token"`
	}
	res = s.Do(t, s.AgentClient(guestKeyOf(t, active)), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "张三"}, &claimed)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领状态码 = %d, 想 201", res.StatusCode)
	}
	res = s.Do(t, s.AgentClient(claimed.DeviceToken), http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(active.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客复制状态码 = %d, 想 403", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "members_only" {
		t.Errorf("访客错误码 = %q, 想 members_only", got.Error.Code)
	}
}

func Test老链接不能直接复制但不影响访问(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "老链接还能访问")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	s.ForgetGuestLinkKey(t, link.ID)

	res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("老链接复制状态码 = %d, 想 409", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "share_text_unavailable" {
		t.Errorf("错误码 = %q, 想 share_text_unavailable", got.Error.Code)
	}
	if !strings.Contains(got.Error.Hint, "换一个新地址") {
		t.Errorf("hint 应提示换新地址：%q", got.Error.Hint)
	}

	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, nil)
	var list struct {
		Links []struct {
			ID                 int64 `json:"id"`
			ShareTextAvailable bool  `json:"share_text_available"`
		} `json:"links"`
	}
	tbtest.DecodeJSON(t, res, &list)
	if len(list.Links) != 1 || list.Links[0].ShareTextAvailable {
		t.Errorf("老链接 share_text_available 应为 false：%+v", list.Links)
	}

	res = s.Do(t, s.AgentClient(guestKeyOf(t, link)), http.MethodGet, "/api/v1/guest-link", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Errorf("老链接仍应能打开认领页：状态码 = %d, 想 200", res.StatusCode)
	}
}

func Test服务重启后仍能再次复制同一条链接(t *testing.T) {
	dataDir := t.TempDir()
	s := tbtest.Start(t, tbtest.WithDataDir(dataDir))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "重启后还能找到链接")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	now := s.Clock().Now()
	s.Shutdown(t)

	s = tbtest.Start(t, tbtest.WithDataDir(dataDir), tbtest.WithClockStart(now))
	web = s.Login("陈工", "secret-pass")
	res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重启后再次复制状态码 = %d, 想 200", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if !strings.Contains(copied.ShareText, link.URL) {
		t.Fatalf("重启后应还原同一条链接：%q\nwant %q", copied.ShareText, link.URL)
	}
}

func Test配置public_url时再次复制用绝对链接(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://taskboard.example"))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	created := createWithGuestLink(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "绝对地址",
		"fields":   map[string]string{},
		"labels":   []string{},
		"guest_link": map[string]any{
			"note":           "检验科-张三",
			"can_edit_brief": true,
		},
	})
	var first feedbackCreateResponse
	tbtest.DecodeJSON(t, created, &first)

	res := s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+first.Number+"/guest-links/"+strconv.FormatInt(first.GuestLink.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("再次复制状态码 = %d, 想 200", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if !strings.Contains(copied.ShareText, first.GuestLink.URL) ||
		!strings.HasPrefix(first.GuestLink.URL, "https://taskboard.example/g/") {
		t.Errorf("文案应包含 public_url 绝对链接：%q / %q", copied.ShareText, first.GuestLink.URL)
	}
}
