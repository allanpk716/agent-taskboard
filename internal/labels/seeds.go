package labels

import (
	"context"
	"fmt"
)

// SeedIfEmpty 在首次启动时导入种子：字典里一个维度都没有才导入，
// 保证这是一次性的（之后治理过的字典不会被启动时重新覆盖）。
func (m *Manager) SeedIfEmpty(ctx context.Context) error {
	var dims int
	if err := m.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM label_dimensions`).Scan(&dims); err != nil {
		return err
	}
	if dims > 0 {
		return nil
	}
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, d := range seedDimensions {
		if _, err := tx.ExecContext(ctx, `INSERT INTO label_dimensions (name) VALUES (?)`, d); err != nil {
			return fmt.Errorf("导入种子维度 %s 失败：%w", d, err)
		}
		for _, v := range seedValues[d] {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO label_values (dimension, value) VALUES (?, ?)`, d, v); err != nil {
				return fmt.Errorf("导入种子值 %s:%s 失败：%w", d, v, err)
			}
		}
	}
	return tx.Commit()
}

// 一次性种子文件（spec #26「初始数据」；orchestrator 决议 1）。
//
// 机型（14 个）与部门（13 个）来自日报系统正式库 2026-10-16 的导出，
// 名单按实施笔记 seeds.md 原样导入，不发明别名（7000VET 与 BH-7000VET 是两个独立的值）。
// 其余维度按 spec 整理：产品线与系统为日报系统中的名称，算法初始为空，
// 立项和子项目以后用编号作值。上线后平台与日报系统各自维护，不做同步。
var seedDimensions = []string{
	"产品线", "系统", "机型", "部门", "算法", "立项", "子项目",
}

var seedValues = map[string][]string{
	"产品线": {"血液", "尿液", "生免", "流式", "人工智能相关"},
	"系统":  {"LIS", "中间件", "智慧实验室", "质控平台-溯源", "动物云平台", "算法数据生命周期"},
	"机型": {
		"7000VET", "7300", "BH-7000VET", "BH-7300", "BH-7680", "BM-10vet", "RM-11",
		"SL-1000", "UD-1290", "UD-1300", "UD-2300", "US-1680", "US-1000", "US-500",
	},
	"部门": {
		"事业一部", "事业二部", "事业三部", "事业四部", "事业五部", "事业七部", "事业八部",
		"人工智能研究部", "集团", "研发部", "国际营销中心", "销售公司", "集团软件技术组",
	},
	"算法":  nil,
	"立项":  nil,
	"子项目": nil,
}
