package server_test

// #37 工单查询与计数：黑盒测试照搬 prototype/ticket-query 的 5 个场景（32 步，
// 见场景 1–5 各测试），加上筛选、分页、关键词、变更序号与计数的逐项断言。
// 世界（queryWorld）是原型 initialState 的移植：三成员 + 各一个 Agent，
// 七张工单覆盖 开启/关闭、任务/反馈、模板/自由文本、访客创建/成员创建。

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"net/url"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// keyFrom 取出提交响应里访客链接的密钥（/g/<密钥>，直接作 Bearer）。
func keyFrom(t testing.TB, submitted intakeSubmitBody) string {
	t.Helper()
	if !strings.HasPrefix(submitted.GuestLink.URL, "/g/") {
		t.Fatalf("提交响应里的访客链接 = %q, 想以 /g/ 开头", submitted.GuestLink.URL)
	}
	return strings.TrimPrefix(submitted.GuestLink.URL, "/g/")
}

// listItem 是列表项的摘要结构。
type listItem struct {
	Number      string        `json:"number"`
	Seq         int64         `json:"seq"`
	URL         string        `json:"url"`
	Kind        string        `json:"kind"`
	Title       string        `json:"title"`
	Status      string        `json:"status"`
	CloseReason *string       `json:"close_reason"`
	Template    *string       `json:"template"`
	Creator     string        `json:"creator"`
	Assignee    *string       `json:"assignee"`
	Labels      []string      `json:"labels"`
	WaitingOn   waitingOnBody `json:"waiting_on"`
	CreatedAt   time.Time     `json:"created_at"`
	UpdatedAt   time.Time     `json:"updated_at"`
	ClosedAt    *time.Time    `json:"closed_at"`
	ReplyCount  int           `json:"reply_count"`
	LastChange  int64         `json:"last_change"`
	ReadFull    string        `json:"read_full"`
	MatchedIn   []string      `json:"matched_in"`
	Snippet     string        `json:"snippet"`
	Brief       *struct {
		Fields   map[string]string `json:"fields"`
		Markdown string            `json:"markdown"`
		Revision int64             `json:"revision"`
		Template *string           `json:"template"`
	} `json:"brief"`
}

// listBody 是 GET /api/v1/tickets 的响应结构。
type listBody struct {
	Total           int64          `json:"total"`
	Count           int            `json:"count"`
	Items           []listItem     `json:"items"`
	Next            *string        `json:"next"`
	Cursor          int64          `json:"cursor"`
	Filters         map[string]any `json:"filters"`
	ResolvedAliases []string       `json:"resolved_aliases"`
}

// countsBody 是 GET /api/v1/tickets/counts 的响应结构。
type countsBody struct {
	GroupBy         string         `json:"group_by"`
	Total           int64          `json:"total"`
	Groups          []countGroup   `json:"groups"`
	Filters         map[string]any `json:"filters"`
	Cursor          int64          `json:"cursor"`
	ResolvedAliases []string       `json:"resolved_aliases"`
	Note            *string        `json:"note"`
}

type countGroup struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// queryWorld 是查询场景的固定班底（原型 initialState 的移植）：
//
//	OLD   20261008-001 反馈·入口提交，已关闭（done），指派李工，机型:UD-2300
//	DOC   20261012-001 任务·赵工自派自，开启
//	WONT  20261014-001 反馈·王工自派自，已关闭（wontfix）
//	FB    20261015-001 反馈·王工自派自 + 访客链接（检验科-张三），开启，轮到张三
//	Q3    20261015-002 任务·王工的 Agent 建并指派李工（模板 assign-task）
//	N1    20261016-001 反馈·入口提交（待初筛 + 部门:检验科），未指派
//	N2    20261016-002 反馈·入口提交（待初筛），未指派
type queryWorld struct {
	s       *tbtest.Server
	webWang *http.Client
	webLi   *http.Client
	webZhao *http.Client
	wangA   *http.Client // 王工 的 Agent · 笔记本
	liA     *http.Client // 李工 的 Agent · 工位
	zhaoA   *http.Client // 赵工 的 Agent · 工位

	old, doc, wont, fb, q3, n1, n2 string // 工单编号
	oldKey                         string // OLD 提交者的访客链接密钥
	fbKey                          string // FB 的访客链接（检验科-张三）密钥
	n1Key, n2Key                   string // 入口提交者的访客链接密钥
}

// at 把假时钟拨到北京时间的某个时刻。
func (w *queryWorld) at(t testing.TB, moment string) {
	t.Helper()
	ts, err := time.ParseInLocation("2006-01-02T15:04", moment, clock.Beijing)
	if err != nil {
		t.Fatalf("时间 %q 写错：%v", moment, err)
	}
	w.s.Clock().Set(ts)
}

// create 走成员凭证建单（带指派），返回编号。
func (w *queryWorld) create(t testing.TB, c *http.Client, body map[string]any) string {
	t.Helper()
	res := w.s.Do(t, c, http.MethodPost, "/api/v1/tickets", body, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单 %v 状态码 = %d, 想 201", body["title"], res.StatusCode)
	}
	var created createAssignedResponse
	tbtest.DecodeJSON(t, res, &created)
	return created.Number
}

// guestReply 访客自报署名回复。
func (w *queryWorld) guestReply(t testing.TB, key, number, bodyMD, authorName string) {
	t.Helper()
	res := w.s.Do(t, w.s.GuestClient(key), http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": bodyMD, "author_name": authorName}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("访客回复 %s 状态码 = %d, 想 201", number, res.StatusCode)
	}
}

// assign 改派（创建时指派也走这里，from 为空）。
func (w *queryWorld) assign(t testing.TB, c *http.Client, number, member string) {
	t.Helper()
	res := w.s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]string{"assignee": member}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("指派 %s → %s 状态码 = %d", number, member, res.StatusCode)
	}
}

// list 发一个列表查询并解码响应。
func (w *queryWorld) list(t testing.TB, c *http.Client, query string) listBody {
	t.Helper()
	var body listBody
	res := w.s.Do(t, c, http.MethodGet, "/api/v1/tickets"+query, nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d, 想 200", query, res.StatusCode)
	}
	return body
}

// listRaw 发一个列表查询，返回原始响应（不预读响应体）。
func (w *queryWorld) listRaw(t testing.TB, c *http.Client, query string) *http.Response {
	t.Helper()
	return w.s.Do(t, c, http.MethodGet, "/api/v1/tickets"+query, nil, nil)
}

