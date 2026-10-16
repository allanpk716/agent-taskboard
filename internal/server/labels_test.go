package server_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// setLabelsBody 是 POST /api/v1/tickets/{id}/labels 的请求体。
type setLabelsBody struct {
	Add      []string `json:"add"`
	Remove   []string `json:"remove"`
	AllowNew bool     `json:"allow_new"`
}

// setLabelsResult 是 POST /api/v1/tickets/{id}/labels 的响应。
type setLabelsResult struct {
	Labels                  []string `json:"labels"`
	CreatedDictionaryValues []string `json:"created_dictionary_values"`
	Changed                 changed  `json:"changed"`
}

// changed 是 setLabelsResult 里「这次实际改了什么」的结构。
type changed struct {
	Added         []string          `json:"added"`
	Removed       []string          `json:"removed"`
	AliasResolved []aliasResolution `json:"alias_resolved"`
}

// aliasResolution 是「写别名被换成标准值」的回执。
type aliasResolution struct {
	Requested string `json:"requested"`
	Label     string `json:"label"`
	Via       string `json:"via"`
}

// aliasResponseBody 是 POST /api/v1/labels/aliases 的响应。
type aliasResponseBody struct {
	Label   string   `json:"label"`
	Aliases []string `json:"aliases"`
}

// setLabels 发一个打标签/摘标签请求并解码响应。
func setLabels(t testing.TB, s *tbtest.Server, c *http.Client, ref string, body setLabelsBody) (int, setLabelsResult) {
	t.Helper()
	var out setLabelsResult
	res := s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+ref+"/labels", body, &out)
	return res.StatusCode, out
}

// addAlias 给某个标准值添加别名，返回原始响应。
func addAlias(t testing.TB, s *tbtest.Server, c *http.Client, label, alias string) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/labels/aliases",
		map[string]string{"label": label, "alias": alias}, nil)
}

// 成员给标准值添加别名后，用别名打标签，工单上记的是标准值（验收标准 1）。
// 别名可以写成「维度:别名」，也可以直接写别名本身（跨维度只有一个值认领它时）。
func Test写别名打标签工单上记的是标准值(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 任何成员都能给标准值添加别名。
	// 建单时打的是另一个维度的标签，机型:UD-2300 还不在单上。
	if res := createTicket(t, s, web, createTicketBody{
		Title: "校准问题", Labels: []string{"部门:研发部"},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	res := addAlias(t, s, web, "机型:UD-2300", "2300")
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("添加别名状态码 = %d, 想 201", res.StatusCode)
	}
	var alias aliasResponseBody
	tbtest.DecodeJSON(t, res, &alias)
	if alias.Label != "机型:UD-2300" || len(alias.Aliases) != 1 || alias.Aliases[0] != "2300" {
		t.Errorf("添加别名响应不对：%+v", alias)
	}

	// 用「维度:别名」打标签。
	status, out := setLabels(t, s, web, "20261016-001", setLabelsBody{Add: []string{"机型:2300"}})
	if status != http.StatusOK {
		t.Fatalf("用别名打标签状态码 = %d, 想 200", status)
	}
	if len(out.Changed.AliasResolved) != 1 ||
		out.Changed.AliasResolved[0].Requested != "机型:2300" ||
		out.Changed.AliasResolved[0].Label != "机型:UD-2300" {
		t.Errorf("changed.alias_resolved 不对：%+v", out.Changed.AliasResolved)
	}
	assertContains(t, out.Changed.Added, "机型:UD-2300")
	for _, l := range out.Labels {
		if l == "机型:2300" {
			t.Errorf("工单标签里出现了别名写法 %q：%v", l, out.Labels)
		}
	}
	assertContains(t, out.Labels, "机型:UD-2300")

	// 用裸别名（不带维度）打标签：跨维度只有一个值认领它时换成标准值。
	if res := addAlias(t, s, web, "机型:US-1680", "1680"); res.StatusCode != http.StatusCreated {
		t.Fatalf("添加第二个别名状态码 = %d", res.StatusCode)
	}
	status, out = setLabels(t, s, web, "20261016-001", setLabelsBody{Add: []string{"1680"}})
	if status != http.StatusOK {
		t.Fatalf("用裸别名打标签状态码 = %d, 想 200", status)
	}
	assertContains(t, out.Labels, "机型:US-1680")
	assertContains(t, out.Changed.Added, "机型:US-1680")

	// 工单读取接口核对：标签是标准值，且同一维度可以有多个值。
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &got)
	assertContains(t, got.Labels, "机型:UD-2300")
	assertContains(t, got.Labels, "机型:US-1680")

	// 标签增删记入时间线：label_added 事件带 label 字段和作者。
	if len(got.Timeline) != 3 {
		t.Fatalf("时间线有 %d 条, 想 3（created + 两次 label_added）", len(got.Timeline))
	}
	for _, ev := range got.Timeline[1:] {
		if ev.Type != "label_added" {
			t.Errorf("事件类型 = %q, 想 label_added", ev.Type)
		}
		if ev.Label == "" {
			t.Errorf("label_added 事件缺 label 字段：%+v", ev)
		}
		if ev.Actor.Member.Name != "陈工" || ev.Actor.Agent != nil {
			t.Errorf("事件作者不对：%+v", ev.Actor)
		}
		if ev.Change <= got.Timeline[0].Change {
			t.Errorf("label_added 事件的 change = %d 应大于 created 的 %d（全局变更序号推进）", ev.Change, got.Timeline[0].Change)
		}
	}
}

