// 后端更新（#45）：更新前备份 → 把当前 exe 改名 .prev → 放入新 exe。
// 进程退出由调用方决定（管理 API 触发时写完响应即退出，由 NSSM 拉起新版本；
// 命令行触发时提示重启）。回滚是命令行 taskboard rollback（把 .prev 换回来，
// 见 cmd/taskboard）。
package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/store"
)

// defaultPreupdate 打开 data_dir 的库做一次更新前备份（ops.Preupdate 与
// 每日定时备份是同一段代码）。返回备份文件名。
func defaultPreupdate(ctx context.Context, o *Options) (PreupdateBackup, error) {
	st, err := store.Open(o.DataDir)
	if err != nil {
		return PreupdateBackup{}, fmt.Errorf("打开数据库做更新前备份：%w", err)
	}
	defer st.Close()
	b, err := ops.NewBackups(st.DB, o.DataDir, o.Backup.BackupOptions(), clock.Real{})
	if err != nil {
		return PreupdateBackup{}, err
	}
	rec, err := b.Preupdate(ctx)
	if err != nil {
		return PreupdateBackup{}, err
	}
	return PreupdateBackup{Filename: rec.Filename, Local: rec.Location == ops.LocationLocal}, nil
}

// swapExe 把 exePath 改名为 exePath.prev，再放入新 exe 的内容。当前 exe
// 正在运行也可以改名（Windows 允许改名、不允许覆盖），所以先改名再放新文件。
func swapExe(exePath string, newExe []byte) error {
	prev := exePath + ".prev"
	if err := os.Remove(prev); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除旧 .prev 失败：%w", err)
	}
	if err := os.Rename(exePath, prev); err != nil {
		return fmt.Errorf("把当前 exe 改名为 .prev 失败：%w", err)
	}
	// 先写 .new 再改名：任何时刻 exePath 要么是旧文件要么是完整的新文件。
	tmp := exePath + ".new"
	if err := writeFileSync(tmp, newExe); err != nil {
		// 尽力恢复：把 .prev 换回去。
		if rb := os.Rename(prev, exePath); rb != nil {
			return fmt.Errorf("写入新 exe 失败（%v），且恢复 .prev 也失败（%v），请手动把 %s 改回 %s", err, rb, prev, exePath)
		}
		return fmt.Errorf("写入新 exe 失败（已恢复原 exe）：%w", err)
	}
	if err := os.Rename(tmp, exePath); err != nil {
		if rb := os.Rename(prev, exePath); rb != nil {
			return fmt.Errorf("放入新 exe 失败（%v），且恢复 .prev 也失败（%v），请手动把 %s 改回 %s", err, rb, prev, exePath)
		}
		return fmt.Errorf("放入新 exe 失败（已恢复原 exe）：%w", err)
	}
	return nil
}

// writeFileSync 写文件并落盘（更新 exe 时尽量保证崩溃后不留半个文件）。
func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// RollbackResult 是一次回滚的结果。
type RollbackResult struct {
	// RestoredExeFrom 是换回的上一版 exe（taskboard.exe.prev，换回后不复存在）。
	RestoredExeFrom string
	// RestoredBackup 是换回的更新前备份文件。
	RestoredBackup string
	// Restore 是恢复库的结果（另存了当前库、补齐附件等，见 internal/ops）。
	Restore *ops.RestoreResult
}

// Rollback 换回上一版：把 .prev 的 exe 换回来，并把库恢复成最近一次更新前
// 备份（新版本起不来时在服务器上执行；服务必须已停止，库恢复复用
// ops.Restore 的校验与补附件逻辑）。
func Rollback(opt Options) (*RollbackResult, error) {
	exePath := opt.exePath()
	prev := exePath + ".prev"
	if _, err := os.Stat(prev); err != nil {
		return nil, fmt.Errorf("没有可回滚的上一版（%s 不存在）：只能重新安装或重新更新", filepath.Base(prev))
	}
	backup, err := latestPreupdateBackup(opt)
	if err != nil {
		return nil, err
	}
	res := &RollbackResult{RestoredExeFrom: prev, RestoredBackup: backup.Path}

	// 先恢复库（ops.Restore 内部会校验备份、另存当前库、执行迁移、补齐附件），
	// 再换 exe：两步都完成后才启动服务。
	restore, err := ops.Restore(opt.DataDir, opt.Port, backup.Path, opt.now())
	if err != nil {
		return nil, fmt.Errorf("恢复更新前备份 %s 失败：%w", backup.Name, err)
	}
	res.Restore = &restore

	if err := os.Remove(exePath); err != nil {
		return nil, fmt.Errorf("移除当前 exe 失败（库已恢复到更新前）：%w", err)
	}
	if err := os.Rename(prev, exePath); err != nil {
		return nil, fmt.Errorf("换回 .prev 失败：%w", err)
	}
	return res, nil
}

// preupdateBackup 是找到一个候选备份后的描述。
type preupdateBackup struct {
	Name string
	Path string
}

// latestPreupdateBackup 找最近的更新前备份（文件名里的时间戳保证字典序就是
// 时间序）。配置了目标时也要看 data_dir\backups：目标不可达时的兜底副本在那里。
func latestPreupdateBackup(opt Options) (*preupdateBackup, error) {
	var matches []string
	dirs := []string{filepath.Join(opt.DataDir, "backups")}
	if opt.Backup.Target != "" {
		dirs = append(dirs, opt.Backup.Target)
	}
	for _, dir := range dirs {
		found, err := filepath.Glob(filepath.Join(dir, "taskboard-*-preupdate.db"))
		if err != nil {
			return nil, err
		}
		matches = append(matches, found...)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("在 %s 下没有更新前备份（taskboard-*-preupdate.db），无法回滚数据库",
			strings.Join(dirs, " 或 "))
	}
	// 字典序最大的就是最新的。
	latest := matches[0]
	for _, m := range matches[1:] {
		if m > latest {
			latest = m
		}
	}
	return &preupdateBackup{Name: filepath.Base(latest), Path: latest}, nil
}
