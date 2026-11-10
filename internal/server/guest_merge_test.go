package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type guestMergePreviewBody struct {
	SourceGuestID   int64  `json:"source_guest_id"`
	SourceGuestName string `json:"source_guest_name"`
	TargetGuestID   int64  `json:"target_guest_id"`
	TargetGuestName string `json:"target_guest_name"`
	LinkCount       int    `json:"link_count"`
	DeviceCount     int    `json:"device_count"`
}

type guestMergeResultBody struct {
	SourceGuestID int64 `json:"source_guest_id"`
	TargetGuestID int64 `json:"target_guest_id"`
	LinkCount     int   `json:"link_count"`
	DeviceCount   int   `json:"device_count"`
}

// 合并重复访客：B 的链接改归 A；A 的凭证不变并能访问原 B 工单，B 的主页、
// 访问码和设备全部失效；历史署名不改写，新回复署名 A。
func Test合并访客后链接改归且B凭证全部失效(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	aTicket := mustCreateTicket(t, s, web, "访客A的工单")
	bTicket := mustCreateTicket(t, s, web, "访客B的工单")
	aLink := mustCreateGuestLink(t, s, web, aTicket,
		map[string]any{"note": "张三这条链接", "new_guest_name": "检验科-张三"})
	bLink := mustCreateGuestLink(t, s, web, bTicket,
		map[string]any{"note": "三哥重复建档", "new_guest_name": "检验科-张三（重复）"})
	aHome := copyGuestHomeText(t, s, web, aLink.GuestID)
	bHome := copyGuestHomeText(t, s, web, bLink.GuestID)
	aClaim := claimGuestHome(t, s, trimGuestHomeKey(aHome.URL), "检验科 张三")
	bClaim := claimGuestHome(t, s, trimGuestHomeKey(bHome.URL), "检验科 张三")
	aDevice := s.AgentClient(aClaim.DeviceToken)
	bDevice := s.AgentClient(bClaim.DeviceToken)
	if res := reply(t, s, bDevice, bTicket, "合并前的历史回复"); res.StatusCode != http.StatusCreated {
		t.Fatalf("合并前访客B回复状态码 = %d，想 201", res.StatusCode)
	}
	var beforeMergeNotices notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &beforeMergeNotices)

	mergeBase := "/api/v1/guests/" + strconv.FormatInt(bLink.GuestID, 10) +
		"/merge-into/" + strconv.FormatInt(aLink.GuestID, 10)
	var preview guestMergePreviewBody
	res := s.Do(t, web, http.MethodGet, mergeBase+"/preview", nil, &preview)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("合并影响预览状态码 = %d，想 200", res.StatusCode)
	}
	if preview.SourceGuestID != bLink.GuestID || preview.TargetGuestID != aLink.GuestID ||
		preview.SourceGuestName != "检验科-张三（重复）" || preview.TargetGuestName != "检验科-张三" ||
		preview.LinkCount != 1 || preview.DeviceCount != 1 {
		t.Fatalf("合并影响预览 = %+v，想 B 一条链接一台设备并入 A", preview)
	}

	var merged guestMergeResultBody
	res = s.Do(t, web, http.MethodPost, "/api/v1/guests/"+strconv.FormatInt(bLink.GuestID, 10)+"/merge-into",
		map[string]int64{"target_guest_id": aLink.GuestID}, &merged)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("合并访客状态码 = %d，想 200", res.StatusCode)
	}
	if merged.SourceGuestID != bLink.GuestID || merged.TargetGuestID != aLink.GuestID ||
		merged.LinkCount != 1 || merged.DeviceCount != 1 {
		t.Fatalf("合并结果 = %+v，想返回 B 一条链接一台设备已处理", merged)
	}
	var afterMergeNotices notificationsBody
	s.Do(t, web, http.MethodGet, "/api/v1/notifications", nil, &afterMergeNotices)
	if len(afterMergeNotices.Items) != len(beforeMergeNotices.Items) {
		t.Fatalf("合并访客不应发通知：%+v", afterMergeNotices.Items)
	}

	if res := s.Do(t, aDevice, http.MethodGet, "/api/v1/tickets/"+bTicket, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("A 的设备读取原 B 链接工单状态码 = %d，想 200", res.StatusCode)
	}
	if res := s.Do(t, aDevice, http.MethodGet, "/api/v1/tickets/"+aTicket, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("A 的设备读取原工单状态码 = %d，想 200", res.StatusCode)
	}
	if res := s.Do(t, bDevice, http.MethodGet, "/api/v1/tickets/"+bTicket, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("B 的设备读取原工单状态码 = %d，想 401", res.StatusCode)
	}
	if res := s.Do(t, s.AgentClient(trimGuestHomeKey(bHome.URL)), http.MethodGet, "/api/v1/guest-home", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("B 的旧主页状态码 = %d，想 401", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/guests/"+strconv.FormatInt(bLink.GuestID, 10)+"/home-share-text", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("合并后复制 B 的主页文案状态码 = %d，想 404", res.StatusCode)
	}
	assertErrorCode(t, res, "guest_already_merged")
	if res := enterGuestAccessCode(t, s, guestKeyOf(t, bLink), bClaim.AccessCode); res.StatusCode < http.StatusBadRequest {
		t.Fatalf("B 的旧访问码状态码 = %d，想失败", res.StatusCode)
	}

	var guests struct {
		Guests []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"guests"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/guests", nil, &guests)
	for _, guest := range guests.Guests {
		if guest.ID == bLink.GuestID {
			t.Fatalf("已并入的 B 仍出现在访客名单：%+v", guests.Guests)
		}
	}
	if res := createGuestLink(t, s, web, mustCreateTicket(t, s, web, "合并后新工单"),
		map[string]any{"note": "不能再选 B", "guest_id": bLink.GuestID}); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("选择已并入访客建链接状态码 = %d，想 422", res.StatusCode)
	}
	legacyTicket := mustCreateTicket(t, s, web, "合并后的老链接")
	legacy := s.CreateLegacyGuestLink(t, web, legacyTicket,
		map[string]any{"note": "上线前的联系"})
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+legacyTicket+"/guest-links/"+strconv.FormatInt(legacy.ID, 10)+"/assign-guest",
		map[string]int64{"guest_id": bLink.GuestID}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("选择已并入访客归属老链接状态码 = %d，想 422", res.StatusCode)
	}
	assertErrorCode(t, res, "guest_not_found")

	if res := reply(t, s, aDevice, bTicket, "合并后的新回复"); res.StatusCode != http.StatusCreated {
		t.Fatalf("A 的设备在原 B 工单回复状态码 = %d，想 201", res.StatusCode)
	}
	var bView ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+bTicket, nil, &bView)
	var historicalGuestName, newGuestName string
	for _, ev := range bView.Timeline {
		if ev.Type == "reply" && ev.BodyMD == "合并前的历史回复" && ev.Actor.GuestName != nil {
			historicalGuestName = *ev.Actor.GuestName
		}
		if ev.Type == "reply" && ev.BodyMD == "合并后的新回复" && ev.Actor.GuestName != nil {
			newGuestName = *ev.Actor.GuestName
		}
	}
	if historicalGuestName != "检验科-张三（重复）" {
		t.Fatalf("合并前回复署名 = %q，想保持 检验科-张三（重复）", historicalGuestName)
	}
	if newGuestName != "检验科-张三" {
		t.Fatalf("合并后回复署名 = %q，想 检验科-张三", newGuestName)
	}
	foundMergeEvent := false
	for _, ev := range bView.Timeline {
		if ev.Type != "guest_link_assigned" {
			continue
		}
		if ev.Reason == "merge" && ev.Note == "三哥重复建档" && ev.To == "检验科-张三" {
			foundMergeEvent = true
		}
	}
	if !foundMergeEvent {
		t.Fatalf("时间线缺少 reason=merge 的改归事件：%+v", bView.Timeline)
	}
}

