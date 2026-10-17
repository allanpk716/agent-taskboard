package server_test

// prototype/ticket-query 的 5 个场景（共 32 步）照搬为黑盒测试（#37 验收标准）。
// 字典值按生产种子改写：机型 BH-7600CS → UD-2300，别名 7600 → 2300，
// 产品线:生化 → 血液（别名 血液线）；断言的语义与原型逐步一致。

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// readMarkdownOf 读单张工单的 Markdown 视图。
func (w *queryWorld) readMarkdownOf(t testing.TB, c *http.Client, number string) string {
	t.Helper()
	res := w.s.Do(t, c, http.MethodGet, "/api/v1/tickets/"+number+"?format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读 %s 的 Markdown 状态码 = %d", number, res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应体失败：%v", err)
	}
	return string(body)
}

// readListMarkdown 读列表 Markdown 视图。
func (w *queryWorld) readListMarkdown(t testing.TB, c *http.Client, path string) string {
	t.Helper()
	res := w.s.Do(t, c, http.MethodGet, path, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d", path, res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应体失败：%v", err)
	}
	return string(body)
}

// 场景 1（初筛 Agent 巡检，7 步）：查「带待初筛、轮到本部门」的队列 →
// 写别名打标签 → 追问访客（工单离开队列）→ 队列空 → 未指派查询 → 指派；
// 验收点：访客回复后工单自动回到队列。
func Test场景1初筛Agent巡检(t *testing.T) {
	w := newQueryWorld(t)
	queue := "?label=" + urlEncode("待初筛") + "&waiting_on=department"

	// 1. 初筛队列（带说明）→ 2 张。
	got := w.list(t, w.liA, queue+"&include=brief")
	if got.Total != 2 || !equalStrings(numbers(got), []string{w.n2, w.n1}) {
		t.Fatalf("初筛队列 = %v, 想 [%s %s]", numbers(got), w.n2, w.n1)
	}
	for _, it := range got.Items {
		if it.Brief == nil || it.Brief.Markdown == "" {
			t.Errorf("%s 的说明没带上", it.Number)
		}
	}

	// 2. 按俗称找标签：2300。
	res := w.s.Do(t, w.liA, http.MethodGet, "/api/v1/labels/resolve?q=2300", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("resolve 状态码 = %d", res.StatusCode)
	}
	var resolve struct {
		Matches []struct {
			Label string `json:"label"`
			Via   string `json:"via"`
			Alias string `json:"alias"`
		} `json:"matches"`
	}
	tbtest.DecodeJSON(t, res, &resolve)
	if len(resolve.Matches) < 1 || resolve.Matches[0].Label != "机型:UD-2300" ||
		resolve.Matches[0].Via != "alias" || resolve.Matches[0].Alias != "2300" {
		t.Errorf("resolve 2300 = %+v, 想 alias 机型:UD-2300", resolve.Matches)
	}

	// 3. 第 1 张：写别名打标签，摘掉待初筛。
	code, out := setLabels(t, w.s, w.liA, w.n1, setLabelsBody{
		Add: []string{"机型:2300", "血液线"}, Remove: []string{"待初筛"},
	})
	if code != http.StatusOK {
		t.Fatalf("别名打标签状态码 = %d", code)
	}
	if !equalStrings(out.Labels, []string{"部门:检验科", "机型:UD-2300", "产品线:血液"}) {
		t.Errorf("N1 打完的标签 = %v", out.Labels)
	}
	assertContains(t, out.Changed.AliasResolved, aliasResolution{Requested: "机型:2300", Label: "机型:UD-2300", Via: "alias"})
	assertContains(t, out.Changed.AliasResolved, aliasResolution{Requested: "血液线", Label: "产品线:血液", Via: "alias"})
	assertContains(t, out.Changed.Removed, "待初筛")

	// 4. 第 2 张缺信息：回复逐项追问 → 轮到访客，暂时离开队列。
	var asked replyBody
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+w.n2+"/replies",
		map[string]string{"body_md": "为了尽快定位，请补充：\n1. 是哪台仪器（机型）？\n2. 每次打印都乱码，还是偶尔？"}, &asked)
	if asked.WaitingOn.Party != "guest" {
		t.Fatalf("追问后 waiting_on = %+v, 想 guest", asked.WaitingOn)
	}
	if got := w.list(t, w.liA, queue); got.Total != 0 {
		t.Errorf("追问后队列应空，仍有 %v", numbers(got))
	}

	// （验收点）访客回复后又自动回到队列。
	w.at(t, "2026-10-16T09:30")
	w.guestReply(t, w.n2Key, w.n2, "机型铭牌看不清，不确定；每次打印都乱码。", "质量部 孙七")
	if got := w.list(t, w.liA, queue); !equalStrings(numbers(got), []string{w.n2}) {
		t.Errorf("访客回复后 %s 应回到队列，得到 %v", w.n2, numbers(got))
	}

	// 5/6. 未指派的开启工单。
	got = w.list(t, w.liA, "?assignee=none")
	if !equalStrings(numbers(got), []string{w.n2, w.n1}) {
		t.Errorf("未指派 = %v, 想 [%s %s]", numbers(got), w.n2, w.n1)
	}

	// 7. 把第 1 张派给李工。
	w.at(t, "2026-10-16T09:35")
	w.assign(t, w.liA, w.n1, "李工")
	if got := w.list(t, w.liA, "?assignee=none"); !equalStrings(numbers(got), []string{w.n2}) {
		t.Errorf("指派后未指派 = %v, 想 [%s]", numbers(got), w.n2)
	}
	if got := w.list(t, w.liA, "?assignee=me"); !equalStrings(numbers(got), []string{w.n1, w.q3}) {
		t.Errorf("指派后李工的查询 = %v, 想 [%s %s]（Q3 本就指派给李工）", numbers(got), w.n1, w.q3)
	}
}

