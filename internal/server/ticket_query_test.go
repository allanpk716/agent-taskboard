package server_test

// #37 工单查询与计数：黑盒测试照搬 prototype/ticket-query 的 5 个场景（32 步，
// 见场景 1–5 各测试），加上筛选、分页、关键词、变更序号与计数的逐项断言。
// 世界（queryWorld）是原型 initialState 的移植：三成员 + 各一个 Agent，
// 七张工单覆盖 开启/关闭、任务/反馈、模板/自由文本、访客创建/成员创建。

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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
	Number      string `json:"number"`
	Seq         int64  `json:"seq"`
	URL         string `json:"url"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Status      string `json:"status"`
	CloseReason *string `json:"close_reason"`
	Template    *string `json:"template"`
	Creator     string `json:"creator"`
	Assignee    *string `json:"assignee"`
	Labels      []string `json:"labels"`
	WaitingOn   waitingOnBody `json:"waiting_on"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	ClosedAt    *time.Time `json:"closed_at"`
	ReplyCount  int `json:"reply_count"`
	LastChange  int64 `json:"last_change"`
	ReadFull    string `json:"read_full"`
	MatchedIn   []string `json:"matched_in"`
	Snippet     string `json:"snippet"`
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
	oldKey                          string // OLD 提交者的访客链接密钥
	fbKey                           string // FB 的访客链接（检验科-张三）密钥
	n1Key, n2Key                    string // 入口提交者的访客链接密钥
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
		"哪台仪器？":        "UD-2300",
		"什么时候发生的？":     "10-08 下午",
		"你做了哪些操作？":     "正常开机，未做其他操作。",
		"实际看到了什么？":     "开机自检到液路检测时报 E-203，之后无法进入待机。",
		"你期望看到什么？":     "进入待机。",
		"出现得频繁吗？":      "偶尔",
	}
}

// n1Fields 是 N1 的入口提交字段（标题与说明里出现 E203；机型「不确定」）。
func n1Fields() map[string]string {
	return map[string]string{
		"哪台仪器？":        "不确定",
		"什么时候发生的？":     "今天早上",
		"你做了哪些操作？":     "早上开机后直接做质控。",
		"实际看到了什么？":     "做质控的时候报警，屏幕显示 E203，质控结果没出来。",
		"你期望看到什么？":     "正常开始质控测试。",
		"出现得频繁吗？":      "仅一次",
	}
}

// n2Fields 是 N2 的入口提交字段。
func n2Fields() map[string]string {
	return map[string]string{
		"哪台仪器？":        "不确定",
		"什么时候发生的？":     "今天上午",
		"你做了哪些操作？":     "审核后点打印。",
		"实际看到了什么？":     "打印出来的报告中文全是乱码。",
		"你期望看到什么？":     "正常打印。",
		"出现得频繁吗？":      "每次",
	}
}

// newQueryWorld 启动服务并按原型 initialState 造出七张工单。时间与原型一致
//（假时钟逐事件回拨/前拨），使排序、时间范围与变更序号语义都可断言。
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
		"labels":  []string{"产品线:尿液", "机型:UD-1300"}, "assignee": "王工",
	})

	// FB：王工自派自的反馈 + 访客链接（检验科-张三），两人各回复一次（10-15）。
	w.at(t, "2026-10-15T09:00")
	w.fb = w.create(t, webWang, map[string]any{
		"kind": "report", "title": "UD-2300 质控结果偏高",
		"brief_md": "检验科反馈：UD-2300 从 10-14 起，ALT 质控连续 3 次偏高 2SD 以上。",
		"labels":  []string{"产品线:血液", "机型:UD-2300"}, "assignee": "王工",
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
			"要达成什么目标？":       "把 Q3 新增的质控规则整理成一张对照表。",
			"交付什么？":          "一份 Excel 对照表：规则名、触发条件、适用机型。",
			"怎样算完成（验收标准）？": "覆盖 Q3 发布说明里的全部新规则；每条注明适用机型。",
			"期限？":            "10-20 前",
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
//（编号、序号、标题、类型、状态、创建者、指派人、标签、waiting_on、时间、
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