// 两个标准值抢同一个别名时，第二个返回 409 alias_taken（验收标准 3）；
// 同一个值重复添加自己的别名返回 409 alias_exists。
func Test两个标准值抢同一个别名返回aliasTaken(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	if res := addAlias(t, s, web, "机型:UD-2300", "2300"); res.StatusCode != http.StatusCreated {
		t.Fatalf("第一个别名状态码 = %d, 想 201", res.StatusCode)
	}

	res := addAlias(t, s, web, "机型:UD-1290", "2300")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("第二个别名状态码 = %d, 想 409", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "alias_taken" {
		t.Errorf("错误码 = %q, 想 alias_taken", got.Error.Code)
	}

	// 已是该值自己的别名 → 409 alias_exists。
	res = addAlias(t, s, web, "机型:UD-2300", "2300")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("重复别名状态码 = %d, 想 409", res.StatusCode)
	}
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "alias_exists" {
		t.Errorf("错误码 = %q, 想 alias_exists", got.Error.Code)
	}

	// 忽略大小写和连字符的写法也算占用（norm 相同）。
	res = addAlias(t, s, web, "机型:UD-1290", "23 00")
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("norm 相同的别名状态码 = %d, 想 409", res.StatusCode)
	}
}

// 别名只能挂在字典中的「维度:值」标准值上：不存在的值、不带维度的标签都返回 422。
func Test别名只能挂在字典中的标准值上(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	for name, body := range map[string]map[string]string{
		"不存在的值":  {"label": "机型:不存在", "alias": "x"},
		"不存在的维度": {"label": "颜色:红", "alias": "x"},
		"不带维度":   {"label": "待初筛", "alias": "x"},
	} {
		t.Run(name, func(t *testing.T) {
			res := s.Do(t, web, http.MethodPost, "/api/v1/labels/aliases", body, nil)
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "unknown_label_value" {
				t.Errorf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
			}
		})
	}

	// 别名为空 → 422 alias_required。
	res := s.Do(t, web, http.MethodPost, "/api/v1/labels/aliases",
		map[string]string{"label": "机型:UD-2300", "alias": ""}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("空别名状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "alias_required" {
		t.Errorf("错误码 = %q, 想 alias_required", got.Error.Code)
	}
}

// 值不在字典中时返回 422 unknown_label_value 并附 suggestions；
// 带 allow_new 后新建成功（验收标准 2）。新维度仍然只能由管理员添加。
func Test值不在字典时给suggestions带allowNew后新建(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	if res := createTicket(t, s, web, createTicketBody{
		Title: "整理机型对照表", Labels: []string{"机型:UD-2300"},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}

	// 不在字典中：422 + suggestions（规范化后互相包含的近似值）。
	status := setLabelsForError(t, s, web, "20261016-001", setLabelsBody{Add: []string{"机型:UD-23"}})
	var got errorBodyWithExtra
	tbtest.DecodeJSON(t, status, &got)
	if status.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("状态码 = %d, 想 422", status.StatusCode)
	}
	if got.Error.Code != "unknown_label_value" {
		t.Errorf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
	}
	assertContains(t, got.Error.Suggestions, "机型:UD-2300")

	// 不带维度的标签同样先拒绝、给 suggestions。
	status = setLabelsForError(t, s, web, "20261016-001", setLabelsBody{Add: []string{"待复测"}})
	tbtest.DecodeJSON(t, status, &got)
	if status.StatusCode != http.StatusUnprocessableEntity || got.Error.Code != "unknown_label_value" {
		t.Fatalf("不带维度标签状态码/错误码 = %d/%q, 想 422/unknown_label_value", status.StatusCode, got.Error.Code)
	}

	// 带 allow_new：维度值与不带维度的标签都新建成功。
	code, out := setLabels(t, s, web, "20261016-001", setLabelsBody{
		Add:      []string{"机型:BH-9999", "待复测"},
		AllowNew: true,
	})
	if code != http.StatusOK {
		t.Fatalf("allow_new 打标签状态码 = %d, 想 200", code)
	}
	assertContains(t, out.Labels, "机型:BH-9999")
	assertContains(t, out.Labels, "待复测")
	assertContains(t, out.CreatedDictionaryValues, "机型:BH-9999")
	assertContains(t, out.CreatedDictionaryValues, "待复测")

	// 新建的值进了字典：第二张工单直接建单就能用。
	if res := createTicket(t, s, web, createTicketBody{
		Title: "另一件事", Labels: []string{"机型:BH-9999", "待复测"},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("用新建值建单状态码 = %d, 想 201", res.StatusCode)
	}

	// 新维度只能由管理员添加：allow_new 也造不出新维度。
	status = setLabelsForError(t, s, web, "20261016-001", setLabelsBody{
		Add: []string{"颜色:红"}, AllowNew: true,
	})
	tbtest.DecodeJSON(t, status, &got)
	if status.StatusCode != http.StatusUnprocessableEntity || got.Error.Code != "unknown_dimension" {
		t.Fatalf("新维度状态码/错误码 = %d/%q, 想 422/unknown_dimension", status.StatusCode, got.Error.Code)
	}

	// 维度:* 是查询用的通配，不能打到工单上。
	status = setLabelsForError(t, s, web, "20261016-001", setLabelsBody{Add: []string{"机型:*"}})
	tbtest.DecodeJSON(t, status, &got)
	if status.StatusCode != http.StatusUnprocessableEntity || got.Error.Code != "wildcard_not_allowed" {
		t.Fatalf("通配状态码/错误码 = %d/%q, 想 422/wildcard_not_allowed", status.StatusCode, got.Error.Code)
	}
}

// setLabelsForError 发一个打标签请求，返回原始响应（断言错误时用；响应体留给断言方读）。
func setLabelsForError(t testing.TB, s *tbtest.Server, c *http.Client, ref string, body setLabelsBody) *http.Response {
	t.Helper()
	return s.Do(t, c, http.MethodPost, "/api/v1/tickets/"+ref+"/labels", body, nil)
}

// 摘标签：摘掉的标签记 label_removed 事件；摘一个工单上没有的标签是幂等成功；
// 摘一个字典里不存在的标签返回 422。
func Test摘标签记入时间线摘不存在的幂等成功(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	if res := createTicket(t, s, web, createTicketBody{
		Title: "校准问题", Labels: []string{"机型:UD-2300", "机型:US-1680"},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}

	// 摘掉一个（写别名同样换成标准值）。
	if res := addAlias(t, s, web, "机型:US-1680", "1680"); res.StatusCode != http.StatusCreated {
		t.Fatalf("添加别名状态码 = %d", res.StatusCode)
	}
	code, out := setLabels(t, s, web, "20261016-001", setLabelsBody{Remove: []string{"1680"}})
	if code != http.StatusOK {
		t.Fatalf("摘标签状态码 = %d, 想 200", code)
	}
	assertContains(t, out.Changed.Removed, "机型:US-1680")
	for _, l := range out.Labels {
		if l == "机型:US-1680" {
			t.Errorf("机型:US-1680 应已被摘掉：%v", out.Labels)
		}
	}

	// 时间线：created + label_removed。
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &got)
	if len(got.Timeline) != 2 || got.Timeline[1].Type != "label_removed" || got.Timeline[1].Label != "机型:US-1680" {
		t.Fatalf("时间线不对：%+v", got.Timeline)
	}

	// 摘一个工单上没有的标签：幂等成功，不新增事件。
	code, out = setLabels(t, s, web, "20261016-001", setLabelsBody{Remove: []string{"机型:US-1680"}})
	if code != http.StatusOK {
		t.Fatalf("重复摘标签状态码 = %d, 想 200（幂等）", code)
	}
	if len(out.Changed.Removed) != 0 {
		t.Errorf("重复摘标签不应有 removed：%+v", out.Changed)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &got)
	if len(got.Timeline) != 2 {
		t.Errorf("幂等摘标签后时间线多了事件：%+v", got.Timeline)
	}

	// 摘一个字典里不存在的标签：422 unknown_label_value。
	status := setLabelsForError(t, s, web, "20261016-001", setLabelsBody{Remove: []string{"机型:不存在"}})
	var errBody errorBodyWithExtra
	tbtest.DecodeJSON(t, status, &errBody)
	if status.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "unknown_label_value" {
		t.Fatalf("摘未知标签状态码/错误码 = %d/%q, 想 422/unknown_label_value", status.StatusCode, errBody.Error.Code)
	}
}

// 未登录不能打标签；不存在的工单返回 404。
func Test未登录打标签返回401不存在工单返回404(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	if res := createTicket(t, s, web, createTicketBody{
		Title: "校准问题", Labels: []string{"机型:UD-2300"},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}

	res := s.Do(t, s.NewClient(), http.MethodPost, "/api/v1/tickets/20261016-001/labels",
		setLabelsBody{Add: []string{"机型:US-1680"}}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("未登录打标签状态码 = %d, 想 401", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/tickets/999/labels",
		setLabelsBody{Add: []string{"机型:US-1680"}}, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在工单状态码 = %d, 想 404", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "ticket_not_found" {
		t.Errorf("错误码 = %q, 想 ticket_not_found", got.Error.Code)
	}
}

// labelsResponseBody 是 GET /api/v1/labels 的响应结构。
type labelsResponseBody struct {
	Dimensions []struct {
		Name   string `json:"name"`
		Values []struct {
			Value   string   `json:"value"`
			Label   string   `json:"label"`
			Aliases []string `json:"aliases"`
		} `json:"values"`
	} `json:"dimensions"`
	Plain *[]string `json:"plain"`
}

// resolveResponseBody 是 GET /api/v1/labels/resolve 的响应结构。
type resolveResponseBody struct {
	Q       string `json:"q"`
	Matches []struct {
		Label string  `json:"label"`
		Via   string  `json:"via"`
		Alias *string `json:"alias"`
	} `json:"matches"`
	Hint string `json:"hint"`
}

// getLabels 拉取字典列表。
func getLabels(t testing.TB, s *tbtest.Server, c *http.Client, query string) (int, labelsResponseBody) {
	t.Helper()
	var out labelsResponseBody
	res := s.Do(t, c, http.MethodGet, "/api/v1/labels"+query, nil, &out)
	return res.StatusCode, out
}

// 字典列表：预置七个维度；每个值带 aliases；不带维度的标签单独列出。
// 指定 dimension 时只列该维度并省略 plain；未知维度 422。
func Test字典列表带别名并另列不带维度标签(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	code, dict := getLabels(t, s, web, "")
	if code != http.StatusOK {
		t.Fatalf("字典列表状态码 = %d, 想 200", code)
	}
	if len(dict.Dimensions) != 7 {
		t.Fatalf("预置维度有 %d 个, 想 7（产品线、系统、机型、部门、算法、立项、子项目）", len(dict.Dimensions))
	}
	if dict.Plain == nil {
		t.Fatal("没有 dimension 时应列出 plain（可以为空数组）")
	}
	// 机型 14 个种子值，别名初始为空。
	var models []string
	for _, d := range dict.Dimensions {
		if d.Name == "机型" {
			for _, v := range d.Values {
				models = append(models, v.Value)
				if v.Aliases == nil {
					t.Errorf("值 %s 的 aliases 为 nil，应为空数组", v.Value)
				}
				if v.Label != "机型:"+v.Value {
					t.Errorf("值的 label 字段不对：%q", v.Label)
				}
			}
		}
	}
	assertContains(t, models, "UD-2300")
	assertContains(t, models, "BM-10vet")
	if len(models) != 14 {
		t.Errorf("机型有 %d 个种子值, 想 14", len(models))
	}

	// 加别名后列表里能看到。
	addAlias(t, s, web, "机型:UD-2300", "2300")
	code, dict = getLabels(t, s, web, "")
	if code != http.StatusOK {
		t.Fatalf("再次拉字典状态码 = %d", code)
	}
	for _, d := range dict.Dimensions {
		if d.Name != "机型" {
			continue
		}
		for _, v := range d.Values {
			if v.Value == "UD-2300" {
				assertContains(t, v.Aliases, "2300")
			}
		}
	}

	// 指定 dimension：只列该维度，省略 plain。
	code, dict = getLabels(t, s, web, "?dimension=部门")
	if code != http.StatusOK {
		t.Fatalf("按维度拉字典状态码 = %d, 想 200", code)
	}
	if len(dict.Dimensions) != 1 || dict.Dimensions[0].Name != "部门" {
		t.Fatalf("?dimension=部门 应只列部门：%+v", dict.Dimensions)
	}
	if len(dict.Dimensions[0].Values) != 13 {
		t.Errorf("部门应有 13 个种子值，拿到 %d 个", len(dict.Dimensions[0].Values))
	}
	if dict.Plain != nil {
		t.Errorf("指定 dimension 时应省略 plain：%v", *dict.Plain)
	}

	// 未知维度：422 unknown_dimension。
	code, _ = getLabels(t, s, web, "?dimension=颜色")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("未知维度状态码 = %d, 想 422", code)
	}

	// 未登录：401。
	code, _ = getLabels(t, s, s.NewClient(), "")
	if code != http.StatusUnauthorized {
		t.Fatalf("未登录拉字典状态码 = %d, 想 401", code)
	}
}

// resolve 把俗称换成标准值并注明匹配方式：exact / alias / fuzzy，
// 无命中时给 hint 指向 allow_new 和加别名；缺 q 或未知维度 422。
func TestResolve注明exactaliasfuzzy匹配方式(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	addAlias(t, s, web, "机型:UD-2300", "2300")

	resolve := func(query string) (int, resolveResponseBody) {
		t.Helper()
		var out resolveResponseBody
		res := s.Do(t, web, http.MethodGet, "/api/v1/labels/resolve"+query, nil, &out)
		return res.StatusCode, out
	}

	// 精确值（忽略大小写、空格、连字符也算 exact）。
	code, out := resolve("?q=" + url.QueryEscape("机型:ud 2300"))
	if code != http.StatusOK {
		t.Fatalf("exact 状态码 = %d, 想 200", code)
	}
	if len(out.Matches) == 0 || out.Matches[0].Label != "机型:UD-2300" || out.Matches[0].Via != "exact" {
		t.Fatalf("exact 匹配不对：%+v", out.Matches)
	}

	// 别名。
	code, out = resolve("?q=2300")
	if code != http.StatusOK {
		t.Fatalf("alias 状态码 = %d", code)
	}
	if len(out.Matches) == 0 || out.Matches[0].Label != "机型:UD-2300" || out.Matches[0].Via != "alias" {
		t.Fatalf("alias 匹配不对：%+v", out.Matches)
	}
	if out.Matches[0].Alias == nil || *out.Matches[0].Alias != "2300" {
		t.Errorf("alias 匹配应注明命中的别名：%+v", out.Matches[0])
	}

	// 模糊：规范化后互相包含。
	code, out = resolve("?q=" + url.QueryEscape("机型:US-168"))
	if code != http.StatusOK {
		t.Fatalf("fuzzy 状态码 = %d", code)
	}
	if len(out.Matches) == 0 {
		t.Fatalf("fuzzy 应有候选：%+v", out)
	}
	found := false
	for _, m := range out.Matches {
		if m.Label == "机型:US-1680" && m.Via == "fuzzy" {
			found = true
		}
	}
	if !found {
		t.Errorf("fuzzy 候选里没有 机型:US-1680：%+v", out.Matches)
	}

	// 维度限定：别的维度的别名不再命中。
	code, out = resolve("?q=2300&dimension=部门")
	if code != http.StatusOK {
		t.Fatalf("维度限定状态码 = %d", code)
	}
	if len(out.Matches) != 0 {
		t.Errorf("部门维度不应命中 2300：%+v", out.Matches)
	}

	// 无命中：hint 指向 allow_new / 加别名。
	code, out = resolve("?q=" + url.QueryEscape("机型:完全不存在"))
	if code != http.StatusOK || len(out.Matches) != 0 {
		t.Fatalf("无命中状态码/候选 = %d/%d, 想 200/0", code, len(out.Matches))
	}
	if out.Hint == "" {
		t.Error("无命中时应给 hint")
	}

	// 缺 q：422 q_required。
	res := s.Do(t, web, http.MethodGet, "/api/v1/labels/resolve", nil, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("缺 q 状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "q_required" {
		t.Errorf("错误码 = %q, 想 q_required", got.Error.Code)
	}

	// 未知维度：422 unknown_dimension。
	code, _ = resolve("?q=x&dimension=颜色")
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("未知维度状态码 = %d, 想 422", code)
	}
}

// 非管理员执行停用、改名、合并、加维度、改别名指向时被拒绝（验收标准 5）；
// 管理员可以做这些治理操作。
func Test非管理员执行停用改名合并加维度被拒绝(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	// 成员可以浏览字典、加别名（下面有独立测试），治理操作不行；每个子用例用互不干扰的标签。
	addAlias(t, s, member, "机型:UD-1290", "1290")
	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]string
	}{
		{"加维度", http.MethodPost, "/api/v1/labels/dimensions", map[string]string{"name": "颜色"}},
		{"停用", http.MethodPost, "/api/v1/labels/deactivate", map[string]string{"label": "机型:RM-11"}},
		{"改名", http.MethodPost, "/api/v1/labels/rename", map[string]string{"label": "机型:SL-1000", "to": "机型:SL-1000X"}},
		{"合并", http.MethodPost, "/api/v1/labels/merge", map[string]string{"from": "机型:US-1000", "into": "机型:US-500"}},
		{"改别名指向", http.MethodPatch, "/api/v1/labels/aliases", map[string]string{"alias": "1290", "label": "机型:UD-2300"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := s.Do(t, member, tc.method, tc.path, tc.body, nil)
			if res.StatusCode != http.StatusForbidden {
				t.Fatalf("状态码 = %d, 想 403", res.StatusCode)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "admin_required" {
				t.Errorf("错误码 = %q, 想 admin_required", got.Error.Code)
			}
			// 管理员可以做。
			res = s.Do(t, admin, tc.method, tc.path, tc.body, nil)
			if res.StatusCode != http.StatusCreated && res.StatusCode != http.StatusOK {
				t.Errorf("管理员做 %s 状态码 = %d, 想 201/200：%v", tc.name, res.StatusCode, res)
			}
		})
	}
}

// 管理员合并两个值后，历史工单上只剩合并后的值（验收标准 4）：
// 同时挂着两个值的工单去重；from 的别名改指向 into；from 从字典里下架（停用）。
func Test合并两个值后历史工单只剩合并后的值(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	// from 认领一个别名，合并后它应改指向 into。
	if res := addAlias(t, s, member, "机型:US-1000", "1000"); res.StatusCode != http.StatusCreated {
		t.Fatalf("添加别名状态码 = %d", res.StatusCode)
	}
	// 第一张：只有 from；第二张：from + into（去重）；第三张：from + 别的维度。
	mustCreate(t, s, member, "US-1000", []string{"机型:US-1000"})
	mustCreate(t, s, member, "两个都有", []string{"机型:US-1000", "机型:US-500"})
	mustCreate(t, s, member, "跨维度", []string{"机型:US-1000", "部门:研发部"})

	var mergeRes struct {
		From           string `json:"from"`
		Into           string `json:"into"`
		TicketsUpdated int64  `json:"tickets_updated"`
	}
	res := s.Do(t, admin, http.MethodPost, "/api/v1/labels/merge",
		map[string]string{"from": "机型:US-1000", "into": "机型:US-500"}, &mergeRes)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("合并状态码 = %d, 想 200", res.StatusCode)
	}
	if mergeRes.TicketsUpdated != 3 {
		t.Errorf("tickets_updated = %d, 想 3", mergeRes.TicketsUpdated)
	}

	// 历史工单上的标签被改写：只剩合并后的值，别的不动。
	var t1, t2, t3 ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/1", nil, &t1)
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/2", nil, &t2)
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/3", nil, &t3)
	assertContains(t, t1.Labels, "机型:US-500")
	if len(t1.Labels) != 1 {
		t.Errorf("第一张应只剩 机型:US-500：%v", t1.Labels)
	}
	if len(t2.Labels) != 1 || t2.Labels[0] != "机型:US-500" {
		t.Errorf("同时挂两个值的工单应去重成一个：%v", t2.Labels)
	}
	assertContains(t, t3.Labels, "机型:US-500")
	assertContains(t, t3.Labels, "部门:研发部")

	// from 从字典下架：列表不再列出，新打报 label_deactivated，摘掉仍可以。
	code, dict := getLabels(t, s, admin, "?dimension=机型")
	if code != http.StatusOK {
		t.Fatalf("拉字典状态码 = %d", code)
	}
	for _, v := range dict.Dimensions[0].Values {
		if v.Value == "US-1000" {
			t.Errorf("合并后的 from 不应再出现在字典列表里：%v", v)
		}
	}
	status := setLabelsForError(t, s, member, "1", setLabelsBody{Add: []string{"机型:US-1000"}})
	var errBody errorBodyWithExtra
	tbtest.DecodeJSON(t, status, &errBody)
	if status.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "label_deactivated" {
		t.Fatalf("新打合并掉的值状态码/错误码 = %d/%q, 想 422/label_deactivated", status.StatusCode, errBody.Error.Code)
	}
	if code, out := setLabels(t, s, member, "1", setLabelsBody{Remove: []string{"机型:US-500"}}); code != http.StatusOK {
		t.Fatalf("摘掉合并后的值状态码 = %d, 想 200", code)
	} else if len(out.Changed.Removed) != 1 {
		t.Errorf("removed = %v, 想 [机型:US-500]", out.Changed.Removed)
	}

	// 别名改指向 into。
	var resolved resolveResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/labels/resolve?q=1000", nil, &resolved)
	if len(resolved.Matches) == 0 || resolved.Matches[0].Label != "机型:US-500" || resolved.Matches[0].Via != "alias" {
		t.Errorf("合并后别名应指向 into：%+v", resolved.Matches)
	}

	// 合并的目标不存在：422 unknown_label_value。
	res = s.Do(t, admin, http.MethodPost, "/api/v1/labels/merge",
		map[string]string{"from": "机型:US-500", "into": "机型:没有这个"}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("合并到不存在的值状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "unknown_label_value" {
		t.Errorf("错误码 = %q, 想 unknown_label_value", got.Error.Code)
	}
}

// mustCreate 建一张必然成功的工单。
func mustCreate(t testing.TB, s *tbtest.Server, c *http.Client, title string, labels []string) {
	t.Helper()
	if res := createTicket(t, s, c, createTicketBody{Title: title, Labels: labels}); res.StatusCode != http.StatusCreated {
		t.Fatalf("建单 %s 状态码 = %d, 想 201", title, res.StatusCode)
	}
}

// 管理员改名牌：字典更新、历史工单上的标签一并改写、别名跟着挪过去。
// 改成已存在的值返回 409 value_taken（提示改用合并）。
func Test改名改写历史工单并挪走别名(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	addAlias(t, s, member, "机型:RM-11", "11")
	mustCreate(t, s, member, "改名前", []string{"机型:RM-11"})

	res := s.Do(t, admin, http.MethodPost, "/api/v1/labels/rename",
		map[string]string{"label": "机型:RM-11", "to": "机型:RM-12"}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("改名状态码 = %d, 想 200", res.StatusCode)
	}

	var got ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/1", nil, &got)
	if len(got.Labels) != 1 || got.Labels[0] != "机型:RM-12" {
		t.Errorf("改名后历史工单标签应改写：%v", got.Labels)
	}
	// 别名跟着挪：resolve 11 命中新值。
	var resolved resolveResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/labels/resolve?q=11", nil, &resolved)
	if len(resolved.Matches) == 0 || resolved.Matches[0].Label != "机型:RM-12" || resolved.Matches[0].Via != "alias" {
		t.Errorf("别名应跟着改名挪到新值：%+v", resolved.Matches)
	}
	// 新值可以继续打。
	if code, out := setLabels(t, s, member, "1", setLabelsBody{Add: []string{"机型:RM-12"}}); code != http.StatusOK {
		t.Fatalf("用新值打标签状态码 = %d", code)
	} else if len(out.Changed.Added) != 0 {
		t.Errorf("已在单上的新值不应重复打：%v", out.Changed.Added)
	}

	// 改成已存在的值：409 value_taken。
	res = s.Do(t, admin, http.MethodPost, "/api/v1/labels/rename",
		map[string]string{"label": "机型:RM-12", "to": "机型:7300"}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("改成已存在值状态码 = %d, 想 409", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "value_taken" {
		t.Errorf("错误码 = %q, 想 value_taken", errBody.Error.Code)
	}
	// 维度不一致：422 dimension_mismatch。
	res = s.Do(t, admin, http.MethodPost, "/api/v1/labels/rename",
		map[string]string{"label": "机型:RM-12", "to": "部门:RM-12"}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("跨维度改名状态码 = %d, 想 422", res.StatusCode)
	}
}

// 管理员改别名的指向：别名换一个标准值认领；不存在的别名 404。
func Test管理员改别名的指向(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	addAlias(t, s, member, "机型:UD-2300", "2300")
	addAlias(t, s, member, "机型:UD-1290", "1290")

	var out aliasResponseBody
	res := s.Do(t, admin, http.MethodPatch, "/api/v1/labels/aliases",
		map[string]string{"alias": "1290", "label": "机型:UD-2300"}, &out)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("改指向状态码 = %d, 想 200", res.StatusCode)
	}
	if out.Label != "机型:UD-2300" || len(out.Aliases) != 2 {
		t.Errorf("改指向响应不对：%+v", out)
	}
	// resolve：1290 改指 机型:UD-2300。
	var resolved resolveResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/labels/resolve?q=1290", nil, &resolved)
	if len(resolved.Matches) == 0 || resolved.Matches[0].Label != "机型:UD-2300" || resolved.Matches[0].Via != "alias" {
		t.Errorf("1290 应改指 机型:UD-2300：%+v", resolved.Matches)
	}

	// 不存在的别名：404 alias_not_found。
	res = s.Do(t, admin, http.MethodPatch, "/api/v1/labels/aliases",
		map[string]string{"alias": "没有这个", "label": "机型:UD-2300"}, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("改不存在的别名状态码 = %d, 想 404", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "alias_not_found" {
		t.Errorf("错误码 = %q, 想 alias_not_found", got.Error.Code)
	}
}

// 管理员停用标签值：历史工单保留，建单与打标签都被拒绝，字典不再列出；
// 不带维度的标签同样可以停用。管理员加新维度后成员可以用 allow_new 打它的值。
func Test停用与加维度(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	mustCreate(t, s, member, "带着将被停用的值", []string{"机型:RM-11"})

	// 停用；重复停用幂等。
	for i := 0; i < 2; i++ {
		res := s.Do(t, admin, http.MethodPost, "/api/v1/labels/deactivate",
			map[string]string{"label": "机型:RM-11"}, nil)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("第 %d 次停用状态码 = %d, 想 200", i+1, res.StatusCode)
		}
	}

	// 历史工单保留该值。
	var got ticketResponseBody
	s.Do(t, admin, http.MethodGet, "/api/v1/tickets/1", nil, &got)
	assertContains(t, got.Labels, "机型:RM-11")
	// 建单与打标签被拒。
	res := createTicket(t, s, member, createTicketBody{Title: "再建", Labels: []string{"机型:RM-11"}})
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("用停用值建单状态码 = %d, 想 422", res.StatusCode)
	}
	var errBody errorBodyWithExtra
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "label_deactivated" {
		t.Errorf("错误码 = %q, 想 label_deactivated", errBody.Error.Code)
	}
	status := setLabelsForError(t, s, member, "1", setLabelsBody{Add: []string{"机型:RM-11"}})
	tbtest.DecodeJSON(t, status, &errBody)
	if status.StatusCode != http.StatusUnprocessableEntity || errBody.Error.Code != "label_deactivated" {
		t.Fatalf("打停用值状态码/错误码 = %d/%q", status.StatusCode, errBody.Error.Code)
	}
	// 摘掉仍可以（工单上还带着）。
	if code, out := setLabels(t, s, member, "1", setLabelsBody{Remove: []string{"机型:RM-11"}}); code != http.StatusOK {
		t.Fatalf("摘停用值状态码 = %d, 想 200", code)
	} else if len(out.Changed.Removed) != 1 {
		t.Errorf("removed = %v", out.Changed.Removed)
	}

	// 不带维度的标签也能停用，停用后不再列出。
	setLabels(t, s, member, "1", setLabelsBody{Add: []string{"待复测"}, AllowNew: true})
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/labels/deactivate",
		map[string]string{"label": "待复测"}, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("停用不带维度标签状态码 = %d", res.StatusCode)
	}
	code, dict := getLabels(t, s, admin, "")
	if code != http.StatusOK {
		t.Fatalf("拉字典状态码 = %d", code)
	}
	for _, p := range *dict.Plain {
		if p == "待复测" {
			t.Errorf("停用的不带维度标签不应再列出：%v", *dict.Plain)
		}
	}

	// 管理员加新维度：成员可以 allow_new 打它的值；重复加 409 dimension_taken。
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/labels/dimensions",
		map[string]string{"name": "颜色"}, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("加维度状态码 = %d, 想 201", res.StatusCode)
	}
	if code, out := setLabels(t, s, member, "1", setLabelsBody{Add: []string{"颜色:红"}, AllowNew: true}); code != http.StatusOK {
		t.Fatalf("新维度打值状态码 = %d, 想 200", code)
	} else {
		assertContains(t, out.Labels, "颜色:红")
		assertContains(t, out.CreatedDictionaryValues, "颜色:红")
	}
	res = s.Do(t, admin, http.MethodPost, "/api/v1/labels/dimensions",
		map[string]string{"name": "颜色"}, nil)
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("重复加维度状态码 = %d, 想 409", res.StatusCode)
	}
	var dup errorBody
	tbtest.DecodeJSON(t, res, &dup)
	if dup.Error.Code != "dimension_taken" {
		t.Errorf("错误码 = %q, 想 dimension_taken", dup.Error.Code)
	}
}