// 场景 2（查重，4 步）：关键词忽略大小写、空格和连字符，E203 也能找到写成
// E-203 的已关闭工单；用别名按机型筛同一张；读老工单全文；回复引用。
func Test场景2查重(t *testing.T) {
	w := newQueryWorld(t)

	// 1. 关键词 E203，包括已关闭。
	got := w.list(t, w.wangA, "?q=E203&status=all")
	if !equalStrings(numbers(got), []string{w.n1, w.old}) {
		t.Fatalf("q=E203 = %v, 想 [N1 OLD]", numbers(got))
	}
	for _, it := range got.Items {
		if !equalStrings(it.MatchedIn, []string{"标题", "说明"}) {
			t.Errorf("%s matched_in = %v, 想 [标题 说明]", it.Number, it.MatchedIn)
		}
		if !strings.Contains(it.Snippet, "E-203") && !strings.Contains(it.Snippet, "E203") {
			t.Errorf("%s snippet = %q, 想包含命中原文", it.Number, it.Snippet)
		}
	}

	// 2. 按机型（写别名）筛已关闭。
	got = w.list(t, w.wangA, "?label=2300&status=closed")
	if !equalStrings(numbers(got), []string{w.old}) || len(got.ResolvedAliases) != 1 {
		t.Errorf("别名筛已关闭 = %v %v, 想 [OLD] 且回显别名", numbers(got), got.ResolvedAliases)
	}

	// 3. 读老工单的全文（Markdown：已关闭）。
	md := w.readMarkdownOf(t, w.wangA, w.old)
	for _, want := range []string{"# 工单 " + w.old, "已关闭（完成）", "E-203"} {
		if !strings.Contains(md, want) {
			t.Errorf("老工单 Markdown 缺少 %q", want)
		}
	}

	// 4. 回复新工单，引用老工单。
	w.at(t, "2026-10-16T09:40")
	var replied replyBody
	res := w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets/"+w.n1+"/replies",
		map[string]string{"body_md": "与 " + w.old + "（/t/" + w.old + "）现象一致：液位传感器固件问题，V3.2.2 已修复。请确认仪器的固件版本。"}, &replied)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("回复新工单状态码 = %d", res.StatusCode)
	}
	if replied.WaitingOn.Party != "guest" {
		t.Errorf("回复后 waiting_on = %+v, 想 guest（等访客确认）", replied.WaitingOn)
	}
}