// counts 发一个计数查询并解码响应。
func (w *queryWorld) counts(t testing.TB, c *http.Client, query string) countsBody {
	t.Helper()
	var body countsBody
	res := w.s.Do(t, c, http.MethodGet, "/api/v1/tickets/counts"+query, nil, &body)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d, 想 200", query, res.StatusCode)
	}
	return body
}

// numbers 把列表压成编号切片（断言用）。
func numbers(b listBody) []string {
	out := make([]string, 0, len(b.Items))
	for _, it := range b.Items {
		out = append(out, it.Number)
	}
	return out
}

// oldFields 是 OLD 的入口提交字段（说明里出现 E-203，供查重）。
func oldFields() map[string]string {
	return map[string]string{
		"哪台仪器？":    "UD-2300",
		"什么时候发生的？": "10-08 下午",
		"你做了哪些操作？": "正常开机，未做其他操作。",
		"实际看到了什么？": "开机自检到液路检测时报 E-203，之后无法进入待机。",
		"你期望看到什么？": "进入待机。",
		"出现得频繁吗？":  "偶尔",
	}
}

// n1Fields 是 N1 的入口提交字段（标题与说明里出现 E203；机型「不确定」）。
func n1Fields() map[string]string {
	return map[string]string{
		"哪台仪器？":    "不确定",
		"什么时候发生的？": "今天早上",
		"你做了哪些操作？": "早上开机后直接做质控。",
		"实际看到了什么？": "做质控的时候报警，屏幕显示 E203，质控结果没出来。",
		"你期望看到什么？": "正常开始质控测试。",
		"出现得频繁吗？":  "仅一次",
	}
}

// n2Fields 是 N2 的入口提交字段。
func n2Fields() map[string]string {
	return map[string]string{
		"哪台仪器？":    "不确定",
		"什么时候发生的？": "今天上午",
		"你做了哪些操作？": "审核后点打印。",
		"实际看到了什么？": "打印出来的报告中文全是乱码。",
		"你期望看到什么？": "正常打印。",
		"出现得频繁吗？":  "每次",
	}
}

