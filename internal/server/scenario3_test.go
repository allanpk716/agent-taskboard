package server_test

// 场景 3（改派与越权，照搬原型）与「只凭 Markdown 的下一步就能写出交付和
// 验收的请求」的验收测试：从 ```http 代码块里解析出请求并真正执行。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 场景 3（改派与越权，照搬原型）：指派给成员 Agent 被拒；不打维度标签不能
// 创建；不是指派人的成员不能交付；改派后工单从李工的收件箱移到赵工的
// 收件箱，原因显示「新指派给你」。
func Test场景3改派与越权(t *testing.T) {
	w := newHandoffWorld(t)

	// 指派给「李工 的 Agent · 工位」→ 422 assignee_must_be_member。
	res := w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "整理对照表", "brief_md": "……", "labels": []string{"机型:UD-2300"},
		"assignee": "李工 的 Agent · 工位",
	}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("指派给成员 Agent 状态码 = %d, 想 422", res.StatusCode)
	}
	// 不打维度标签 → 422 dimension_label_required。
	res = w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "整理对照表", "brief_md": "……", "labels": []string{"紧急"},
	}, nil)
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("无维度标签状态码 = %d, 想 422", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "dimension_label_required" {
		t.Errorf("错误码 = %q, 想 dimension_label_required", got.Error.Code)
	}

	// 建一张派给李工的工单；赵工（不是指派人）尝试交付 → 403。
	number := w.createHandoffTicket(t, w.wangA, "整理 Q3 质控规则对照表", "李工").Number
	res = w.s.Do(t, w.zhaoA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "我顺手做了", "deliver": true}, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("非指派人交付状态码 = %d, 想 403", res.StatusCode)
	}

	// 李工：这周排不开 → 轮到王工。
	var asked replyBody
	w.s.Do(t, w.liA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]string{"body_md": "这周在跑注册检验，排不开，能否转给别人？"}, &asked)
	if asked.WaitingOn.Party != "creator" || asked.WaitingOn.Reason != "指派人回复了，待你答复" {
		t.Errorf("提问后 waiting_on = %+v", asked.WaitingOn)
	}

	// 改派给赵工；赵工的收件箱出现「新指派给你」，李工的收件箱清空。
	w.s.Do(t, w.webWang, http.MethodPost, "/api/v1/tickets/"+number+"/assign",
		map[string]string{"assignee": "赵工"}, nil)
	if reason := w.inboxOf(t, w.zhaoA).reasonByNumber()[number]; reason != "新指派给你" {
		t.Errorf("改派后赵工信箱原因 = %q, 想 新指派给你", reason)
	}
	for _, it := range w.inboxOf(t, w.liA).Items {
		if it.Number == number {
			t.Errorf("改派后李工信箱不应再有 %s", number)
		}
	}
	// 赵工的收件箱项角色是指派人。
	for _, it := range w.inboxOf(t, w.zhaoA).Items {
		if it.Number == number && it.Role != "assignee" {
			t.Errorf("赵工在这张工单里的角色 = %s, 想 assignee", it.Role)
		}
	}

	// 改派后赵工能交付（原指派人不能）。
	var delivered replyBody
	res2 := w.s.Do(t, w.zhaoA, http.MethodPost, "/api/v1/tickets/"+number+"/replies",
		map[string]any{"body_md": "我来做。", "deliver": true}, &delivered)
	if res2.StatusCode != http.StatusCreated || !delivered.Deliver {
		t.Fatalf("改派后赵工交付状态码 = %d, 想 201", res2.StatusCode)
	}
}

// httpStep 是从 Markdown「下一步」解析出的一个可执行请求。
type httpStep struct {
	Method string
	Path   string
	Body   map[string]any
}

// httpBlockAfter 取 note 之后第一个 ```http 代码块并解析成请求。
// 格式：第一行「METHOD path」，之后是请求头，空行后是 JSON 请求体。
func httpBlockAfter(t testing.TB, md, note string) httpStep {
	t.Helper()
	idx := strings.Index(md, note)
	if idx < 0 {
		t.Fatalf("Markdown 里没有 %q：\n%s", note, md)
	}
	rest := md[idx:]
	start := strings.Index(rest, "```http")
	if start < 0 {
		t.Fatalf("%q 之后没有 ```http 代码块：\n%s", note, rest)
	}
	tail := rest[start+len("```http"):]
	end := strings.Index(tail, "```")
	if end < 0 {
		t.Fatalf("```http 代码块没有闭合：\n%s", tail)
	}
	block := tail[:end]
	lines := strings.Split(strings.TrimSpace(block), "\n")
	head := strings.Fields(strings.TrimSpace(lines[0]))
	if len(head) != 2 {
		t.Fatalf("代码块第一行不是「METHOD path」：%q", lines[0])
	}
	step := httpStep{Method: head[0], Path: head[1]}
	if bodyIdx := findBlank(lines); bodyIdx >= 0 && bodyIdx+1 < len(lines) {
		body := strings.Join(lines[bodyIdx+1:], "\n")
		if err := json.Unmarshal([]byte(body), &step.Body); err != nil {
			t.Fatalf("解析请求体 %q 失败：%v", body, err)
		}
	}
	return step
}

// findBlank 找代码块里请求头与请求体之间的空行。
func findBlank(lines []string) int {
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			return i
		}
	}
	return -1
}

