// 列表的 Markdown 视图（#37，spec「Markdown 视图」模块）：一张表格 +
// 末尾的「下一步」，供 Agent 直接阅读。
package markdownview

import (
	"strconv"
	"strings"
	"time"
)

// ListRow 是表格一行的输入（纯数据，由 HTTP 层从列表行填充）。
type ListRow struct {
	Number        string
	Title         string
	Status        string    // open | closed
	CloseReason   string    // done | wontfix | duplicate（空 = 未关闭）
	Assignee      string    // 空 = 未指派
	WaitingOnText string    // 如「李工（指派人）」
	Labels        []string
	UpdatedAt     time.Time
	MatchedIn     []string  // q 命中的位置（无 q 时空）
	Snippet       string    // q 命中的片段
}

// ListInput 是列表 Markdown 视图的输入。
type ListInput struct {
	Total           int64
	Count           int
	ResolvedAliases []string // 别名换算的回显（“7600 → 机型:BH-7600CS”）
	Rows            []ListRow
	Next            *string // 下一页完整地址
	Cursor          int64   // 全局变更序号（下次作 changed_after）
}

// statusText 把状态翻成中文（已关闭附原因）。
func (r ListRow) statusText() string {
	if r.Status == "open" {
		return "开启"
	}
	reason := r.CloseReason
	switch reason {
	case "done":
		reason = "完成"
	case "wontfix":
		reason = "不处理"
	case "duplicate":
		reason = "重复"
	case "":
		reason = "已关闭"
	}
	return "已关闭（" + reason + "）"
}

// List 渲染列表 Markdown：标题、别名回显、表格与「下一步」。
func List(in ListInput) string {
	var L []string
	L = append(L, "# 工单列表（共 "+strconv.FormatInt(in.Total, 10)+" 张，本页 "+
		strconv.Itoa(in.Count)+" 张）", "")
	if len(in.ResolvedAliases) > 0 {
		L = append(L, "> 别名已换成标准值："+strings.Join(in.ResolvedAliases, "；"), "")
	}
	L = append(L,
		"| 编号 | 标题 | 状态 | 指派人 | 轮到 | 标签 | 更新 |",
		"|---|---|---|---|---|---|---|")
	for _, r := range in.Rows {
		title := r.Title
		if len(r.MatchedIn) > 0 {
			title += "<br>…命中" + strings.Join(r.MatchedIn, "、") + "：" + r.Snippet
		}
		assignee := r.Assignee
		if assignee == "" {
			assignee = "未指派"
		}
		L = append(L, "| "+strings.Join([]string{
			r.Number, title, r.statusText(), assignee,
			r.WaitingOnText, strings.Join(r.Labels, "、"),
			r.UpdatedAt.Format("2006-01-02 15:04"),
		}, " | ")+" |")
	}
	if len(in.Rows) == 0 {
		L = append(L, "| （没有符合条件的工单） | | | | | | |")
	}
	L = append(L, "", "## 下一步", "",
		"- 读某一张的全文：`GET /api/v1/tickets/{编号}?format=markdown`")
	if in.Next != nil {
		L = append(L, "- 下一页：`GET "+*in.Next+"`")
	}
	L = append(L, "- 以后只看变化过的：在同样的条件上加 `changed_after="+
		strconv.FormatInt(in.Cursor, 10)+"`")
	return strings.Join(L, "\n")
}
