package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/notifications"
)

func Test通知视图序列化新增一句话字段(t *testing.T) {
	view := newNotificationItemView(notifications.Item{
		ID: 1, Kind: "turn", Reason: "assigned",
		Text:     "张三 把工单「生产线通讯中断」指派给你",
		Headline: "张三 派给你", Number: "T-0412", Title: "生产线通讯中断",
	})

	body, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("序列化通知失败：%v", err)
	}
	if !strings.Contains(string(body), `"headline":"张三 派给你"`) {
		t.Fatalf("通知 JSON 缺少 headline 字段：%s", body)
	}
	if !strings.Contains(string(body), `"text":"张三 把工单「生产线通讯中断」指派给你"`) {
		t.Fatalf("通知 JSON 不应改变旧 text 字段：%s", body)
	}
}
