package server_test

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

const testClientVerifier = "test-verifier-with-enough-characters-for-pkce-1234567890"

func testClientChallenge() string {
	sum := sha256.Sum256([]byte(testClientVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// 成员在网页确认后，客户端凭 PKCE code 换到只能读通知的客户端凭证；
// 使用痕迹、列表和撤销都从公开 HTTP 接口可观察。旧桌面客户端与 Orca 插件
// 都按各自 User-Agent 上报版本。
func Test客户端凭证完整签发使用与撤销流程(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "secret-pass")
	s.CreateMember("wang", "member-pass")
	admin := s.Login("admin", "secret-pass")
	web := s.Login("wang", "member-pass")

	connect := s.StartClientConnect(t, web, "WORKSTATION-01")
	if connect.State != "client-state" {
		t.Fatalf("回调 state = %q, 想 client-state", connect.State)
	}
	if u, err := url.Parse(connect.RedirectURL); err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "/cb" {
		t.Fatalf("回调地址不合法：%q (%v)", connect.RedirectURL, err)
	}

	issued := s.ExchangeClientCode(t, connect, testClientVerifier)
	if issued.Token == "" || issued.Hostname != "WORKSTATION-01" {
		t.Fatalf("换取响应不对：%+v", issued)
	}
	client := s.ClientCredentialClient(issued.Token)

	var assigned struct {
		Number string `json:"number"`
	}
	s.Do(t, admin, http.MethodPost, "/api/v1/tickets", map[string]any{
		"kind": "task", "title": "客户端标已读", "brief_md": "请处理。",
		"labels": []string{"机型:UD-2300"}, "assignee": "wang",
	}, &assigned)

	var session sessionBody
	res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, &session)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("客户端查 session 状态码 = %d", res.StatusCode)
	}
	if session.Viewer.Kind != "member" || session.Viewer.Member.Username != "wang" || session.Viewer.Agent != nil {
		t.Fatalf("客户端 session 身份不对：%+v", session.Viewer)
	}
	if session.Viewer.Client == nil || session.Viewer.Client.ID != issued.ID || session.Viewer.Client.Hostname != "WORKSTATION-01" {
		t.Fatalf("客户端 session 缺 client：%+v", session.Viewer.Client)
	}

	if res := s.Do(t, client, http.MethodGet, "/api/v1/notifications", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("客户端读通知状态码 = %d", res.StatusCode)
	}
	var read struct {
		UnreadCount int `json:"unread_count"`
	}
	s.Do(t, client, http.MethodPost, "/api/v1/notifications/read",
		map[string]string{"ticket": assigned.Number}, &read)
	if read.UnreadCount != 0 {
		t.Fatalf("客户端按工单标已读后的未读数 = %d, 想 0", read.UnreadCount)
	}

	s.Clock().Advance(2 * time.Hour)
	res = s.DoWithHeaders(t, client, http.MethodGet, "/api/v1/notifications/unread",
		map[string]string{"User-Agent": "taskboard-client/1.4.9"}, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("客户端读未读数状态码 = %d", res.StatusCode)
	}

	var list clientListBody
	s.Do(t, web, http.MethodGet, "/api/v1/me/clients", nil, &list)
	if len(list.Clients) != 1 {
		t.Fatalf("已连接电脑数量 = %d, 想 1：%+v", len(list.Clients), list.Clients)
	}
	got := list.Clients[0]
	if got.ID != issued.ID || got.Hostname != "WORKSTATION-01" || got.ClientVersion != "1.4.9" {
		t.Fatalf("电脑列表不对：%+v", got)
	}
	if got.LastUsedAt == "" || got.CreatedAt == "" {
		t.Fatalf("电脑列表缺时间：%+v", got)
	}

	res = s.DoWithHeaders(t, client, http.MethodGet, "/api/v1/notifications/unread",
		map[string]string{"User-Agent": "taskboard-orca-plugin/3.1.4"}, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Orca 插件读未读数状态码 = %d", res.StatusCode)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/me/clients", nil, &list)
	if len(list.Clients) != 1 || list.Clients[0].ClientVersion != "3.1.4" {
		t.Fatalf("Orca 插件使用后的电脑列表不对：%+v", list.Clients)
	}

	if res := s.Do(t, web, http.MethodDelete, "/api/v1/me/clients/"+itoa(issued.ID), nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("网页撤销客户端状态码 = %d", res.StatusCode)
	}
	res = s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("撤销后客户端状态码 = %d, 想 401", res.StatusCode)
	}
}

