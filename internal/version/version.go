// Package version 记录当前组件的版本。
//
// 后端版本由发布流水线注入（.github/workflows/release-server.yml 的
// -ldflags "-X .../internal/version.Server=X.Y.Z"）；开发构建没有注入时是
// 0.0.0——任何已发布版本都比它新。前端版本不在这里：前端在磁盘上独立更新，
// 当前版本写在 web 目录里的 version.json（见 internal/updater）。
package version

import (
	"strconv"
	"strings"
)

// Server 是当前后端（taskboard 二进制）的版本。
var Server = "0.0.0"

// Client 是当前 Windows 桌面客户端的版本。
var Client = "0.0.0"

// parse 把 X.Y.Z 解析成三元组；非法段按 0 处理。
func parse(v string) [3]int {
	var out [3]int
	for i, part := range strings.SplitN(strings.TrimPrefix(strings.TrimSpace(v), "v"), ".", 3) {
		if i > 2 {
			break
		}
		n, _ := strconv.Atoi(strings.TrimSuffix(part, "-")) // 容错：只比数字段
		out[i] = n
	}
	return out
}

// Compare 比较两个 X.Y.Z 版本号：a<b 返回 -1，相等返回 0，a>b 返回 1。
func Compare(a, b string) int {
	pa, pb := parse(a), parse(b)
	for i := range pa {
		switch {
		case pa[i] < pb[i]:
			return -1
		case pa[i] > pb[i]:
			return 1
		}
	}
	return 0
}

// Less 报告 a 是否比 b 旧。
func Less(a, b string) bool { return Compare(a, b) < 0 }