// newQueryWorld 启动服务并按原型 initialState 造出七张工单。时间与原型一致
// （假时钟逐事件回拨/前拨），使排序、时间范围与变更序号语义都可断言。
func newQueryWorld(t testing.TB) *queryWorld {
	t.Helper()
	s := tbtest.Start(t)
	s.CreateAdmin("王工", "secret-pass")
	s.CreateMember("李工", "secret-pass")
	s.CreateMember("赵工", "secret-pass")
	webWang := s.Login("王工", "secret-pass")
	webLi := s.Login("李工", "secret-pass")
	webZhao := s.Login("赵工", "secret-pass")
	_, wangToken := s.IssueAgentToken(t, webWang, "笔记本")
	_, liToken := s.IssueAgentToken(t, webLi, "工位")
	_, zhaoToken := s.IssueAgentToken(t, webZhao, "工位")
	w := &queryWorld{
		s: s, webWang: webWang, webLi: webLi, webZhao: webZhao,
		wangA: s.AgentClient(wangToken), liA: s.AgentClient(liToken), zhaoA: s.AgentClient(zhaoToken),
	}

	// 常用别名（原型：7600 → 机型:BH-7600CS；这里换成字典里的 UD-2300）。
	if res := addAlias(t, s, webWang, "机型:UD-2300", "2300"); res.StatusCode != http.StatusCreated {
		t.Fatalf("加别名 2300 状态码 = %d", res.StatusCode)
	}
	if res := addAlias(t, s, webWang, "产品线:血液", "血液线"); res.StatusCode != http.StatusCreated {
		t.Fatalf("加别名 血液线 状态码 = %d", res.StatusCode)
	}

	// OLD：入口提交（10-08）→ 初筛（摘待初筛、补标签）→ 指派李工 → 回复两次 → 完成。
	w.at(t, "2026-10-08T14:10")
	createIntake(t, s, webWang, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	old := submitToIntake(t, s, "bug", map[string]any{
		"title": "UD-2300 开机自检报 E-203", "fields": oldFields(), "author_name": "检验科 王五",
	})
	w.old, w.oldKey = old.Ticket.Number, keyFrom(t, old)
	w.at(t, "2026-10-08T14:20")
	if code, _ := setLabels(t, s, webLi, w.old, setLabelsBody{
		Add: []string{"产品线:血液", "部门:检验科"}, Remove: []string{"待初筛"}, AllowNew: true,
	}); code != http.StatusOK {
		t.Fatalf("OLD 初筛打标签状态码 = %d", code)
	}
	w.at(t, "2026-10-08T14:30")
	w.assign(t, webWang, w.old, "李工")
	w.at(t, "2026-10-09T10:20")
	reply(t, s, webLi, w.old, "液位传感器固件问题，V3.2.2 已修复，明天上门升级。")
	w.at(t, "2026-10-12T16:30")
	w.guestReply(t, w.oldKey, w.old, "升级后没再出现，谢谢。", "检验科 王五")
	w.at(t, "2026-10-12T16:35")
	if res := closeTicket(t, s, webLi, w.old, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("OLD 关闭状态码 = %d", res.StatusCode)
	}

	// DOC：赵工自派自的任务（10-12），allow_new 顺手把 算法:质控规则 建进字典。
	w.at(t, "2026-10-12T11:00")
	w.doc = w.create(t, webZhao, map[string]any{
		"title": "整理质控规则说明文档", "brief_md": "把现有质控规则的说明整理成一份文档，供售后培训用。",
		"labels": []string{"产品线:血液"}, "assignee": "赵工",
	})
	w.at(t, "2026-10-12T11:05")
	if code, _ := setLabels(t, s, webZhao, w.doc, setLabelsBody{
		Add: []string{"算法:质控规则"}, AllowNew: true,
	}); code != http.StatusOK {
		t.Fatalf("DOC 打算法标签状态码 = %d", code)
	}

	// WONT：王工自派自的反馈（10-14），回复一次后 不处理 关闭（10-15）。
	w.at(t, "2026-10-14T09:30")
	w.wont = w.create(t, webWang, map[string]any{
		"kind": "report", "title": "UD-1300 尿液项目偶发结果为空",
		"brief_md": "质量部转来：UD-1300 尿液项目偶发结果为空，已确认是第三方试剂不兼容。",
		"labels":   []string{"产品线:尿液", "机型:UD-1300"}, "assignee": "王工",
	})

	// FB：王工自派自的反馈 + 访客链接（检验科-张三），两人各回复一次（10-15）。
	w.at(t, "2026-10-15T09:00")
	w.fb = w.create(t, webWang, map[string]any{
		"kind": "report", "title": "UD-2300 质控结果偏高",
		"brief_md": "检验科反馈：UD-2300 从 10-14 起，ALT 质控连续 3 次偏高 2SD 以上。",
		"labels":   []string{"产品线:血液", "机型:UD-2300"}, "assignee": "王工",
	})
	w.at(t, "2026-10-15T09:01")
	fbLink := s.CreateGuestLink(t, webWang, w.fb, map[string]any{"note": "检验科-张三"})
	w.fbKey = fbLink.Key(t)
	w.at(t, "2026-10-15T09:40")
	w.guestReply(t, w.fbKey, w.fb, "附上近一个月的质控数据。", "张三")
	w.at(t, "2026-10-15T09:55")
	reply(t, s, webWang, w.fb, "收到。请补充当前使用的试剂批号。")

	w.at(t, "2026-10-15T11:00")
	reply(t, s, webWang, w.wont, "第三方试剂不在支持范围内，不处理。")
	w.at(t, "2026-10-15T11:01")
	if res := closeTicket(t, s, webWang, w.wont, closeBody{Reason: "wontfix"}); res.StatusCode != http.StatusOK {
		t.Fatalf("WONT 关闭状态码 = %d", res.StatusCode)
	}

	// Q3：王工的 Agent 按模板建的任务（10-15），指派李工。
	w.at(t, "2026-10-15T16:02")
	w.q3 = w.create(t, w.wangA, map[string]any{
		"template": "assign-task", "title": "整理 Q3 质控规则对照表",
		"fields": map[string]string{
			"要达成什么目标？":     "把 Q3 新增的质控规则整理成一张对照表。",
			"交付什么？":        "一份 Excel 对照表：规则名、触发条件、适用机型。",
			"怎样算完成（验收标准）？": "覆盖 Q3 发布说明里的全部新规则；每条注明适用机型。",
			"期限？":          "10-20 前",
		},
		"labels":   []string{"算法:质控规则"},
		"assignee": "李工",
	})

	// N1 / N2：今天的两条入口反馈（待初筛），N1 由初筛 Agent 补上 部门:检验科。
	w.at(t, "2026-10-16T08:50")
	n1 := submitToIntake(t, s, "bug", map[string]any{
		"title": "UD-2300 质控时报警，提示 E203", "fields": n1Fields(), "author_name": "检验科 赵六",
	})
	w.n1, w.n1Key = n1.Ticket.Number, keyFrom(t, n1)
	w.at(t, "2026-10-16T08:55")
	if code, _ := setLabels(t, s, w.liA, w.n1, setLabelsBody{Add: []string{"部门:检验科"}}); code != http.StatusOK {
		t.Fatalf("N1 补部门标签状态码 = %d", code)
	}
	w.at(t, "2026-10-16T09:12")
	n2 := submitToIntake(t, s, "bug", map[string]any{
		"title": "打印报告乱码", "fields": n2Fields(), "author_name": "质量部 孙七",
	})
	w.n2, w.n2Key = n2.Ticket.Number, keyFrom(t, n2)
	return w
}

// 列表仅限成员：匿名 401，访客 403 members_only。
func Test列表仅限成员(t *testing.T) {
	w := newQueryWorld(t)
	res := w.s.Do(t, w.s.NewClient(), http.MethodGet, "/api/v1/tickets", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("匿名列表状态码 = %d, 想 401", res.StatusCode)
	}
	res = w.s.Do(t, w.s.GuestClient(w.fbKey), http.MethodGet, "/api/v1/tickets", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客列表状态码 = %d, 想 403", res.StatusCode)
	}
	var e errorBody
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "members_only" {
		t.Errorf("访客列表错误码 = %q, 想 members_only", e.Error.Code)
	}
}

// 默认条件：status=open；响应带 total/count/items/cursor/filters；每项只给摘要
// （编号、序号、标题、类型、状态、创建者、指派人、标签、waiting_on、时间、
// 回复数、last_change、read_full），时间线不在列表里。
func Test列表默认开启并返回摘要(t *testing.T) {
	w := newQueryWorld(t)
	body := w.list(t, w.wangA, "")

	if body.Total != 5 || body.Count != 5 {
		t.Fatalf("total/count = %d/%d, 想 5/5（默认只列开启）", body.Total, body.Count)
	}
	wantOrder := []string{w.n2, w.n1, w.q3, w.fb, w.doc} // 默认 -updated：更新时间倒序（最新在前）
	if got := numbers(body); !equalStrings(got, wantOrder) {
		t.Errorf("默认排序 = %v, 想 %v（按更新时间倒序）", got, wantOrder)
	}
	if body.Next != nil {
		t.Errorf("没有下一页时 next 应为 null，得到 %q", *body.Next)
	}
	if body.Cursor <= 0 {
		t.Errorf("cursor = %d, 想是当前全局变更序号", body.Cursor)
	}
	if fmt.Sprint(body.Filters["status"]) != "open" {
		t.Errorf("filters.status = %v, 想 open（默认值也回显）", body.Filters["status"])
	}
	if fmt.Sprint(body.Filters["sort"]) != "-updated" {
		t.Errorf("filters.sort = %v, 想 -updated", body.Filters["sort"])
	}

	n1 := body.Items[1]
	if n1.Number != w.n1 || n1.Seq != 6 || n1.URL != "/t/"+w.n1 {
		t.Errorf("N1 摘要 number/seq/url = %s/%d/%s", n1.Number, n1.Seq, n1.URL)
	}
	if n1.Kind != "report" || n1.Status != "open" || n1.CloseReason != nil {
		t.Errorf("N1 kind/status/close_reason = %s/%s/%v", n1.Kind, n1.Status, n1.CloseReason)
	}
	if n1.Template == nil || *n1.Template != "bug-report" {
		t.Errorf("N1 template = %v, 想 bug-report", n1.Template)
	}
	if n1.Creator != "访客" {
		t.Errorf("N1 creator = %q, 想 访客", n1.Creator)
	}
	if n1.Assignee != nil {
		t.Errorf("N1 assignee = %v, 想 null（未指派）", n1.Assignee)
	}
	assertContains(t, n1.Labels, "待初筛")
	assertContains(t, n1.Labels, "部门:检验科")
	if n1.WaitingOn.Party != "department" || n1.WaitingOn.Text != "本部门（未指派）" {
		t.Errorf("N1 waiting_on = %+v, 想 department/本部门（未指派）", n1.WaitingOn)
	}
	if want := "2026-10-16T08:50:00+08:00"; n1.CreatedAt.Format(time.RFC3339) != want {
		t.Errorf("N1 created_at = %s, 想 %s", n1.CreatedAt.Format(time.RFC3339), want)
	}
	if want := "2026-10-16T08:55:00+08:00"; n1.UpdatedAt.Format(time.RFC3339) != want {
		t.Errorf("N1 updated_at = %s, 想 %s（最后一条事件是补标签）", n1.UpdatedAt.Format(time.RFC3339), want)
	}
	if n1.ClosedAt != nil {
		t.Errorf("N1 closed_at = %v, 想 null", n1.ClosedAt)
	}
	if n1.ReplyCount != 0 {
		t.Errorf("N1 reply_count = %d, 想 0", n1.ReplyCount)
	}
	if n1.LastChange <= 0 {
		t.Errorf("N1 last_change = %d, 想是它最后一条事件的变更序号", n1.LastChange)
	}
	if n1.ReadFull != "/api/v1/tickets/"+w.n1+"?format=markdown" {
		t.Errorf("N1 read_full = %q", n1.ReadFull)
	}
	if n1.Brief != nil {
		t.Errorf("默认不给说明，brief = %v", n1.Brief)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 筛选条件之间是「并且」：kind/template/status=all/closed 组合。
func Test筛选kind模板与状态(t *testing.T) {
	w := newQueryWorld(t)
	got := w.list(t, w.zhaoA, "?kind=report&status=all")
	want := []string{w.n2, w.n1, w.wont, w.fb, w.old} // 更新时间倒序（最新在前）
	if !equalStrings(numbers(got), want) {
		t.Errorf("kind=report&status=all = %v, 想 %v", numbers(got), want)
	}
	got = w.list(t, w.zhaoA, "?status=closed")
	if !equalStrings(numbers(got), []string{w.wont, w.old}) {
		t.Errorf("status=closed = %v, 想 [%s %s]", numbers(got), w.wont, w.old)
	}
	got = w.list(t, w.zhaoA, "?template=assign-task")
	if !equalStrings(numbers(got), []string{w.q3}) {
		t.Errorf("template=assign-task = %v, 想 [%s]", numbers(got), w.q3)
	}
	if fmt.Sprint(got.Filters["template"]) != "assign-task" {
		t.Errorf("filters.template = %v", got.Filters["template"])
	}
	// 未知模板 422。
	res := w.listRaw(t, w.zhaoA, "?template=no-such")
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("未知模板状态码 = %d, 想 422", res.StatusCode)
	}
	var e errorBody
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "template_not_found" {
		t.Errorf("未知模板错误码 = %q, 想 template_not_found", e.Error.Code)
	}
}

// 标签筛选：label 必须全部带有、no_label 必须不带有，都可以重复；
// 维度:值 / 不带维度 / 维度:* / 别名；别名换算回显在 resolved_aliases。
func Test筛选标签与别名(t *testing.T) {
	w := newQueryWorld(t)

	got := w.list(t, w.wangA, "?label="+urlEncode("机型:UD-2300")+"&status=all")
	if !equalStrings(numbers(got), []string{w.fb, w.old}) {
		t.Errorf("label=机型:UD-2300&status=all = %v, 想 [%s %s]", numbers(got), w.fb, w.old)
	}
	// 写别名 2300 → 机型:UD-2300，回显换算。
	got = w.list(t, w.wangA, "?label=2300&status=closed")
	if !equalStrings(numbers(got), []string{w.old}) {
		t.Errorf("label=2300&status=closed = %v, 想 [%s]", numbers(got), w.old)
	}
	if len(got.ResolvedAliases) != 1 || got.ResolvedAliases[0] != "2300 → 机型:UD-2300" {
		t.Errorf("resolved_aliases = %v, 想 [2300 → 机型:UD-2300]", got.ResolvedAliases)
	}
	if labels, _ := got.Filters["label"].([]any); len(labels) != 1 || fmt.Sprint(labels[0]) != "机型:UD-2300" {
		t.Errorf("filters.label = %v, 想换算后的标准值", got.Filters["label"])
	}
	// 通配：机型有任何一个值。
	got = w.list(t, w.wangA, "?label="+urlEncode("机型:*")+"&status=all")
	if !equalStrings(numbers(got), []string{w.wont, w.fb, w.old}) {
		t.Errorf("label=机型:*&status=all = %v, 想 [%s %s %s]", numbers(got), w.wont, w.fb, w.old)
	}
	// no_label：不带 待初筛 的开启单。
	got = w.list(t, w.wangA, "?no_label="+urlEncode("待初筛"))
	if !equalStrings(numbers(got), []string{w.q3, w.fb, w.doc}) {
		t.Errorf("no_label=待初筛 = %v, 想 [%s %s %s]", numbers(got), w.q3, w.fb, w.doc)
	}
	// 重复参数之间是并且：同时有 血液 和 机型:* → FB 与 OLD。
	got = w.list(t, w.wangA, "?label="+urlEncode("产品线:血液")+"&label="+urlEncode("机型:*")+"&status=all")
	if !equalStrings(numbers(got), []string{w.fb, w.old}) {
		t.Errorf("血液+机型:* = %v, 想 [%s %s]", numbers(got), w.fb, w.old)
	}
	// 字典里没有的值：422 unknown_label_value + suggestions。
	res := w.listRaw(t, w.wangA, "?label="+urlEncode("机型:D-2300"))
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("未知标签值状态码 = %d, 想 422", res.StatusCode)
	}
	var e errorBodyWithExtra
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "unknown_label_value" {
		t.Errorf("未知标签值错误码 = %q", e.Error.Code)
	}
	if len(e.Error.Suggestions) == 0 || e.Error.Suggestions[0] != "机型:UD-2300" {
		t.Errorf("suggestions = %v, 想以 机型:UD-2300 开头（norm 包含）", e.Error.Suggestions)
	}
	// 未知维度：422 unknown_dimension。
	res = w.listRaw(t, w.wangA, "?label="+urlEncode("不存在:值"))
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("未知维度状态码 = %d, 想 422", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "unknown_dimension" {
		t.Errorf("未知维度错误码 = %q, 想 unknown_dimension", e.Error.Code)
	}
}

// 成员与等待方筛选：assignee=名|me|none、creator=名|me|guest、
// waiting_on=assignee|creator|guest|department|me；不存在的成员 422。
func Test筛选指派人创建者与等待方(t *testing.T) {
	w := newQueryWorld(t)

	got := w.list(t, w.liA, "?assignee=me&status=all")
	if !equalStrings(numbers(got), []string{w.q3, w.old}) {
		t.Errorf("assignee=me(李工) = %v, 想 [%s %s]", numbers(got), w.q3, w.old)
	}
	if fmt.Sprint(got.Filters["assignee"]) != "李工" {
		t.Errorf("filters.assignee = %v, 想解析成姓名", got.Filters["assignee"])
	}
	got = w.list(t, w.liA, "?assignee=none")
	if !equalStrings(numbers(got), []string{w.n2, w.n1}) {
		t.Errorf("assignee=none = %v, 想 [%s %s]", numbers(got), w.n2, w.n1)
	}
	got = w.list(t, w.wangA, "?assignee=王工&status=all")
	if !equalStrings(numbers(got), []string{w.wont, w.fb}) {
		t.Errorf("assignee=王工&status=all = %v, 想 [%s %s]", numbers(got), w.wont, w.fb)
	}
	// creator=guest：经反馈入口提交的。
	got = w.list(t, w.wangA, "?creator=guest")
	if !equalStrings(numbers(got), []string{w.n2, w.n1}) {
		t.Errorf("creator=guest = %v, 想 [%s %s]", numbers(got), w.n2, w.n1)
	}
	got = w.list(t, w.wangA, "?creator=王工&status=all")
	if !equalStrings(numbers(got), []string{w.q3, w.wont, w.fb}) {
		t.Errorf("creator=王工 = %v, 想 [%s %s %s]", numbers(got), w.q3, w.wont, w.fb)
	}
	// waiting_on=department：初筛队列（未指派且轮到本部门）。
	got = w.list(t, w.liA, "?waiting_on=department")
	if !equalStrings(numbers(got), []string{w.n2, w.n1}) {
		t.Errorf("waiting_on=department = %v, 想 [%s %s]", numbers(got), w.n2, w.n1)
	}
	// waiting_on=guest：等访客回话（成员回复过的反馈）。
	got = w.list(t, w.liA, "?waiting_on=guest")
	if !equalStrings(numbers(got), []string{w.fb}) {
		t.Errorf("waiting_on=guest = %v, 想 [%s]", numbers(got), w.fb)
	}
	// waiting_on=me：Q3 轮到指派人李工（王工的 FB 自己回复过，轮到访客）。
	got = w.list(t, w.liA, "?waiting_on=me")
	if !equalStrings(numbers(got), []string{w.q3}) {
		t.Errorf("waiting_on=me(李工) = %v, 想 [%s]", numbers(got), w.q3)
	}
	// 不存在的成员 422 unknown_member。
	res := w.listRaw(t, w.wangA, "?assignee=没有这个人")
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("未知成员状态码 = %d, 想 422", res.StatusCode)
	}
	var e errorBody
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "unknown_member" {
		t.Errorf("未知成员错误码 = %q, 想 unknown_member", e.Error.Code)
	}
	// waiting_on 拼错 422。
	res = w.listRaw(t, w.wangA, "?waiting_on=anyone")
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("waiting_on=anyone 状态码 = %d, 想 422", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "invalid_waiting_on" {
		t.Errorf("waiting_on=anyone 错误码 = %q, 想 invalid_waiting_on", e.Error.Code)
	}
}

// 时间范围（可注入时钟）：_after 包含该时刻、_before 不包含；日期默认 00:00。
func Test筛选时间范围边界(t *testing.T) {
	w := newQueryWorld(t)

	count := func(query string) int64 {
		t.Helper()
		return w.list(t, w.zhaoA, query).Total
	}
	// OLD 关闭于 2026-10-12 16:35：_after 含该时刻、_before 不含（WONT 关闭于 10-15 11:01）。
	if n := count("?status=closed&closed_after=2026-10-12T16:35&closed_before=2026-10-12T16:36"); n != 1 {
		t.Errorf("closed_after=16:35（含）的一分钟内 = %d, 想 1（OLD）", n)
	}
	if n := count("?status=closed&closed_after=2026-10-12T16:36&closed_before=2026-10-12T16:37"); n != 0 {
		t.Errorf("closed_after=16:36（不含 16:35）= %d, 想 0", n)
	}
	// 日期默认 00:00：closed_before=10-13 覆盖 OLD；closed_after=10-13 只剩 WONT。
	if n := count("?status=closed&closed_before=2026-10-13"); n != 1 {
		t.Errorf("closed_before=10-13 总数 = %d, 想 1", n)
	}
	if n := count("?status=closed&closed_after=2026-10-13"); n != 1 {
		t.Errorf("closed_after=10-13 总数 = %d, 想 1（WONT）", n)
	}
	// created：after 含当天 00:00（默认 status=open 会把 WONT 挡掉，要 status=all）。
	if n := count("?status=all&created_after=2026-10-14&created_before=2026-10-15"); n != 1 {
		t.Errorf("10-14 当天创建 = %d, 想 1（WONT）", n)
	}
	// updated：FB 最后更新于 10-15 09:55（王工的回复）。
	if n := count("?updated_after=2026-10-15T09:55&updated_before=2026-10-15T09:56"); n != 1 {
		t.Errorf("FB 更新的一分钟 = %d, 想 1", n)
	}
	// 时间范围内的关闭单筛选不作用于未关闭单。
	if n := count("?status=all&closed_after=2026-10-01"); n != 2 {
		t.Errorf("closed_after=10-01&status=all = %d, 想 2（只有两张关闭单）", n)
	}
	// 格式不对 422 invalid_time。
	res := w.listRaw(t, w.zhaoA, "?created_after=2026/10/01")
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("错误时间格式状态码 = %d, 想 422", res.StatusCode)
	}
	var e errorBody
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "invalid_time" {
		t.Errorf("时间格式错误码 = %q, 想 invalid_time", e.Error.Code)
	}
}

// 关键词 q：在标题、说明与回复里查找，忽略大小写、空格和连字符
// （E203 找到 E-203）；命中项带 matched_in 与 snippet。
func Test关键词忽略连字符并带片段(t *testing.T) {
	w := newQueryWorld(t)
	got := w.list(t, w.wangA, "?q=E203&status=all")

	if got.Total != 2 {
		t.Fatalf("q=E203 命中 %d 张, 想 2（OLD 与 N1）：%v", got.Total, numbers(got))
	}
	if !equalStrings(numbers(got), []string{w.n1, w.old}) {
		t.Errorf("q=E203 = %v, 想 [%s %s]", numbers(got), w.n1, w.old)
	}
	n1, old := got.Items[0], got.Items[1]
	if !equalStrings(n1.MatchedIn, []string{"标题", "说明"}) {
		t.Errorf("N1 matched_in = %v, 想 [标题 说明]", n1.MatchedIn)
	}
	if !strings.Contains(n1.Snippet, "E203") {
		t.Errorf("N1 snippet = %q, 想包含命中原文", n1.Snippet)
	}
	if !equalStrings(old.MatchedIn, []string{"标题", "说明"}) {
		t.Errorf("OLD matched_in = %v, 想 [标题 说明]", old.MatchedIn)
	}
	if !strings.Contains(old.Snippet, "E-203") {
		t.Errorf("OLD snippet = %q, 想保留连字符的原文", old.Snippet)
	}
	// 回复里的关键词也算。
	got = w.list(t, w.wangA, "?q=试剂批号")
	if got.Total != 1 || got.Items[0].Number != w.fb {
		t.Errorf("q=试剂批号 = %v, 想 [%s]（只在王工的回复里）", numbers(got), w.fb)
	}
	if !equalStrings(got.Items[0].MatchedIn, []string{"回复 2"}) {
		t.Errorf("回复命中的 matched_in = %v, 想 [回复 2]", got.Items[0].MatchedIn)
	}
}

// 变更序号：列表响应带 cursor；changed_after 只返回之后变化过的工单
// （包括只有标签变化的），与其他条件可组合；没有变化时空列表、cursor 不变。
func Test变更序号增量拉取(t *testing.T) {
	w := newQueryWorld(t)

	first := w.list(t, w.wangA, "?status=all&limit=1")
	cursor := first.Cursor

	// 期间：张三回复 FB、赵工给 DOC 摘掉一个标签（只有标签变化）。
	w.at(t, "2026-10-16T10:00")
	w.guestReply(t, w.fbKey, w.fb, "试剂批号 R20260930。", "张三")
	w.at(t, "2026-10-16T10:05")
	if code, _ := setLabels(t, w.s, w.webZhao, w.doc, setLabelsBody{Remove: []string{"产品线:血液"}}); code != http.StatusOK {
		t.Fatalf("DOC 摘标签状态码 = %d", code)
	}

	got := w.list(t, w.wangA, "?status=all&changed_after="+fmt.Sprintf("%d", cursor))
	if !equalStrings(numbers(got), []string{w.fb, w.doc}) {
		t.Errorf("changed_after = %v, 想 [%s %s]（含只有标签变化的 DOC）", numbers(got), w.fb, w.doc)
	}
	for _, it := range got.Items {
		if it.LastChange <= cursor {
			t.Errorf("%s 的 last_change = %d, 应大于 cursor %d", it.Number, it.LastChange, cursor)
		}
	}
	if got.Cursor <= cursor {
		t.Errorf("新 cursor = %d, 应已推进（> %d）", got.Cursor, cursor)
	}

	// 与其他条件组合：只看变化过且轮到我（王工是指派人）。
	got = w.list(t, w.wangA, "?changed_after="+fmt.Sprintf("%d", cursor)+"&waiting_on=me")
	if !equalStrings(numbers(got), []string{w.fb}) {
		t.Errorf("变化过且轮到我 = %v, 想 [%s]", numbers(got), w.fb)
	}

	// 用新 cursor 再拿 → 空列表、cursor 不变。
	after := w.list(t, w.wangA, "?status=all&changed_after="+fmt.Sprintf("%d", got.Cursor))
	if after.Total != 0 || len(after.Items) != 0 {
		t.Errorf("无变化时应为空列表，得到 %v", numbers(after))
	}
	if after.Cursor != got.Cursor {
		t.Errorf("无变化时 cursor 应不变：%d ≠ %d", after.Cursor, got.Cursor)
	}

	// changed_after 非整数 422。
	res := w.listRaw(t, w.wangA, "?changed_after=abc")
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("changed_after=abc 状态码 = %d, 想 422", res.StatusCode)
	}
	var e errorBody
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "invalid_cursor" {
		t.Errorf("changed_after=abc 错误码 = %q, 想 invalid_cursor", e.Error.Code)
	}
}

