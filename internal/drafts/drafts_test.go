package drafts

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCreateRequestUnmarshal接受数字成员引用(t *testing.T) {
	var req CreateRequest
	const body = `{
		"title": "数字引用",
		"assignee": 4,
		"collaborators": [5, "6", "李工"]
	}`
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("反序列化失败：%v", err)
	}
	if req.Assignee != "4" {
		t.Fatalf("assignee = %q, 想 4", req.Assignee)
	}
	if strings.Join(req.Collaborators, ",") != "5,6,李工" {
		t.Fatalf("collaborators = %#v, 想 [5 6 李工]", req.Collaborators)
	}
}

func TestCreateRequestUnmarshal拒绝非法成员引用(t *testing.T) {
	cases := []string{
		`{"assignee": 0}`,
		`{"assignee": true}`,
		`{"collaborators": [null]}`,
		`{"collaborators": [0]}`,
		`{"collaborators": [" "]}`,
	}
	for _, body := range cases {
		var req CreateRequest
		if err := json.Unmarshal([]byte(body), &req); err == nil {
			t.Fatalf("%s 应返回错误，得到 %#v", body, req)
		}
	}
}