type clientListBody struct {
	Clients []struct {
		ID            int64  `json:"id"`
		Hostname      string `json:"hostname"`
		CreatedAt     string `json:"created_at"`
		LastUsedAt    string `json:"last_used_at"`
		ClientVersion string `json:"client_version"`
	} `json:"clients"`
}

// code 是一次性的；过期和 verifier 不匹配都换不到凭证；非法端口不能生成回调。
func Test客户端PKCEcode一次性过期与verifier校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")

	connect := s.StartClientConnect(t, web, "LAB-PC")
	s.ExchangeClientCode(t, connect, testClientVerifier)
	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/client/token",
		map[string]string{"code": connect.Code, "code_verifier": testClientVerifier}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("code 重复使用状态码 = %d, 想 401", res.StatusCode)
	}

	connect = s.StartClientConnect(t, web, "LAB-PC")
	s.Clock().Advance(11 * time.Minute)
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/client/token",
		map[string]string{"code": connect.Code, "code_verifier": testClientVerifier}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("过期 code 状态码 = %d, 想 401", res.StatusCode)
	}

	connect = s.StartClientConnect(t, web, "LAB-PC")
	res = s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/client/token",
		map[string]string{"code": connect.Code, "code_verifier": "wrong-verifier-with-enough-length-1234567890"}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("verifier 不匹配状态码 = %d, 想 401", res.StatusCode)
	}

	for _, port := range []string{"0", "65536", "-1", "example.com:1234"} {
		res := s.Do(t, web, http.MethodPost, "/api/v1/client/connect", map[string]any{
			"port": port, "state": "s", "code_challenge": testClientChallenge(), "hostname": "LAB-PC",
		}, nil)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("非法 port %q 状态码 = %d, 想 422", port, res.StatusCode)
		}
	}
}