// urlEncode 编码查询参数里的中文（测试里手拼查询串用）。
func urlEncode(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// 排序：-updated/-created/created/-closed/change；limit 默认 50、最大 200；
// 翻页用 page_token（偏移量），跟着 next 走。
func Test排序与翻页(t *testing.T) {
	w := newQueryWorld(t)

	// sort=created：按创建时间正序 → 完整拉取靠它。
	got := w.list(t, w.zhaoA, "?status=all&sort=created")
	if !equalStrings(numbers(got), []string{w.old, w.doc, w.wont, w.fb, w.q3, w.n1, w.n2}) {
		t.Errorf("sort=created = %v", numbers(got))
	}
	// sort=-created：倒序。
	got = w.list(t, w.zhaoA, "?status=all&sort=-created")
	if !equalStrings(numbers(got), []string{w.n2, w.n1, w.q3, w.fb, w.wont, w.doc, w.old}) {
		t.Errorf("sort=-created = %v", numbers(got))
	}
	// sort=-closed：关闭时间倒序（只有两张关闭单）。
	got = w.list(t, w.zhaoA, "?status=closed&sort=-closed")
	if !equalStrings(numbers(got), []string{w.wont, w.old}) {
		t.Errorf("sort=-closed = %v, 想 [WONT OLD]", numbers(got))
	}
	// 每页 3 张，跟着 next 翻到第 2 页。
	page1 := w.list(t, w.zhaoA, "?status=all&sort=created&limit=3")
	if page1.Total != 7 || page1.Count != 3 || !equalStrings(numbers(page1), []string{w.old, w.doc, w.wont}) {
		t.Fatalf("第 1 页 = %v（total %d）", numbers(page1), page1.Total)
	}
	if page1.Next == nil || !strings.Contains(*page1.Next, "/api/v1/tickets?") ||
		!strings.Contains(*page1.Next, "page_token=p3") {
		t.Fatalf("next = %v, 想带 page_token=p3 的完整地址", page1.Next)
	}
	res := w.s.Do(t, w.zhaoA, http.MethodGet, *page1.Next, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET next 状态码 = %d", res.StatusCode)
	}
	var page2 listBody
	tbtest.DecodeJSON(t, res, &page2)
	if !equalStrings(numbers(page2), []string{w.fb, w.q3, w.n1}) {
		t.Errorf("第 2 页 = %v, 想 [FB Q3 N1]", numbers(page2))
	}
	if page2.Next == nil || !strings.Contains(*page2.Next, "page_token=p6") {
		t.Errorf("第 2 页 next = %v", page2.Next)
	}
	// 最后一页：只有 1 张、next 为 null。
	var page3 listBody
	w.s.Do(t, w.zhaoA, http.MethodGet, *page2.Next, nil, &page3)
	if !equalStrings(numbers(page3), []string{w.n2}) || page3.Next != nil {
		t.Errorf("第 3 页 = %v next %v", numbers(page3), page3.Next)
	}
	// 手写 page_token 也可以（就是偏移量）。
	got = w.list(t, w.zhaoA, "?status=all&sort=created&limit=3&page_token=p3")
	if !equalStrings(numbers(got), []string{w.fb, w.q3, w.n1}) {
		t.Errorf("手写 page_token = %v", numbers(got))
	}
}

// 参数校验：unknown_parameter（含可用参数列表）、limit 超界、page_token/sort 拼错。
func Test查询参数校验(t *testing.T) {
	w := newQueryWorld(t)

	res := w.listRaw(t, w.wangA, "?lable="+urlEncode("待初筛"))
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("拼错参数状态码 = %d, 想 422", res.StatusCode)
	}
	var e errorBodyWithExtra
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "unknown_parameter" {
		t.Errorf("拼错参数错误码 = %q, 想 unknown_parameter", e.Error.Code)
	}
	if !strings.Contains(e.Error.Message, "lable") || !strings.Contains(e.Error.Hint, "label") {
		t.Errorf("unknown_parameter 应说明哪个参数、可用哪些：%+v", e.Error)
	}
	for _, bad := range []struct{ query, code string }{
		{"?limit=500", "invalid_limit"},
		{"?limit=0", "invalid_limit"},
		{"?limit=abc", "invalid_limit"},
		{"?page_token=x", "invalid_page_token"},
		{"?sort=updated_desc", "invalid_sort"},
		{"?status=OPEN", "invalid_status"},
		{"?kind=bug", "invalid_kind"},
		{"?include=full", "invalid_include"},
	} {
		res := w.listRaw(t, w.wangA, bad.query)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s 状态码 = %d, 想 422", bad.query, res.StatusCode)
			continue
		}
		var e errorBody
		tbtest.DecodeJSON(t, res, &e)
		if e.Error.Code != bad.code {
			t.Errorf("%s 错误码 = %q, 想 %q", bad.query, e.Error.Code, bad.code)
		}
	}
}

