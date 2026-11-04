package client_test

import (
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/client"
)

func Test工单通知弹窗用一句话作标题并完整显示标题(t *testing.T) {
	const title = "生产线 3 号机型在高温环境下连续运行 48 小时后出现间歇性通讯中断"
	var n client.Notification
	n.Headline = "张三 派给你"
	n.Summary = "已复现看门狗复位。"
	n.Ticket.Number = "T-0412"
	n.Ticket.Title = title

	gotTitle, gotBody := client.NotificationToast(n)

	if gotTitle != "张三 派给你 · T-0412" {
		t.Fatalf("弹窗标题 = %q，想 %q", gotTitle, "张三 派给你 · T-0412")
	}
	if gotBody != title+"\n已复现看门狗复位。" {
		t.Fatalf("弹窗正文 = %q，想完整标题后接摘要", gotBody)
	}
}

func Test工单通知没有摘要时弹窗正文只放标题(t *testing.T) {
	var n client.Notification
	n.Headline = "李四 交付了，等你验收"
	n.Ticket.Number = "T-0408"
	n.Ticket.Title = "季度报表导出"

	gotTitle, gotBody := client.NotificationToast(n)

	if gotTitle != "李四 交付了，等你验收 · T-0408" {
		t.Fatalf("弹窗标题 = %q，想 %q", gotTitle, "李四 交付了，等你验收 · T-0408")
	}
	if gotBody != "季度报表导出" {
		t.Fatalf("弹窗正文 = %q，想只放完整标题", gotBody)
	}
}

func Test旧服务端没有一句话时弹窗回落现有格式(t *testing.T) {
	const title = "客户反馈：设备开机界面乱码"
	var n client.Notification
	n.Text = "张三 把工单「" + title + "」指派给你"
	n.Summary = "英文系统下复现。"
	n.Ticket.Number = "T-0405"
	n.Ticket.Title = title

	gotTitle, gotBody := client.NotificationToast(n)

	if gotTitle != "T-0405 "+title {
		t.Fatalf("弹窗标题 = %q，想旧格式「编号 标题」", gotTitle)
	}
	wantBody := n.Text + "\n" + n.Summary
	if gotBody != wantBody {
		t.Fatalf("弹窗正文 = %q，想旧格式 %q", gotBody, wantBody)
	}
}

func Test系统通知弹窗保持现状(t *testing.T) {
	var n client.Notification
	n.Text = "备份连续失败，请检查对象存储。"

	gotTitle, gotBody := client.NotificationToast(n)

	if gotTitle != n.Text {
		t.Fatalf("弹窗标题 = %q，想通知原文 %q", gotTitle, n.Text)
	}
	if gotBody != "" {
		t.Fatalf("弹窗正文 = %q，想为空", gotBody)
	}
}
