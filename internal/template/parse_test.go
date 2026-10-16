package template

import (
	"strings"
	"testing"
)

// spec #26「模板语法」一节的示例模板，解析的黄金用例。
const specExample = `---
名称: Bug 反馈
类型: 反馈
预设标签: [待初筛]
---
## 哪台仪器？ {维度:机型 必填}
在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。
## 你做了哪些操作？ {必填}
按顺序写，一步一行。
> 示例：1. 开机自检完成 2. 进入质控界面 3. 点击“开始”
`

// 解析 spec 示例：YAML 头（名称、类型、预设标签）、字段（问句标题、引导、
// 示例、必填、维度）都各就各位。
func Test解析spec示例模板(t *testing.T) {
	def, err := Parse(specExample)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if def.Name != "Bug 反馈" {
		t.Errorf("名称 = %q, 想 Bug 反馈", def.Name)
	}
	if def.Kind != "report" {
		t.Errorf("类型 = %q, 想 report（反馈）", def.Kind)
	}
	if len(def.PresetLabels) != 1 || def.PresetLabels[0] != "待初筛" {
		t.Errorf("预设标签 = %v, 想 [待初筛]", def.PresetLabels)
	}
	if def.AgentNote != "" {
		t.Errorf("给 Agent 的说明应为空：%q", def.AgentNote)
	}
	if len(def.Fields) != 2 {
		t.Fatalf("字段数 = %d, 想 2", len(def.Fields))
	}
	model := def.Fields[0]
	if model.Key != "哪台仪器？" || model.Title != "哪台仪器？" {
		t.Errorf("第一个字段的键/标题 = %q / %q", model.Key, model.Title)
	}
	if model.Type != "dimension" || model.Dimension != "机型" || !model.Required {
		t.Errorf("机型字段不对：%+v", model)
	}
	if model.Guide != "在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。" {
		t.Errorf("机型字段的引导 = %q", model.Guide)
	}
	if model.Example != "" {
		t.Errorf("机型字段不应有示例：%q", model.Example)
	}
	steps := def.Fields[1]
	if steps.Type != "text" || !steps.Required {
		t.Errorf("操作步骤字段不对：%+v", steps)
	}
	if steps.Guide != "按顺序写，一步一行。" {
		t.Errorf("操作步骤的引导 = %q", steps.Guide)
	}
	if steps.Example != "1. 开机自检完成 2. 进入质控界面 3. 点击“开始”" {
		t.Errorf("操作步骤的示例 = %q", steps.Example)
	}
}

// 不写标记的字段默认为多行文本、选填；类型 任务 映射成 task。
func Test不写标记默认为多行文本选填(t *testing.T) {
	def, err := Parse("---\n名称: 分配任务\n类型: 任务\n---\n## 要达成什么目标？\n一句话说明。\n")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if def.Kind != "task" {
		t.Errorf("类型 = %q, 想 task（任务）", def.Kind)
	}
	f := def.Fields[0]
	if f.Type != "text" || f.Required {
		t.Errorf("默认字段应为多行文本、选填：%+v", f)
	}
}

// 单选标记：`{单选: A | B 必填}` 解析出选项和必填。
func Test解析单选标记(t *testing.T) {
	def, err := Parse("---\n名称: 问题咨询\n类型: 任务\n---\n## 出现得频繁吗？ {单选: 每次 | 经常 | 偶尔 | 仅一次 必填}\n选一个。\n")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	f := def.Fields[0]
	if f.Type != "radio" {
		t.Errorf("类型 = %q, 想 radio", f.Type)
	}
	if !f.Required {
		t.Error("应为必填")
	}
	want := []string{"每次", "经常", "偶尔", "仅一次"}
	if strings.Join(f.Options, ",") != strings.Join(want, ",") {
		t.Errorf("选项 = %v, 想 %v", f.Options, want)
	}
}

// 「给 Agent 的说明」是一段说明，不是字段；正文原样保留。
func Test给Agent的说明不是字段(t *testing.T) {
	def, err := Parse("---\n名称: 分配任务\n类型: 任务\n---\n## 要达成什么目标？ {必填}\n一句话。\n## 给 Agent 的说明\n交付时用 deliver: true。\n")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(def.Fields) != 1 {
		t.Fatalf("字段数 = %d, 想 1（给 Agent 的说明不算字段）", len(def.Fields))
	}
	if def.AgentNote != "交付时用 deliver: true。" {
		t.Errorf("给 Agent 的说明 = %q", def.AgentNote)
	}
}

// 维度字段可以只写维度不写必填（选填的维度字段）。
func Test维度字段可以选填(t *testing.T) {
	def, err := Parse("---\n名称: x\n类型: 任务\n---\n## 影响哪些部门？ {维度:部门}\n可选。\n")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	f := def.Fields[0]
	if f.Type != "dimension" || f.Dimension != "部门" || f.Required {
		t.Errorf("维度字段不对：%+v", f)
	}
}

