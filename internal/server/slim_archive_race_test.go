// S5：瘦身与归档并发的交错场景。两个模块各自一把互斥锁，但可能改同一批
// 附件；这里按可能的交错顺序逐个验证：数据库事务内的重新校验必须发现
// 对方提交过的变化并拒绝（stale），不能误删数据或留下孤儿文件。
package server_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// slimRaceFixture 造一张已关闭满 3 个月、带一个附件的工单。
type slimRaceFixture struct {
	s       *tbtest.Server
	admin   *http.Client
	member  *http.Client
	number  string
	att     attachmentUploadBody
	attPath string
}

func newSlimRaceFixture(t testing.TB, title string) *slimRaceFixture {
	t.Helper()
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	member := s.Login("王工", "member-pass")

	setStorageClock(t, s, 2026, time.June, 2)
	number := mustCreateTicket(t, s, member, title)
	att := mustUploadAttachment(t, s, member, number, "服务日志.log", []byte("race-body"))
	if res := revise(t, s, member, number, map[string]any{
		"attachment_ids": []string{att.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂说明附件状态码 = %d", res.StatusCode)
	}
	if res := closeTicket(t, s, member, number, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭工单状态码 = %d", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.October, 16)
	return &slimRaceFixture{
		s: s, admin: s.Login("陈工", "secret-pass"), member: s.Login("王工", "member-pass"),
		number: number, att: att,
		attPath: filepath.Join(s.DataDir(), "attachments", strings.TrimPrefix(att.ID, "att_")),
	}
}

func (f *slimRaceFixture) generateArchive(t testing.TB) archivePackageBody {
	t.Helper()
	var pkg archivePackageBody
	if res := f.s.Do(t, f.admin, http.MethodPost, "/api/v1/admin/archive/packages",
		map[string]any{"ticket_numbers": []string{f.number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成归档包状态码 = %d", res.StatusCode)
	}
	return pkg
}

func (f *slimRaceFixture) generateSlim(t testing.TB) slimPackageBody {
	t.Helper()
	var pkg slimPackageBody
	if res := f.s.Do(t, f.admin, http.MethodPost, "/api/v1/admin/slim/packages",
		map[string]any{"ticket_numbers": []string{f.number}}, &pkg); res.StatusCode != http.StatusCreated {
		t.Fatalf("生成瘦身包状态码 = %d", res.StatusCode)
	}
	return pkg
}

func (f *slimRaceFixture) removeArchive(t testing.TB, pkg archivePackageBody) *http.Response {
	t.Helper()
	return f.s.Do(t, f.admin, http.MethodPost,
		"/api/v1/admin/archive/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil)
}

func (f *slimRaceFixture) removeSlim(t testing.TB, pkg slimPackageBody) *http.Response {
	t.Helper()
	return f.s.Do(t, f.admin, http.MethodPost,
		"/api/v1/admin/slim/packages/"+pkg.Name+"/remove", map[string]any{"saved": true}, nil)
}

// 交错一：归档包生成后、归档移出前发生瘦身移出。附件瘦身状态变了，
// 归档移出的内容指纹重算必须拒绝（stale），工单与瘦身标记都保持原样。
func Test归档移出能发现生成后的瘦身移出(t *testing.T) {
	f := newSlimRaceFixture(t, "归档后瘦身移出")
	archivePkg := f.generateArchive(t)

	slimPkg := f.generateSlim(t)
	if res := f.removeSlim(t, slimPkg); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d", res.StatusCode)
	}

	res := f.removeArchive(t, archivePkg)
	var rejected errorBody
	tbtest.DecodeJSON(t, res, &rejected)
	if res.StatusCode != http.StatusConflict || rejected.Error.Code != "archive_package_stale" {
		t.Fatalf("归档移出 = %d/%s，想 409 archive_package_stale", res.StatusCode, rejected.Error.Code)
	}
	var detail slimDetailBody
	if res := f.s.Do(t, f.member, http.MethodGet, "/api/v1/tickets/"+f.number, nil, &detail); res.StatusCode != http.StatusOK {
		t.Fatalf("被拒后工单应仍在平台，状态码 = %d", res.StatusCode)
	}
	if detail.Slim.Status != "full" {
		t.Fatalf("被拒后瘦身状态 = %q，想 full", detail.Slim.Status)
	}
	if _, err := os.Stat(f.attPath); !os.IsNotExist(err) {
		t.Fatalf("被拒后瘦身移出结果不应回滚：%v", err)
	}
}

// 交错二：归档包生成后、归档移出前发生瘦身恢复。归档包里没有这份附件
// 文件，移出必须拒绝，恢复出来的文件不能被归档移出顺带删掉。
func Test归档移出能发现生成后的瘦身恢复(t *testing.T) {
	f := newSlimRaceFixture(t, "归档后瘦身恢复")
	slimPkg := f.generateSlim(t)
	if res := f.removeSlim(t, slimPkg); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d", res.StatusCode)
	}
	pkgRes := f.s.Do(t, f.admin, http.MethodGet, slimPkg.DownloadURL, nil, nil)
	if pkgRes.StatusCode != http.StatusOK {
		t.Fatalf("下载瘦身包状态码 = %d", pkgRes.StatusCode)
	}
	pkgBytes, err := io.ReadAll(pkgRes.Body)
	if err != nil {
		t.Fatal(err)
	}

	archivePkg := f.generateArchive(t)
	res, got := restoreSlim(t, f.s, f.admin, pkgBytes, f.number)
	if res.StatusCode != http.StatusOK || len(got.Restored) != 1 {
		t.Fatalf("瘦身恢复 = %d/%+v", res.StatusCode, got)
	}

	res2 := f.removeArchive(t, archivePkg)
	var rejected errorBody
	tbtest.DecodeJSON(t, res2, &rejected)
	if res2.StatusCode != http.StatusConflict || rejected.Error.Code != "archive_package_stale" {
		t.Fatalf("归档移出 = %d/%s，想 409 archive_package_stale", res2.StatusCode, rejected.Error.Code)
	}
	if _, err := os.Stat(f.attPath); err != nil {
		t.Fatalf("恢复出来的附件文件不应被拒绝的归档移出删除：%v", err)
	}
	var detail slimDetailBody
	f.s.Do(t, f.member, http.MethodGet, "/api/v1/tickets/"+f.number, nil, &detail)
	if detail.Slim.Status != "none" {
		t.Fatalf("归档移出被拒后瘦身状态 = %q，想 none", detail.Slim.Status)
	}
}

// 交错三：瘦身包生成后、确认移出前工单被整张归档移除。瘦身移出的
// 重新校验查不到工单，必须拒绝，不能给已删除数据打孤儿标记。
func Test瘦身移出在工单归档移除后拒绝(t *testing.T) {
	f := newSlimRaceFixture(t, "归档先移除")
	slimPkg := f.generateSlim(t)

	archivePkg := f.generateArchive(t)
	if res := f.removeArchive(t, archivePkg); res.StatusCode != http.StatusOK {
		t.Fatalf("归档移除状态码 = %d", res.StatusCode)
	}

	res := f.removeSlim(t, slimPkg)
	var rejected errorBody
	tbtest.DecodeJSON(t, res, &rejected)
	if res.StatusCode != http.StatusConflict || rejected.Error.Code != "slim_package_stale" {
		t.Fatalf("瘦身移出 = %d/%s，想 409 slim_package_stale", res.StatusCode, rejected.Error.Code)
	}
	var operations adminOperationsBody
	f.s.Do(t, f.admin, http.MethodGet, "/api/v1/admin/operations?limit=20", nil, &operations)
	for _, op := range operations.Operations {
		if op.OperationType == "slim_remove" {
			t.Fatalf("工单已归档移除后不应写 slim_remove：%+v", op)
		}
	}
}

// 交错四：恢复瘦身包时工单已被整张归档移除。检查按工单说明不可恢复，
// 执行恢复只产出拒绝结果，不往已删除工单写回文件。
func Test瘦身恢复在工单归档移除后拒绝(t *testing.T) {
	f := newSlimRaceFixture(t, "恢复时已归档")
	slimPkg := f.generateSlim(t)
	if res := f.removeSlim(t, slimPkg); res.StatusCode != http.StatusOK {
		t.Fatalf("确认瘦身状态码 = %d", res.StatusCode)
	}
	pkgRes := f.s.Do(t, f.admin, http.MethodGet, slimPkg.DownloadURL, nil, nil)
	if pkgRes.StatusCode != http.StatusOK {
		t.Fatalf("下载瘦身包状态码 = %d", pkgRes.StatusCode)
	}
	pkgBytes, err := io.ReadAll(pkgRes.Body)
	if err != nil {
		t.Fatal(err)
	}

	archivePkg := f.generateArchive(t)
	if res := f.removeArchive(t, archivePkg); res.StatusCode != http.StatusOK {
		t.Fatalf("归档移除状态码 = %d", res.StatusCode)
	}

	inspectRes, inspection := postSlimRestore(t, f.s, f.admin,
		"/api/v1/admin/slim/restore/inspect", pkgBytes)
	if inspectRes.StatusCode != http.StatusOK || len(inspection.Tickets) != 1 ||
		inspection.Tickets[0].Restorable || !strings.Contains(inspection.Tickets[0].Reason, "工单已不在平台") {
		t.Fatalf("恢复检查 = %d/%+v，想提示工单已不在平台", inspectRes.StatusCode, inspection)
	}
	res, got := restoreSlim(t, f.s, f.admin, pkgBytes, f.number)
	if res.StatusCode != http.StatusOK || len(got.Restored) != 0 ||
		len(got.Rejected) != 1 || !strings.Contains(got.Rejected[0].Reason, "工单已不在平台") {
		t.Fatalf("恢复结果 = %d/%+v，想只拒绝不恢复", res.StatusCode, got)
	}
	if _, err := os.Stat(f.attPath); !os.IsNotExist(err) {
		t.Fatalf("不应向已归档移除的工单写回附件文件：%v", err)
	}
}