// 场景 3（统计，6 步）：按维度计数、按关闭原因计数（时间范围）、检验科提的
// 按机型计数；列表按创建时间每页 3 张、跟着 next 翻页；Markdown 表格。
func Test场景3统计(t *testing.T) {
	w := newQueryWorld(t)

	// 1. 全部工单按产品线计数。
	got := w.counts(t, w.zhaoA, "?group_by="+urlEncode("产品线")+"&status=all")
	if got.Total != 7 || !equalGroups(got.Groups, []countGroup{
		{Value: "血液", Count: 3}, {Value: "(无)", Count: 3}, {Value: "尿液", Count: 1},
	}) {
		t.Errorf("按产品线 = %v（total %d）", got.Groups, got.Total)
	}

	// 2. 10 月关闭的，按关闭原因计数。
	got = w.counts(t, w.zhaoA, "?group_by=close_reason&status=closed&closed_after=2026-10-01&closed_before=2026-11-01")
	if got.Total != 2 || !equalGroups(got.Groups, []countGroup{{Value: "完成", Count: 1}, {Value: "不处理", Count: 1}}) {
		t.Errorf("10 月关闭按原因 = %v（total %d）", got.Groups, got.Total)
	}

	// 3. 检验科提的，按机型计数（N1 机型「不确定」→ (无)）。
	got = w.counts(t, w.zhaoA, "?group_by="+urlEncode("机型")+"&status=all&label="+urlEncode("部门:检验科"))
	if got.Total != 2 || !equalGroups(got.Groups, []countGroup{{Value: "UD-2300", Count: 1}, {Value: "(无)", Count: 1}}) {
		t.Errorf("检验科按机型 = %v（total %d）", got.Groups, got.Total)
	}

	// 4. 列表：按创建时间，每页 3 张。
	page1 := w.list(t, w.zhaoA, "?status=all&sort=created&limit=3")
	if !equalStrings(numbers(page1), []string{w.old, w.doc, w.wont}) {
		t.Fatalf("第 1 页 = %v", numbers(page1))
	}

	// 5. 跟着 next 翻到第 2 页。
	var page2 listBody
	w.s.Do(t, w.zhaoA, http.MethodGet, *page1.Next, nil, &page2)
	if !equalStrings(numbers(page2), []string{w.fb, w.q3, w.n1}) {
		t.Errorf("第 2 页 = %v, 想 [FB Q3 N1]", numbers(page2))
	}

	// 6. Markdown 表格。
	md := w.readListMarkdown(t, w.zhaoA, "/api/v1/tickets?status=all&format=markdown")
	if !strings.Contains(md, "# 工单列表（共 7 张，本页 7 张）") || !strings.Contains(md, "## 下一步") {
		t.Errorf("Markdown 表格缺标题或下一步：\n%s", md)
	}
}

// 场景 4（增量拉取，6 步）：记下 cursor；任何事件都会推进变更序号；
// changed_after 与其他条件可组合；没有变化时空列表、cursor 不变。
func Test场景4增量拉取(t *testing.T) {
	w := newQueryWorld(t)

	// 1. 先拿一次，记下 cursor。
	first := w.list(t, w.wangA, "?status=all&limit=1")
	if first.Count != 1 || first.Cursor <= 0 {
		t.Fatalf("第一页 count/cursor = %d/%d", first.Count, first.Cursor)
	}
	cursor := first.Cursor

	// 2.（期间）张三回复试剂批号。
	w.at(t, "2026-10-16T10:00")
	w.guestReply(t, w.fbKey, w.fb, "试剂批号 R20260930。", "张三")
	// 3.（期间）李工给 Q3 任务提问。
	w.at(t, "2026-10-16T10:01")
	reply(t, w.s, w.webLi, w.q3, "“适用机型”要细到软件版本吗？")

	// 4. 只拿变化过的（带 changed_after 时默认按变更顺序排）。
	got := w.list(t, w.wangA, "?status=all&changed_after="+fmt.Sprintf("%d", cursor))
	if !equalStrings(numbers(got), []string{w.fb, w.q3}) {
		t.Errorf("变化过的 = %v, 想 [FB Q3]（按变更顺序）", numbers(got))
	}

	// 5. 变化过、并且轮到我（王工：FB 指派人 + Q3 发起人）。
	got = w.list(t, w.wangA, "?changed_after="+fmt.Sprintf("%d", cursor)+"&waiting_on=me")
	if !equalStrings(numbers(got), []string{w.fb, w.q3}) {
		t.Errorf("变化过且轮到我 = %v, 想 [FB Q3]", numbers(got))
	}

	// 6. 用新 cursor 再拿 → 空。
	after := w.list(t, w.wangA, "?status=all&changed_after="+fmt.Sprintf("%d", got.Cursor))
	if after.Total != 0 || after.Cursor != got.Cursor {
		t.Errorf("无变化 = total %d cursor %d（应 0 / 不变 %d）", after.Total, after.Cursor, got.Cursor)
	}
}