// 验收点：只凭 Markdown 的「下一步」，就能写出交付和验收的请求——把代码块
// 解析出来，原样发给服务，交付成功、验收关闭成功。
func Test凭Markdown的下一步写出交付与验收请求(t *testing.T) {
	w := newHandoffWorld(t)
	number := w.createHandoffTicket(t, w.wangA, "整理 Q3 质控规则对照表", "李工").Number

	// 李工的 Agent 读 Markdown，找到「做完了」的交付请求并照抄执行。
	md := w.readMarkdown(t, w.liA, "/api/v1/tickets/"+number+"?format=markdown")
	deliver := httpBlockAfter(t, md, "4. **做完了**")
	if deliver.Method != http.MethodPost || deliver.Path != "/api/v1/tickets/"+number+"/replies" {
		t.Fatalf("交付请求 = %s %s, 想 POST /api/v1/tickets/%s/replies",
			deliver.Method, deliver.Path, number)
	}
	if deliver.Body["deliver"] != true {
		t.Fatalf("交付请求体应为 {\"body_md\": …, \"deliver\": true}：%v", deliver.Body)
	}
	var delivered replyBody
	res := w.s.Do(t, w.liA, deliver.Method, deliver.Path, deliver.Body, &delivered)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("照抄交付请求状态码 = %d, 想 201（响应体：%+v）", res.StatusCode, delivered)
	}

	// 不轮到的人（旁观成员）：指引去查收件箱。
	md = w.readMarkdown(t, w.zhaoA, "/api/v1/tickets/"+number+"?format=markdown")
	for _, want := range []string{"现在不需要你行动", "GET /api/v1/inbox"} {
		if !strings.Contains(md, want) {
			t.Errorf("旁观成员的 Markdown 缺少 %q：\n%s", want, md)
		}
	}

	// 王工的 Agent 读 Markdown，找到验收通过 close{done} 的请求并照抄执行。
	md = w.readMarkdown(t, w.wangA, "/api/v1/tickets/"+number+"?format=markdown")
	accept := httpBlockAfter(t, md, "- **验收通过**：以“完成”关闭：")
	if accept.Method != http.MethodPost || accept.Path != "/api/v1/tickets/"+number+"/close" {
		t.Fatalf("验收请求 = %s %s, 想 POST …/close", accept.Method, accept.Path)
	}
	if accept.Body["reason"] != "done" {
		t.Fatalf("验收请求体 = %v, 想 {\"reason\": \"done\"}", accept.Body)
	}
	var closed struct {
		Status string `json:"status"`
	}
	res = w.s.Do(t, w.wangA, accept.Method, accept.Path, accept.Body, &closed)
	if res.StatusCode != http.StatusOK || closed.Status != "closed" {
		t.Fatalf("照抄验收请求状态码/状态 = %d/%s, 想 200/closed", res.StatusCode, closed.Status)
	}

	// 关闭后的 Markdown：不需要再做什么；成员可重开。
	md = w.readMarkdown(t, w.webWang, "/api/v1/tickets/"+number+"?format=markdown")
	for _, want := range []string{"工单已关闭（完成），不需要再做什么", "确实还要继续时，先重新开启：", "POST /api/v1/tickets/" + number + "/reopen"} {
		if !strings.Contains(md, want) {
			t.Errorf("关闭后的 Markdown 缺少 %q：\n%s", want, md)
		}
	}
}

// 模板工单（#34）与指派（#36）合在一起：用「分配任务」模板建单并指派给同事，
// 说明按字段拼出、assigned 事件与 share_text 照常；指派人的 Markdown「下一步」
// 末尾附模板「给 Agent 的说明」，交付请求仍可直接照抄执行。
func Test用分配任务模板建单并指派(t *testing.T) {
	w := newHandoffWorld(t)
	var created createAssignedResponse
	res := w.s.Do(t, w.wangA, http.MethodPost, "/api/v1/tickets", map[string]any{
		"template": "assign-task", "title": "BH-5100 质控模块回归测试", "assignee": "李工",
		"labels": []string{"机型:UD-2300", "部门:研发部"},
		"fields": map[string]string{
			"要达成什么目标？":     "验证 v3.2 的质控模块在 BH-5100 上没有回归。",
			"交付什么？":        "一份测试报告（Markdown 即可），附失败用例截图。",
			"怎样算完成（验收标准）？": "Westgard 6 条规则全部覆盖；失败用例写清复现步骤。",
		},
	}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("模板建单指派状态码 = %d, 想 201", res.StatusCode)
	}
	if created.Assignee == nil || created.Assignee.Name != "李工" || created.ShareText == nil {
		t.Errorf("模板建单的 assignee/share_text 不对：%+v", created)
	}
	if len(created.WaitingOn.Party) == 0 || created.WaitingOn.Party != "assignee" {
		t.Errorf("waiting_on = %+v, 想 assignee", created.WaitingOn)
	}

	// 指派人的 Markdown：模板的 Agent 说明出现在「下一步」里，交付请求可照抄。
	md := w.readMarkdown(t, w.liA, "/api/v1/tickets/"+created.Number+"?format=markdown")
	for _, want := range []string{
		"### 要达成什么目标？",
		"验证 v3.2 的质控模块在 BH-5100 上没有回归。",
		"分配任务 模板给 Agent 的说明：交付时在回复里带 deliver: true",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("模板工单的 Markdown 缺少 %q：\n%s", want, md)
		}
	}
	deliver := httpBlockAfter(t, md, "4. **做完了**")
	var delivered replyBody
	res = w.s.Do(t, w.liA, deliver.Method, deliver.Path, deliver.Body, &delivered)
	if res.StatusCode != http.StatusCreated || !delivered.Deliver {
		t.Fatalf("照抄交付请求状态码 = %d, 想 201", res.StatusCode)
	}
	// 模板说明里的验收标准就是发起人的验收依据。
	md = w.readMarkdown(t, w.wangA, "/api/v1/tickets/"+created.Number+"?format=markdown")
	if !strings.Contains(md, "对照说明中的验收标准检查交付物") {
		t.Errorf("发起人 Markdown 应提示按验收标准检查：\n%s", md)
	}
}
