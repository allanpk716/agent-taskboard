package templates

import (
	"context"
	"database/sql"
	"errors"

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

// UpgradePresetBugReport 是一次性迁移（#63）：只有当 bug-report 的当前版本
// 与升级前的预置原文**完全一致**时，才用带分组的新预置原文产生一个新版本
//（版本历史里可见，修改人留空）；改过的、或不存在的都不动。升级判断自带
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
	if markdown != LegacyBugReportMarkdown {
		return nil
	}
	_, err = m.Update(ctx, presetBugReportKey, seedTemplates[presetBugReportKey], identity.Viewer{})
	return err
}
