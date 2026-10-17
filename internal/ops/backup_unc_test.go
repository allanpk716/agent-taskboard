package ops_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/ops"
)

// 备份目标是 UNC 路径（生产上是 NAS 共享）时，副本要能通过完整性校验。
// 之前 file: URI 把 \\host\share 拼成 file://host/share，SQLite 当成 URI
// authority 报错，副本被误判为损坏改名 .bad（#46 首次部署时发现）。
// 用本机管理共享 \\localhost\C$ 模拟；访问不了（非 Windows / 非管理员）就跳过。
func Test备份目标是UNC路径时副本通过完整性校验(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("UNC 路径只在 Windows 上有意义")
	}
	local := t.TempDir()
	vol := filepath.VolumeName(local) // 形如 C:
	unc := `\\localhost\` + strings.TrimSuffix(vol, ":") + `$` + strings.TrimPrefix(local, vol)
	if _, err := os.Stat(unc); err != nil {
		t.Skipf("访问不了管理共享 %s：%v", unc, err)
	}

	b, _, _ := newBackups(t, unc)
	rec, err := b.BackupNow(context.Background(), ops.KindDaily)
	if err != nil {
		t.Fatalf("备份到 UNC 目标失败：%v", err)
	}
	if !rec.Success() {
		t.Fatalf("备份应成功：%+v", rec)
	}
	if _, err := os.Stat(filepath.Join(local, rec.Filename)); err != nil {
		t.Errorf("副本应以原名落在目标目录（不应被改名 .bad）：%v", err)
	}
}