// 场景 5（别名与报错，8 步）：访客 403、参数拼错、limit 超界、标签近似值、
// 加别名、别名冲突 409、用新别名查、字典带别名。
func Test场景5别名与报错(t *testing.T) {
	w := newQueryWorld(t)

	// 1. 访客查列表 → 403。
	res := w.s.Do(t, w.s.GuestClient(w.fbKey), http.MethodGet, "/api/v1/tickets", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("访客列表状态码 = %d, 想 403", res.StatusCode)
	}
	var e errorBody
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "members_only" {
		t.Errorf("访客列表错误码 = %q", e.Error.Code)
	}

	// 2. 参数拼错 → 422。
	res = w.s.Do(t, w.wangA, http.MethodGet, "/api/v1/tickets?lable="+urlEncode("待初筛"), nil, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("参数拼错状态码 = %d", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "unknown_parameter" || !strings.Contains(e.Error.Hint, "label") {
		t.Errorf("参数拼错 = %q hint %q", e.Error.Code, e.Error.Hint)
	}

	// 3. limit=500 → 422。
	res = w.s.Do(t, w.wangA, http.MethodGet, "/api/v1/tickets?limit=500", nil, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("limit=500 状态码 = %d", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "invalid_limit" {
		t.Errorf("limit=500 错误码 = %q", e.Error.Code)
	}

	// 4. 标签值写错 → 422 并给出近似值。
	res = w.s.Do(t, w.wangA, http.MethodGet, "/api/v1/tickets?label="+urlEncode("机型:D-2300"), nil, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("标签写错状态码 = %d", res.StatusCode)
	}
	var ex errorBodyWithExtra
	tbtest.DecodeJSON(t, res, &ex)
	if ex.Error.Code != "unknown_label_value" || len(ex.Error.Suggestions) == 0 {
		t.Errorf("标签写错 = %q suggestions %v", ex.Error.Code, ex.Error.Suggestions)
	}

	// 5. 加别名。
	res = addAlias(t, w.s, w.wangA, "机型:UD-2300", "2300机")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("加别名状态码 = %d", res.StatusCode)
	}
	var added aliasResponseBody
	tbtest.DecodeJSON(t, res, &added)
	if !equalStrings(added.Aliases, []string{"2300", "2300机"}) {
		t.Errorf("UD-2300 的别名 = %v", added.Aliases)
	}

	// 6. 把「2300」挂到 UD-1300 → 409 alias_taken。
	res = addAlias(t, w.s, w.wangA, "机型:UD-1300", "2300")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("别名冲突状态码 = %d, 想 409", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &e)
	if e.Error.Code != "alias_taken" {
		t.Errorf("别名冲突错误码 = %q, 想 alias_taken", e.Error.Code)
	}

	// 7. 用新别名查。
	got := w.list(t, w.wangA, "?label="+urlEncode("机型:2300机")+"&status=all")
	if !equalStrings(numbers(got), []string{w.fb, w.old}) {
		t.Errorf("新别名查询 = %v, 想 [FB OLD]", numbers(got))
	}
	if len(got.ResolvedAliases) != 1 || got.ResolvedAliases[0] != "2300机 → 机型:UD-2300" {
		t.Errorf("新别名回显 = %v", got.ResolvedAliases)
	}

	// 8. 机型字典（带别名）。
	res = w.s.Do(t, w.wangA, http.MethodGet, "/api/v1/labels?dimension="+urlEncode("机型"), nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("字典状态码 = %d", res.StatusCode)
	}
	var dict struct {
		Dimensions []struct {
			Name   string `json:"name"`
			Values []struct {
				Value   string   `json:"value"`
				Aliases []string `json:"aliases"`
			} `json:"values"`
		} `json:"dimensions"`
	}
	tbtest.DecodeJSON(t, res, &dict)
	if len(dict.Dimensions) != 1 || dict.Dimensions[0].Name != "机型" {
		t.Fatalf("字典 = %+v", dict.Dimensions)
	}
	found := false
	for _, v := range dict.Dimensions[0].Values {
		if v.Value == "UD-2300" {
			found = true
			if !equalStrings(v.Aliases, []string{"2300", "2300机"}) {
				t.Errorf("UD-2300 的别名 = %v", v.Aliases)
			}
		}
	}
	if !found {
		t.Error("机型字典里没有 UD-2300")
	}
}
