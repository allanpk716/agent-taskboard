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
1. 先读说明书：%s/agent.md
2. 凭证：用我在网页「我的 → Agent」签发的 token，请求头 Authorization: Bearer <token>；没有就先停下来让我去签发，不要用密码登录。
3. 读工单：curl -H "Authorization: Bearer $TOKEN" "%s/api/v1/tickets/%s?format=markdown"
4. 照工单末尾「下一步」做；之后的待办看 %s/api/v1/inbox.`,
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
	wantReassign := fmt.Sprintf("赵工：工单 %s「整理对照表」转给你了，网页 %s/t/%s（也在你的收件箱里）。下面这段交给你的 Agent：\n\n%s",
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
