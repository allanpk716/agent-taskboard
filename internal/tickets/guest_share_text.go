package tickets

import (
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
)

// GuestShareText 生成面向访客的转发文案（#64）：称呼对方 + 工单标题 +
// 请按提示写详细、可传日志照片 + 访客链接 + 不用登录、有效期至某日。
// 建单响应与成员网页 / 成员 Agent 用的都是这一份。
func GuestShareText(note, title, url string, expiresAt time.Time) string {
	return note + "：想请你帮忙反馈「" + title + "」的详细情况。打开这个链接，按提示把情况写具体一些" +
		"（什么时候、做了什么、看到什么），可以上传日志和照片：" + url +
		"。不用登录，有效期至 " + expiresAt.In(clock.Beijing).Format("2006-01-02") +
		"。链接只归第一位打开并确认的人，请不要转发"
}
