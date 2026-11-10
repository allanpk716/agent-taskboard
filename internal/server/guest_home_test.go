package server_test

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type guestHomeTicketBody struct {
	Number    string `json:"number"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	API       string `json:"api"`
	WaitingOn bool   `json:"waiting_on_you"`
	Reason    string `json:"reason"`
	ReadOnly  bool   `json:"read_only"`
}

type guestHomeBody struct {
	State     string                `json:"state"`
	GuestName string                `json:"guest_name"`
	Tickets   []guestHomeTicketBody `json:"tickets"`
}

func copyGuestHomeText(t testing.TB, s *tbtest.Server, c *http.Client, guestID int64) struct {
	URL       string `json:"home_url"`
	ShareText string `json:"share_text"`
} {
	t.Helper()
	var got struct {
		URL       string `json:"home_url"`
		ShareText string `json:"share_text"`
	}
	res := s.Do(t, c, http.MethodPost, "/api/v1/guests/"+strconv.FormatInt(guestID, 10)+"/home-share-text", nil, &got)
	if res.StatusCode != http.StatusOK {
		var errBody errorBody
		tbtest.DecodeJSON(t, res, &errBody)
		t.Fatalf("复制访客主页文案状态码 = %d，想 200：%+v", res.StatusCode, errBody)
	}
	return got
}

// 访客主页是长期凭证：首次打开即认领整个访客身份，列表只收有效链接，
// 轮到访客的排前并给原因；已关闭工单只读，打开具体工单才写访问记录。
func Test访客主页认领列表与进入工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	complete := mustCreateTicket(t, s, web, "请补全质控说明")
	completeLink := mustCreateGuestLink(t, s, web, complete,
		map[string]any{"note": "补全说明", "new_guest_name": "检验科-张三", "can_edit_brief": true})
	var repliedCreated struct {
		Number string `json:"number"`
	}
	res := createWithGuestLink(t, s, web, map[string]any{
		"kind": "report", "title": "打印机异常", "brief_md": "打印日志待补充",
		"labels": []string{"机型:UD-2300"}, "guest_link": map[string]any{
			"note": "追问打印日志", "guest_id": completeLink.GuestID,
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建反馈工单状态码 = %d，想 201", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &repliedCreated)
	replied := repliedCreated.Number
	if res := reply(t, s, web, replied, "请把打印日志发一下"); res.StatusCode != http.StatusCreated {
		t.Fatalf("成员回复状态码 = %d，想 201", res.StatusCode)
	}
	closed := mustCreateTicket(t, s, web, "已处理的旧工单")
	mustCreateGuestLink(t, s, web, closed,
		map[string]any{"note": "回看结果", "guest_id": completeLink.GuestID})
	if res := s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+closed+"/close",
		map[string]any{"reason": "done"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d，想 200", res.StatusCode)
	}
	revokedTicket := mustCreateTicket(t, s, web, "撤销后的工单")
	revoked := mustCreateGuestLink(t, s, web, revokedTicket,
		map[string]any{"note": "误发", "guest_id": completeLink.GuestID})
	if res := s.Do(t, web, http.MethodDelete,
		"/api/v1/tickets/"+revokedTicket+"/guest-links/"+strconv.FormatInt(revoked.ID, 10), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销访客链接状态码 = %d，想 204", res.StatusCode)
	}
	expiredTicket := mustCreateTicket(t, s, web, "过期后的工单")
	_ = mustCreateGuestLink(t, s, web, expiredTicket, map[string]any{
		"note": "短期联系", "guest_id": completeLink.GuestID,
		"expires_at": s.Clock().Now().Add(time.Hour).Format(time.RFC3339Nano),
	})
	s.Clock().Advance(2 * time.Hour)

	home := copyGuestHomeText(t, s, web, completeLink.GuestID)
	if !strings.HasPrefix(home.URL, "/h/tbh_") {
		t.Fatalf("主页地址 = %q，想使用 /h/tbh_ 前缀", home.URL)
	}
	if strings.Contains(home.ShareText, "access_code") || strings.Contains(home.ShareText, "新访问码：") {
		t.Fatalf("主页文案不应包含访问码：%q", home.ShareText)
	}
	again := copyGuestHomeText(t, s, web, completeLink.GuestID)
	if again.URL != home.URL || again.ShareText == "" {
		t.Fatalf("主页文案应可重复复制且地址稳定：%q / %q", again.URL, again.ShareText)
	}
	homeKey := strings.TrimPrefix(home.URL, "/h/")

	raw := s.AgentClient(homeKey)
	var info guestHomeBody
	res = s.Do(t, raw, http.MethodGet, "/api/v1/guest-home", nil, &info)
	if res.StatusCode != http.StatusOK || info.State != "unclaimed" || info.GuestName != "检验科-张三" {
		t.Fatalf("未认领主页信息 = %d/%+v，想 200/unclaimed", res.StatusCode, info)
	}
	var claim struct {
		DeviceToken string `json:"device_token"`
		AccessCode  string `json:"access_code"`
	}
	res = s.Do(t, raw, http.MethodPost, "/api/v1/guest-home/claim",
		map[string]string{"author_name": "检验科 张三"}, &claim)
	if res.StatusCode != http.StatusCreated || claim.DeviceToken == "" || len(claim.AccessCode) != 6 {
		t.Fatalf("认领主页 = %d/%+v，想 201 且一次性设备凭证与访问码", res.StatusCode, claim)
	}
	res = s.Do(t, raw, http.MethodGet, "/api/v1/guest-home", nil, &info)
	if res.StatusCode != http.StatusOK || info.State != "claimed" {
		t.Fatalf("已认领主页的原始密钥应进入输码态 = %d/%+v，想 200/claimed", res.StatusCode, info)
	}
	var entered struct {
		DeviceToken string `json:"device_token"`
	}
	res = s.Do(t, raw, http.MethodPost, "/api/v1/guest-home/enter",
		map[string]string{"access_code": claim.AccessCode}, &entered)
	if res.StatusCode != http.StatusCreated || entered.DeviceToken == "" {
		t.Fatalf("凭主页访问码换设备 = %d/%+v，想 201", res.StatusCode, entered)
	}

	guest := s.AgentClient(claim.DeviceToken)
	var list guestHomeBody
	res = s.Do(t, guest, http.MethodGet, "/api/v1/guest-home", nil, &list)
	if res.StatusCode != http.StatusOK || list.State != "claimed" {
		t.Fatalf("认领后的主页 = %d/%+v，想 200/claimed", res.StatusCode, list)
	}
	if len(list.Tickets) != 3 {
		t.Fatalf("主页工单有 %d 张，想 3（撤销和过期不列）：%+v", len(list.Tickets), list.Tickets)
	}
	if !list.Tickets[0].WaitingOn || list.Tickets[0].Reason == "" {
		t.Errorf("轮到访客的工单应排最前并带原因：%+v", list.Tickets[0])
	}
	if !list.Tickets[1].WaitingOn || list.Tickets[1].Reason == "" {
		t.Errorf("两张轮到访客的工单都应带原因：%+v", list.Tickets[1])
	}
	if item := homeTicket(list, complete); item == nil || item.Reason != "请补全说明" {
		t.Errorf("补全说明工单应带「请补全说明」：%+v", list.Tickets)
	}
	if item := homeTicket(list, replied); item == nil || item.Reason != "对方回复了" {
		t.Errorf("成员回复后的工单应带「对方回复了」：%+v", list.Tickets)
	}
	if list.Tickets[2].Number != closed || !list.Tickets[2].ReadOnly || list.Tickets[2].WaitingOn {
		t.Errorf("已关闭工单应只读且排在后面：%+v", list.Tickets[2])
	}
	if item := homeTicket(list, complete); item == nil || item.API != "/api/v1/tickets/"+complete {
		t.Errorf("补全说明工单的 API 不正确：%+v", list.Tickets)
	}
	for _, ref := range []string{revokedTicket, expiredTicket} {
		for _, item := range list.Tickets {
			if item.Number == ref {
				t.Errorf("失效链接对应的工单 %s 不应出现在主页", ref)
			}
		}
	}

	records := readAccessRecords(t, s, web, complete)
	if len(records.Records) != 0 {
		t.Fatalf("只打开主页不应写访问记录：%+v", records.Records)
	}
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+complete, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("从主页打开工单状态码 = %d，想 200", res.StatusCode)
	}
	records = readAccessRecords(t, s, web, complete)
	if len(records.Records) != 1 || records.Records[0].Action != "view" {
		t.Fatalf("打开工单应写一条查看记录：%+v", records.Records)
	}
	if res := s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+complete+"/replies",
		map[string]string{"body_md": "质控说明已补全", "author_name": "检验科 张三"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("主页进入后的回复状态码 = %d，想 201", res.StatusCode)
	}
	if res := s.Do(t, guest, http.MethodPost, "/api/v1/tickets/"+closed+"/replies",
		map[string]string{"body_md": "已处理", "author_name": "检验科 张三"}, nil); res.StatusCode != http.StatusConflict {
		t.Fatalf("已关闭工单回复状态码 = %d，想 409", res.StatusCode)
	}

	linkKey := guestKeyOf(t, completeLink)
	linkClient := s.AgentClient(linkKey + ":" + claim.AccessCode)
	if res := s.Do(t, linkClient, http.MethodGet, "/api/v1/tickets/"+complete, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("认领主页后名下链接应直接可信，状态码 = %d", res.StatusCode)
	}
	var session struct {
		Viewer struct {
			Kind  string `json:"kind"`
			Guest struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"guest"`
			Home struct {
				API string `json:"api"`
			} `json:"home"`
		} `json:"viewer"`
	}
	res = s.Do(t, guest, http.MethodGet, "/api/v1/session", nil, &session)
	if res.StatusCode != http.StatusOK || session.Viewer.Kind != "guest" ||
		session.Viewer.Guest.Name != "检验科-张三" || session.Viewer.Home.API != "/api/v1/guest-home" {
		t.Fatalf("访客会话 = %d/%+v，想返回访客身份和主页信息", res.StatusCode, session)
	}
}

