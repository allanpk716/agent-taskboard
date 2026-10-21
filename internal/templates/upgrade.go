package templates

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// LegacyBugReportMarkdown 是 #63 之前预置的 Bug 反馈模板原文（不带分组），
// 只用于 UpgradePresetBugReport 的「原样判断」：线上 bug-report 的当前版本
// 与它逐字节一致才升级。这段文字是历史事实，不再随模板演进修改。
const LegacyBugReportMarkdown = `---
名称: Bug 反馈
类型: 反馈
预设标签: [待初筛]
---
## 哪台仪器？ {维度:机型 必填}
在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。
## 序列号和软件版本是多少？
在“设置 → 关于本机”里可以看到。
> 示例：序列号 7600CS-2023-0412，软件 V3.2.1
## 什么时候发生的？ {必填}
大概时间即可，便于我们查仪器日志。
> 示例：今天上午 9 点左右
## 你做了哪些操作？ {必填}
按顺序写，一步一行。
> 示例：1. 开机自检完成 2. 进入质控界面 3. 点击“开始”
## 实际看到了什么？ {必填}
屏幕上的报警或提示请原样照抄，包括编号；能拍照最好。
> 示例：弹出“E-203 试剂余量不足”
## 你期望看到什么？ {必填}
本来应该发生什么。
> 示例：正常开始质控测试
## 出现得频繁吗？ {单选: 每次 | 经常 | 偶尔 | 仅一次 必填}
## 对工作有影响吗？ {单选: 无法出报告 | 能绕过 | 不影响}
帮助我们判断优先级。
## 你的猜测或补充？
如果你觉得是什么原因，写在这里；猜错也没关系，和上面的现象分开。
> 示例：可能是试剂瓶的液位传感器没识别到
## 给 Agent 的说明
照片和日志作为附件上传（上传接口见工单说明）；缺少必填项时用普通回复向提交者逐项追问，不改写对方的说明。信息齐全、可以处理后摘掉「待初筛」标签并指派给指派人。
`

// BugReport63Markdown 是 #63 到 #154 之前预置的 Bug 反馈模板原文（带分组），
// 只用于 UpgradePresetBugReport 的「原样判断」。这段文字是历史事实，不再随
// 模板演进修改。
const BugReport63Markdown = `---
名称: Bug 反馈
类型: 反馈
预设标签: [待初筛]
---
# 哪台仪器
型号和版本
## 哪台仪器？ {维度:机型 必填}
在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。
## 序列号和软件版本是多少？
在“设置 → 关于本机”里可以看到。
> 示例：序列号 7600CS-2023-0412，软件 V3.2.1
# 发生了什么
时间、操作、现象，照片和日志
## 什么时候发生的？ {必填}
大概时间即可，便于我们查仪器日志。
> 示例：今天上午 9 点左右
## 你做了哪些操作？ {必填}
按顺序写，一步一行。
> 示例：1. 开机自检完成 2. 进入质控界面 3. 点击“开始”
## 实际看到了什么？ {必填}
屏幕上的报警或提示请原样照抄，包括编号；能拍照最好。
> 示例：弹出“E-203 试剂余量不足”
## 你期望看到什么？ {必填}
本来应该发生什么。
> 示例：正常开始质控测试
# 影响多大
频率、影响和你的猜测
## 出现得频繁吗？ {单选: 每次 | 经常 | 偶尔 | 仅一次 必填}
## 对工作有影响吗？ {单选: 无法出报告 | 能绕过 | 不影响}
帮助我们判断优先级。
## 你的猜测或补充？
如果你觉得是什么原因，写在这里；猜错也没关系，和上面的现象分开。
> 示例：可能是试剂瓶的液位传感器没识别到
## 给 Agent 的说明
照片和日志作为附件上传（上传接口见工单说明）；缺少必填项时用普通回复向提交者逐项追问，不改写对方的说明。信息齐全、可以处理后摘掉「待初筛」标签并指派给指派人。
`

