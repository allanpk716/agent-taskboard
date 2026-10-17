// 恢复（#43）：taskboard restore <备份文件> 的五步依次是确认服务已停止、
// 另存当前库、换成指定的备份、从镜像中补齐附件、（由 NSSM 或操作员）启动服务。
// 服务本身不在本进程里启停：Windows 上对运行中的库做热替换不安全（文件也被
// 占用），所以恢复要求服务已停止（端口上仍在监听时拒绝执行），完成后由 NSSM
// 或操作员重新启动。
package ops

import (
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/store"
)

// RestoreResult 是一次恢复的结果摘要。
type RestoreResult struct {
	// SavedCurrentAs 是另存的当前库路径（原库没丢，需要时还能换回去）。
	SavedCurrentAs string
	// BackupFile 是换上的备份文件（原文件保留不动）。
	BackupFile string
	// DBPath 是恢复后的库文件。
	DBPath string
	// AttachmentsRestored 是从镜像补齐的附件文件数。
	AttachmentsRestored int
	// MissingAttachments 是镜像里也补不回来的附件 id（库里有记录、两边都没有
	// 文件；恢复不因此中止，列出来让人处理）。
	MissingAttachments []string
}

// Restore 把 dataDir 的库换成 backupFile，并从备份文件旁的镜像目录
// （<备份所在目录>\attachments\，与备份时写的镜像同源）补齐主存储里缺的
// 附件文件。port 上有服务在监听时拒绝执行。
func Restore(dataDir string, port int, backupFile string, now time.Time) (RestoreResult, error) {
	res := RestoreResult{BackupFile: backupFile, DBPath: filepath.Join(dataDir, "taskboard.db")}

	// 1. 确认服务已停止。
	if port > 0 {
		if conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 300*time.Millisecond); err == nil {
			conn.Close()
			return res, fmt.Errorf("127.0.0.1:%d 仍有服务在监听：先停止 taskboard 服务（如 nssm stop <服务名>）再恢复", port)
		}
	}
	backupFile, err := filepath.Abs(backupFile)
	if err != nil {
		return res, err
	}
	if filepath.Ext(backupFile) == ".bad" {
		return res, fmt.Errorf("%s 是被标记损坏的副本（.bad），换一个备份文件", backupFile)
	}
	if _, err := os.Stat(backupFile); err != nil {
		return res, fmt.Errorf("读不到备份文件 %s：%w", backupFile, err)
	}
	if ok, err := verifyCopy(backupFile); err != nil || !ok {
		return res, fmt.Errorf("备份文件 %s 完整性校验未通过，不能用于恢复", backupFile)
	}

	// 新机器上恢复时数据目录还不存在（#46）。
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return res, fmt.Errorf("创建数据目录 %s 失败：%w", dataDir, err)
	}

	// 2. 另存当前库（连同它的 -wal/-shm 一起挪走，避免残留在新库旁边）。
	suffix := "pre-restore-" + now.Format("20060102-150405")
	for _, name := range []string{"taskboard.db", "taskboard.db-wal", "taskboard.db-shm"} {
		src := filepath.Join(dataDir, name)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(dataDir, name+"."+suffix)
		if err := os.Rename(src, dst); err != nil {
			return res, fmt.Errorf("另存当前库 %s 失败：%w", src, err)
		}
		if name == "taskboard.db" {
			res.SavedCurrentAs = dst
		}
	}

	// 3. 换成指定的备份（复制而不是移动：备份文件本身保留）。
	if err := copyFile(backupFile, res.DBPath); err != nil {
		return res, err
	}

	// 4. 从镜像中补齐附件：库里有记录、主存储里缺文件的，从备份旁边的镜像拷回。
	// store.Open 会执行迁移：用旧备份配上新二进制时，库会升到当前版本的表结构。
	st, err := store.Open(dataDir)
	if err != nil {
		return res, fmt.Errorf("打开恢复后的库失败：%w", err)
	}
	defer st.Close()
	ids, err := attachmentIDs(st.DB)
	if err != nil {
		return res, err
	}
	mirror := filepath.Join(filepath.Dir(backupFile), "attachments")
	attsDir := filepath.Join(dataDir, "attachments")
	if err := os.MkdirAll(attsDir, 0o755); err != nil {
		return res, err
	}
	for _, id := range ids {
		dest := filepath.Join(attsDir, id)
		if _, err := os.Stat(dest); err == nil {
			continue // 主存储里已有
		}
		from := filepath.Join(mirror, id)
		if _, err := os.Stat(from); err != nil {
			res.MissingAttachments = append(res.MissingAttachments, id)
			continue
		}
		if err := copyFile(from, dest); err != nil {
			return res, err
		}
		res.AttachmentsRestored++
	}
	return res, nil
}

// attachmentIDs 列出库里有记录的附件 id（文件名就是 id）。
func attachmentIDs(db *sql.DB) ([]string, error) {
	rows, err := db.Query(`SELECT id FROM attachments ORDER BY id`)
	if err != nil {
		// 备份里还没有附件表（比 #33 更早的库）：没有附件可补。
		return nil, nil
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return ids, err
		}
		ids = append(ids, strconv.FormatInt(id, 10))
	}
	return ids, rows.Err()
}