func homeTicket(list guestHomeBody, number string) *guestHomeTicketBody {
	for i := range list.Tickets {
		if list.Tickets[i].Number == number {
			return &list.Tickets[i]
		}
	}
	return nil
}

// 主页 + 访问码的 Agent 凭证能批量列出全部工单；错误计数与链接凭证共享。
func Test主页Agent凭证可批量处理并共享锁定计数(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	first := mustCreateTicket(t, s, web, "主页 Agent 第一张")
	firstLink := mustCreateGuestLink(t, s, web, first,
		map[string]any{"note": "第一张", "new_guest_name": "检验科-张三"})
	second := mustCreateTicket(t, s, web, "主页 Agent 第二张")
	_ = mustCreateGuestLink(t, s, web, second,
		map[string]any{"note": "第二张", "guest_id": firstLink.GuestID})

	home := copyGuestHomeText(t, s, web, firstLink.GuestID)
	homeKey := strings.TrimPrefix(home.URL, "/h/")
	var claim struct {
		DeviceToken string `json:"device_token"`
		AccessCode  string `json:"access_code"`
	}
	res := s.Do(t, s.AgentClient(homeKey), http.MethodPost, "/api/v1/guest-home/claim",
		map[string]string{"author_name": "检验科 张三"}, &claim)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领主页状态码 = %d，想 201", res.StatusCode)
	}

	agent := s.AgentClient(homeKey + ":" + claim.AccessCode)
	var list guestHomeBody
	res = s.Do(t, agent, http.MethodGet, "/api/v1/guest-home", nil, &list)
	if res.StatusCode != http.StatusOK || len(list.Tickets) != 2 {
		t.Fatalf("主页 Agent 列表 = %d/%+v，想两张工单", res.StatusCode, list)
	}
	if res := s.Do(t, agent, http.MethodGet, "/api/v1/tickets/"+first, nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("主页 Agent 读工单状态码 = %d，想 200", res.StatusCode)
	}
	if res := s.Do(t, agent, http.MethodPost, "/api/v1/tickets/"+first+"/replies",
		map[string]string{"body_md": "Agent 按主页批量处理", "author_name": "检验科 张三（Agent 代写）"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("主页 Agent 回复状态码 = %d，想 201", res.StatusCode)
	}

	for i := 0; i < 4; i++ {
		res := s.Do(t, s.AgentClient(homeKey+":000000"), http.MethodGet, "/api/v1/guest-home", nil, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Fatalf("第 %d 次错误主页访问码状态码 = %d，想 401", i+1, res.StatusCode)
		}
	}
	linkKey := guestKeyOf(t, firstLink)
	res = s.Do(t, s.AgentClient(linkKey+":000000"), http.MethodGet, "/api/v1/tickets/"+first, nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("主页与链接凭证应共享第 5 次锁定，状态码 = %d，想 403", res.StatusCode)
	}
}