// 合并只由能同时管理 A、B 的网页成员本人执行；非法目标一律拒绝，
// 已并入的访客既不能作为来源，也不能作为目标。
func Test合并访客拒绝Agent非法目标与权限边界(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	outside := s.Login("王工", "secret-pass")
	first := mustCreateTicket(t, s, admin, "合并目标检查A")
	second := mustCreateTicket(t, s, admin, "合并目标检查B")
	third := mustCreateTicket(t, s, admin, "合并目标检查C")
	a := mustCreateGuestLink(t, s, admin, first,
		map[string]any{"note": "A", "new_guest_name": "重复A"})
	b := mustCreateGuestLink(t, s, admin, second,
		map[string]any{"note": "B", "new_guest_name": "重复B"})
	c := mustCreateGuestLink(t, s, admin, third,
		map[string]any{"note": "C", "new_guest_name": "重复C"})
	_, token := s.IssueAgentToken(t, admin, "助手")
	agent := s.AgentClient(token)
	mergeURL := func(source int64) string {
		return "/api/v1/guests/" + strconv.FormatInt(source, 10) + "/merge-into"
	}

	previewPath := mergeURL(b.GuestID) + "/" + strconv.FormatInt(a.GuestID, 10) + "/preview"
	res := s.Do(t, agent, http.MethodGet, previewPath, nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员 Agent 读取合并预览状态码 = %d，想 403", res.StatusCode)
	}
	assertErrorCode(t, res, "web_session_required")
	res = s.Do(t, agent, http.MethodPost, mergeURL(b.GuestID),
		map[string]int64{"target_guest_id": a.GuestID}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("成员 Agent 合并访客状态码 = %d，想 403", res.StatusCode)
	}
	assertErrorCode(t, res, "web_session_required")

	res = s.Do(t, admin, http.MethodPost, mergeURL(b.GuestID),
		map[string]int64{"target_guest_id": b.GuestID}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("合并到自己状态码 = %d，想 409", res.StatusCode)
	}
	assertErrorCode(t, res, "guest_merge_self")

	res = s.Do(t, outside, http.MethodPost, mergeURL(b.GuestID),
		map[string]int64{"target_guest_id": a.GuestID}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("无关成员合并状态码 = %d，想 403", res.StatusCode)
	}
	assertErrorCode(t, res, "guest_management_not_allowed")

	if res := setGuestDisabled(t, s, admin, a.GuestID, "disable"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("停用目标访客状态码 = %d，想 204", res.StatusCode)
	}
	res = s.Do(t, admin, http.MethodPost, mergeURL(b.GuestID),
		map[string]int64{"target_guest_id": a.GuestID}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("合并到已停用访客状态码 = %d，想 409", res.StatusCode)
	}
	assertErrorCode(t, res, "guest_merge_target_disabled")
	if res := setGuestDisabled(t, s, admin, a.GuestID, "enable"); res.StatusCode != http.StatusNoContent {
		t.Fatalf("恢复目标访客状态码 = %d，想 204", res.StatusCode)
	}

	if res := s.Do(t, admin, http.MethodPost, mergeURL(b.GuestID),
		map[string]int64{"target_guest_id": a.GuestID}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("合法合并状态码 = %d，想 200", res.StatusCode)
	}
	mergedBase := "/api/v1/guests/" + strconv.FormatInt(b.GuestID, 10)
	for name, method := range map[string]string{
		"重置访问码预览": http.MethodGet + " " + mergedBase + "/reset-access-code/preview",
		"重置访问码":   http.MethodPost + " " + mergedBase + "/reset-access-code",
		"重置主页地址":  http.MethodPost + " " + mergedBase + "/reset-home-key",
	} {
		parts := strings.SplitN(method, " ", 2)
		res := s.Do(t, admin, parts[0], parts[1], nil, nil)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("[%s] 操作已并入访客状态码 = %d，想 409", name, res.StatusCode)
		}
		assertErrorCode(t, res, "guest_already_merged")
	}
	res = s.Do(t, admin, http.MethodPost, mergeURL(c.GuestID),
		map[string]int64{"target_guest_id": b.GuestID}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("合并到已并入访客状态码 = %d，想 409", res.StatusCode)
	}
	assertErrorCode(t, res, "guest_merge_target_merged")
	res = s.Do(t, admin, http.MethodPost, mergeURL(b.GuestID),
		map[string]int64{"target_guest_id": c.GuestID}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("再次合并已并入访客状态码 = %d，想 409", res.StatusCode)
	}
	assertErrorCode(t, res, "guest_already_merged")
}

func trimGuestHomeKey(url string) string {
	if len(url) >= 3 && url[:3] == "/h/" {
		return url[3:]
	}
	return url
}

func assertErrorCode(t testing.TB, res *http.Response, code string) {
	t.Helper()
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != code {
		t.Fatalf("错误码 = %q，想 %q（message=%q）", got.Error.Code, code, got.Error.Message)
	}
}