// 客户端凭证越过通知与清单面时统一 403 client_scope；它自己可以撤销自己。
func Test客户端凭证权限边界与自撤销(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateMember("wang", "member-pass")
	web := s.Login("wang", "member-pass")
	issued := s.IssueClientCredential(t, web, "WORKSTATION-01")
	client := s.ClientCredentialClient(issued.Token)

	var errBody errorBody
	res := s.Do(t, client, http.MethodGet, "/api/v1/tickets", nil, &errBody)
	if res.StatusCode != http.StatusForbidden || errBody.Error.Code != "client_scope" {
		t.Fatalf("越权状态码/错误码 = %d/%q, 想 403/client_scope", res.StatusCode, errBody.Error.Code)
	}
	if errBody.Error.Hint != "请用浏览器打开工单处理；如需其它操作，登录网页后操作" {
		t.Fatalf("client_scope hint = %q", errBody.Error.Hint)
	}
	if errBody.Error.Message != "客户端凭证只能读取通知、身份、收件箱、在等他与协作中清单" {
		t.Fatalf("client_scope message = %q", errBody.Error.Message)
	}

	if res := s.Do(t, client, http.MethodDelete, "/api/v1/me/clients/self", nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("客户端自撤销状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("自撤销后状态码 = %d, 想 401", res.StatusCode)
	}
}

// 客户端凭证读到的收件箱与网页会话逐字段一致；可见范围外的工单不出现。
func Test客户端凭证读取收件箱与网页一致(t *testing.T) {
	w := newHandoffWorld(t)
	inboxTicket := w.createHandoffTicket(t, w.webWang, "王工派给李工", "李工").Number
	foreign := w.createHandoffTicket(t, w.webWang, "王工派给赵工", "赵工").Number
	issued := w.s.IssueClientCredential(t, w.webLi, "WORKSTATION-01")
	client := w.s.ClientCredentialClient(issued.Token)

	clientBody := w.sameBodyForWebAndClient(t, w.webLi, client, "收件箱", "/api/v1/inbox")

	var inbox inboxBody
	if err := json.Unmarshal(clientBody, &inbox); err != nil {
		t.Fatalf("解码收件箱失败：%v", err)
	}
	if inbox.Count != 1 || len(inbox.Items) != 1 || inbox.Items[0].Number != inboxTicket {
		t.Fatalf("客户端收件箱 = %+v，想只有 %s", inbox, inboxTicket)
	}
	if inbox.itemByNumber(foreign) != nil {
		t.Fatalf("可见范围外的工单 %s 泄进客户端收件箱：%+v", foreign, inbox.Items)
	}
}

// 客户端凭证读到的「在等他」与网页会话逐字段一致；可见范围外的工单不出现。
func Test客户端凭证读取在等他与网页一致(t *testing.T) {
	w := newHandoffWorld(t)
	waiting := w.createHandoffTicket(t, w.webLi, "李工等王工处理", "王工").Number
	foreign := w.createHandoffTicket(t, w.webZhao, "赵工等王工处理", "王工").Number
	issued := w.s.IssueClientCredential(t, w.webLi, "WORKSTATION-01")
	client := w.s.ClientCredentialClient(issued.Token)

	clientBody := w.sameBodyForWebAndClient(
		t, w.webLi, client, "在等他", "/api/v1/inbox/waiting-on-counterparts")

	var waitingBody inboxBody
	if err := json.Unmarshal(clientBody, &waitingBody); err != nil {
		t.Fatalf("解码在等他失败：%v", err)
	}
	if waitingBody.Count != 1 || len(waitingBody.Items) != 1 || waitingBody.Items[0].Number != waiting {
		t.Fatalf("客户端在等他 = %+v，想只有 %s", waitingBody, waiting)
	}
	if waitingBody.itemByNumber(foreign) != nil {
		t.Fatalf("可见范围外的工单 %s 泄进客户端在等他：%+v", foreign, waitingBody.Items)
	}
}

// 管理员的客户端凭证也沿用网页口径：三份清单都与非空数据下的网页会话一致。
func Test管理员客户端凭证读取三份清单与网页一致(t *testing.T) {
	w := newHandoffWorld(t)
	inboxTicket := w.createHandoffTicket(t, w.webLi, "李工派给王工", "王工").Number
	waitingTicket := w.createHandoffTicket(t, w.webWang, "王工等李工处理", "李工").Number
	collaboratingTicket := w.createTicketForCollaborator(t, w.webLi, "李工抄送王工", "王工")
	issued := w.s.IssueClientCredential(t, w.webWang, "WORKSTATION-01")
	client := w.s.ClientCredentialClient(issued.Token)

	inboxJSON := w.sameBodyForWebAndClient(t, w.webWang, client, "管理员收件箱", "/api/v1/inbox")
	waitingJSON := w.sameBodyForWebAndClient(
		t, w.webWang, client, "管理员在等他", "/api/v1/inbox/waiting-on-counterparts")
	collaboratingJSON := w.sameBodyForWebAndClient(
		t, w.webWang, client, "管理员协作中清单", "/api/v1/tickets?collaborator=me&status=open")

	var inbox inboxBody
	if err := json.Unmarshal(inboxJSON, &inbox); err != nil {
		t.Fatalf("解码管理员收件箱失败：%v", err)
	}
	if inbox.Count == 0 || inbox.itemByNumber(inboxTicket) == nil {
		t.Fatalf("管理员收件箱 = %+v，想包含 %s", inbox, inboxTicket)
	}

	var waiting inboxBody
	if err := json.Unmarshal(waitingJSON, &waiting); err != nil {
		t.Fatalf("解码管理员在等他失败：%v", err)
	}
	if waiting.Count != 1 || waiting.itemByNumber(waitingTicket) == nil {
		t.Fatalf("管理员在等他 = %+v，想包含 %s", waiting, waitingTicket)
	}

	var collaborating listBody
	if err := json.Unmarshal(collaboratingJSON, &collaborating); err != nil {
		t.Fatalf("解码管理员协作中清单失败：%v", err)
	}
	if collaborating.Total != 1 || !containsString(numbers(collaborating), collaboratingTicket) {
		t.Fatalf("管理员协作中清单 = %+v，想包含 %s", collaborating, collaboratingTicket)
	}
}

// 客户端凭证的清单跟随协作者移出结果；旁观者三份清单都看不到不可见工单。
func Test客户端凭证清单跟随协作者移出且旁观者不可见(t *testing.T) {
	w := newHandoffWorld(t)
	target := w.createTicketForCollaborator(t, w.webWang, "王工抄送李工", "李工")
	liClient := w.s.ClientCredentialClient(w.s.IssueClientCredential(t, w.webLi, "WORKSTATION-01").Token)
	zhaoClient := w.s.ClientCredentialClient(w.s.IssueClientCredential(t, w.webZhao, "WORKSTATION-02").Token)

	beforeJSON := w.sameBodyForWebAndClient(
		t, w.webLi, liClient, "协作中清单", "/api/v1/tickets?collaborator=me&status=open")
	var before listBody
	if err := json.Unmarshal(beforeJSON, &before); err != nil {
		t.Fatalf("解码移出前的协作中清单失败：%v", err)
	}
	if before.Total != 1 || !containsString(numbers(before), target) {
		t.Fatalf("移出前的协作中清单 = %+v，想只有 %s", before, target)
	}

	// 给旁观者三份清单各放一张可见工单，避免空清单让“不泄漏”断言变弱。
	zhaoInbox := w.createHandoffTicket(t, w.webWang, "王工派给赵工", "赵工").Number
	zhaoWaiting := w.createHandoffTicket(t, w.webZhao, "赵工等王工处理", "王工").Number
	zhaoCollaborating := w.createTicketForCollaborator(t, w.webWang, "王工抄送赵工", "赵工")

	inboxJSON := w.sameBodyForWebAndClient(t, w.webZhao, zhaoClient, "旁观者收件箱", "/api/v1/inbox")
	var inbox inboxBody
	if err := json.Unmarshal(inboxJSON, &inbox); err != nil {
		t.Fatalf("解码旁观者收件箱失败：%v", err)
	}
	if inbox.itemByNumber(zhaoInbox) == nil || inbox.itemByNumber(target) != nil {
		t.Fatalf("旁观者收件箱 = %+v，想含 %s、不含 %s", inbox, zhaoInbox, target)
	}

	waitingJSON := w.sameBodyForWebAndClient(
		t, w.webZhao, zhaoClient, "旁观者在等他", "/api/v1/inbox/waiting-on-counterparts")
	var waiting inboxBody
	if err := json.Unmarshal(waitingJSON, &waiting); err != nil {
		t.Fatalf("解码旁观者在等他失败：%v", err)
	}
	if waiting.itemByNumber(zhaoWaiting) == nil || waiting.itemByNumber(target) != nil {
		t.Fatalf("旁观者在等他 = %+v，想含 %s、不含 %s", waiting, zhaoWaiting, target)
	}

	collaboratingJSON := w.sameBodyForWebAndClient(
		t, w.webZhao, zhaoClient, "旁观者协作中清单", "/api/v1/tickets?collaborator=me&status=open")
	var collaborating listBody
	if err := json.Unmarshal(collaboratingJSON, &collaborating); err != nil {
		t.Fatalf("解码旁观者协作中清单失败：%v", err)
	}
	if !containsString(numbers(collaborating), zhaoCollaborating) ||
		containsString(numbers(collaborating), target) {
		t.Fatalf("旁观者协作中清单 = %v，想含 %s、不含 %s",
			numbers(collaborating), zhaoCollaborating, target)
	}

	ids := memberIDsByName(t, w.s, w.webWang)
	res := w.s.Do(t, w.webWang, http.MethodDelete,
		"/api/v1/tickets/"+target+"/collaborators/"+itoa(ids["李工"]), nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("移出协作者状态码 = %d，想 200", res.StatusCode)
	}

	afterJSON := w.sameBodyForWebAndClient(
		t, w.webLi, liClient, "移出后的协作中清单", "/api/v1/tickets?collaborator=me&status=open")
	var after listBody
	if err := json.Unmarshal(afterJSON, &after); err != nil {
		t.Fatalf("解码移出后的协作中清单失败：%v", err)
	}
	if after.Total != 0 || containsString(numbers(after), target) {
		t.Fatalf("移出后的协作中清单 = %+v，想为空", after)
	}
}

// 网页撤销设备后，三份清单和身份接口一样立即拒绝。
func Test网页撤销客户端凭证后三份清单均拒绝(t *testing.T) {
	w := newHandoffWorld(t)
	w.createTicketForCollaborator(t, w.webWang, "王工抄送李工", "李工")
	issued := w.s.IssueClientCredential(t, w.webLi, "WORKSTATION-01")
	client := w.s.ClientCredentialClient(issued.Token)

	res := w.s.Do(t, w.webLi, http.MethodDelete, "/api/v1/me/clients/"+itoa(issued.ID), nil, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("网页撤销客户端状态码 = %d，想 204", res.StatusCode)
	}

	for name, path := range map[string]string{
		"收件箱": "/api/v1/inbox",
		"在等他": "/api/v1/inbox/waiting-on-counterparts",
		"协作中": "/api/v1/tickets?collaborator=me&status=open",
	} {
		var errBody errorBody
		res := w.s.Do(t, client, http.MethodGet, path, nil, &errBody)
		if res.StatusCode != http.StatusUnauthorized || errBody.Error.Code != "invalid_token" {
			t.Fatalf("撤销后读%s状态码/错误码 = %d/%q，想 401/invalid_token",
				name, res.StatusCode, errBody.Error.Code)
		}
	}
}

// sameBodyForWebAndClient 用网页会话与客户端凭证读同一路径，要求响应逐字节一致，
// 并返回客户端响应体。
func (w *handoffWorld) sameBodyForWebAndClient(
	t testing.TB, web, client *http.Client, listName, path string,
) []byte {
	t.Helper()
	webRes := w.s.Do(t, web, http.MethodGet, path, nil, nil)
	clientRes := w.s.Do(t, client, http.MethodGet, path, nil, nil)
	if webRes.StatusCode != http.StatusOK || clientRes.StatusCode != http.StatusOK {
		t.Fatalf("网页/客户端读%s状态码 = %d/%d，想 200/200",
			listName, webRes.StatusCode, clientRes.StatusCode)
	}

	webBody, err := io.ReadAll(webRes.Body)
	if err != nil {
		t.Fatalf("读网页%s响应失败：%v", listName, err)
	}
	clientBody, err := io.ReadAll(clientRes.Body)
	if err != nil {
		t.Fatalf("读客户端%s响应失败：%v", listName, err)
	}
	if string(webBody) != string(clientBody) {
		t.Fatalf("网页与客户端%s不一致：\n网页：%s\n客户端：%s", listName, webBody, clientBody)
	}
	return clientBody
}

// createTicketForCollaborator 建一张把 collaborator 抄送进去的工单，返回编号。
func (w *handoffWorld) createTicketForCollaborator(
	t testing.TB, c *http.Client, title, collaborator string,
) string {
	t.Helper()
	var created createTicketResponse
	res := w.s.Do(t, c, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": title, "brief_md": "请知情。", "labels": []string{"机型:UD-2300"},
		"collaborators": []any{collaborator},
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单（抄送 %s）状态码 = %d，想 201", collaborator, res.StatusCode)
	}
	return created.Number
}

// 客户端凭证读到的「协作中」清单与网页会话逐字段一致；limit 与 next
// 照常可用；协作对象不是本人的工单不出现。
func Test客户端凭证读取协作中清单与网页一致(t *testing.T) {
	w := newHandoffWorld(t)
	first := w.createTicketForCollaborator(t, w.webWang, "王工抄送李工的第一张", "李工")
	w.s.Clock().Advance(10 * time.Minute)
	second := w.createTicketForCollaborator(t, w.webWang, "王工抄送李工的第二张", "李工")
	foreign := w.createTicketForCollaborator(t, w.webWang, "王工抄送赵工", "赵工")
	assigned := w.createHandoffTicket(t, w.webWang, "指派给李工不算协作中", "李工").Number
	issued := w.s.IssueClientCredential(t, w.webLi, "WORKSTATION-01")
	client := w.s.ClientCredentialClient(issued.Token)

	clientBody := w.sameBodyForWebAndClient(
		t, w.webLi, client, "协作中清单", "/api/v1/tickets?collaborator=me&status=open")

	var body listBody
	if err := json.Unmarshal(clientBody, &body); err != nil {
		t.Fatalf("解码协作中清单失败：%v", err)
	}
	if body.Total != 2 || !equalStrings(numbers(body), []string{second, first}) {
		t.Fatalf("客户端协作中清单 = %+v，想只有 %s、%s", body, first, second)
	}
	for _, leaked := range []string{foreign, assigned} {
		if containsString(numbers(body), leaked) {
			t.Fatalf("非协作中工单 %s 泄进客户端清单：%v", leaked, numbers(body))
		}
	}

	// 带排序与 limit 时照常放行，跟随 next 翻完整清单。
	page1 := w.collaboratingPage(t, client, "?collaborator=me&status=open&sort=created&limit=1")
	if page1.Count != 1 || !equalStrings(numbers(page1), []string{first}) {
		t.Fatalf("协作中第 1 页 = %v，想只有 %s", numbers(page1), first)
	}
	if page1.Next == nil {
		t.Fatal("协作中第 1 页缺少 next 地址")
	}
	page2 := w.collaboratingPage(t, client, strings.TrimPrefix(*page1.Next, "/api/v1/tickets"))
	if page2.Count != 1 || !equalStrings(numbers(page2), []string{second}) || page2.Next != nil {
		t.Fatalf("协作中第 2 页 = %v（next %v），想只有 %s 且 next 为空", numbers(page2), page2.Next, second)
	}
}

func (w *handoffWorld) collaboratingPage(t testing.TB, c *http.Client, query string) listBody {
	t.Helper()
	var body listBody
	res := w.s.Do(t, c, http.MethodGet, "/api/v1/tickets"+query, nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/tickets%s 状态码 = %d，想 200", query, res.StatusCode)
	}
	return body
}

// 工单列表只放行协作中这一个组合：参数重复、留空、叠加其他筛选或格式参数
// 都拒绝；counts、子路径与写操作照旧拒绝；网页会话与成员 Agent 不受影响。
func Test客户端凭证工单列表只放行协作中组合(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createTicketForCollaborator(t, w.webWang, "王工抄送李工", "李工")
	issued := w.s.IssueClientCredential(t, w.webLi, "WORKSTATION-01")
	client := w.s.ClientCredentialClient(issued.Token)

	for name, query := range map[string]string{
		"无参数":             "",
		"缺 collaborator":  "?status=open",
		"缺 status":        "?collaborator=me",
		"status=closed":   "?collaborator=me&status=closed",
		"status=all":      "?collaborator=me&status=all",
		"status 重复":       "?collaborator=me&status=open&status=closed",
		"status 同值重复":     "?collaborator=me&status=open&status=open",
		"collaborator 重复": "?collaborator=me&collaborator=me&status=open",
		"collaborator 空值": "?collaborator=&status=open",
		"status 空值":       "?collaborator=me&status=",
		"多 kind":          "?collaborator=me&status=open&kind=task",
		"多 template":      "?collaborator=me&status=open&template=assign-task",
		"多 label":         "?collaborator=me&status=open&label=机型:UD-2300",
		"多 q":             "?collaborator=me&status=open&q=关键词",
		"多 changed_after": "?collaborator=me&status=open&changed_after=0",
		"include=brief":   "?collaborator=me&status=open&include=brief",
		"format=markdown": "?collaborator=me&status=open&format=markdown",
		"limit 空值":        "?collaborator=me&status=open&limit=",
	} {
		var errBody errorBody
		res := w.s.Do(t, client, http.MethodGet, "/api/v1/tickets"+query, nil, &errBody)
		if res.StatusCode != http.StatusForbidden || errBody.Error.Code != "client_scope" {
			t.Fatalf("%s 状态码/错误码 = %d/%q，想 403/client_scope", name, res.StatusCode, errBody.Error.Code)
		}
	}

	for name, target := range map[string][2]string{
		"计数":  {http.MethodGet, "/api/v1/tickets/counts"},
		"详情":  {http.MethodGet, "/api/v1/tickets/" + number},
		"时间线": {http.MethodGet, "/api/v1/tickets/" + number + "/timeline"},
		"建单":  {http.MethodPost, "/api/v1/tickets"},
	} {
		var errBody errorBody
		res := w.s.Do(t, client, target[0], target[1], nil, &errBody)
		if res.StatusCode != http.StatusForbidden || errBody.Error.Code != "client_scope" {
			t.Fatalf("%s 状态码/错误码 = %d/%q，想 403/client_scope", name, res.StatusCode, errBody.Error.Code)
		}
	}

	// 同一组合对网页会话与成员 Agent 的行为不变。
	for name, c := range map[string]*http.Client{"网页会话": w.webLi, "成员 Agent": w.liA} {
		var body listBody
		res := w.s.Do(t, c, http.MethodGet, "/api/v1/tickets?collaborator=me&status=open", nil, &body)
		if res.StatusCode != http.StatusOK || body.Total != 1 || len(body.Items) != 1 || body.Items[0].Number != number {
			t.Fatalf("%s读协作中 = %d/%+v，想 200/%s", name, res.StatusCode, body, number)
		}
	}
}

// 清单是摘要读取：不推进工单未读和通知已读；read_new/read_full 仍被挡住。
func Test客户端凭证读清单不推进已读且全文仍拒绝(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.webWang, "王工派给李工", "李工").Number
	issued := w.s.IssueClientCredential(t, w.webLi, "WORKSTATION-01")
	client := w.s.ClientCredentialClient(issued.Token)

	before := notificationsOf(t, w, client, "")
	if before.UnreadCount != 1 || len(before.Items) != 1 || before.Items[0].ReadAt != nil {
		t.Fatalf("读清单前通知状态 = %+v，想一条未读", before)
	}
	inbox := w.inboxOf(t, client)
	item := inbox.itemByNumber(number)
	if item == nil || !item.Unread || item.ReadNew == "" || item.ReadFull == "" {
		t.Fatalf("客户端收件箱摘要不对：%+v", inbox.Items)
	}
	w.waitingCounterpartsOf(t, client)

	var unread struct {
		UnreadCount int `json:"unread_count"`
	}
	res := w.s.Do(t, client, http.MethodGet, "/api/v1/notifications/unread", nil, &unread)
	if res.StatusCode != http.StatusOK || unread.UnreadCount != 1 {
		t.Fatalf("客户端未读数 = %d/%d，想 200/1", res.StatusCode, unread.UnreadCount)
	}
	after := notificationsOf(t, w, client, "")
	if after.UnreadCount != 1 || len(after.Items) != 1 || after.Items[0].ReadAt != nil {
		t.Fatalf("读清单后通知被标已读：%+v", after)
	}
	if webInbox := w.inboxOf(t, w.webLi); webInbox.itemByNumber(number) == nil || !webInbox.itemByNumber(number).Unread {
		t.Fatalf("读清单后工单未读被推进：%+v", webInbox.Items)
	}

	for name, path := range map[string]string{"read_new": item.ReadNew, "read_full": item.ReadFull} {
		var errBody errorBody
		res := w.s.Do(t, client, http.MethodGet, path, nil, &errBody)
		if res.StatusCode != http.StatusForbidden || errBody.Error.Code != "client_scope" {
			t.Fatalf("%s 状态码/错误码 = %d/%q，想 403/client_scope", name, res.StatusCode, errBody.Error.Code)
		}
	}
}

