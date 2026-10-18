package httpapi

import (
	"encoding/json"
	"testing"
)

func TestAssigneeRefUnmarshalJSON(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		invalid bool
	}{
		{name: "字符串id", input: `"4"`, want: "4"},
		{name: "姓名", input: `"陈工"`, want: "陈工"},
		{name: "空字符串", input: `""`, want: ""},
		{name: "null", input: `null`, want: ""},
		{name: "数字id", input: `4`, want: "4"},
		{name: "大数字id", input: `9223372036854775807`, want: "9223372036854775807"},
		{name: "小数", input: `4.5`, invalid: true},
		{name: "带小数零", input: `4.0`, invalid: true},
		{name: "零", input: `0`, invalid: true},
		{name: "负数", input: `-4`, invalid: true},
		{name: "布尔", input: `true`, invalid: true},
		{name: "对象", input: `{}`, invalid: true},
		{name: "数组", input: `[]`, invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got assigneeRef
			if err := json.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatalf("UnmarshalJSON 不应返回错误：%v", err)
			}
			if got.ref != tc.want || got.invalid != tc.invalid {
				t.Fatalf("got = %+v, want ref=%q invalid=%v", got, tc.want, tc.invalid)
			}
		})
	}
}
