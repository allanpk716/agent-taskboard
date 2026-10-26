package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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

	for name, id := range map[string]int64{"已撤销": revoked.ID, "已过期": expiring.ID} {
		res := s.Do(t, web, http.MethodPost,
			"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(id, 10)+"/rotate-key", nil, nil)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("[%s] 换新地址状态码 = %d, 想 409", name, res.StatusCode)
		}
		if got := res.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("[%s] Cache-Control = %q, 想 no-store", name, got)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "link_not_extendable" {
			t.Errorf("[%s] 换新地址错误码 = %q, 想 link_not_extendable", name, got.Error.Code)
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
	res = s.Do(t, s.AgentClient(claimed.DeviceToken), http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(active.ID, 10)+"/rotate-key", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客换新地址状态码 = %d, 想 403", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "members_only" {
		t.Errorf("访客换新地址错误码 = %q, 想 members_only", got.Error.Code)
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

func Test老访客链接换新地址保留认领并记入时间线(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "老链接换新地址")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{
		"note": "检验科-张三", "can_edit_brief": true,
	})

	var claim struct {
		DeviceToken string `json:"device_token"`
		AccessCode  string `json:"access_code"`
	}
	res := s.Do(t, s.AgentClient(guestKeyOf(t, link)), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "张三"}, &claim)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领状态码 = %d, 想 201", res.StatusCode)
	}
	s.ForgetGuestLinkKey(t, link.ID)

	var before ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &before)

	idem := map[string]string{"Idempotency-Key": "rotate-guest-link-once"}
	rotatePath := "/api/v1/tickets/" + number + "/guest-links/" + strconv.FormatInt(link.ID, 10) + "/rotate-key"
	res = s.DoWithHeaders(t, web, http.MethodPost, rotatePath, idem, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("老链接换新地址状态码 = %d, 想 200", res.StatusCode)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, 想 no-store", got)
	}
	first := readAll(t, res)
	var rotated struct {
		URL       string `json:"url"`
		ShareText string `json:"share_text"`
	}
	if err := json.Unmarshal(first, &rotated); err != nil {
		t.Fatalf("解码换新地址响应失败：%v", err)
	}
	if rotated.URL == link.URL || !strings.HasPrefix(rotated.URL, "/g/tbg_") {
		t.Fatalf("应换成一个新地址：old %q, new %q", link.URL, rotated.URL)
	}
	if !strings.Contains(rotated.ShareText, rotated.URL) ||
		!strings.Contains(rotated.ShareText, "换了设备需要输入当初的访问码") {
		t.Errorf("新转发文案应包含新地址与访问码提醒：%q", rotated.ShareText)
	}

	res = s.DoWithHeaders(t, web, http.MethodPost, rotatePath, idem, nil, nil)
	if res.StatusCode != http.StatusOK || string(first) != string(readAll(t, res)) {
		t.Fatalf("幂等重放应返回首次结果：%d / %d", res.StatusCode, http.StatusOK)
	}

	if res := s.Do(t, s.AgentClient(guestKeyOf(t, link)), http.MethodGet, "/api/v1/guest-link", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("旧地址应立即失效：状态码 = %d, 想 401", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Errorf("已认领设备应继续访问：状态码 = %d, 想 200", res.StatusCode)
	}
	newKey := strings.TrimPrefix(rotated.URL, "/g/")
	res = s.Do(t, s.AgentClient(newKey), http.MethodPost, "/api/v1/guest-links/enter",
		map[string]string{"access_code": claim.AccessCode}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Errorf("旧访问码应继续可用：状态码 = %d, 想 201", res.StatusCode)
	}

	var list struct {
		Links []struct {
			Note               string `json:"note"`
			ExpiresAt          string `json:"expires_at"`
			CanEditBrief       bool   `json:"can_edit_brief"`
			ShareTextAvailable bool   `json:"share_text_available"`
			Status             string `json:"status"`
			ClaimName          string `json:"claim_name"`
		} `json:"links"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, &list)
	if len(list.Links) != 1 {
		t.Fatalf("换新地址不应新增链接，现有 %d 条", len(list.Links))
	}
	got := list.Links[0]
	if got.Note != link.Note || got.ExpiresAt != link.ExpiresAt || !got.CanEditBrief ||
		got.Status != "claimed" || got.ClaimName != "张三" || !got.ShareTextAvailable {
		t.Errorf("链接备注、有效期、权限、认领状态或可复制状态不对：%+v", got)
	}

	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("换新地址后应能直接复制：状态码 = %d, 想 200", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if copied.ShareText != rotated.ShareText {
		t.Errorf("直接复制文案应与换新地址响应一致：\ngot  %q\nwant %q", copied.ShareText, rotated.ShareText)
	}

	var after ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &after)
	var event *timelineEventBody
	for i := range after.Timeline {
		if after.Timeline[i].Type == "guest_link_rotated" {
			if event != nil {
				t.Fatalf("guest_link_rotated 事件有多条")
			}
			event = &after.Timeline[i]
		}
	}
	if event == nil {
		t.Fatal("时间线应有 guest_link_rotated 事件")
	}
	if event.Note != link.Note || event.Change <= before.Timeline[len(before.Timeline)-1].Change {
		t.Errorf("换新地址事件应带备注并推进全局变更序号：%+v", event)
	}
	var guestView ticketResponseBody
	s.Do(t, s.AgentClient(claim.DeviceToken), http.MethodGet, "/api/v1/tickets/"+number, nil, &guestView)
	found := false
	for _, ev := range guestView.Timeline {
		if ev.Type == "guest_link_rotated" {
			found = true
		}
	}
	if !found {
		t.Error("访客时间线也应看到换新地址事件")
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

// 密钥文件内容损坏时重启：服务照常启动，已有链接照常访问（鉴权只看哈希），
// 但全部按老链接处理；换新地址后恢复可复制。重启后新建的链接用新密钥，
// 可以直接复制。
func Test密钥文件内容损坏时按老链接降级且新链接可直接复制(t *testing.T) {
	dataDir := t.TempDir()
	s := tbtest.Start(t, tbtest.WithDataDir(dataDir))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "密钥损坏后链接还能用")
	first := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	second := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "设备科-李四"})
	now := s.Clock().Now()
	s.Shutdown(t)

	if err := os.WriteFile(filepath.Join(dataDir, "guest-link-key.bin"), []byte("not-a-key"), 0o600); err != nil {
		t.Fatal(err)
	}
	s = tbtest.Start(t, tbtest.WithDataDir(dataDir), tbtest.WithClockStart(now))
	web = s.Login("陈工", "secret-pass")

	kept, err := filepath.Glob(filepath.Join(dataDir, "guest-link-key.bin.corrupt-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != 1 {
		t.Fatalf("坏密钥文件应改名留存且不删除，找到 %d 个：%v", len(kept), kept)
	}
	if got, err := os.ReadFile(kept[0]); err != nil || string(got) != "not-a-key" {
		t.Errorf("留存的坏密钥文件内容应保持原样：got %q, err %v", got, err)
	}

	res := s.Do(t, s.AgentClient(guestKeyOf(t, first)), http.MethodGet, "/api/v1/guest-link", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("密钥损坏后旧链接应照常访问：状态码 = %d, 想 200", res.StatusCode)
	}

	var list struct {
		Links []struct {
			ID                 int64  `json:"id"`
			Note               string `json:"note"`
			ShareTextAvailable bool   `json:"share_text_available"`
		} `json:"links"`
	}
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, &list)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读访客链接列表状态码 = %d, 想 200", res.StatusCode)
	}
	if len(list.Links) != 2 {
		t.Fatalf("列表有 %d 条链接, 想 2", len(list.Links))
	}
	for _, link := range list.Links {
		if link.ShareTextAvailable {
			t.Errorf("链接 %s 应按老链接处理，share_text_available 应为假", link.Note)
		}
	}

	for _, id := range []int64{first.ID, second.ID} {
		res := s.Do(t, web, http.MethodPost,
			"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(id, 10)+"/share-text", nil, nil)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("旧密文复制状态码 = %d, 想 409", res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "share_text_unavailable" {
			t.Errorf("错误码 = %q, 想 share_text_unavailable", got.Error.Code)
		}
	}

	fresh := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-王五"})
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(fresh.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("重启后新建的链接应能直接复制：状态码 = %d, 想 200", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	tbtest.DecodeJSON(t, res, &copied)
	if !strings.Contains(copied.ShareText, fresh.URL) {
		t.Fatalf("新链接文案应含同一条链接：%q\nwant %q", copied.ShareText, fresh.URL)
	}

	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(first.ID, 10)+"/rotate-key", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("老链接换新地址状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(first.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("换新地址后应恢复可复制：状态码 = %d, 想 200", res.StatusCode)
	}
}

// 删掉密钥文件后重启：首次启动逻辑会生成新密钥，旧链接照常访问（鉴权只看
// 哈希），旧密文全部按老链接处理；换新地址后恢复可复制。
func Test删掉密钥文件后重启旧链接按老链接处理(t *testing.T) {
	dataDir := t.TempDir()
	s := tbtest.Start(t, tbtest.WithDataDir(dataDir))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "密钥丢失后链接还能用")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	now := s.Clock().Now()
	s.Shutdown(t)

	if err := os.Remove(filepath.Join(dataDir, "guest-link-key.bin")); err != nil {
		t.Fatal(err)
	}
	s = tbtest.Start(t, tbtest.WithDataDir(dataDir), tbtest.WithClockStart(now))
	web = s.Login("陈工", "secret-pass")

	res := s.Do(t, s.AgentClient(guestKeyOf(t, link)), http.MethodGet, "/api/v1/guest-link", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("密钥丢失后旧链接应照常访问：状态码 = %d, 想 200", res.StatusCode)
	}

	var list struct {
		Links []struct {
			ShareTextAvailable bool `json:"share_text_available"`
		} `json:"links"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, &list)
	if len(list.Links) != 1 {
		t.Fatalf("列表有 %d 条链接, 想 1", len(list.Links))
	}
	if list.Links[0].ShareTextAvailable {
		t.Error("密钥丢失后 share_text_available 应为假")
	}

	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("复制状态码 = %d, 想 409", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "share_text_unavailable" {
		t.Errorf("错误码 = %q, 想 share_text_unavailable", got.Error.Code)
	}

	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/rotate-key", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("换新地址状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+number+"/guest-links/"+strconv.FormatInt(link.ID, 10)+"/share-text", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("换新地址后应恢复可复制：状态码 = %d, 想 200", res.StatusCode)
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
