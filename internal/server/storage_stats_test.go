// 存储统计接口（#234）：GET /api/v1/admin/storage 返回数据库、附件目录、
// 备份目录的字节数，以及按关闭月份分组的「已关闭满 N 个月」可归档工单
// （N 由 closed_months 查询参数控制，默认 3，只是查询参数不入库）。
package server_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/ops"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// storageMonthBody 是 archivable 数组里一个月度分组。
type storageMonthBody struct {
	Month           string              `json:"month"`
	TicketCount     int64               `json:"ticket_count"`
	AttachmentBytes int64               `json:"attachment_bytes"`
	Tickets         []storageTicketBody `json:"tickets"`
}

type storageTicketBody struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	AttachmentBytes int64  `json:"attachment_bytes"`
}

// storageStatsBody 是 GET /api/v1/admin/storage 的响应结构。
type storageStatsBody struct {
	DatabaseBytes    int64              `json:"database_bytes"`
	AttachmentsBytes int64              `json:"attachments_bytes"`
	BackupsBytes     int64              `json:"backups_bytes"`
	ClosedMonths     int                `json:"closed_months"`
	Cutoff           string             `json:"cutoff"`
	Archivable       []storageMonthBody `json:"archivable"`
}

// setStorageClock 把服务的假时钟拨到指定北京时间（10:00，避开跨天边界）。
func setStorageClock(t testing.TB, s *tbtest.Server, year int, month time.Month, day int) {
	t.Helper()
	s.Clock().Set(time.Date(year, month, day, 10, 0, 0, 0, clock.Beijing))
}

// createClosedTicketAt 当前时钟下建单、传一个附件并关闭（done）。
func createClosedTicketAt(t testing.TB, s *tbtest.Server, c *http.Client, title string, attachmentSizes ...int) string {
	t.Helper()
	number := mustCreateTicket(t, s, c, title)
	for i, size := range attachmentSizes {
		mustUploadAttachment(t, s, c, number, title+"-附件-"+string(rune('a'+i)), bytesOf(size))
	}
	res := closeTicket(t, s, c, number, closeBody{Reason: "done"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("关闭 %q 状态码 = %d, 想 200", title, res.StatusCode)
	}
	return number
}

// bytesOf 造一段确定长度的内容（附件大小是外部行为的一部分）。
func bytesOf(n int) []byte {
	return make([]byte, n)
}

// 三类占用：数据库文件 > 0；附件目录随上传增加；备份一次后备份目录 > 0；
// 备份目录默认在 data_dir/backups（附件目录的兄弟目录），两边各计各的，
// 不因为都在 data_dir 下而重复计入。
func Test存储统计返回三类占用(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, admin, "带附件的工单")
	mustUploadAttachment(t, s, admin, number, "日志.txt", bytesOf(1234))

	var stats storageStatsBody
	res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	if stats.DatabaseBytes <= 0 {
		t.Errorf("database_bytes = %d, 应大于 0", stats.DatabaseBytes)
	}
	if stats.AttachmentsBytes != 1234 {
		t.Errorf("attachments_bytes = %d, 想 1234", stats.AttachmentsBytes)
	}
	if stats.BackupsBytes != 0 {
		t.Errorf("还没备份时 backups_bytes = %d, 想 0", stats.BackupsBytes)
	}
	if stats.Archivable == nil || len(stats.Archivable) != 0 {
		t.Errorf("没有已关闭工单时 archivable 应为空数组，得到 %+v", stats.Archivable)
	}

	// 再传一个附件：附件目录字节数精确增加。
	mustUploadAttachment(t, s, admin, number, "截图.png", bytesOf(100))
	stats = storageStatsBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats)
	if stats.AttachmentsBytes != 1334 {
		t.Errorf("再上传后 attachments_bytes = %d, 想 1334", stats.AttachmentsBytes)
	}

	// 做一次本机备份：备份目录 > 0，附件目录不受影响。
	if _, err := s.Backups().BackupNow(context.Background(), ops.KindDaily); err != nil {
		t.Fatal(err)
	}
	stats = storageStatsBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats)
	if stats.BackupsBytes <= 0 {
		t.Errorf("备份后 backups_bytes = %d, 应大于 0", stats.BackupsBytes)
	}
	if stats.AttachmentsBytes != 1334 {
		t.Errorf("备份不应改变 attachments_bytes = %d, 想 1334", stats.AttachmentsBytes)
	}
}