// include=brief 附带说明（拼好的 Markdown 与按字段的 fields）；format=markdown
// 返回一张表格，末尾附「下一步」。
func Test附带说明与markdown表格(t *testing.T) {
	w := newQueryWorld(t)

	got := w.list(t, w.liA, "?label="+urlEncode("待初筛")+"&waiting_on=department&include=brief")
	if !equalStrings(numbers(got), []string{w.n2, w.n1}) {
		t.Fatalf("初筛队列（带说明）= %v, 想 [%s %s]", numbers(got), w.n2, w.n1)
	}
	brief := got.Items[1].Brief // N1
	if brief == nil {
		t.Fatal("include=brief 应附带说明")
	}
	if !strings.Contains(brief.Markdown, "做质控的时候报警，屏幕显示 E203") {
		t.Errorf("N1 说明 Markdown = %q, 想包含字段内容", brief.Markdown)
	}
	if brief.Fields == nil {
		t.Error("模板工单的 brief 应带 fields")
	} else if brief.Fields["哪台仪器？"] != "不确定" {
		t.Errorf("fields[哪台仪器？] = %q", brief.Fields["哪台仪器？"])
	}
	if brief.Template == nil || *brief.Template != "bug-report" {
		t.Errorf("brief.template = %v", brief.Template)
	}
	if brief.Revision != 1 {
		t.Errorf("brief.revision = %d, 想 1", brief.Revision)
	}

	// format=markdown：表格 + 下一步。
	res := w.s.Do(t, w.zhaoA, http.MethodGet, "/api/v1/tickets?status=all&format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("markdown 列表状态码 = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, 想 text/markdown", ct)
	}
	body, _ := io.ReadAll(res.Body)
	md := string(body)
	for _, want := range []string{
		"# 工单列表（共 7 张，本页 7 张）",
		"| 编号 | 标题 | 状态 | 指派人 | 轮到 | 标签 | 更新 |",
		"| " + w.old + " | UD-2300 开机自检报 E-203 | 已关闭（完成） | 李工 | 无（已关闭） |",
		"| " + w.n2 + " | 打印报告乱码 | 开启 | 未指派 | 本部门（未指派） | 待初筛 |",
		"## 下一步",
		"changed_after=",
		"读某一张的全文",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("列表 Markdown 缺少 %q：\n%s", want, md)
		}
	}
	// 空结果也有占位行。
	res = w.s.Do(t, w.zhaoA, http.MethodGet, "/api/v1/tickets?q=不存在词&format=markdown", nil, nil)
	body, _ = io.ReadAll(res.Body)
	if !strings.Contains(string(body), "（没有符合条件的工单）") {
		t.Errorf("空结果 Markdown 应有占位行：\n%s", string(body))
	}
	// 带 q 的行内联命中信息。
	res = w.s.Do(t, w.zhaoA, http.MethodGet, "/api/v1/tickets?q=E203&status=all&format=markdown", nil, nil)
	body, _ = io.ReadAll(res.Body)
	if !strings.Contains(string(body), "…命中标题、说明：") {
		t.Errorf("q 命中的行应带 matched_in/snippet：\n%s", string(body))
	}
}

