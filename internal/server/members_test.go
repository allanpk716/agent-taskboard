package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// 成员网页（#41）的指派下拉需要成员名单：GET /api/v1/members 列出全部成员
// （只读；账号管理写接口在 #42）。仅成员可读：匿名 401，访客 403 members_only。
func Test成员列表接口供指派下拉使用(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	s.CreateMember("王工", "member-pass")
	member := s.Login("王工", "member-pass")

	var got struct {
		Members []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"members"`
	}
	res := s.Do(t, member, http.MethodGet, "/api/v1/members", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	if len(got.Members) != 2 {
		t.Fatalf("成员数 = %d, 想 2：%+v", len(got.Members), got.Members)
	}
	if got.Members[1].Name != "王工" {
		t.Errorf("第二位成员不对：%+v", got.Members[1])
	}
	if got.Members[0].Name != "陈工" {
		t.Errorf("第一位成员不对：%+v", got.Members[0])
	}
	if got.Members[0].ID == got.Members[1].ID {
		t.Errorf("两位成员的 id 不应相同")
	}

	// 匿名 401。
	res = s.Do(t, s.NewClient(), http.MethodGet, "/api/v1/members", nil, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("匿名读成员列表状态码 = %d, 想 401", res.StatusCode)
	}

	// 访客 403 members_only（决议 4）：先建一张工单再开链接。
	mustCreate(t, s, member, "供访客链接用的工单", []string{"机型:UD-2300"})
	var first struct {
		Number string `json:"number"`
	}
	createRes := s.Do(t, member, http.MethodPost, "/api/v1/tickets", map[string]any{
		"title": "拿编号用", "labels": []string{"机型:US-1680"},
	}, &first)
	if createRes.StatusCode != http.StatusCreated {
		t.Fatalf("建单状态码 = %d", createRes.StatusCode)
	}
	link := s.CreateGuestLink(t, member, first.Number, map[string]any{"note": "检验科-张三"})
	guest := s.GuestClient(link.Key(t))
	res = s.Do(t, guest, http.MethodGet, "/api/v1/members", nil, nil)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("访客读成员列表状态码 = %d, 想 403", res.StatusCode)
	}
}
