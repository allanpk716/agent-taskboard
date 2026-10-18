package httpapi

import "fmt"

// handoffText 集中生成成员交接文案。Agent 段不含凭证，且与谁复制无关。
type handoffText struct {
	baseURL string
}

func newHandoffText(baseURL string) handoffText {
	return handoffText{baseURL: baseURL}
}

func (h handoffText) agent(number, title string) string {
	return fmt.Sprintf(`请处理 agent-taskboard 上的工单 %s「%s」。
1. 先读说明书：%s/agent.md
2. 凭证：用我在网页「我的 → Agent」签发的 token，请求头 Authorization: Bearer <token>；没有就先停下来让我去签发，不要用密码登录。
3. 读工单：curl -H "Authorization: Bearer $TOKEN" "%s/api/v1/tickets/%s?format=markdown"
4. 照工单末尾「下一步」做；之后的待办看 %s/api/v1/inbox.`,
		number, title, h.baseURL, h.baseURL, number, h.baseURL)
}

func (h handoffText) createShare(assigneeName, number, title string) string {
	return fmt.Sprintf("%s：给你派了一张工单 %s「%s」，网页 %s/t/%s（也在你的收件箱里）。下面这段交给你的 Agent：\n\n%s",
		assigneeName, number, title, h.baseURL, number, h.agent(number, title))
}

func (h handoffText) reassignShare(assigneeName, number, title string) string {
	return fmt.Sprintf("%s：工单 %s「%s」转给你了，网页 %s/t/%s（也在你的收件箱里）。下面这段交给你的 Agent：\n\n%s",
		assigneeName, number, title, h.baseURL, number, h.agent(number, title))
}
