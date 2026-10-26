package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// 服务端加密密钥写不出来（如数据目录只读）时，平台进入无密钥降级模式：
// 新建链接、换新地址都不能静默失败成 500，要给出明确的错误码。
func Test无密钥降级模式返回明确错误(t *testing.T) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/tickets/1/guest-links/1/rotate-key", nil)
	(&API{}).writeTicketError(w, r, "1", tickets.ErrGuestLinkCipherUnavailable)

	res := w.Result()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("状态码 = %d, 想 503", res.StatusCode)
	}
	var got errorEnvelope
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Error["code"] != "guest_link_cipher_unavailable" {
		t.Errorf("错误码 = %v, 想 guest_link_cipher_unavailable", got.Error["code"])
	}
	if got.Error["message"] == "" || got.Error["hint"] == "" {
		t.Errorf("应给出可操作的错误信息与提示：%+v", got.Error)
	}
}