// 标签事件在 Markdown 视图里有中文文案；成员 Agent 打标签时事件署名是具名 Agent
//（ADR 0003）；不带维度的标签在 resolve 里按 exact 命中。
func Test标签事件的Markdown文案与Agent署名(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)

	if res := createTicket(t, s, web, createTicketBody{
		Title: "校准问题", Labels: []string{"部门:研发部"},
	}); res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	// 成员 Agent 打一个不带维度的标签（allow_new 新建）。
	if code, _ := setLabels(t, s, agent, "20261016-001", setLabelsBody{
		Add: []string{"待初筛"}, AllowNew: true,
	}); code != http.StatusOK {
		t.Fatalf("Agent 打标签状态码 = %d, 想 200", code)
	}

	res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001?format=markdown", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("Markdown 状态码 = %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	md := string(body)
	for _, want := range []string{
		"打上标签 待初筛",
		"陈工 的 Agent · 笔记本 打上标签 待初筛",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown 缺少 %q：\n%s", want, md)
		}
	}

	// 事件作者署名是具名 Agent。
	var got ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001", nil, &got)
	if len(got.Timeline) != 2 || got.Timeline[1].Type != "label_added" {
		t.Fatalf("时间线不对：%+v", got.Timeline)
	}
	if got.Timeline[1].Actor.Agent == nil || got.Timeline[1].Actor.Agent.Name != "笔记本" {
		t.Errorf("label_added 事件的作者应是具名 Agent：%+v", got.Timeline[1].Actor)
	}

	// 摘掉后 Markdown 写「摘掉标签」。
	setLabels(t, s, web, "20261016-001", setLabelsBody{Remove: []string{"待初筛"}})
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/20261016-001?format=markdown", nil, nil)
	body, _ = io.ReadAll(res.Body)
	if !strings.Contains(string(body), "摘掉标签 待初筛") {
		t.Errorf("Markdown 缺少「摘掉标签 待初筛」：\n%s", string(body))
	}

	// 不带维度的标签在 resolve 里按 exact 命中。
	var resolved resolveResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/labels/resolve?q="+url.QueryEscape("待初筛"), nil, &resolved)
	if len(resolved.Matches) == 0 || resolved.Matches[0].Label != "待初筛" || resolved.Matches[0].Via != "exact" {
		t.Errorf("不带维度的标签应按 exact 命中：%+v", resolved.Matches)
	}
}