// 计数：按一个维度分组；筛选条件与列表相同；多值各算一次；没有该维度计入
// (无)；assignee 无 → (未指派)、close_reason 无 → (未关闭)；按 count 降序。
func Test计数按维度分组(t *testing.T) {
	w := newQueryWorld(t)

	got := w.counts(t, w.zhaoA, "?group_by="+urlEncode("产品线")+"&status=all")
	if got.Total != 7 {
		t.Fatalf("total = %d, 想 7", got.Total)
	}
	wantGroups := []countGroup{
		{Value: "血液", Count: 3},  // OLD、DOC、FB
		{Value: "(无)", Count: 3}, // Q3、N1、N2
		{Value: "尿液", Count: 1},  // WONT
	}
	if !equalGroups(got.Groups, wantGroups) {
		t.Errorf("group_by=产品线 = %v, 想 %v（按 count 降序，同数按首现顺序）", got.Groups, wantGroups)
	}
	if got.Note == nil || !strings.Contains(*got.Note, "各组之和可能大于 total") {
		t.Errorf("标签维度分组的 note = %v", got.Note)
	}
	// 一张工单在同一维度有多个值时各算一次。
	if code, _ := setLabels(t, w.s, w.webWang, w.old, setLabelsBody{Add: []string{"产品线:尿液"}}); code != http.StatusOK {
		t.Fatalf("给 OLD 补第二个产品线标签状态码 = %d", code)
	}
	got = w.counts(t, w.zhaoA, "?group_by="+urlEncode("产品线")+"&status=all")
	wantGroups = []countGroup{
		{Value: "血液", Count: 3},
		{Value: "(无)", Count: 3},
		{Value: "尿液", Count: 2}, // WONT + OLD（OLD 两个值各算一次，但数量仍按次数）
	}
	if !equalGroups(got.Groups, wantGroups) {
		t.Errorf("多值各算一次 = %v, 想 %v", got.Groups, wantGroups)
	}
	if got.Total != 7 {
		t.Errorf("total 仍是工单数 = %d, 想 7", got.Total)
	}
}

