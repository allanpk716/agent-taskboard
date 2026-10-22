package httpapi

import "fmt"

// handoffText 集中生成成员侧的 Agent 文案。接入文案是唯一带明文 token 的
// 文案；工单指令和交接文案永远不含凭证。
type handoffText struct {
	baseURL string
}

func newHandoffText(baseURL string) handoffText {
	return handoffText{baseURL: baseURL}
}

func (h handoffText) instruction(number, title string) string {
	return fmt.Sprintf(`请处理 agent-taskboard 上的工单 %s「%s」。
1. 凭证：用环境变量 TASKBOARD_TOKEN，请求头 Authorization: Bearer $TASKBOARD_TOKEN；读不到或返回 401 就停下来，让我去「我的 → Agent」生成 token 并把接入文案贴给你，不要用密码登录。
2. 不熟悉平台先读说明书：%s/agent.md
3. 读工单：curl -H "Authorization: Bearer $TASKBOARD_TOKEN" "%s/api/v1/tickets/%s?format=markdown"
4. 照工单末尾「下一步」做；之后的待办看 %s/api/v1/inbox。
5. 你写的建单、回复、交付或说明修订，可能会先存成草稿（响应是 202，带 draft 和 preview_url），我确认后才发出；不要当作已经发出。
6. 写完把 preview_url 发给我，用一两句话说明写了什么。之后要改，就在这份草稿上改（PUT /api/v1/drafts/<id>，带 base_version），不要另起一份。`,
		number, title, h.baseURL, h.baseURL, number, h.baseURL)
}

func (h handoffText) share(reassigned bool, assigneeName, number, title string) string {
	lead := fmt.Sprintf("%s：给你派了一张工单 %s「%s」", assigneeName, number, title)
	if reassigned {
		lead = fmt.Sprintf("%s：工单 %s「%s」转给你了", assigneeName, number, title)
	}
	return fmt.Sprintf("%s，网页 %s/t/%s（也在你的收件箱里）。\n下面这段交给你的 Agent（第一次用，先在「我的 → Agent」生成 token，把接入文案贴给它）：\n\n%s",
		lead, h.baseURL, number, h.instruction(number, title))
}

func (h handoffText) onboarding(memberName, agentName, token string) string {
	return fmt.Sprintf(`你是我（%s）在 agent-taskboard 上的成员 Agent「%s」。请完成一次性接入：
1. 把凭证永久保存为用户环境变量（只需做一次）：
   setx TASKBOARD_URL "%s"
   setx TASKBOARD_TOKEN "%s"
   新变量要到新开的终端才生效，本次会话先直接用这两个值。
2. 读说明书：%s/agent.md
3. 验证：curl -H "Authorization: Bearer %s" "%s/api/v1/inbox"
   能返回就回复我「接入成功」。
以后我发来工单指令，就用 TASKBOARD_TOKEN 处理；读不到或返回 401 时停下来告诉我，不要用密码登录。
如果你不能执行命令，请把第 1 步的两条命令原样转告我，我自己在终端里执行。`,
		memberName, agentName, h.baseURL, token, h.baseURL, token, h.baseURL)
}
