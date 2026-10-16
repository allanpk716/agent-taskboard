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
	BodyMD        string // reply 的正文
	Deliver       bool   // reply 带「交付」标记（#36 启用）
	Revision      int64  // brief_revised 修订后的版本号
	Reason        string // closed 的原因：done | wontfix | duplicate
	DuplicateOf   string // closed=重复时另一张工单的编号
	LinkExpiresAt string // guest_link_extended 的新到期时刻（RFC3339，#32）
	AssignFrom    string // assigned 事件的改派前指派人；空 = 创建时指派（#36）
	AssignTo      string // assigned 事件的指派对象（#36）
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
	ViewerName      string // 已按 ADR 0003 拼好的署名
	ViewerRole      string // assignee | creator | member | guest
	YourTurn        bool
	APIPath         string // /api/v1/tickets/<编号>，「下一步」引用
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
		"## 附件", "",
		"（无）",
		"",
		"## 时间线", "")
	for _, e := range in.Timeline {
		if e.Type == "reply" {
			// 回复是正文主体，单独成节（附件引用在 #33 起跟在正文后）。
			head := e.AuthorName + " 回复"
			if e.Deliver {
				head = e.AuthorName + " **交付**"
			}
			L = append(L,
				"### ["+strconv.FormatInt(e.Seq, 10)+"] "+e.At.Format("2006-01-02 15:04")+" · "+head, "",
				e.BodyMD, "")
			continue
		}
		L = append(L, "- ["+strconv.FormatInt(e.Seq, 10)+"] "+e.At.Format("2006-01-02 15:04")+" · "+e.AuthorName+" "+eventText(e))
	}
	L = append(L, "")
	L = append(L, nextStep(in)...)
	return strings.Join(L, "\n")
}

// nextStep 生成末尾的「下一步」。访客（#32）按轮次给回复或增量读取的示例；
// 完整的按角色×轮次的成员指引（含交付、验收的请求示例）随 #36 的回复与指派加入。
func nextStep(in Input) []string {
	L := []string{
		"## 下一步（给 Agent 的操作指引）", "",
		"你是 **" + in.ViewerName + "**，在这张工单里的角色是 **" + roleText(in.ViewerRole) +
			"**；现在轮到：**" + in.WaitingOnText + "**。", "",
	}
	if in.ViewerRole == "guest" {
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
		return L
	}
	if in.YourTurn {
		L = append(L, "现在轮到你行动；先重读这张工单，按说明行事：", "")
	} else {
		L = append(L, "现在不需要你行动。之后要查是否轮到你：", "")
	}
	L = append(L,
		"```http",
		"GET "+in.APIPath,
		"Authorization: Bearer <你的 token>",
		"```", "")
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
	case "label_added":
		return "打上标签 " + e.Label
	case "label_removed":
		return "摘掉标签 " + e.Label
	case "guest_link_created":
		return "创建访客链接「" + e.Note + "」"
	case "guest_link_revoked":
		return "撤销访客链接「" + e.Note + "」"
	case "guest_link_extended":
		return "延期访客链接「" + e.Note + "」至 " + e.LinkExpiresAt
	case "brief_revised":
		return "修订说明（第 " + strconv.FormatInt(e.Revision, 10) + " 版）"
	case "closed":
		if e.Reason == "duplicate" && e.DuplicateOf != "" {
			return "关闭工单（重复：与 " + e.DuplicateOf + " 重复）"
		}
		return "关闭工单（" + closeReasonText(e.Reason) + "）"
	case "reopened":
		return "重新开启工单"
	}
	return e.Type
}

// closeReasonText 把关闭原因码翻成中文。
func closeReasonText(reason string) string {
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
