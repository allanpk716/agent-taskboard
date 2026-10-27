package server_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test读工单返回交接文案且与建单shareText一致(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("王工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	s.CreateMember("赵工", "secret-pass")
	webWang := s.Login("王工", "secret-pass")
	webLi := s.Login("李工", "secret-pass")
	webZhao := s.Login("赵工", "secret-pass")
	_, wangToken := s.IssueAgentToken(t, webWang, "笔记本")
	_, liToken := s.IssueAgentToken(t, webLi, "工位")

	var created createAssignedResponse
	res := s.Do(t, s.AgentClient(wangToken), http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "整理对照表", "brief_md": "背景：……", "labels": []string{"机型:UD-2300"},
		"assignee": "李工",
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}

	var read ticketResponseBody
	s.Do(t, s.AgentClient(liToken), http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &read)
	if read.HandoffText == "" || read.HandoffShareText == nil {
		t.Fatalf("成员 Agent 读工单应带 handoff_text/handoff_share_text：%+v/%v",
			read.HandoffText, read.HandoffShareText)
	}
	wantAgent := fmt.Sprintf(`请处理 agent-taskboard 上的工单 %s「整理对照表」。
1. 凭证：用环境变量 TASKBOARD_TOKEN，请求头 Authorization: Bearer $TASKBOARD_TOKEN；读不到或返回 401 就停下来，让我去「我的 → Agent」生成 token 并把接入文案贴给你，不要用密码登录。
2. 不熟悉平台先读说明书：%s/agent.md
3. 读工单：curl -H "Authorization: Bearer $TASKBOARD_TOKEN" "%s/api/v1/tickets/%s?format=markdown"
4. 照工单末尾「下一步」做；之后的待办看 %s/api/v1/inbox。
5. 你写的建单、回复、交付或说明修订，可能会先存成草稿（响应是 202，带 draft 和 preview_url），我确认后才发出；不要当作已经发出。
6. 写完把 preview_url 发给我，用一两句话说明写了什么。之后要改，就在这份草稿上改（PUT /api/v1/drafts/<id>，带 base_version），不要另起一份。`,
		created.Number, s.URL(), s.URL(), created.Number, s.URL())
	if read.HandoffText != wantAgent {
		t.Fatalf("handoff_text = %q\n想 %q", read.HandoffText, wantAgent)
	}
	if !strings.HasSuffix(*created.ShareText, read.HandoffText) {
		t.Fatalf("建单 share_text 的 Agent 段不一致：\n%q", *created.ShareText)
	}

	var reassigned struct {
		ShareText *string `json:"share_text"`
	}
	s.Do(t, s.AgentClient(wangToken), http.MethodPost, "/api/v1/tickets/"+created.Number+"/assign",
		map[string]string{"assignee": "赵工"}, &reassigned)
	wantReassign := fmt.Sprintf("赵工：工单 %s「整理对照表」转给你了，网页 %s/t/%s（也在你的收件箱里）。\n下面这段交给你的 Agent（第一次用，先在「我的 → Agent」生成 token，把接入文案贴给它）：\n\n%s",
		created.Number, s.URL(), created.Number, wantAgent)
	if reassigned.ShareText == nil || *reassigned.ShareText != wantReassign {
		t.Fatalf("改派 share_text = %v\n想 %q", reassigned.ShareText, wantReassign)
	}
	var zhaoRead ticketResponseBody
	s.Do(t, webZhao, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &zhaoRead)
	if zhaoRead.HandoffText != wantAgent {
		t.Fatalf("改派后读工单的 Agent 段不一致：\n%q", zhaoRead.HandoffText)
	}

	for name, text := range map[string]string{
		"发起人 token": wangToken,
		"指派人 token": liToken,
	} {
		if strings.Contains(read.HandoffText, text) || strings.Contains(*read.HandoffShareText, text) {
			t.Errorf("%s 泄露进交接文案：%q", name, text)
		}
	}
}

func Test详情交接文案不点名且未指派也有(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("王工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	web := s.Login("王工", "secret-pass")

	var assigned createAssignedResponse
	res := s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "整理对照表", "brief_md": "背景：……", "labels": []string{"机型:UD-2300"},
		"assignee": "李工",
	}, &assigned)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}

	checkNeutral := func(number, title string) string {
		var read ticketResponseBody
		s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number, nil, &read)
		if read.HandoffShareText == nil {
			t.Fatalf("%s 的 handoff_share_text 不应为空", number)
		}
		want := fmt.Sprintf("请让你的 Agent 处理工单 %s「%s」，网页 %s/t/%s。\n下面这段交给你的 Agent（第一次用，先在「我的 → Agent」生成 token，把接入文案贴给它）：\n\n%s",
			number, title, s.URL(), number, read.HandoffText)
		if *read.HandoffShareText != want {
			t.Fatalf("%s handoff_share_text = %q\n想 %q", number, *read.HandoffShareText, want)
		}
		if strings.Contains(*read.HandoffShareText, "李工：") || strings.Contains(*read.HandoffShareText, "给你派了") {
			t.Fatalf("详情页交接文案不应点名指派人：\n%q", *read.HandoffShareText)
		}
		return *read.HandoffShareText
	}
	memberShare := checkNeutral(assigned.Number, "整理对照表")

	var unassigned createAssignedResponse
	res = s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "未指派对照表", "brief_md": "背景：……", "labels": []string{"机型:UD-2300"},
	}, &unassigned)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("未指派建单状态码 = %d, 想 201", res.StatusCode)
	}
	checkNeutral(unassigned.Number, "未指派对照表")

	link := s.CreateGuestLink(t, web, assigned.Number, map[string]any{"note": "检验科-张三"})
	var guest ticketResponseBody
	s.Do(t, s.GuestClient(link.Key(t)), http.MethodGet, "/api/v1/tickets/"+assigned.Number, nil, &guest)
	if guest.HandoffText != "" {
		t.Fatalf("访客不应拿到成员工单指令 handoff_text = %q", guest.HandoffText)
	}
	if guest.HandoffShareText == nil || *guest.HandoffShareText != memberShare {
		t.Fatalf("访客 handoff_share_text = %v, 想与成员一致的中性交接文案", guest.HandoffShareText)
	}
}

