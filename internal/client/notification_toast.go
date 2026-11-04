package client

// NotificationToast 生成 Windows 弹窗的标题和正文；不依赖 Windows API，便于跨平台测试。
func NotificationToast(n Notification) (title, body string) {
	if n.Ticket.Number == "" {
		return n.Text, ""
	}
	if n.Headline == "" {
		title = n.Ticket.Number + " " + n.Ticket.Title
		body = n.Text
		if n.Summary != "" {
			body += "\n" + n.Summary
		}
		return title, body
	}
	body = n.Ticket.Title
	if n.Summary != "" {
		body += "\n" + n.Summary
	}
	return n.Headline + " · " + n.Ticket.Number, body
}
