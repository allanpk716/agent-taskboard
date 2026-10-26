// Package markdownview 生成给 Agent 读的 Markdown 视图（spec「Markdown 视图」模块）。
// 它只依赖投影结果（Input 是纯数据），不碰数据库和 HTTP。
package markdownview

import (
	"strconv"
	"strings"
	"time"
)

// Event 是时间线上一条事件的输入（#29 只有 created；#30 起有回复、说明修订、
// 关闭与重开；#31 标签；#32 访客链接；#36 assigned）。
type Event struct {
	Seq        int64
	At         time.Time
	AuthorName string // 已按 ADR 0003 拼好的署名（访客写法见 tickets.Author.DisplayName）
	Type       string
	Label      string // label_added / label_removed 的标签（#31）
	Note       string // guest_link_created / guest_link_revoked 的备注（#32）

	// 附加字段，按事件类型出现。
	BodyMD        string   // reply 的正文
	Deliver       bool     // reply 带「交付」标记（#36 启用）
	Revision      int64    // brief_revised 修订后的版本号
	Reason        string   // closed 的原因：done | wontfix | duplicate
	DuplicateOf   string   // closed=重复时另一张工单的编号
	LinkExpiresAt string   // guest_link_extended 的新到期时刻（RFC3339，#32）
	AttachmentIDs []string // reply 引用的附件（#33）
	AssignFrom    string   // assigned 事件的改派前指派人；空 = 创建时指派（#36）
	AssignTo      string   // assigned 事件的指派对象（#36）
	Collaborator  string   // 协作者事件的对象（#158）
}

// Attachment 是附件小节里的一项（#33）：URL 由 HTTP 层按当前凭证生成
// （访客拿到的是与链接绑定的短时效签名地址）。
type Attachment struct {
	Filename   string
	URL        string
	SizeLabel  string // 如「1.2 MB」
	AttachedTo string // 「说明」或「回复 N」
}

// Input 是单张工单 Markdown 视图的输入（纯数据，由 HTTP 层从投影填充）。
type Input struct {
	Number          string
	Title           string
	Kind            string    // task | report
	Status          string    // open | closed
	CloseReasonText string    // 已关闭时的原因文案（如「完成」）
	CreatorName     string    // 已按 ADR 0003 拼好的署名
	AssigneeName    string    // 指派人姓名；空 = 未指派（#36）
	WaitingOnText   string    // 如「李工（指派人）」
	WaitingOnReason string    // 如「已交付，待你验收」
	Labels          *[]string // nil = 访客视角（#32）：不输出标签行
	BriefMarkdown   string
	BriefRevision   int64
	Timeline        []Event
	Attachments     []Attachment // 附件小节（#33）；空时写（无）
	Usage           string       // 附件用量一行，如「12.3 / 50 MB」；空时省略
	ViewerName      string       // 已按 ADR 0003 拼好的署名
	ViewerRole      string       // assignee | creator | member | guest
	ViewerIsGuest   bool         // 访客凭证（#35：入口提交者的角色是 creator，指引仍按访客给）
	YourTurn        bool
	APIPath         string // /api/v1/tickets/<编号>，「下一步」引用

	// 模板工单（#34）：模板名与「给 Agent 的说明」（出现在「下一步」里，spec 用户故事 35）。
	TemplateName string
	AgentNote    string

	// #36：按 角色×轮次 生成「下一步」所需。
	WaitingOnWhy     string // 原因码：new|guest_replied|delivered|assignee_replied|sent_back|creator_replied
	SinceSeq         int64  // 推导所依据的回复事件 seq
	CanReopen        bool   // 已关闭时是否可重新开启（成员恒可）
	LastSeq          int64  // 时间线最后一条（可见）事件的 seq，增量读取用
	ReopenedByViewer bool   // 工单开启且最后一条事件是重开、重开者就是当前凭证一方（spec 已接受边界）
	MyDraft          *MyDraft
}

type MyDraft struct {
	ID      string
	Kind    string
	Version int64
	BodyMD  string
	URL     string
}