func Test访客Agent指令带链接占位与鉴权方法(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("王工", "secret-pass")
	web := s.Login("王工", "secret-pass")

	var created createAssignedResponse
	res := s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "访客Agent的工单", "brief_md": "背景：……", "labels": []string{"机型:UD-2300"},
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	link := s.CreateGuestLink(t, web, created.Number, map[string]any{"note": "检验科-张三"})
	key := link.Key(t)

	var guest ticketResponseBody
	s.Do(t, s.GuestClient(key), http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &guest)
	if guest.GuestAgentInstruction == nil {
		t.Fatal("访客视图应带 guest_agent_instruction")
	}
	want := fmt.Sprintf(`请处理 agent-taskboard 上的工单 %s「访客Agent的工单」。
1. 凭证：下面这条访客链接只给你自己的 Agent，不要转发给其他人：
   {GUEST_LINK_URL}
   访问码：<请填入你的 6 位访问码>
   请求头写 Authorization: Bearer tbg_<链接密钥>:<访问码>；「链接密钥」是上面链接 /g/ 后面那一串，访问码由我私下补给你。读不到访问码或返回 401/403 就停下来找我，不要使用别人的凭证。
2. 不熟悉平台先读说明书：%s/agent.md
3. 读工单：curl -H "Authorization: Bearer tbg_<链接密钥>:<访问码>" "%s/api/v1/tickets/%s?format=markdown"
4. 照工单末尾「下一步」做。
5. 你写的回复和说明修订会直接发出（访客 Agent 不走成员草稿流程）；署名如实写明由 Agent 写成，不要冒充我本人。
6. 写完用一两句话告诉我做了什么。`,
		created.Number, s.URL(), s.URL(), created.Number)
	if *guest.GuestAgentInstruction != want {
		t.Fatalf("guest_agent_instruction = %q\n想 %q", *guest.GuestAgentInstruction, want)
	}
	if strings.Contains(*guest.GuestAgentInstruction, key) {
		t.Fatalf("后端拿不到明文链接密钥，指令里应只有占位符：\n%q", *guest.GuestAgentInstruction)
	}
}
