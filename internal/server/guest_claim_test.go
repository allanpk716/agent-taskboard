package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test未认领访客链接除认领接口外被拒并记录访问(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "未认领链接不能读取")

	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	guest := s.AgentClient(guestKeyOf(t, link))
	for name, req := range map[string]struct{ method, path string }{
		"会话":  {http.MethodGet, "/api/v1/session"},
		"读工单": {http.MethodGet, "/api/v1/tickets/" + number},
		"回复":  {http.MethodPost, "/api/v1/tickets/" + number + "/replies"},
	} {
		res := s.Do(t, guest, req.method, req.path, map[string]string{"body_md": "认领前回复"}, nil)
		if res.StatusCode != http.StatusUnauthorized {
			t.Errorf("[%s] 状态码 = %d, 想 401", name, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "link_unclaimed" {
			t.Errorf("[%s] 错误码 = %q, 想 link_unclaimed", name, got.Error.Code)
		}
		if !strings.Contains(got.Error.Hint, "认领") {
			t.Errorf("[%s] hint 应附认领指引：%q", name, got.Error.Hint)
		}
	}

	records := readAccessRecords(t, s, web, number)
	if len(records.Records) != 3 {
		t.Fatalf("被拒访问记录有 %d 条, 想 3：%+v", len(records.Records), records.Records)
	}
	for _, r := range records.Records {
		if r.Action != "denied" || r.Actor.Kind != "guest" || r.Actor.EntryMethod != "unclaimed" {
			t.Errorf("被拒访问记录不对：%+v", r)
		}
	}
}

func Test访客认领链接后凭设备凭证读取工单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "认领后才能看到的标题")
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	key := guestKeyOf(t, link)

	var info struct {
		State    string `json:"state"`
		Inviter  string `json:"inviter"`
		Note     string `json:"note"`
		Validity string `json:"valid_until"`
		Ticket   any    `json:"ticket"`
	}
	res := s.Do(t, s.AgentClient(key), http.MethodGet, "/api/v1/guest-link", nil, &info)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读取认领页状态码 = %d, 想 200", res.StatusCode)
	}
	if info.State != "unclaimed" || info.Inviter != "陈工" || info.Note != "检验科-张三" || info.Validity != link.ExpiresAt {
		t.Errorf("认领页信息不对：%+v", info)
	}
	if info.Ticket != nil {
		t.Errorf("认领页不应带工单内容：%v", info.Ticket)
	}

	var claimed struct {
		DeviceToken string `json:"device_token"`
	}
	res = s.Do(t, s.AgentClient(key), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "检验科 张三"}, &claimed)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领状态码 = %d, 想 201", res.StatusCode)
	}
	if !strings.HasPrefix(claimed.DeviceToken, "tbgd_") {
		t.Fatalf("设备凭证 = %q, 想以 tbgd_ 开头", claimed.DeviceToken)
	}

	var view ticketResponseBody
	res = s.Do(t, s.AgentClient(claimed.DeviceToken), http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("认领后读工单状态码 = %d, 想 200", res.StatusCode)
	}
	foundClaim := false
	for _, ev := range view.Timeline {
		if ev.Type == "guest_link_claimed" {
			foundClaim = true
			if ev.Actor.Member != nil || ev.Actor.Agent != nil {
				t.Errorf("认领事件作者不对：%+v", ev.Actor)
			}
			if ev.Actor.LinkNote == nil || *ev.Actor.LinkNote != "检验科-张三" ||
				ev.Actor.AuthorName == nil || *ev.Actor.AuthorName != "检验科 张三" {
				t.Errorf("认领事件未记录访客自报称呼：%+v", ev.Actor)
			}
		}
	}
	if !foundClaim {
		t.Fatalf("时间线缺少认领事件：%+v", view.Timeline)
	}

	res = s.Do(t, s.AgentClient(key), http.MethodGet, "/api/v1/tickets/"+number, nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("已认领后单凭链接密钥状态码 = %d, 想 401", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "link_claimed" {
		t.Errorf("已认领错误码 = %q, 想 link_claimed", got.Error.Code)
	}
}

func Test访客链接列表显示认领状态且附件下载用设备凭证(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "附件下载需要设备凭证")
	att := mustUploadAttachment(t, s, web, number, "现场.png", []byte("png"))
	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	key := guestKeyOf(t, link)
	var claimed struct {
		DeviceToken string `json:"device_token"`
	}
	res := s.Do(t, s.AgentClient(key), http.MethodPost, "/api/v1/guest-links/claim",
		map[string]string{"author_name": "检验科 张三"}, &claimed)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("认领状态码 = %d, 想 201", res.StatusCode)
	}

	var list struct {
		Links []struct {
			Status      string  `json:"status"`
			ClaimName   *string `json:"claim_name"`
			ClaimedAt   *string `json:"claimed_at"`
			DeviceCount int     `json:"device_count"`
		} `json:"links"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/guest-links", nil, &list)
	if len(list.Links) != 1 {
		t.Fatalf("访客链接列表 = %+v", list.Links)
	}
	got := list.Links[0]
	if got.Status != "claimed" || got.ClaimName == nil || *got.ClaimName != "检验科 张三" ||
		got.ClaimedAt == nil || got.DeviceCount != 1 {
		t.Errorf("认领状态不对：%+v", got)
	}

	guest := s.AgentClient(claimed.DeviceToken)
	var view attachmentsTicketBody
	s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number, nil, &view)
	if len(view.Attachments) != 1 {
		t.Fatalf("附件列表 = %+v", view.Attachments)
	}
	url := view.Attachments[0].URL
	if strings.Contains(url, "sig=") || strings.Contains(url, "link=") {
		t.Fatalf("访客附件地址仍是签名地址：%q", url)
	}
	if res := downloadAttachment(t, s, guest, url); res.StatusCode != http.StatusOK {
		t.Fatalf("设备凭证下载状态码 = %d, 想 200", res.StatusCode)
	}
	if res := downloadAttachment(t, s, nil, url); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("无凭证下载状态码 = %d, 想 401", res.StatusCode)
	}
	records := readAccessRecords(t, s, web, number)
	found := false
	for _, r := range records.Records {
		if r.Action == "download" && r.Attachment.ID == att.ID && r.Actor.Kind == "guest" {
			found = true
			if r.Actor.Name != "检验科-张三 · 检验科 张三" || r.Actor.EntryMethod != "device" {
				t.Errorf("访客下载署名不对：%+v", r.Actor)
			}
		}
	}
	if !found {
		t.Fatalf("缺少访客下载记录：%+v", records.Records)
	}
}
