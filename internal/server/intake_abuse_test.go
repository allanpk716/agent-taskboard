package server_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func postIntakeFrom(t testing.TB, s *tbtest.Server, remoteAddr, slug string, headers map[string]string, n int) *http.Response {
	t.Helper()
	client := &http.Client{Transport: fixedRemoteTransport{remote: remoteAddr}}
	return s.DoWithHeaders(t, client, http.MethodPost, "/api/v1/intakes/"+slug+"/tickets",
		headers, map[string]any{
			"title":       "防刷提交",
			"fields":      bugFields(),
			"author_name": "赵六",
			"n":           n,
		}, nil)
}

func Test入口提交按入口和IP限流(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithIntake(1, 200, 200))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	headers := map[string]string{"X-Forwarded-For": "10.1.1.1"}

	first := postIntakeFrom(t, s, "127.0.0.1:1", "bug", headers, 1)
	second := postIntakeFrom(t, s, "127.0.0.1:1", "bug", headers, 2)
	otherIP := postIntakeFrom(t, s, "127.0.0.1:1", "bug",
		map[string]string{"X-Forwarded-For": "10.1.1.2"}, 3)
	otherIntake := createIntake(t, s, web, map[string]any{"slug": "bug2", "name": "另一入口", "template": "bug-report"})
	_ = otherIntake
	sameIPOtherIntake := postIntakeFrom(t, s, "127.0.0.1:1", "bug2", headers, 4)

	if first.StatusCode != http.StatusCreated || second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("同入口同 IP = %d/%d, 想 201/429", first.StatusCode, second.StatusCode)
	}
	if otherIP.StatusCode != http.StatusCreated || sameIPOtherIntake.StatusCode != http.StatusCreated {
		t.Fatalf("不同 IP/入口应互不影响 = %d/%d, 想 201/201", otherIP.StatusCode, sameIPOtherIntake.StatusCode)
	}
}

func Test入口达到每日上限后暂停并通知管理员次日恢复(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithIntake(100, 200, 2))
	s.CreateAdmin("陈工", "secret-pass")
	admin := s.Login("陈工", "secret-pass")
	createIntake(t, s, admin, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})

	for i := 0; i < 2; i++ {
		if res := postIntakeFrom(t, s, "127.0.0.1:1", "bug", nil, i); res.StatusCode != http.StatusCreated {
			t.Fatalf("前两笔提交 %d = %d, 想 201", i, res.StatusCode)
		}
	}
	paused := postIntakeFrom(t, s, "127.0.0.1:1", "bug", nil, 2)
	var got errorBody
	tbtest.DecodeJSON(t, paused, &got)
	if paused.StatusCode != http.StatusTooManyRequests || got.Error.Code != "intake_paused" {
		t.Fatalf("第三笔 = %d/%s, 想 429/intake_paused", paused.StatusCode, got.Error.Code)
	}
	if !strings.Contains(got.Error.Hint, "入口今日已暂停") || !strings.Contains(got.Error.Hint, "联系部门") {
		t.Fatalf("暂停提示 = %q", got.Error.Hint)
	}

	var notes notificationsBody
	s.Do(t, admin, http.MethodGet, "/api/v1/notifications?limit=10", nil, &notes)
	var pauses []notificationBody
	for _, n := range notes.Items {
		if n.Reason == "intake_paused" {
			pauses = append(pauses, n)
		}
	}
	if len(pauses) != 1 || !strings.Contains(pauses[0].Text, "部门 Bug 反馈") ||
		!strings.Contains(pauses[0].Text, "今日已暂停") {
		t.Fatalf("暂停系统通知 = %+v, 想一条入口暂停通知", pauses)
	}

	s.Clock().Advance(24 * time.Hour)
	nextDay := postIntakeFrom(t, s, "127.0.0.1:1", "bug", nil, 3)
	if nextDay.StatusCode != http.StatusCreated {
		t.Fatalf("次日提交 = %d, 想 201", nextDay.StatusCode)
	}
}

func Test访客附件超限提示联系成员而成员保留NAS提示(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithAttachments(1, 5, 10, 50))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "附件提示对照")
	link := s.CreateGuestLink(t, web, number, map[string]any{"note": "检验科-张三", "valid_days": 30})
	guest := s.GuestClient(link.Key(t))
	// 超过 multipart 粗粒度上限（max_file_mb + 1MB），直接走 MaxBytesReader 分支。
	big := make([]byte, 2*(1<<20)+1)

	memberRes := uploadAttachment(t, s, web, number, "成员大文件.zip", big)
	if memberRes.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("成员上传 = %d, 想 413", memberRes.StatusCode)
	}
	var memberErr errorBody
	tbtest.DecodeJSON(t, memberRes, &memberErr)
	if !strings.Contains(memberErr.Error.Hint, "NAS") {
		t.Fatalf("成员超限提示 = %q, 想保留 NAS 提示", memberErr.Error.Hint)
	}

	guestRes := uploadAttachment(t, s, guest, number, "访客大文件.zip", big)
	if guestRes.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("访客上传 = %d, 想 413", guestRes.StatusCode)
	}
	var guestErr errorBody
	tbtest.DecodeJSON(t, guestRes, &guestErr)
	if !strings.Contains(guestErr.Error.Hint, "请压缩或联系对接成员") || strings.Contains(guestErr.Error.Hint, "NAS") {
		t.Fatalf("访客超限提示 = %q, 想请压缩或联系对接成员", guestErr.Error.Hint)
	}
}
