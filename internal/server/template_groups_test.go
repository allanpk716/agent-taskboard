package server_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/config"
	"github.com/allanpk716/agent-taskboard/internal/server"
	"github.com/allanpk716/agent-taskboard/internal/store"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
	"github.com/allanpk716/agent-taskboard/internal/template"
	"github.com/allanpk716/agent-taskboard/internal/templates"
)

// 一份带分组的自建模板：两组、组副标题、给 Agent 的说明夹在两组之间。
const groupedTemplate = `---
名称: 分组巡检
类型: 任务
---
# 巡检情况
时间、地点和结论
## 巡检哪条产品线？ {维度:产品线 必填}
从字典里选。
# 后续处理
怎么跟进
## 结论如何？ {单选: 正常 | 异常 必填}
## 给 Agent 的说明
异常时先别关单，回复里写明细节。
`

// #63：保存（新建、修改）带分组的模板成功；读取时 groups 与字段上的 group
// 都在；格式错误（字段写在第一组之前、空组）返回带行号的 422。
func Test保存和读取带分组的模板(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := s.Do(t, web, http.MethodPost, "/api/v1/templates",
		map[string]string{"key": "grouped", "markdown": groupedTemplate}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("新建状态码 = %d, 想 201", res.StatusCode)
	}
	var got templateBody
	s.Do(t, web, http.MethodGet, "/api/v1/templates/grouped", nil, &got)
	if len(got.Groups) != 2 {
		t.Fatalf("分组数 = %d, 想 2：%+v", len(got.Groups), got.Groups)
	}
	if got.Groups[0] != (groupBody{Title: "巡检情况", Subtitle: "时间、地点和结论"}) {
		t.Errorf("分组 0 = %+v", got.Groups[0])
	}
	if got.Groups[1] != (groupBody{Title: "后续处理", Subtitle: "怎么跟进"}) {
		t.Errorf("分组 1 = %+v", got.Groups[1])
	}
	wantGroups := map[string]int{"巡检哪条产品线？": 0, "结论如何？": 1}
	if len(got.Fields) != len(wantGroups) {
		t.Fatalf("字段数 = %d, 想 %d：%+v", len(got.Fields), len(wantGroups), got.Fields)
	}
	for _, f := range got.Fields {
		if f.Group == nil || *f.Group != wantGroups[f.Key] {
			t.Errorf("字段 %q 的组序号 = %v, 想 %d", f.Key, f.Group, wantGroups[f.Key])
		}
	}
	if got.AgentNote != "异常时先别关单，回复里写明细节。" {
		t.Errorf("给 Agent 的说明 = %q（夹在两组之间也应照常解析）", got.AgentNote)
	}

	// 修改：第二组加一个字段，产生版本 2；分组随版本保存。
	updated := strings.Replace(groupedTemplate,
		"## 结论如何？ {单选: 正常 | 异常 必填}",
		"## 结论如何？ {单选: 正常 | 异常 必填}\n## 要跟进到什么时候？", 1)
	res = s.Do(t, web, http.MethodPatch, "/api/v1/templates/grouped",
		map[string]string{"markdown": updated}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("修改状态码 = %d, 想 200", res.StatusCode)
	}
	s.Do(t, web, http.MethodGet, "/api/v1/templates/grouped", nil, &got)
	if got.Version != 2 || len(got.Fields) != 3 {
		t.Errorf("版本 = %d 字段数 = %d, 想 2 / 3", got.Version, len(got.Fields))
	}
	for _, f := range got.Fields {
		if f.Group == nil {
			t.Errorf("版本 2 的字段 %q 应仍带组序号", f.Key)
		}
	}

	// 分组写错：字段写在第一个分组之前 / 空组，都返回带行号的 422。
	for name, tc := range map[string]struct {
		markdown string
		wantLine string
	}{
		"字段写在第一组之前": {
			"---\n名称: x\n类型: 任务\n---\n## 先问？\n答。\n# 分组\n副标题\n## 再问？\n答。\n", "第 5 行"},
		"空组": {
			"---\n名称: x\n类型: 任务\n---\n# 组一\n副标题\n## 字段？\nx\n# 组二\n没有字段\n", "第 9 行"},
		"三级标题": {
			"---\n名称: x\n类型: 任务\n---\n### 字段？\nx\n", "第 5 行"},
	} {
		t.Run(name, func(t *testing.T) {
			res := s.Do(t, web, http.MethodPost, "/api/v1/templates",
				map[string]string{"key": "bad", "markdown": tc.markdown}, nil)
			if res.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("状态码 = %d, 想 422", res.StatusCode)
			}
			var got errorBody
			tbtest.DecodeJSON(t, res, &got)
			if got.Error.Code != "template_format_error" {
				t.Errorf("错误码 = %q, 想 template_format_error", got.Error.Code)
			}
			if !strings.Contains(got.Error.Message, tc.wantLine) {
				t.Errorf("message 应含行号 %q：%q", tc.wantLine, got.Error.Message)
			}
		})
	}
}