// 两个不同维度的值认领同一个裸别名时多义不换：打标签报 422 并给两个候选
//（ticket-query 原型 resolveLabel 的语义）。
func Test裸别名多义不换(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	addAlias(t, s, web, "机型:UD-2300", "共用")
	addAlias(t, s, web, "部门:研发部", "共用")
	mustCreate(t, s, web, "多义别名", []string{"部门:研发部"})

	// resolve 能列出两个候选。
	var resolved resolveResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/labels/resolve?q="+url.QueryEscape("共用"), nil, &resolved)
	if len(resolved.Matches) != 2 {
		t.Fatalf("多义别名应有两个候选：%+v", resolved.Matches)
	}

	// 打标签不换：422 + suggestions 列出两个标准值（带 allow_new 也不行）。
	for _, allowNew := range []bool{false, true} {
		status := setLabelsForError(t, s, web, "1", setLabelsBody{Add: []string{"共用"}, AllowNew: allowNew})
		var got errorBodyWithExtra
		tbtest.DecodeJSON(t, status, &got)
		if status.StatusCode != http.StatusUnprocessableEntity || got.Error.Code != "unknown_label_value" {
			t.Fatalf("allow_new=%v 时状态码/错误码 = %d/%q, 想 422/unknown_label_value", allowNew, status.StatusCode, got.Error.Code)
		}
		assertContains(t, got.Error.Suggestions, "机型:UD-2300")
		assertContains(t, got.Error.Suggestions, "部门:研发部")
	}

	// 写成「维度:别名」就无歧义了。
	if code, out := setLabels(t, s, web, "1", setLabelsBody{Add: []string{"机型:共用"}}); code != http.StatusOK {
		t.Fatalf("维度限定的别名状态码 = %d, 想 200", code)
	} else {
		assertContains(t, out.Labels, "机型:UD-2300")
	}
}

// 合并后摘掉旧写法是幂等成功（工单上已经没有了）。
func Test合并后摘掉旧写法幂等成功(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")

	mustCreate(t, s, member, "将被合并", []string{"机型:US-1000", "机型:US-500"})
	res := s.Do(t, admin, http.MethodPost, "/api/v1/labels/merge",
		map[string]string{"from": "机型:US-1000", "into": "机型:US-500"}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("合并状态码 = %d", res.StatusCode)
	}
	if code, out := setLabels(t, s, member, "1", setLabelsBody{Remove: []string{"机型:US-1000"}}); code != http.StatusOK {
		t.Fatalf("摘掉合并掉的旧写法状态码 = %d, 想 200", code)
	} else if len(out.Changed.Removed) != 0 {
		t.Errorf("旧写法已不在单上，不应有 removed：%v", out.Changed.Removed)
	}
}
