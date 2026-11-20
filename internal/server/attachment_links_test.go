package server_test

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type attachmentLinkBody struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
	Mime      string `json:"mime"`
	Size      int64  `json:"size"`
	Filename  string `json:"filename"`
}

type attachmentLinkErrorBody struct {
	URL   string `json:"url"`
	Error struct {
		Code            string `json:"code"`
		Message         string `json:"message"`
		Hint            string `json:"hint"`
		SlimPackageName string `json:"slim_package_name"`
		SlimRemovedAt   string `json:"slim_removed_at"`
	} `json:"error"`
}

func Test成员Agent换取附件短链接并免token下载(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://taskboard.example.com"))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "Agent 附件链接")

	pending := mustUploadAttachment(t, s, web, number, "截图.png", []byte("photo"))
	agentID, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	// 待用附件不能换取链接；网页会话和访客也都不属于成员 Agent。
	if res := s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("待用附件取链接状态码 = %d, 想 404", res.StatusCode)
	}
	if res := s.Do(t, web, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("网页会话取链接状态码 = %d, 想 403", res.StatusCode)
	}
	link := mustCreateGuestLink(t, s, web, number, map[string]any{"note": "检验科-张三"})
	guestKey := strings.TrimPrefix(link.URL, "https://taskboard.example.com/g/")
	guest := s.GuestClient(guestKey)
	if res := s.Do(t, guest, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, nil); res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客取链接状态码 = %d, 想 403", res.StatusCode)
	}
	var denied errorBody
	res := s.Do(t, web, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, &denied)
	if denied.Error.Code != "agents_only" {
		t.Fatalf("网页会话错误码 = %q, 想 agents_only", denied.Error.Code)
	}

	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{pending.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}
	var got attachmentLinkBody
	res = s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Agent 取链接状态码 = %d, 想 200", res.StatusCode)
	}
	if got.URL == "" || !strings.HasPrefix(got.URL, "https://taskboard.example.com/api/v1/attachments/"+pending.ID+"/") ||
		!strings.Contains(got.URL, "agent="+strconv.FormatInt(agentID, 10)) || !strings.Contains(got.URL, "exp=") || !strings.Contains(got.URL, "sig=") {
		t.Fatalf("链接不对：%+v", got)
	}
	if got.Mime != "image/png" || got.Size != int64(len("photo")) || got.Filename != "截图.png" || got.ExpiresAt == "" {
		t.Fatalf("链接元数据不对：%+v", got)
	}

	// 地址本身就是凭证：不带 cookie / Bearer 也能下载，并按成员 Agent 署名记录。
	parsed, err := url.Parse(got.URL)
	if err != nil {
		t.Fatalf("解析链接失败：%v", err)
	}
	path := parsed.EscapedPath() + "?" + parsed.RawQuery
	if res := downloadAttachment(t, s, s.NewClient(), path); res.StatusCode != http.StatusOK {
		t.Fatalf("免 token 下载状态码 = %d, 想 200", res.StatusCode)
	}
	records := readAccessRecords(t, s, web, number)
	found := false
	for _, record := range records.Records {
		if record.Action == "download" && record.Attachment.ID == pending.ID &&
			record.Actor.Kind == "agent" && strings.Contains(record.Actor.Name, "笔记本") {
			found = true
		}
	}
	if !found {
		t.Fatalf("签名下载没有按成员 Agent 记录：%+v", records.Records)
	}

	// 到期即失效；签名和附件引用绑定，不能挪到另一个附件上。
	s.Clock().Advance(10 * time.Minute)
	if res := downloadAttachment(t, s, s.NewClient(), path); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期签名状态码 = %d, 想 401", res.StatusCode)
	}
	var fresh attachmentLinkBody
	s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, &fresh)
	freshURL, _ := url.Parse(fresh.URL)
	forged := strings.Replace(fresh.URL, "sig=", "sig=deadbeef", 1)
	if res := downloadAttachment(t, s, s.NewClient(), forged[strings.Index(forged, "/api/v1/"):]); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("篡改签名状态码 = %d, 想 401", res.StatusCode)
	}
	other := mustUploadAttachment(t, s, web, number, "第二张.png", []byte("second"))
	if res := claimByReply(t, s, web, number, map[string]any{"attachment_ids": []string{other.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂第二个附件状态码 = %d, 想 201", res.StatusCode)
	}
	reused := "/api/v1/attachments/" + other.ID + "/" + url.PathEscape("第二张.png") + "?" + freshURL.RawQuery
	if res := downloadAttachment(t, s, s.NewClient(), reused); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("签名挪用状态码 = %d, 想 401", res.StatusCode)
	}

	// Agent 撤销后，仍末过期的地址也立即失效。
	if res := s.Do(t, web, http.MethodDelete, "/api/v1/me/agent-tokens/"+strconv.FormatInt(agentID, 10), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("撤销 Agent 状态码 = %d, 想 204", res.StatusCode)
	}
	var afterRevoke attachmentLinkBody
	if res := s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, &afterRevoke); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后取链接状态码 = %d, 想 401", res.StatusCode)
	}
	if res := downloadAttachment(t, s, s.NewClient(), freshURL.EscapedPath()+"?"+freshURL.RawQuery); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后下载状态码 = %d, 想 401", res.StatusCode)
	}
}

func Test无权工单附件不能换取链接(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	outsider := s.Login("王工", "secret-pass")
	var created createTicketResponse
	res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "不可见工单附件", "labels": []string{"机型:UD-2300"}, "assignee": "陈工",
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	att := mustUploadAttachment(t, s, admin, created.Number, "内部.png", []byte("secret"))
	if res := claimByReply(t, s, admin, created.Number, map[string]any{"attachment_ids": []string{att.ID}}); res.StatusCode != http.StatusCreated {
		t.Fatalf("挂附件状态码 = %d, 想 201", res.StatusCode)
	}
	_, token := s.IssueAgentToken(t, outsider, "外线")
	if res := s.Do(t, s.AgentClient(token), http.MethodGet, "/api/v1/attachment-links/"+att.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("无权附件取链接状态码 = %d, 想 404", res.StatusCode)
	}
}