// #63：按带分组的模板建单，工单读取接口的 brief.template_fields 带 group、
// brief.template_groups 带分组（成员与访客视角都是）；不分组模板的已有工单
// 读取结果不变（没有 template_groups，字段没有 group）。
func Test工单读取带上模板分组(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	res := createFromTemplate(t, s, web, map[string]any{
		"template": "bug-report",
		"title":    "UD-2300 质控报警",
		"fields": map[string]string{
			"哪台仪器？":    "UD-2300",
			"你做了哪些操作？": "1. 开机 2. 进质控",
		},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	var created createTicketResponse
	tbtest.DecodeJSON(t, res, &created)

	var view ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &view)
	if len(view.Brief.TemplateGroups) != 3 {
		t.Fatalf("template_groups 数 = %d, 想 3：%+v", len(view.Brief.TemplateGroups), view.Brief.TemplateGroups)
	}
	wantGroups := []groupBody{
		{Title: "哪台仪器", Subtitle: "型号和版本"},
		{Title: "发生了什么", Subtitle: "时间、操作、现象，照片和日志"},
		{Title: "影响多大", Subtitle: "频率、影响和你的猜测"},
	}
	for i, want := range wantGroups {
		if view.Brief.TemplateGroups[i] != want {
			t.Errorf("分组 %d = %+v, 想 %+v", i, view.Brief.TemplateGroups[i], want)
		}
	}
	wantFieldGroups := map[string]int{
		"哪台仪器？":        0,
		"序列号和软件版本是多少？": 0,
		"什么时候发生的？":     1,
		"出现得频繁吗？":      2,
	}
	for _, f := range view.Brief.TemplateFields {
		want, ok := wantFieldGroups[f.Key]
		if !ok {
			continue
		}
		if f.Group == nil || *f.Group != want {
			t.Errorf("工单字段 %q 的组序号 = %v, 想 %d", f.Key, f.Group, want)
		}
	}

	// 访客视角同样带分组（#65 的访客分组填写页按它渲染）。
	link := s.CreateGuestLink(t, web, created.Number, map[string]any{
		"note": "检验科-张三", "valid_days": 30, "can_edit_brief": true,
	})
	var guestView ticketResponseBody
	s.Do(t, s.GuestClient(link.Key(t)), http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &guestView)
	if len(guestView.Brief.TemplateGroups) != 3 {
		t.Errorf("访客读到的 template_groups 数 = %d, 想 3", len(guestView.Brief.TemplateGroups))
	}
	if len(guestView.Brief.TemplateFields) == 0 || guestView.Brief.TemplateFields[0].Group == nil {
		t.Errorf("访客读到的 template_fields 应带 group：%+v", guestView.Brief.TemplateFields)
	}

	// 不分组的模板（question）建单：没有 template_groups，字段没有 group；
	// 模板接口同样没有 groups 键。
	res = createFromTemplate(t, s, web, map[string]any{
		"template": "question", "title": "固件推送时间",
		"fields":  map[string]string{"想问什么？": "V3.3 什么时候推？"},
		"labels":  []string{"机型:UD-2300"},
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", res.StatusCode)
	}
	var plain ticketResponseBody
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/2", nil, &plain)
	if plain.Brief.TemplateGroups != nil {
		t.Errorf("不分组模板的工单不应有 template_groups：%+v", plain.Brief.TemplateGroups)
	}
	for _, f := range plain.Brief.TemplateFields {
		if f.Group != nil {
			t.Errorf("不分组模板的字段 %q 不应带 group", f.Key)
		}
	}
	if raw := topLevelJSON(t, s, web, "/api/v1/templates/question"); raw != nil {
		if _, has := raw["groups"]; has {
			t.Error("不分组模板的响应不应有 groups 键")
		}
	}
}

// 全新的空库导入的 Bug 反馈模板直接带三组分组（验收标准）。
func Test全新空库的Bug反馈模板直接带分组(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	var got templateBody
	res := s.Do(t, web, http.MethodGet, "/api/v1/templates/bug-report", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读模板状态码 = %d", res.StatusCode)
	}
	if got.Version != 1 {
		t.Errorf("全新库的 bug-report 版本 = %d, 想 1（直接导入带分组的版本，不走迁移）", got.Version)
	}
	if len(got.Groups) != 3 || got.Groups[0].Title != "哪台仪器" || got.Groups[2].Title != "影响多大" {
		t.Errorf("预置 bug-report 应带三组分组：%+v", got.Groups)
	}
	// 其他两份预置模板不分组：没有 groups 键。
	if raw := topLevelJSON(t, s, web, "/api/v1/templates/assign-task"); raw != nil {
		if _, has := raw["groups"]; has {
			t.Error("assign-task 不分分组，不应有 groups 键")
		}
	}
}

// 迁移：原样（与升级前预置原文逐字节一致）的 Bug 反馈模板在服务重启后
// 出现带分组的新版本，版本历史里两个版本都可见。
func Test原样的Bug反馈模板升级出带分组的新版本(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 把数据目录伪装成升级前的老库：bug-report 版本 1 = 升级前原文。
	setBugReportCurrent(t, s, templates.LegacyBugReportMarkdown)

	url := restartServer(t, s)

	var got templateBody
	res := doURL(t, web, http.MethodGet, url+"/api/v1/templates/bug-report", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读模板状态码 = %d", res.StatusCode)
	}
	if got.Version != 2 {
		t.Fatalf("升级后版本 = %d, 想 2", got.Version)
	}
	if len(got.Groups) != 3 {
		t.Fatalf("升级后分组数 = %d, 想 3：%+v", len(got.Groups), got.Groups)
	}
	if got.Groups[0] != (groupBody{Title: "哪台仪器", Subtitle: "型号和版本"}) {
		t.Errorf("分组 0 = %+v", got.Groups[0])
	}
	for _, f := range got.Fields {
		if f.Group == nil {
			t.Fatalf("升级后字段 %q 应带组序号", f.Key)
		}
	}

	// 版本历史：版本 1 原样保留，版本 2 带分组。
	var versions struct {
		Versions []struct {
			Version   int64       `json:"version"`
			Markdown  string      `json:"markdown"`
			UpdatedBy *authorBody `json:"updated_by"`
		} `json:"versions"`
	}
	res = doURL(t, web, http.MethodGet, url+"/api/v1/templates/bug-report/versions", nil, &versions)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读版本状态码 = %d", res.StatusCode)
	}
	if len(versions.Versions) != 2 {
		t.Fatalf("版本数 = %d, 想 2：%+v", len(versions.Versions), versions.Versions)
	}
	newest, oldest := versions.Versions[0], versions.Versions[1]
	if newest.Version != 2 || oldest.Version != 1 {
		t.Fatalf("版本顺序不对：%+v", versions.Versions)
	}
	if oldest.Markdown != templates.LegacyBugReportMarkdown {
		t.Error("升级前的版本 1 原文应逐字节保留")
	}
	if !strings.Contains(newest.Markdown, "# 哪台仪器") {
		t.Errorf("新版本原文应带分组标记：\n%s", newest.Markdown)
	}
	// 再次重启不重复升级（当前版本已不是旧原文）。
	url2 := restartServer(t, s)
	var again templateBody
	doURL(t, web, http.MethodGet, url2+"/api/v1/templates/bug-report", nil, &again)
	if again.Version != 2 {
		t.Errorf("第二次重启后版本 = %d, 想 2（一次性迁移不重复执行）", again.Version)
	}
}

// 迁移：改过的 Bug 反馈模板不动（不产生新版本，也不加分组）。
func Test改过的Bug反馈模板不迁移(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 老库，但 bug-report 已经被成员改过一个字。
	modified := strings.Replace(templates.LegacyBugReportMarkdown,
		"帮助我们判断优先级。", "帮助我们判断优先级（本部门改过）。", 1)
	setBugReportCurrent(t, s, modified)

	url := restartServer(t, s)

	var got templateBody
	res := doURL(t, web, http.MethodGet, url+"/api/v1/templates/bug-report", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读模板状态码 = %d", res.StatusCode)
	}
	if got.Version != 1 {
		t.Fatalf("改过的模板版本 = %d, 想 1（不迁移）", got.Version)
	}
	if got.Markdown != modified {
		t.Error("改过的模板原文应保持不动")
	}
	if raw := topLevelJSONAt(t, web, url+"/api/v1/templates/bug-report"); raw != nil {
		if _, has := raw["groups"]; has {
			t.Error("改过的模板不应被加上分组")
		}
	}
}

// setBugReportCurrent 把 bug-report 的当前版本改成 markdown（同时把解析后的
// 字段定义、分组写回），模拟一个升级前的老数据目录。造数据走数据库快路径，
// 断言仍全部走 HTTP。
func setBugReportCurrent(t testing.TB, s *tbtest.Server, markdown string) {
	t.Helper()
	def, err := template.Parse(markdown)
	if err != nil {
		t.Fatalf("解析迁移用的模板原文失败：%v", err)
	}
	fields, err := json.Marshal(def.Fields)
	if err != nil {
		t.Fatal(err)
	}
	db := s.Store().DB
	if _, err := db.Exec(`UPDATE template_versions SET markdown = ?, fields = ?, groups = '[]'
		WHERE template_key = 'bug-report' AND version = 1`, markdown, string(fields)); err != nil {
		t.Fatalf("改写 bug-report 版本 1 失败：%v", err)
	}
	if _, err := db.Exec(`UPDATE templates SET current_version = 1 WHERE key = 'bug-report'`); err != nil {
		t.Fatalf("回置 bug-report 当前版本失败：%v", err)
	}
}

// restartServer 在同一数据目录上再启动一个真实服务并返回根地址——模拟
// 升级后重启进程（启动时的种子与一次性迁移会重新执行）。
func restartServer(t testing.TB, s *tbtest.Server) string {
	t.Helper()
	st, err := store.Open(s.DataDir())
	if err != nil {
		t.Fatalf("重开数据库失败：%v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Config{DataDir: s.DataDir(), WebDir: s.WebDir()}
	config.ApplyDefaults(&cfg)
	srv, err := server.New(cfg, st, clock.NewFake(time.Now()))
	if err != nil {
		t.Fatalf("重启服务失败：%v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败：%v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), server.ShutdownTimeout)
		defer cancel()
		_ = srv.Shutdown(ctx)
		<-done
	})
	return "http://" + ln.Addr().String()
}

// doURL 对任意根地址发 JSON 请求（restartServer 起的第二个服务）；
// 客户端沿用登录会话（cookie 固定在 transport 上，与地址无关）。
func doURL(t testing.TB, c *http.Client, method, url string, in, out any) *http.Response {
	t.Helper()
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("构造请求体失败：%v", err)
		}
		body = strings.NewReader(string(buf))
	}
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s 请求失败：%v", method, url, err)
	}
	t.Cleanup(func() { res.Body.Close() })
	if out != nil {
		tbtest.DecodeJSON(t, res, out)
	}
	return res
}

// topLevelJSON 把响应解码成顶层 map，用于断言某个键（如 groups）不存在。
func topLevelJSON(t testing.TB, s *tbtest.Server, c *http.Client, path string) map[string]json.RawMessage {
	t.Helper()
	res := s.Do(t, c, http.MethodGet, path, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d", path, res.StatusCode)
	}
	return decodeTopLevel(t, res)
}

func topLevelJSONAt(t testing.TB, c *http.Client, url string) map[string]json.RawMessage {
	t.Helper()
	res := doURL(t, c, http.MethodGet, url, nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET %s 状态码 = %d", url, res.StatusCode)
	}
	return decodeTopLevel(t, res)
}

func decodeTopLevel(t testing.TB, res *http.Response) map[string]json.RawMessage {
	t.Helper()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("读响应体失败：%v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		t.Fatalf("响应体不是合法 JSON：%s", string(body))
	}
	return top
}