// 预设标签可以写多个，也可以不写。
func Test预设标签多个与缺省(t *testing.T) {
	def, err := Parse("---\n名称: x\n类型: 反馈\n预设标签: [待初筛, 加急]\n---\n## 想问什么？ {必填}\n一句话。\n")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if strings.Join(def.PresetLabels, ",") != "待初筛,加急" {
		t.Errorf("预设标签 = %v", def.PresetLabels)
	}
	def, err = Parse("---\n名称: x\n类型: 反馈\n---\n## 想问什么？ {必填}\n一句话。\n")
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	if len(def.PresetLabels) != 0 {
		t.Errorf("没写预设标签时应为空：%v", def.PresetLabels)
	}
}

// 格式错误要报出行号（1 起）。行号是解析器对外契约的一部分。
func Test格式错误报出行号(t *testing.T) {
	cases := []struct {
		name string
		md   string
		line int
		want string // message 里应包含的关键字
	}{
		{"没有YAML头", "## 想问什么？\n一句话。\n", 1, "---"},
		{"YAML头没收尾", "---\n名称: x\n类型: 任务\n", 4, "---"},
		{"YAML头键未知", "---\n名称: x\n类型: 任务\n描述: y\n---\n## 想问什么？\n一句话。\n", 4, "描述"},
		{"类型非法", "---\n名称: x\n类型: 问题\n---\n## 想问什么？\n一句话。\n", 3, "类型"},
		{"缺名称", "---\n类型: 任务\n---\n## 想问什么？\n一句话。\n", 1, "名称"},
		{"名称为空", "---\n名称:  \n类型: 任务\n---\n## 想问什么？\n一句话。\n", 2, "名称"},
		{"预设标签格式错", "---\n名称: x\n类型: 任务\n预设标签: 待初筛\n---\n## 想问什么？\n一句话。\n", 4, "["},
		{"YAML行没有冒号", "---\n名称: x\n类型\n---\n## 想问什么？\n一句话。\n", 3, "键"},
		{"正文在##小节之外", "---\n名称: x\n类型: 任务\n---\n这句话多余。\n## 想问什么？\n一句话。\n", 5, "##"},
		{"一级标题", "---\n名称: x\n类型: 任务\n---\n# 想问什么？\n一句话。\n", 5, "##"},
		{"小节标题为空", "---\n名称: x\n类型: 任务\n---\n## {必填}\n一句话。\n", 5, "标题"},
		{"标记不在行末", "---\n名称: x\n类型: 任务\n---\n## {必填} 想问什么？\n一句话。\n", 5, "{"},
		{"标记里的未知写法", "---\n名称: x\n类型: 任务\n---\n## 想问什么？ {必填 文本}\n一句话。\n", 5, "文本"},
		{"单选没有选项", "---\n名称: x\n类型: 任务\n---\n## 频率？ {单选:}\n一句话。\n", 5, "单选"},
		{"维度名单独出现", "---\n名称: x\n类型: 任务\n---\n## 哪台？ {维度:}\n一句话。\n", 5, "维度"},
		{"维度字段又标单选", "---\n名称: x\n类型: 任务\n---\n## 哪台？ {维度:机型 单选: A | B}\n一句话。\n", 5, "单选"},
		{"字段标题重复", "---\n名称: x\n类型: 任务\n---\n## 想问什么？\n一句话。\n## 想问什么？\n又一句。\n", 7, "重复"},
		{"没有字段小节", "---\n名称: x\n类型: 任务\n---\n## 给 Agent 的说明\n只有说明。\n", 5, "字段"},
		{"给Agent的说明带标记", "---\n名称: x\n类型: 任务\n---\n## 想问什么？\n一句话。\n## 给 Agent 的说明 {必填}\nx\n", 7, "给 Agent 的说明"},
		{"给Agent的说明写两段", "---\n名称: x\n类型: 任务\n---\n## 想问什么？\n一句话。\n## 给 Agent 的说明\n一\n## 给 Agent 的说明\n二\n", 9, "给 Agent 的说明"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.md)
			if err == nil {
				t.Fatalf("应报格式错误，但解析成功了")
			}
			pe, ok := err.(*ParseError)
			if !ok {
				t.Fatalf("错误应为 *ParseError，实际 %T：%v", err, err)
			}
			if pe.Line != tc.line {
				t.Errorf("行号 = %d, 想 %d（%v）", pe.Line, tc.line, pe)
			}
			if !strings.Contains(pe.Message, tc.want) {
				t.Errorf("错误信息 %q 应包含 %q", pe.Message, tc.want)
			}
			if !strings.Contains(err.Error(), "第") || !strings.Contains(err.Error(), "行") {
				t.Errorf("Error() 应带行号：%q", err.Error())
			}
		})
	}
}

// 按字段拼说明（brief_md）：只拼已填的字段，问句作三级标题。
func Test按字段拼说明(t *testing.T) {
	def, err := Parse(specExample)
	if err != nil {
		t.Fatalf("解析失败：%v", err)
	}
	got := RenderBrief(map[string]string{
		"哪台仪器？":    "BH-7600CS",
		"你做了哪些操作？": "",
	}, def.Fields)
	want := "### 哪台仪器？\n\nBH-7600CS"
	if got != want {
		t.Errorf("拼出的说明 = %q, 想 %q", got, want)
	}
	if all := RenderBrief(nil, def.Fields); all != "" {
		t.Errorf("全空字段的说明应为空：%q", all)
	}
}
