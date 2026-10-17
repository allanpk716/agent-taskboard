package templates

import (
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/template"
)

// 三份预置模板都要能解析（#63 验收标准）。
func Test预置模板都能解析(t *testing.T) {
	for key, md := range seedTemplates {
		if _, err := template.Parse(md); err != nil {
			t.Errorf("预置模板 %s 解析失败：%v", key, err)
		}
	}
}

// 预置的 Bug 反馈模板分成三组（#63）：哪台仪器 / 发生了什么 / 影响多大，
// 副标题按原型 prototype/guest-fill 的 c.html 写。全新的空库导入即带分组。
func Test预置Bug反馈模板带三组分组(t *testing.T) {
	def, err := template.Parse(seedTemplates["bug-report"])
	if err != nil {
		t.Fatalf("解析 bug-report 失败：%v", err)
	}
	want := []template.Group{
		{Title: "哪台仪器", Subtitle: "型号和版本"},
		{Title: "发生了什么", Subtitle: "时间、操作、现象，照片和日志"},
		{Title: "影响多大", Subtitle: "频率、影响和你的猜测"},
	}
	if len(def.Groups) != len(want) {
		t.Fatalf("分组数 = %d, 想 %d：%+v", len(def.Groups), len(want), def.Groups)
	}
	for i, g := range want {
		if def.Groups[i] != g {
			t.Errorf("分组 %d = %+v, 想 %+v", i, def.Groups[i], g)
		}
	}
	wantFieldGroups := map[string]int{
		"哪台仪器？":        0,
		"序列号和软件版本是多少？": 0,
		"什么时候发生的？":     1,
		"你做了哪些操作？":     1,
		"实际看到了什么？":     1,
		"你期望看到什么？":     1,
		"出现得频繁吗？":      2,
		"对工作有影响吗？":     2,
		"你的猜测或补充？":     2,
	}
	if len(def.Fields) != len(wantFieldGroups) {
		t.Fatalf("字段数 = %d, 想 %d：%+v", len(def.Fields), len(wantFieldGroups), def.Fields)
	}
	for _, f := range def.Fields {
		if f.Group == nil || *f.Group != wantFieldGroups[f.Key] {
			t.Errorf("字段 %q 的组序号 = %v, 想 %d", f.Key, f.Group, wantFieldGroups[f.Key])
		}
	}

	// 其余两份预置模板不分组。
	for _, key := range []string{"assign-task", "question"} {
		def, err := template.Parse(seedTemplates[key])
		if err != nil {
			t.Fatalf("解析 %s 失败：%v", key, err)
		}
		if len(def.Groups) != 0 {
			t.Errorf("%s 不应分组：%+v", key, def.Groups)
		}
		for _, f := range def.Fields {
			if f.Group != nil {
				t.Errorf("%s 的字段 %q 不应带组序号", key, f.Key)
			}
		}
	}
}

// 迁移用的「升级前原文」要能解析、且不带分组，并与新预置原文不同——
// 迁移的「原样判断」靠它逐字节比对。
func Test升级前Bug反馈原文可解析且与新原文不同(t *testing.T) {
	old, err := template.Parse(LegacyBugReportMarkdown)
	if err != nil {
		t.Fatalf("升级前原文解析失败：%v", err)
	}
	if len(old.Groups) != 0 {
		t.Errorf("升级前原文不应带分组：%+v", old.Groups)
	}
	if LegacyBugReportMarkdown == seedTemplates["bug-report"] {
		t.Fatal("升级前原文应与新预置原文不同，否则迁移无从判断")
	}
}
