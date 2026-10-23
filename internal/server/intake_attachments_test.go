package server_test

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type uploadedAttachmentBody struct {
	ID       string `json:"id"`
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	State    string `json:"state"`
	Markdown string `json:"markdown"`
}

func uploadIntakeAttachment(t testing.TB, s *tbtest.Server, remoteAddr, slug, key, filename string, content []byte, headers map[string]string) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, s.URL()+"/api/v1/intakes/"+slug+"/attachments", &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Idempotency-Key", key)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Transport: fixedRemoteTransport{remote: remoteAddr}}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

type fixedRemoteTransport struct{ remote string }

func (t fixedRemoteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.RemoteAddr = t.remote
	return http.DefaultTransport.RoundTrip(clone)
}

func mustUploadIntake(t testing.TB, s *tbtest.Server, slug, key, filename string, content []byte) uploadedAttachmentBody {
	t.Helper()
	res := uploadIntakeAttachment(t, s, "127.0.0.1:12345", slug, key, filename, content, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("入口上传状态码 = %d：%s", res.StatusCode, readAll(t, res))
	}
	var got uploadedAttachmentBody
	tbtest.DecodeJSON(t, res, &got)
	return got
}

func Test入口附件上传提交时认领且无说明修订事件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	sourceFields := bugFields()
	fields := map[string]any{}
	for k, v := range sourceFields {
		fields[k] = v
	}
	att := mustUploadIntake(t, s, "bug", "submit-key", "报警截图.png", []byte("png"))
	fields["出错的例子"] = fields["出错的例子"].(string) + "\n\n![报警截图.png](att:" + att.ID + ")"
	res := s.DoWithHeaders(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/bug/tickets",
		map[string]string{"Idempotency-Key": "submit-key"}, map[string]any{
			"title": "入口附件提交", "fields": fields, "author_name": "赵六",
		}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("入口提交状态码 = %d：%s", res.StatusCode, readAll(t, res))
	}
	var submitted intakeSubmitBody
	tbtest.DecodeJSON(t, res, &submitted)

	var full struct {
		ticketResponseBody
		attachmentsTicketBody
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+submitted.Ticket.Number, nil, &full)
	if !strings.Contains(full.Brief.Markdown, "(att:"+att.ID+")") {
		t.Fatalf("说明未带入口附件：%s", full.Brief.Markdown)
	}
	if len(full.Attachments) != 1 || full.Attachments[0].AttachedTo != "说明" {
		t.Fatalf("入口附件未挂到说明：%+v", full.Attachments)
	}
	for _, ev := range full.Timeline {
		if strings.Contains(ev.Type, "brief") || strings.Contains(ev.Type, "修订") {
			t.Fatalf("不应产生说明修订事件：%+v", ev)
		}
	}
}

func Test入口附件按入口和幂等钥防串用(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	createIntake(t, s, web, map[string]any{"slug": "bug2", "name": "另一入口", "template": "bug-report"})
	att := mustUploadIntake(t, s, "bug", "right-key", "照片.png", []byte("png"))

	for name, tc := range map[string]struct{ slug, key string }{
		"别的幂等键": {"bug", "wrong-key"},
		"别的入口":  {"bug2", "right-key"},
	} {
		fields := bugFields()
		fields["出错的例子"] += " ![照片](att:" + att.ID + ")"
		res := s.DoWithHeaders(t, s.NewClient(), http.MethodPost, "/api/v1/intakes/"+tc.slug+"/tickets",
			map[string]string{"Idempotency-Key": tc.key}, map[string]any{
				"title": name, "fields": fields, "author_name": "赵六",
			}, nil)
		if res.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("[%s] 状态码 = %d, 想 422", name, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "attachment_not_usable" {
			t.Fatalf("[%s] 错误码 = %s", name, got.Error.Code)
		}
	}
}

func Test入口上传按入口和IP限流并信任回环反代(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithIntake(1, 200, 200))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	first := uploadIntakeAttachment(t, s, "127.0.0.1:1", "bug", "k", "a.png", []byte("a"), map[string]string{"X-Forwarded-For": "10.1.1.1"})
	second := uploadIntakeAttachment(t, s, "127.0.0.1:1", "bug", "k", "b.png", []byte("b"), map[string]string{"X-Forwarded-For": "10.1.1.1"})
	if first.StatusCode != http.StatusCreated || second.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("回环代理限流状态 = %d/%d, 想 201/429", first.StatusCode, second.StatusCode)
	}
	if second.Header.Get("Retry-After") == "" {
		t.Fatal("429 缺 Retry-After")
	}
}

func Test入口未提交附件受临时配额限制(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithIntake(20, 1, 200))
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	res := uploadIntakeAttachment(t, s, "127.0.0.1:1", "bug", "k", "big.bin", bytes.Repeat([]byte("x"), (1<<20)+1), nil)
	if res.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("状态码 = %d, 想 413", res.StatusCode)
	}
	var got errorBody
	tbtest.DecodeJSON(t, res, &got)
	if got.Error.Code != "intake_pending_quota_exceeded" {
		t.Fatalf("错误码 = %s", got.Error.Code)
	}
}

func Test入口未提交附件二十四小时后清除(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	att := mustUploadIntake(t, s, "bug", "k", "待清除.png", []byte("x"))
	id := strings.TrimPrefix(att.ID, "att_")
	path := filepath.Join(s.DataDir(), "attachments", id)
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	s.Clock().Advance(24 * time.Hour)
	if n, err := s.Attachments().CleanupUnreferenced(context.Background()); err != nil || n != 1 {
		t.Fatalf("清除 = %d/%v, 想 1", n, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("文件未清除：%v", err)
	}
}

func Test入口附件CHECK拦住无归属行(t *testing.T) {
	s := tbtest.Start(t)
	if _, err := s.Store().DB.Exec(
		`INSERT INTO attachments (filename,size,uploaded_at) VALUES ('bad',1,'2026-10-20T00:00:00+08:00')`); err == nil {
		t.Fatal("CHECK 应拦住 ticket_seq 与 uploader_intake_id 都为空的行")
	}
}
