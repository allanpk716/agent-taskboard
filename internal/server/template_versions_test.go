package server_test

import (
	"net/http"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

// #42 模板维护页要查看历史版本：GET /api/v1/templates/{key}/versions 列出
// 每个版本的修改人与时间（新→旧），带模板原文。
func Test查看模板的历史版本(t *testing.T) {
	s := tbtest.Start(t)
	s.CreateAdmin("陈工", "secret-pass")
	member := s.Login("陈工", "secret-pass")

	body := map[string]string{"key": "check", "markdown": checkTemplate}
	if res := s.Do(t, member, http.MethodPost, "/api/v1/templates", body, nil); res.StatusCode != http.StatusCreated {
		t.Fatalf("新建模板状态码 = %d", res.StatusCode)
	}
	// 改两次：v2、v3。
	for _, extra := range []string{"## 追加的第一版 {必填}\nv2\n", "## 追加的第二版 {必填}\nv3\n"} {
		var cur templateBody
		s.Do(t, member, http.MethodGet, "/api/v1/templates/check", nil, &cur)
		if res := s.Do(t, member, http.MethodPatch, "/api/v1/templates/check",
			map[string]string{"markdown": cur.Markdown + extra}, nil); res.StatusCode != http.StatusOK {
			t.Fatalf("修改模板状态码 = %d, 想 200", res.StatusCode)
		}
	}

	var got struct {
		Versions []struct {
			Version   int64  `json:"version"`
			Name      string `json:"name"`
			Markdown  string `json:"markdown"`
			UpdatedBy *struct {
				Member *struct {
					Name string `json:"name"`
				} `json:"member"`
				Agent *struct {
					Name string `json:"name"`
				} `json:"agent"`
			} `json:"updated_by"`
			UpdatedAt string `json:"updated_at"`
		} `json:"versions"`
	}
	res := s.Do(t, member, http.MethodGet, "/api/v1/templates/check/versions", nil, &got)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("版本列表状态码 = %d, 想 200", res.StatusCode)
	}
	if len(got.Versions) != 3 {
		t.Fatalf("版本数 = %d, 想 3：%+v", len(got.Versions), got.Versions)
	}
	// 新→旧排列；每版带修改人与原文（最早一版是新建时的原文）。
	if got.Versions[0].Version != 3 || got.Versions[2].Version != 1 {
		t.Errorf("版本排序不对：首 %d 末 %d，想新→旧", got.Versions[0].Version, got.Versions[2].Version)
	}
	if got.Versions[0].Markdown == "" || got.Versions[2].Markdown != checkTemplate {
		t.Errorf("各版本应带模板原文（v1 为新建原文）")
	}
	for i, v := range got.Versions {
		if v.UpdatedBy == nil || v.UpdatedBy.Member == nil || v.UpdatedBy.Member.Name != "陈工" {
			t.Errorf("第 %d 版修改人不对：%+v", i+1, v.UpdatedBy)
		}
		if v.UpdatedAt == "" {
			t.Errorf("第 %d 版缺 updated_at", i+1)
		}
	}

	// 不存在的模板 → 404 template_not_found。
	res = s.Do(t, member, http.MethodGet, "/api/v1/templates/no-such/versions", nil, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("不存在模板的版本列表状态码 = %d, 想 404", res.StatusCode)
	}
	var errBody errorBody
	tbtest.DecodeJSON(t, res, &errBody)
	if errBody.Error.Code != "template_not_found" {
		t.Errorf("错误码 = %q, 想 template_not_found", errBody.Error.Code)
	}
}