func Test已瘦身附件不能换取短链接并保留旧地址瘦身拦截(t *testing.T) {
	f := newSlimRaceFixture(t, "已瘦身附件换链接")
	_, agentToken := f.s.IssueAgentToken(t, f.member, "现场助理")
	agent := f.s.AgentClient(agentToken)

	f.s.CreateMember("外线", "outside-pass")
	outsider := f.s.Login("外线", "outside-pass")
	_, outsiderToken := f.s.IssueAgentToken(t, outsider, "外线助理")
	outsiderAgent := f.s.AgentClient(outsiderToken)
	pendingNumber := mustCreateTicket(t, f.s, f.member, "待用附件不因瘦身改变")
	pending := mustUploadAttachment(t, f.s, f.member, pendingNumber, "待用.log", []byte("pending"))

	var before attachmentLinkBody
	if res := f.s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+f.att.ID, nil, &before); res.StatusCode != http.StatusOK {
		t.Fatalf("瘦身前取链接状态码 = %d, 想 200", res.StatusCode)
	}
	if res := f.s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("待用附件取链接状态码 = %d, 想 404", res.StatusCode)
	}
	if res := f.s.Do(t, outsiderAgent, http.MethodGet, "/api/v1/attachment-links/"+f.att.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("无权附件取链接状态码 = %d, 想 404", res.StatusCode)
	}
	oldURL, err := url.Parse(before.URL)
	if err != nil {
		t.Fatalf("解析瘦身前链接失败：%v", err)
	}
	oldPath := oldURL.EscapedPath() + "?" + oldURL.RawQuery

	pkg := f.generateSlim(t)
	pkgRes := f.s.Do(t, f.admin, http.MethodGet, pkg.DownloadURL, nil, nil)
	if pkgRes.StatusCode != http.StatusOK {
		t.Fatalf("下载瘦身包状态码 = %d, 想 200", pkgRes.StatusCode)
	}
	pkgBytes := readAll(t, pkgRes)
	var removed slimRemoveBody
	if res := f.removeSlim(t, pkg); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d, 想 200", res.StatusCode)
	} else {
		tbtest.DecodeJSON(t, res, &removed)
	}

	var denied attachmentLinkErrorBody
	res := f.s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+f.att.ID, nil, &denied)
	if res.StatusCode != http.StatusGone {
		t.Fatalf("瘦身后取链接状态码 = %d, 想 410", res.StatusCode)
	}
	if denied.URL != "" {
		t.Fatalf("瘦身后不应返回下载地址：%+v", denied)
	}
	if denied.Error.Code != "attachment_slimmed" {
		t.Fatalf("瘦身后取链接错误码 = %q, 想 attachment_slimmed", denied.Error.Code)
	}
	if denied.Error.SlimPackageName != pkg.Name || denied.Error.SlimRemovedAt != removed.RemovedAt ||
		!strings.Contains(denied.Error.Message, pkg.Name) {
		t.Fatalf("瘦身后取链接错误体 = %+v，想包含包 %s 和移出时间 %s", denied, pkg.Name, removed.RemovedAt)
	}
	if res := f.s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+pending.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("瘦身后待用附件取链接状态码 = %d, 想 404", res.StatusCode)
	}
	if res := f.s.Do(t, outsiderAgent, http.MethodGet, "/api/v1/attachment-links/"+f.att.ID, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Fatalf("瘦身后无权附件取链接状态码 = %d, 想 404", res.StatusCode)
	}

	oldRes := downloadAttachment(t, f.s, f.s.NewClient(), oldPath)
	if oldRes.StatusCode != http.StatusGone {
		t.Fatalf("瘦身前签出的地址下载状态码 = %d, 想 410", oldRes.StatusCode)
	}
	var oldDenied attachmentLinkErrorBody
	tbtest.DecodeJSON(t, oldRes, &oldDenied)
	if oldDenied.Error.Code != "attachment_slimmed" || oldDenied.Error.SlimPackageName != pkg.Name ||
		oldDenied.Error.SlimRemovedAt != removed.RemovedAt {
		t.Fatalf("旧地址瘦身错误体 = %+v，想包 %s 和移出时间 %s", oldDenied, pkg.Name, removed.RemovedAt)
	}

	if res, got := restoreSlim(t, f.s, f.admin, pkgBytes, f.number); res.StatusCode != http.StatusOK ||
		len(got.Restored) != 1 || got.Restored[0].Number != f.number {
		t.Fatalf("恢复瘦身包 = %d/%+v，想恢复 %s", res.StatusCode, got, f.number)
	}
	var restored attachmentLinkBody
	if res := f.s.Do(t, agent, http.MethodGet, "/api/v1/attachment-links/"+f.att.ID, nil, &restored); res.StatusCode != http.StatusOK {
		t.Fatalf("恢复后取链接状态码 = %d, 想 200", res.StatusCode)
	}
	restoredURL, err := url.Parse(restored.URL)
	if err != nil {
		t.Fatalf("解析恢复后链接失败：%v", err)
	}
	download := downloadAttachment(t, f.s, f.s.NewClient(), restoredURL.EscapedPath()+"?"+restoredURL.RawQuery)
	downloadBody := string(readAll(t, download))
	if download.StatusCode != http.StatusOK || downloadBody != "race-body" {
		t.Fatalf("恢复后下载 = %d/%q, 想 200/race-body", download.StatusCode, downloadBody)
	}
}
