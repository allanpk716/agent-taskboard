package httpapi

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// collaboratorRefs 是建单请求里的 collaborators（抄送，#274）：一个成员引用
// 数组，元素可写正整数成员 id、字符串成员 id 或姓名；null 与缺省都表示不指定。
// invalid 让 handler 返回 422，而不是把类型错误混进 invalid_json。
type collaboratorRefs struct {
	refs    []string
	invalid bool
}

func (c *collaboratorRefs) UnmarshalJSON(data []byte) error {
	*c = collaboratorRefs{}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil
	}
	if trimmed[0] != '[' {
		c.invalid = true
		return nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(trimmed, &raw); err != nil {
		c.invalid = true
		return nil
	}
	refs := make([]string, 0, len(raw))
	for _, item := range raw {
		item = bytes.TrimSpace(item)
		if len(item) == 0 || bytes.Equal(item, []byte("null")) {
			c.invalid = true
			return nil
		}
		if item[0] == '"' {
			var s string
			if err := json.Unmarshal(item, &s); err != nil {
				c.invalid = true
				return nil
			}
			if strings.TrimSpace(s) == "" {
				c.invalid = true
				return nil
			}
			refs = append(refs, s)
			continue
		}
		n, err := strconv.ParseInt(string(item), 10, 64)
		if err != nil || n <= 0 {
			c.invalid = true
			return nil
		}
		refs = append(refs, strconv.FormatInt(n, 10))
	}
	c.refs = refs
	return nil
}