// 计数与内建维度：close_reason（时间范围筛选同列表）、assignee、waiting_on。
func Test计数内建维度(t *testing.T) {
	w := newQueryWorld(t)

	got := w.counts(t, w.zhaoA, "?group_by=close_reason&status=closed&closed_after=2026-10-01&closed_before=2026-11-01")
	if !equalGroups(got.Groups, []countGroup{
		{Value: "完成", Count: 1}, {Value: "不处理", Count: 1}, // OLD 先关闭，先出现
	}) {
		t.Errorf("10 月关闭按原因 = %v", got.Groups)
	}
	got = w.counts(t, w.zhaoA, "?group_by=close_reason")
	if !equalGroups(got.Groups, []countGroup{{Value: "(未关闭)", Count: 5}}) {
		t.Errorf("默认只看开启单，未关闭 = %v", got.Groups)
	}
	got = w.counts(t, w.zhaoA, "?group_by=assignee")
	want := []countGroup{
		{Value: "(未指派)", Count: 2},
		{Value: "赵工", Count: 1}, // DOC 先建先出现
		{Value: "王工", Count: 1},
		{Value: "李工", Count: 1},
	}
	if !equalGroups(got.Groups, want) {
		t.Errorf("assignee = %v, 想 %v", got.Groups, want)
	}
	got = w.counts(t, w.zhaoA, "?group_by=waiting_on")
	want = []countGroup{
		{Value: "本部门（未指派）", Count: 2},
		{Value: "赵工（指派人）", Count: 1},
		{Value: "访客", Count: 1},
		{Value: "李工（指派人）", Count: 1},
	}
	if !equalGroups(got.Groups, want) {
		t.Errorf("waiting_on = %v, 想 %v", got.Groups, want)
	}
	// 标签筛选与别名换算在计数里同样生效（resolved_aliases 回显）。
	got = w.counts(t, w.zhaoA, "?group_by="+urlEncode("机型")+"&status=all&label="+urlEncode("部门:检验科"))
	if got.Total != 2 || !equalGroups(got.Groups, []countGroup{
		{Value: "UD-2300", Count: 1}, {Value: "(无)", Count: 1},
	}) {
		t.Errorf("检验科提的按机型 = total %d, %v", got.Total, got.Groups)
	}
	got = w.counts(t, w.zhaoA, "?group_by="+urlEncode("机型")+"&status=closed&label=2300")
	if !equalGroups(got.Groups, []countGroup{{Value: "UD-2300", Count: 1}}) {
		t.Errorf("别名筛选 + 计数 = %v", got.Groups)
	}
	if len(got.ResolvedAliases) != 1 || got.ResolvedAliases[0] != "2300 → 机型:UD-2300" {
		t.Errorf("counts 的 resolved_aliases = %v", got.ResolvedAliases)
	}
	// status / kind 原值分组。
	got = w.counts(t, w.zhaoA, "?group_by=status")
	if !equalGroups(got.Groups, []countGroup{{Value: "open", Count: 5}}) {
		t.Errorf("status = %v", got.Groups)
	}
}

