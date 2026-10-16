package version

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "1.0.0", 0},
		{"1.0.0", "1.0.1", -1},
		{"1.2.0", "1.10.0", -1}, // 数字段比较，不是字符串
		{"2.0.0", "1.99.99", 1},
		{"0.0.0", "0.0.1", -1},
		{"v1.2.3", "1.2.3", 0}, // v 前缀
		{"", "0.0.0", 0},       // 未知版本按 0.0.0
		{"1.0.0", "", 1},
	}
	for _, tc := range cases {
		if got := Compare(tc.a, tc.b); got != tc.want {
			t.Errorf("Compare(%q, %q) = %d, 想 %d", tc.a, tc.b, got, tc.want)
		}
	}
	if !Less("1.0.0", "1.0.1") {
		t.Error("Less(1.0.0, 1.0.1) 应为 true")
	}
}