// Ticket 渲染单张工单的 Markdown：头部信息、说明、附件（占位）、时间线和「下一步」。
func Ticket(in Input) string {
	var L []string
	L = append(L,
		"# 工单 "+in.Number+"："+in.Title, "",
		"- 类型："+kindText(in.Kind)+" · 状态："+statusText(in),
		"- 发起人："+in.CreatorName+" · 指派人："+assigneeText(in.AssigneeName),
		"- 现在轮到："+in.WaitingOnText+reasonSuffix(in.WaitingOnReason),
	)
	if in.Labels != nil {
		L = append(L, "- 标签："+strings.Join(*in.Labels, "、"))
	}
	L = append(L,
		"",
		"## 说明（第 "+strconv.FormatInt(in.BriefRevision, 10)+" 版）", "",
		in.BriefMarkdown, "",
		"## 附件", "")
	if in.Usage != "" {
		L = append(L, "- 用量："+in.Usage)
	}
	if len(in.Attachments) == 0 {
		L = append(L, "（无）")
	} else {
		for _, att := range in.Attachments {
			L = append(L, "- ["+att.Filename+"]("+att.URL+")（"+att.SizeLabel+"，附于"+att.AttachedTo+"）")
		}
	}
	L = append(L, "", "## 时间线", "")
	for _, e := range in.Timeline {
		if e.Type == "reply" {
			// 回复是正文主体，单独成节；引用的附件跟在正文后（#33）。
			head := e.AuthorName + " 回复"
			if e.Deliver {
				head = e.AuthorName + " **交付**"
			}
			L = append(L,
				"### ["+strconv.FormatInt(e.Seq, 10)+"] "+e.At.Format("2006-01-02 15:04")+" · "+head, "",
				e.BodyMD)
			if len(e.AttachmentIDs) > 0 {
				L = append(L, "", "附件："+strings.Join(e.AttachmentIDs, "、"))
			}
			L = append(L, "")
			continue
		}
		L = append(L, "- ["+strconv.FormatInt(e.Seq, 10)+"] "+e.At.Format("2006-01-02 15:04")+" · "+e.AuthorName+" "+eventText(e))
	}
	L = append(L, "")
	if in.MyDraft != nil {
		L = append(L,
			"## 你的草稿", "",
			"- "+in.MyDraft.Kind+" · 第 "+strconv.FormatInt(in.MyDraft.Version, 10)+" 版 · 未发出",
			"- 查看/修改："+in.MyDraft.URL, "",
			in.MyDraft.BodyMD, "")
	}
	L = append(L, nextStep(in)...)
	return strings.Join(L, "\n")
}

// nextStep 生成末尾的「下一步」：按 角色×轮次 给出可直接复制执行的 HTTP
// 请求（含请求体），照搬 prototype/member-handoff 的 guidance()。
// 验收点：只凭这一节就能写出交付和验收的请求。
func nextStep(in Input) []string {
	L := []string{
		"## 下一步（给 Agent 的操作指引）", "",
		"你是 **" + in.ViewerName + "**，在这张工单里的角色是 **" + roleText(in.ViewerRole) +
			"**；现在轮到：**" + in.WaitingOnText + "**。", "",
	}

	// 模板里写给 Agent 的说明出现在工单 Markdown 的「下一步」里（spec 用户故事 35）。
	if in.AgentNote != "" {
		name := "模板"
		if in.TemplateName != "" {
			name = in.TemplateName + " 模板"
		}
		L = append(L, name+"给 Agent 的说明："+in.AgentNote, "")
	}

	// req 生成一个可复制执行的 ```http 代码块。
	req := func(method, path string, body string) []string {
		out := []string{"```http", method + " " + path, "Authorization: Bearer <你的 token>"}
		if body != "" {
			out = append(out, "Content-Type: application/json", "", body)
		}
		return append(out, "```")
	}
	since := func(prefix string) string {
		return prefix + "在 [" + strconv.FormatInt(in.SinceSeq, 10) + "] "
	}

	switch {
	case in.Status == "closed":
		L = append(L, "工单已关闭（"+in.CloseReasonText+"），不需要再做什么。")
		if in.CanReopen {
			L = append(L, "", "确实还要继续时，先重新开启：", "")
			L = append(L, req("POST", in.APIPath+"/reopen", "")...)
		}
	case in.ViewerIsGuest:
		// 访客凭证（链接或经入口提交、角色记 creator 的提交者，#35）：指引按访客给。
		if in.YourTurn {
			L = append(L, "现在轮到你（访客）回复：", "",
				"```http",
				"POST "+in.APIPath+"/replies",
				"Authorization: Bearer <访客链接密钥>",
				"Content-Type: application/json",
				"",
				`{"body_md": "……", "author_name": "你的署名", "via_agent": false}`,
				"```", "")
		} else {
			L = append(L, "现在不需要你行动；等成员回复后再来看增量：", "",
				"```http",
				"GET "+in.APIPath+"/timeline?after=<上次响应里的 cursor>",
				"Authorization: Bearer <访客链接密钥>",
				"```", "")
		}
	case in.ReopenedByViewer:
		// 已接受边界（spec）：重开后「下一步」提示重开的一方先回复说明原因。
		L = append(L, "这张工单刚由你重新开启。先回复说明重开原因：", "")
		L = append(L, req("POST", in.APIPath+"/replies", `{"body_md": "重开原因：……"}`)...)
	case in.ViewerRole == "assignee" && in.YourTurn:
		switch in.WaitingOnWhy {
		case "sent_back":
			L = append(L, since("发起人")+"退回了上次的交付，先读那条回复，按要求修改。", "")
		case "creator_replied":
			L = append(L, since("发起人")+"回复了你，先读那条回复。", "")
		case "guest_replied":
			L = append(L, since("访客")+"回复了，先读那条回复。", "")
		}
		L = append(L,
			"1. 按上面的说明完成工作，以说明中的验收标准为准。",
			"2. **有疑问**：普通回复提问，之后轮到发起人答复：", "")
		L = append(L, req("POST", in.APIPath+"/replies", `{"body_md": "我的问题……"}`)...)
		L = append(L, "",
			"3. **要交文件**：先用 `POST "+in.APIPath+"/attachments` 上传拿到 `att:` 引用"+
				"（单个文件超过 5MB 时改在正文中写 NAS 路径），再写进交付回复的正文。",
			"4. **做完了**：带上 `deliver: true` 回复，写明做了什么、交付物在哪里，然后轮到发起人验收：", "")
		L = append(L, req("POST", in.APIPath+"/replies", `{"body_md": "已完成……", "deliver": true}`)...)
		L = append(L, "",
			"交付后**不要关闭工单**，由发起人验收后关闭。需要人来判断时，先问你所属的成员。")
	case in.ViewerRole == "creator" && in.YourTurn:
		if in.WaitingOnWhy == "delivered" {
			L = append(L, since("指派人")+"**交付**了。对照说明中的验收标准检查交付物；需要人来判断时，先问你所属的成员。", "",
				"- **验收通过**：以“完成”关闭：", "")
			L = append(L, req("POST", in.APIPath+"/close", `{"reason": "done"}`)...)
			L = append(L, "", "- **不通过**：普通回复，写明哪里不符合，工单会退回指派人：", "")
			L = append(L, req("POST", in.APIPath+"/replies", `{"body_md": "还差……"}`)...)
		} else {
			L = append(L, since("指派人")+"回复了，等你答复：", "")
			L = append(L, req("POST", in.APIPath+"/replies", `{"body_md": "答复……"}`)...)
		}
	default:
		L = append(L, "现在不需要你行动。之后要查是否轮到你：", "")
		L = append(L, req("GET", "/api/v1/inbox", "")...)
		L = append(L, "", "或者只读这张工单的新内容：", "")
		L = append(L, req("GET", in.APIPath+"/timeline?after="+strconv.FormatInt(in.LastSeq, 10), "")...)
	}
	return L
}

