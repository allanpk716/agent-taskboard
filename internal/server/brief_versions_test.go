package server_test

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

type briefVersionBody struct {
	Revision      int64             `json:"revision"`
	Title         string            `json:"title"`
	Markdown      string            `json:"markdown"`
	Fields        map[string]string `json:"fields"`
	AttachmentIDs []string          `json:"attachment_ids"`
	Author        struct {
		Member *struct{ Name string } `json:"member"`
		Agent  *struct{ Name string } `json:"agent"`
	} `json:"author"`
	At string `json:"at"`
}

func Test并发同一说明版本只有一个修订成功(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "并发修订")

	const n = 8
	statuses := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res := revise(t, s, web, number, map[string]any{
				"brief_md": fmt.Sprintf("第 %d 份修订", i+1), "base_revision": 1,
			})
			statuses[i] = res.StatusCode
		}(i)
	}
	wg.Wait()
	ok, conflict := 0, 0
	for _, code := range statuses {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusConflict:
			conflict++
		default:
			t.Fatalf("并发修订状态码 = %d, 想 200 或 409", code)
		}
	}
	if ok != 1 || conflict != n-1 {
		t.Fatalf("成功/冲突 = %d/%d, 想 1/%d", ok, conflict, n-1)
	}
	var list struct {
		Versions []briefVersionBody `json:"versions"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/brief-versions", nil, &list)
	if len(list.Versions) != 2 || list.Versions[1].Revision != 2 {
		t.Fatalf("并发后版本行不对：%+v", list.Versions)
	}
}

func Test访客可读取说明版本且跨工单仍不可见(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	number := mustCreateTicket(t, s, web, "访客看版本")
	other := mustCreateTicket(t, s, web, "另一张工单")
	link := s.CreateGuestLink(t, web, number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(link.Key(t))

	res := revise(t, s, web, number, map[string]any{"brief_md": "访客可见的新版", "base_revision": 1})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("修订状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number+"/brief-versions", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客读版本列表状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+number+"/brief-versions/2", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("访客读版本快照状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, guest, http.MethodGet, "/api/v1/tickets/"+other+"/brief-versions", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("访客读其他工单版本状态码 = %d, 想 404", res.StatusCode)
	}
}

func Test说明版本API返回建单与修订快照(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	_, token := s.IssueAgentToken(t, web, "笔记本")
	agent := s.AgentClient(token)
	number := mustCreateTicket(t, s, web, "升级固件")

	res := s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/brief-versions", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("列表状态码 = %d, 想 200", res.StatusCode)
	}
	var list struct {
		Versions []briefVersionBody `json:"versions"`
	}
	tbtest.DecodeJSON(t, res, &list)
	if len(list.Versions) != 1 || list.Versions[0].Revision != 1 || list.Versions[0].Title != "升级固件" {
		t.Fatalf("第 1 版快照不对：%+v", list.Versions)
	}
	if list.Versions[0].Author.Member == nil || list.Versions[0].Author.Member.Name != "陈工" {
		t.Fatalf("第 1 版作者不对：%+v", list.Versions[0].Author)
	}

	res = revise(t, s, agent, number, map[string]any{"brief_md": "新版说明", "base_revision": 1})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("修订状态码 = %d, 想 200", res.StatusCode)
	}
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+number+"/brief-versions/2", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("快照状态码 = %d, 想 200", res.StatusCode)
	}
	var version briefVersionBody
	tbtest.DecodeJSON(t, res, &version)
	if version.Markdown != "新版说明" || version.Author.Agent == nil || version.Author.Agent.Name != "笔记本" {
		t.Fatalf("第 2 版快照不对：%+v", version)
	}
}

func Test说明版本快照保留字段和被移除附件(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	web := s.Login("陈工", "secret-pass")
	res := s.Do(t, web, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title":    "模板说明版本",
		"template": "bug-report",
		"fields": map[string]string{
			"哪台仪器？":    "UD-2300",
			"什么时候发生的？": "今天上午",
			"你的猜测或补充？": "疑似试剂问题",
		},
	}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建模板工单状态码 = %d", res.StatusCode)
	}
	var created struct {
		Number string `json:"number"`
	}
	tbtest.DecodeJSON(t, res, &created)
	att := mustUploadAttachment(t, s, web, created.Number, "旧日志.log", []byte("old-log"))
	if res := revise(t, s, web, created.Number, map[string]any{
		"attachment_ids": []string{att.ID}, "base_revision": 1,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("挂附件状态码 = %d", res.StatusCode)
	}
	if res := revise(t, s, web, created.Number, map[string]any{
		"fields": map[string]string{"什么时候发生的？": "今天下午"}, "attachment_ids": []string{}, "base_revision": 2,
	}); res.StatusCode != http.StatusOK {
		t.Fatalf("修订状态码 = %d", res.StatusCode)
	}

	var v3 briefVersionBody
	res = s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number+"/brief-versions/3", nil, &v3)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("读第 3 版状态码 = %d", res.StatusCode)
	}
	if v3.Title != "模板说明版本" || v3.Fields["哪台仪器？"] != "UD-2300" || v3.Fields["什么时候发生的？"] != "今天下午" {
		t.Fatalf("第 3 版快照不对：%+v", v3)
	}

	var ticket struct {
		Attachments []attachmentViewBody `json:"attachments"`
	}
	s.Do(t, web, http.MethodGet, "/api/v1/tickets/"+created.Number, nil, &ticket)
	var oldAtt *attachmentViewBody
	for i := range ticket.Attachments {
		if ticket.Attachments[i].ID == att.ID {
			oldAtt = &ticket.Attachments[i]
		}
	}
	if oldAtt == nil {
		t.Fatalf("被移除的附件不在工单附件投影里：%+v", ticket.Attachments)
	}
	download := s.Do(t, web, http.MethodGet, oldAtt.URL, nil, nil)
	if download.StatusCode != http.StatusOK {
		t.Fatalf("下载被移除附件状态码 = %d", download.StatusCode)
	}
	body, err := io.ReadAll(download.Body)
	if err != nil || string(body) != "old-log" {
		t.Fatalf("下载内容不对：%q (err=%v)", body, err)
	}
}
