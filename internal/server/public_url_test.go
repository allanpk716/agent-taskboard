package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 配置了 [server] public_url 时（评审 P2-7，#41「复制链接」/#32）：访客链接
// 与入口提交回给用户的链接是绝对地址（可以直接复制给别人），share_text 里的
// /t/<编号> 同样换成绝对地址；没配置时保持相对路径（现有测试覆盖）。
func Test配置public_url时访客链接与shareText用绝对地址(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithPublicURL("https://taskboard.urithub.com/")) // 结尾斜杠也能容忍
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("赵工", "secret-pass")
	s.CreateMember("王工", "secret-pass")
	web := s.Login("陈工", "secret-pass")

	// 建单指派：share_text 里用绝对地址。
	var created struct {
		Number    string  `json:"number"`
		ShareText *string `json:"share_text"`
	}
	res := s.Do(t, web, http.MethodPost, "/api/v1/tickets",
		map[string]any{"kind": "task", "title": "升级 UD-2300 固件", "brief_md": "x",
			"labels": []string{"机型:UD-2300"}, "assignee": "赵工"}, &created)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d, 想 201", res.StatusCode)
	}
	if created.ShareText == nil || !strings.Contains(*created.ShareText, "https://taskboard.urithub.com/t/20261016-001") {
		t.Errorf("share_text 应包含绝对工单地址：%v", created.ShareText)
	}

	// 访客链接创建：url 是绝对地址（复制即用）。
	var link struct {
		URL string `json:"url"`
	}
	res = s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+created.Number+"/guest-links",
		map[string]any{"note": "检验科-张三", "valid_days": 7}, &link)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("建访客链接状态码 = %d, 想 201", res.StatusCode)
	}
	if !strings.HasPrefix(link.URL, "https://taskboard.urithub.com/g/") || strings.Contains(link.URL, "//g/") {
		t.Errorf("访客链接应是绝对地址（无重复斜杠）：%q", link.URL)
	}

	// 改派 share_text 同样用绝对地址。
	var reassigned struct {
		ShareText *string `json:"share_text"`
	}
	s.Do(t, web, http.MethodPost, "/api/v1/tickets/"+created.Number+"/assign",
		map[string]string{"assignee": "王工"}, &reassigned)
	if reassigned.ShareText == nil || !strings.Contains(*reassigned.ShareText, "https://taskboard.urithub.com/t/20261016-001") {
		t.Errorf("改派 share_text 应包含绝对工单地址：%v", reassigned.ShareText)
	}

	// 入口提交：回给提交者的访客链接也是绝对地址。
	createIntake(t, s, web, map[string]any{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"})
	submitted := submitToIntake(t, s, "bug", map[string]any{
		"title": "UD-2300 报警", "fields": bugFields(), "author_name": "赵六",
	})
	if !strings.HasPrefix(submitted.GuestLink.URL, "https://taskboard.urithub.com/g/") {
		t.Errorf("入口提交的访客链接应是绝对地址：%q", submitted.GuestLink.URL)
	}
}
