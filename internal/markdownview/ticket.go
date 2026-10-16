// Package markdownview 生成给 Agent 读的 Markdown 视图（spec「Markdown 视图」模块）。
// 它只依赖投影结果（Input 是纯数据），不碰数据库和 HTTP。
package markdownview

import (
	"strconv"
	"strings"
	"time"
)

// Event 是时间线上一条事件的输入（#29 只有 created；#30 起扩展回复等类型）。
type Event struct {
	Seq        int64
	At         time.Time
	AuthorName string // 已按 ADR 0003 拼好的署名
	Type       string
}

// Input 是单张工单 Markdown 视图的输入（纯数据，由 HTTP 层从投影填充）。
type Input struct {
	Number          string
	Title           string
	Kind            string // task | report
	Status          string // open | closed
	CloseReasonText string // 已关闭时的原因文案（如「完成」）
	CreatorName     string // 已按 ADR 0003 拼好的署名
	WaitingOnText   string // 如「本部门（未指派）」
	WaitingOnReason string // 如「尚未指派」
	Labels          []string
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
		"- 发起人："+in.CreatorName+" · 指派人：未指派",
		"- 现在轮到："+in.WaitingOnText+reasonSuffix(in.WaitingOnReason),
		"- 标签："+strings.Join(in.Labels, "、"),
		"",
		"## 说明（第 "+strconv.FormatInt(in.BriefRevision, 10)+" 版）", "",
		in.BriefMarkdown, "",
		"## 附件", "",
		"（无）",
		"",
		"## 时间线", "")
	for _, e := range in.Timeline {
		L = append(L, "- ["+strconv.FormatInt(e.Seq, 10)+"] "+e.At.Format("2006-01-02 15:04")+" · "+e.AuthorName+" "+eventText(e))
	}
	L = append(L, "")
	L = append(L, nextStep(in)...)
	return strings.Join(L, "\n")
}

// nextStep 生成末尾的「下一步」。完整的按角色×轮次的指引（含交付、验收的请求示例）
// 随 #30/#36 的回复与指派加入；#29 只写身份、轮次和可用的读取方式。
func nextStep(in Input) []string {
	L := []string{
		"## 下一步（给 Agent 的操作指引）", "",
		"你是 **" + in.ViewerName + "**，在这张工单里的角色是 **" + roleText(in.ViewerRole) +
			"**；现在轮到：**" + in.WaitingOnText + "**。", "",
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
	if e.Type == "created" {
		return "创建工单"
	}
	return e.Type
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return "（" + reason + "）"
}