// UpgradePresetBugReport 是一次性迁移（#63/#154）：只有当 bug-report 的当前
// 版本与升级前的预置原文**完全一致**时，才用新预置原文产生一个新版本
// （版本历史里可见，修改人留空）；改过的、或不存在的都不动。升级判断自带
// 幂等：升级后当前版本不再等于旧原文，再次启动不会重复升级。
func (m *Manager) UpgradePresetBugReport(ctx context.Context) error {
	var markdown string
	err := m.db.QueryRowContext(ctx, `
		SELECT v.markdown
		FROM templates t
		JOIN template_versions v ON v.template_key = t.key AND v.version = t.current_version
		WHERE t.key = ?`, presetBugReportKey).Scan(&markdown)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if markdown != LegacyBugReportMarkdown && markdown != BugReport63Markdown {
		return nil
	}
	_, err = m.Update(ctx, presetBugReportKey, seedTemplates[presetBugReportKey], identity.Viewer{})
	return err
}

// ImportPresetBugReportData 在已有部署缺少 bug-report-data 时导入预置的
// 「Bug 反馈（带数据）」。key 已存在（无论当前内容或停用状态）都不动；
// 导入后再次启动也不会产生新版本。
func (m *Manager) ImportPresetBugReportData(ctx context.Context) error {
	var known int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM templates WHERE key = ?`, "bug-report-data").Scan(&known); err != nil {
		return err
	}
	if known > 0 {
		return nil
	}
	_, err := m.Create(ctx, "bug-report-data", seedTemplates["bug-report-data"], identity.Viewer{})
	if errors.Is(err, ErrKeyTaken) {
		return nil
	}
	return err
}

// ImportPresetRequest 在已有部署缺少 request 时导入预置的「提需求」（#194）。
// key 已存在（无论当前内容或停用状态）都不动；导入后再次启动也不会产生新版本。
func (m *Manager) ImportPresetRequest(ctx context.Context) error {
	var known int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM templates WHERE key = ?`, "request").Scan(&known); err != nil {
		return err
	}
	if known > 0 {
		return nil
	}
	_, err := m.Create(ctx, "request", seedTemplates["request"], identity.Viewer{})
	if errors.Is(err, ErrKeyTaken) {
		return nil
	}
	return err
}

// presetScenarioUpgrade 描述一份预置模板的适用场景升级。From 是上线前最后
// 一版预置原文；只有当前版本与它逐字节一致才升级到 To。
type presetScenarioUpgrade struct {
	Key  string
	From string
	To   string
}

// presetScenarioUpgrades 构造 #193 的四份升级：当时的预置原文与新版相比，
// 只有 YAML 头里多出「适用场景」一行。后续预置模板升级可在同一结构里扩展。
func presetScenarioUpgrades() []presetScenarioUpgrade {
	keys := []string{presetBugReportKey, "bug-report-data", "assign-task", "question"}
	out := make([]presetScenarioUpgrade, 0, len(keys))
	for _, key := range keys {
		next := seedTemplates[key]
		out = append(out, presetScenarioUpgrade{Key: key, From: withoutScenarioLine(next), To: next})
	}
	return out
}

func withoutScenarioLine(markdown string) string {
	lines := strings.Split(markdown, "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.HasPrefix(line, "适用场景:") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// UpgradePresetScenarios 给原样的预置模板补上适用场景（#193）。成员改过
// 的模板与目标版本都不动；升级后当前版本不等于旧原文，重复启动幂等。
func (m *Manager) UpgradePresetScenarios(ctx context.Context) error {
	for _, up := range presetScenarioUpgrades() {
		var markdown string
		err := m.db.QueryRowContext(ctx, `
			SELECT v.markdown
			FROM templates t
			JOIN template_versions v ON v.template_key = t.key AND v.version = t.current_version
			WHERE t.key = ?`, up.Key).Scan(&markdown)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		if markdown != up.From {
			continue
		}
		if _, err := m.Update(ctx, up.Key, up.To, identity.Viewer{}); err != nil {
			return err
		}
	}
	return nil
}