// 停用账号、管理员重置密码撤销全部客户端凭证；自助改密不撤销。
func Test账号事件对客户端凭证的撤销规则(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("admin", "secret-pass")
	s.CreateMember("wang", "member-pass")
	admin := s.Login("admin", "secret-pass")
	web := s.Login("wang", "member-pass")
	issued := s.IssueClientCredential(t, web, "WORKSTATION-01")
	client := s.ClientCredentialClient(issued.Token)
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatal("前置：客户端凭证应可用")
	}

	var wrongOld errorBody
	res := s.Do(t, web, http.MethodPost, "/api/v1/me/password",
		map[string]string{"old_password": "wrong-pass", "new_password": "new-member-pass"}, &wrongOld)
	if res.StatusCode != http.StatusUnprocessableEntity || wrongOld.Error.Code != "invalid_old_password" {
		t.Fatalf("原密码错误状态码/错误码 = %d/%q, 想 422/invalid_old_password",
			res.StatusCode, wrongOld.Error.Code)
	}

	res = s.Do(t, web, http.MethodPost, "/api/v1/me/password",
		map[string]string{"old_password": "member-pass", "new_password": "new-member-pass"}, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("自助改密状态码 = %d", res.StatusCode)
	}
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("自助改密后客户端状态码 = %d, 想 200", res.StatusCode)
	}

	var session sessionBody
	s.Do(t, web, http.MethodGet, "/api/v1/session", nil, &session)
	res = s.Do(t, admin, http.MethodPost,
		"/api/v1/members/"+itoa(session.Viewer.Member.ID)+"/reset-password", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("管理员重置密码状态码 = %d", res.StatusCode)
	}
	var reset struct {
		InitialPassword string `json:"initial_password"`
	}
	tbtest.DecodeJSON(t, res, &reset)
	if len(reset.InitialPassword) < 8 {
		t.Fatalf("重置密码太短：%q", reset.InitialPassword)
	}
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("管理员重置后客户端状态码 = %d, 想 401", res.StatusCode)
	}

	web = s.Login("wang", reset.InitialPassword)
	issued = s.IssueClientCredential(t, web, "WORKSTATION-02")
	client = s.ClientCredentialClient(issued.Token)
	s.Do(t, admin, http.MethodPost,
		"/api/v1/members/"+itoa(session.Viewer.Member.ID)+"/deactivate", nil, nil)
	if res := s.Do(t, client, http.MethodGet, "/api/v1/session", nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("停用后客户端状态码 = %d, 想 401", res.StatusCode)
	}
}