// 可归档口径：当前状态是已关闭、且最近一次关闭时间 <= cutoff（now 减 N 个月），
// 按关闭时间的北京时间月份分组（新月份在前）；未关闭的老工单不算，
// 重开后再关闭的以最近一次关闭时间为准。
func Test可归档工单按关闭月份分组(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	// 3 月关：可归档（默认 N=3 时 cutoff 是 2026-07-16）。
	setStorageClock(t, s, 2026, time.March, 2)
	createClosedTicketAt(t, s, admin, "三月关闭", 25)

	// 5 月关两张：一张带 100+50 字节附件，一张不带。
	setStorageClock(t, s, 2026, time.May, 10)
	createClosedTicketAt(t, s, admin, "五月关闭-带附件", 100, 50)
	setStorageClock(t, s, 2026, time.May, 20)
	createClosedTicketAt(t, s, admin, "五月关闭-无附件")

	// 未关闭的老工单：无论多老都不算。
	setStorageClock(t, s, 2026, time.April, 5)
	mustCreateTicket(t, s, admin, "一直开着的老工单")

	// 曾经关闭过、现在开着的老工单：当前状态未关闭，不算。
	setStorageClock(t, s, 2026, time.February, 1)
	reopened := mustCreateTicket(t, s, admin, "关闭过又重开的老工单")
	if res := closeTicket(t, s, admin, reopened, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭状态码 = %d, 想 200", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.September, 20)
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+reopened+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重开状态码 = %d, 想 200", res.StatusCode)
	}

	// 8 月关闭：默认 N=3 时还不够老。
	setStorageClock(t, s, 2026, time.August, 20)
	createClosedTicketAt(t, s, admin, "八月关闭")

	// 重开后又关闭：以最近一次关闭时间（2026-09-05）计。
	setStorageClock(t, s, 2026, time.April, 1)
	reclosed := mustCreateTicket(t, s, admin, "重开后再关闭")
	if res := closeTicket(t, s, admin, reclosed, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("关闭状态码 = %d, 想 200", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.June, 1)
	if res := s.Do(t, admin, http.MethodPost, "/api/v1/tickets/"+reclosed+"/reopen", nil, nil); res.StatusCode != http.StatusOK {
		t.Fatalf("重开状态码 = %d, 想 200", res.StatusCode)
	}
	setStorageClock(t, s, 2026, time.September, 5)
	if res := closeTicket(t, s, admin, reclosed, closeBody{Reason: "done"}); res.StatusCode != http.StatusOK {
		t.Fatalf("再次关闭状态码 = %d, 想 200", res.StatusCode)
	}

	// 查询时刻：2026-10-16 09:00，默认 N=3 → cutoff 2026-07-16 09:00。
	s.Clock().Set(time.Date(2026, 10, 16, 9, 0, 0, 0, clock.Beijing))
	var stats storageStatsBody
	res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	if stats.ClosedMonths != 3 {
		t.Errorf("默认 closed_months = %d, 想 3", stats.ClosedMonths)
	}
	if stats.Cutoff != "2026-07-16T09:00:00+08:00" {
		t.Errorf("cutoff = %q, 想 2026-07-16T09:00:00+08:00", stats.Cutoff)
	}
	want := []storageMonthBody{
		{Month: "2026-05", TicketCount: 2, AttachmentBytes: 150},
		{Month: "2026-03", TicketCount: 1, AttachmentBytes: 25},
	}
	assertStorageMonths(t, stats.Archivable, want)

	// N=1：cutoff 2026-09-16，八月与「重开后再关闭」（2026-09-05）都进来。
	stats = storageStatsBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage?closed_months=1", nil, &stats)
	if stats.ClosedMonths != 1 || stats.Cutoff != "2026-09-16T09:00:00+08:00" {
		t.Fatalf("closed_months=1 时 closed_months/cutoff = %d/%q", stats.ClosedMonths, stats.Cutoff)
	}
	assertStorageMonths(t, stats.Archivable, []storageMonthBody{
		{Month: "2026-09", TicketCount: 1, AttachmentBytes: 0},
		{Month: "2026-08", TicketCount: 1, AttachmentBytes: 0},
		{Month: "2026-05", TicketCount: 2, AttachmentBytes: 150},
		{Month: "2026-03", TicketCount: 1, AttachmentBytes: 25},
	})

	// N=6：cutoff 2026-04-16，只剩三月那张。
	stats = storageStatsBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage?closed_months=6", nil, &stats)
	assertStorageMonths(t, stats.Archivable, []storageMonthBody{
		{Month: "2026-03", TicketCount: 1, AttachmentBytes: 25},
	})

	// 推进时钟到 2027-03-16：原本不满足的「重开后再关闭」（2026-09-05）
	// 变为满足，且按最近一次关闭时间落在 2026-09。
	s.Clock().Set(time.Date(2027, time.March, 16, 9, 0, 0, 0, clock.Beijing))
	// 网页会话有时效，推进几个月后重新登录再查。
	admin = s.Login("陈工", "secret-pass")
	stats = storageStatsBody{}
	s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats)
	if stats.Cutoff != "2026-12-16T09:00:00+08:00" {
		t.Fatalf("推进后 cutoff = %q, 想 2026-12-16T09:00:00+08:00", stats.Cutoff)
	}
	if len(stats.Archivable) == 0 || stats.Archivable[0].Month != "2026-09" || stats.Archivable[0].TicketCount != 1 {
		t.Fatalf("推进后最新的月份应是 2026-09（重开后再关闭那张）：%+v", stats.Archivable)
	}
}

