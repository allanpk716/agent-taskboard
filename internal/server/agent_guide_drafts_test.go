package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func TestAgent说明书包含草稿定稿文案(t *testing.T) {
	s := tbtest.Start(t)
	res := s.Do(t, s.NewClient(), http.MethodGet, "/agent.md", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d, 想 200", res.StatusCode)
	}
	guide := string(readAll(t, res))
	for _, want := range []string{
		"## 草稿",
		"新签发的 token 和上线前已有的 token，都是必须走草稿",
		"`409 draft_pending`",
		"草稿的每一版都会随正式内容存档",
		"访客 Agent 不受草稿限制",
	} {
		if !strings.Contains(guide, want) {
			t.Errorf("Agent 说明书缺少 %q", want)
		}
	}
}
