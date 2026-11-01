package httpapi

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCollaboratorRefsUnmarshalJSON(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    []string
		invalid bool
	}{
		{name: "缺省为空", input: `null`, want: []string(nil)},
		{name: "数字id与字符串", input: `[4, "5", "陈工"]`, want: []string{"4", "5", "陈工"}},
		{name: "空数组", input: `[]`, want: []string{}},
		{name: "写成字符串", input: `"陈工"`, invalid: true},
		{name: "空字符串元素", input: `[""]`, invalid: true},
		{name: "null元素", input: `[null]`, invalid: true},
		{name: "零", input: `[0]`, invalid: true},
		{name: "负数", input: `[-4]`, invalid: true},
		{name: "小数", input: `[4.5]`, invalid: true},
		{name: "布尔", input: `[true]`, invalid: true},
		{name: "对象", input: `[{}]`, invalid: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got collaboratorRefs
			if err := json.Unmarshal([]byte(tc.input), &got); err != nil {
				t.Fatalf("UnmarshalJSON 不应返回错误：%v", err)
			}
			if got.invalid != tc.invalid {
				t.Fatalf("invalid = %v, 想 %v", got.invalid, tc.invalid)
			}
			if !reflect.DeepEqual(got.refs, tc.want) {
				t.Fatalf("refs = %#v, 想 %#v", got.refs, tc.want)
			}
		})
	}
}