// 统计页要支持「按月整批、逐张勾选或排除」，所以每个月度分组里必须带
// 可归档工单的编号与标题；仍打开的工单即使更老也不进入候选列表。
func Test存储统计月份带可逐张选择的工单清单(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	setStorageClock(t, s, 2026, time.May, 10)
	first := createClosedTicketAt(t, s, admin, "五月第一张", 25)
	setStorageClock(t, s, 2026, time.May, 20)
	second := createClosedTicketAt(t, s, admin, "五月第二张")
	setStorageClock(t, s, 2026, time.June, 1)
	_ = mustCreateTicket(t, s, admin, "六月仍打开")
	s.Clock().Set(time.Date(2026, time.October, 16, 9, 0, 0, 0, clock.Beijing))
	admin = s.Login("陈工", "secret-pass")

	var stats storageStatsBody
	res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage", nil, &stats)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	if len(stats.Archivable) != 1 {
		t.Fatalf("archivable = %+v, 想只有 2026-05 一组", stats.Archivable)
	}
	got := stats.Archivable[0].Tickets
	want := []storageTicketBody{
		{Number: first, Title: "五月第一张", AttachmentBytes: 25},
		{Number: second, Title: "五月第二张", AttachmentBytes: 0},
	}
	if len(got) != len(want) {
		t.Fatalf("月份工单清单 = %+v, 想 %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("月份工单清单[%d] = %+v, 想 %+v", i, got[i], want[i])
		}
	}
}

// assertStorageMonths 逐项断言月度分组（顺序也要求：新月份在前）。
func assertStorageMonths(t testing.TB, got, want []storageMonthBody) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("archivable = %+v, 想 %+v", got, want)
	}
	for i := range want {
		if got[i].Month != want[i].Month || got[i].TicketCount != want[i].TicketCount ||
			got[i].AttachmentBytes != want[i].AttachmentBytes {
			t.Errorf("archivable[%d] = %+v, 想 %+v", i, got[i], want[i])
		}
	}
}

// closed_months 省略时默认 3；必须是 1–120 的整数，否则 400。
func Test存储统计closedMonths参数校验(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")

	for _, raw := range []string{"0", "abc", "121", "-1", "1.5"} {
		res := s.Do(t, admin, http.MethodGet, "/api/v1/admin/storage?closed_months="+raw, nil, nil)
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("closed_months=%q 状态码 = %d, 想 400", raw, res.StatusCode)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "invalid_closed_months" {
			t.Errorf("closed_months=%q 错误码 = %q, 想 invalid_closed_months", raw, got.Error.Code)
		}
	}
}

// 存储统计只对管理员的网页会话开放：匿名 401，访客 403 members_only，
// 普通成员 403 admin_required，Agent token 403 web_session_required
// （与成员账号管理、登录锁定这些管理端点一致）。
func Test存储统计只对管理员开放(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	admin := s.Login("陈工", "secret-pass")
	member := s.Login("王工", "member-pass")
	_, token := s.IssueAgentToken(t, admin, "秘书")
	adminAgent := s.AgentClient(token)
	guest := s.GuestClient(tbguestKey(t, s, admin))

	cases := []struct {
		name string
		c    *http.Client
		code string
		want int
	}{
		{"匿名", s.NewClient(), "unauthenticated", http.StatusUnauthorized},
		{"访客", guest, "members_only", http.StatusForbidden},
		{"普通成员", member, "admin_required", http.StatusForbidden},
		{"管理员Agent", adminAgent, "web_session_required", http.StatusForbidden},
	}
	for _, tc := range cases {
		res := s.Do(t, tc.c, http.MethodGet, "/api/v1/admin/storage", nil, nil)
		if res.StatusCode != tc.want {
			t.Errorf("%s 状态码 = %d, 想 %d", tc.name, res.StatusCode, tc.want)
			continue
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != tc.code {
			t.Errorf("%s 错误码 = %q, 想 %q", tc.name, got.Error.Code, tc.code)
		}
	}
}
