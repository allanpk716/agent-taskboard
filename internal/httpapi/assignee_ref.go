package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"
)

// assigneeRef 是请求里的 assignee：JSON 可写正整数成员 id、字符串成员 id 或姓名；
// null 与空字符串都表示不指派/取消指派。invalid 让 handler 返回 422，而不是
// 把类型错误混进 invalid_json。
type assigneeRef struct {
	ref     string
	invalid bool
}

func (a *assigneeRef) UnmarshalJSON(data []byte) error {
	*a = assigneeRef{}
	trimmed := bytes.TrimSpace(data)
	if bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			a.invalid = true
			return nil
		}
		a.ref = s
		return nil
	}
	if n, err := strconv.ParseInt(string(trimmed), 10, 64); err == nil && n > 0 {
		a.ref = strconv.FormatInt(n, 10)
		return nil
	}
	a.invalid = true
	return nil
}

func writeInvalidAssignee(w http.ResponseWriter) {
	WriteError(w, http.StatusUnprocessableEntity, CodeInvalidAssignee,
		"assignee 必须是成员 id（数字或字符串）或姓名",
		`可接受 "assignee": 4、"assignee": "4"、"assignee": "陈工"；空字符串表示取消指派`)
}