func kindText(kind string) string {
	if kind == "report" {
		return "反馈"
	}
	return "任务"
}

// assigneeText 把指派人姓名转成 Markdown 头部文案；空 = 未指派。
func assigneeText(name string) string {
	if name == "" {
		return "未指派"
	}
	return name
}

func statusText(in Input) string {
	if in.Status == "closed" {
		if in.CloseReasonText != "" {
			return "已关闭（" + in.CloseReasonText + "）"
		}
		return "已关闭"
	}
	return "开启"
}

func roleText(role string) string {
	switch role {
	case "assignee":
		return "指派人"
	case "creator":
		return "发起人"
	case "guest":
		return "访客"
	default:
		return "旁观的成员"
	}
}

func eventText(e Event) string {
	switch e.Type {
	case "created":
		return "创建工单"
	case "assigned":
		if e.AssignFrom == "" {
			return "指派给 " + e.AssignTo
		}
		return "改派：" + e.AssignFrom + " → " + e.AssignTo
	case "collaborator_added":
		return "把 " + e.Collaborator + " 加入协作"
	case "collaborator_removed":
		return "把 " + e.Collaborator + " 移出协作"
	case "collaborator_left":
		return e.Collaborator + " 退出协作"
	case "label_added":
		return "打上标签 " + e.Label
	case "label_removed":
		return "摘掉标签 " + e.Label
	case "guest_link_created":
		return "创建访客链接「" + e.Note + "」"
	case "guest_link_claimed":
		return "认领访客链接「" + e.Note + "」"
	case "guest_link_revoked":
		return "撤销访客链接「" + e.Note + "」"
	case "guest_link_extended":
		return "延期访客链接「" + e.Note + "」至 " + e.LinkExpiresAt
	case "guest_link_rotated":
		return "更换访客链接「" + e.Note + "」的地址"
	case "brief_revised":
		return "修订说明（第 " + strconv.FormatInt(e.Revision, 10) + " 版）"
	case "closed":
		if e.Reason == "duplicate" && e.DuplicateOf != "" {
			return "关闭工单（重复：与 " + e.DuplicateOf + " 重复）"
		}
		return "关闭工单（" + CloseReasonText(e.Reason) + "）"
	case "reopened":
		return "重新开启工单"
	}
	return e.Type
}

// CloseReasonText 把关闭原因码翻成中文（API 的 close_reason 文本与 Markdown
// 视图共用这一份，评审 P3-16）。
func CloseReasonText(reason string) string {
	switch reason {
	case "done":
		return "完成"
	case "wontfix":
		return "不处理"
	case "duplicate":
		return "重复"
	}
	return reason
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return "（" + reason + "）"
}
