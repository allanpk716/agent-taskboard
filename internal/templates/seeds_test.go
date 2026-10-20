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

// 预置的两份 Bug 反馈模板都分成三组；带数据版本用中间组名区分，并把
// 「正常的例子」设为必填。
func Test预置Bug反馈模板带三组分组(t *testing.T) {
	for key, wantMiddle := range map[string]template.Group{
		"bug-report":      {Title: "发生了什么", Subtitle: "时间、操作、出错和正常的例子"},
		"bug-report-data": {Title: "正确和错误的例子", Subtitle: "请同时给出正确和错误的例子：每个例子写清输入了什么、得到了什么"},
	} {
		t.Run(key, func(t *testing.T) {
			checkBugGroups(t, key, wantMiddle)
		})
	}
}

func checkBugGroups(t *testing.T, key string, middle template.Group) {
	t.Helper()
	def, err := template.Parse(seedTemplates[key])
	if err != nil {
		t.Fatalf("解析 %s 失败：%v", key, err)
	}
	want := []template.Group{
		{Title: "哪台仪器", Subtitle: "型号和版本"},
		middle,
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
		"出错的例子":        1,
		"正常的例子":        1,
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
	if key == "bug-report-data" {
		for _, f := range def.Fields {
			if f.Key == "正常的例子" && !f.Required {
				t.Error("bug-report-data 的「正常的例子」应为必填")
			}
		}
	}
}

// 其余两份预置模板不分组。
func Test其他预置模板不分组(t *testing.T) {
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

// 迁移用的历史原文要能解析，并与新预置原文不同——迁移的「原样判断」
// 靠它逐字节比对。
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
	b63, err := template.Parse(BugReport63Markdown)
	if err != nil {
		t.Fatalf("#63 原文解析失败：%v", err)
	}
	if len(b63.Groups) != 3 {
		t.Fatalf("#63 原文应带三组：%+v", b63.Groups)
	}
	if BugReport63Markdown == seedTemplates["bug-report"] {
		t.Fatal("#63 原文应与新预置原文不同，否则迁移无从判断")
	}
}