// 部门访客名单全员可见，但主页凭证只交给能管理这位访客的成员；
// 无关成员不能借主页绕过工单可见范围。
func Test无关成员不能复制访客主页凭证(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	wang := s.Login("王工", "member-pass")
	number := mustCreateTicket(t, s, admin, "只有管理员处理的工单")
	link := mustCreateGuestLink(t, s, admin, number,
		map[string]any{"note": "检验科联系", "new_guest_name": "检验科-张三"})

	res := s.Do(t, wang, http.MethodPost,
		"/api/v1/guests/"+strconv.FormatInt(link.GuestID, 10)+"/home-share-text", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("无关成员复制主页状态码 = %d，想 403", res.StatusCode)
	}
	var err errorBody
	tbtest.DecodeJSON(t, res, &err)
	if err.Error.Code != "guest_management_not_allowed" {
		t.Fatalf("错误码 = %q，想 guest_management_not_allowed", err.Error.Code)
	}
	if res := s.Do(t, admin, http.MethodPost,
		"/api/v1/guests/"+strconv.FormatInt(link.GuestID, 10)+"/home-share-text", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("访客建立者复制主页状态码 = %d，想 200", res.StatusCode)
	}
}

// 转发文案在访客未认领时附主页地址；认领后只提示工单已加到主页。
func Test转发文案按访客认领状态附主页(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	var created struct {
		Number    string `json:"number"`
		GuestLink struct {
			ID        int64  `json:"id"`
			GuestID   int64  `json:"guest_id"`
			ShareText string `json:"share_text"`
		} `json:"guest_link"`
	}
	res := createWithGuestLink(t, s, web, map[string]any{
		"template": "bug-report", "title": "未认领时的转发文案", "fields": map[string]string{},
		"labels":     []string{"产品线:血液"},
		"guest_link": map[string]any{"note": "请补全说明", "new_guest_name": "检验科-张三"},
	})
	if res.StatusCode != http.StatusCreated {
		var errBody errorBody
		tbtest.DecodeJSON(t, res, &errBody)
		t.Fatalf("建单状态码 = %d，想 201：%+v", res.StatusCode, errBody)
	}
	tbtest.DecodeJSON(t, res, &created)
	if !strings.Contains(created.GuestLink.ShareText, "/h/tbh_") {
		t.Fatalf("未认领转发文案应附主页地址：%q", created.GuestLink.ShareText)
	}

	home := copyGuestHomeText(t, s, web, created.GuestLink.GuestID)
	homeKey := strings.TrimPrefix(home.URL, "/h/")
	var claim struct {
		DeviceToken string `json:"device_token"`
	}
	res = s.Do(t, s.AgentClient(homeKey), http.MethodPost, "/api/v1/guest-home/claim",
		map[string]string{"author_name": "检验科 张三"}, &claim)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领主页状态码 = %d，想 201", res.StatusCode)
	}
	var copied struct {
		ShareText string `json:"share_text"`
	}
	res = s.Do(t, web, http.MethodPost,
		"/api/v1/tickets/"+created.Number+"/guest-links/"+strconv.FormatInt(created.GuestLink.ID, 10)+"/share-text",
		nil, &copied)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("再次复制转发文案状态码 = %d，想 200", res.StatusCode)
	}
	if !strings.Contains(copied.ShareText, "这张工单已加到你的主页") || strings.Contains(copied.ShareText, "/h/") {
		t.Fatalf("已认领转发文案应只提示已加到主页：%q", copied.ShareText)
	}
}