// 计数的错误与限制：缺 group_by、非法 group_by、不接受 sort/limit/翻页、
// 仅成员可用。
func Test计数校验(t *testing.T) {
	w := newQueryWorld(t)

	res := w.s.Do(t, w.zhaoA, http.MethodGet, "/api/v1/tickets/counts", nil, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("缺 group_by 状态码 = %d", res.StatusCode)
	}
	var e errorBody
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "group_by_required" {
		t.Errorf("缺 group_by 错误码 = %q", e.Error.Code)
	}
	res = w.s.Do(t, w.zhaoA, http.MethodGet, "/api/v1/tickets/counts?group_by=标题", nil, nil)
	tbtest.DecodeJSON(t, res, &e)
	if res.StatusCode != http.StatusUnprocessableEntity || e.Error.Code != "invalid_group_by" {
		t.Errorf("非法 group_by = %d/%q, 想 422/invalid_group_by", res.StatusCode, e.Error.Code)
	}
	for _, q := range []string{"?group_by=status&sort=created", "?group_by=status&limit=10",
		"?group_by=status&page_token=p1", "?group_by=status&include=brief", "?group_by=status&format=markdown"} {
		res := w.s.Do(t, w.zhaoA, http.MethodGet, "/api/v1/tickets/counts"+q, nil, nil)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("%s 状态码 = %d, 想 422", q, res.StatusCode)
			continue
		}
		tbtest.DecodeJSON(t, res, &e)
		if e.Error.Code != "unknown_parameter" {
			t.Errorf("%s 错误码 = %q, 想 unknown_parameter", q, e.Error.Code)
		}
	}
	// 列表接口反过来不接受 group_by。
	res = w.s.Do(t, w.zhaoA, http.MethodGet, "/api/v1/tickets?group_by=status", nil, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("列表带 group_by 状态码 = %d, 想 422", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "unknown_parameter" {
		t.Errorf("列表带 group_by 错误码 = %q", e.Error.Code)
	}
	// 仅成员。
	res = w.s.Do(t, w.s.GuestClient(w.fbKey), http.MethodGet, "/api/v1/tickets/counts?group_by=status", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客计数状态码 = %d, 想 403", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "members_only" {
		t.Errorf("访客计数错误码 = %q, 想 members_only", e.Error.Code)
	}
}

// equalGroups 比较计数分组（值与数量都一致）。
func equalGroups(a, b []countGroup) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
