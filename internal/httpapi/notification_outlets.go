package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/allanpk716/agent-taskboard/internal/outlet"
)

// handleNotificationOutlet 处理这位成员的唯一通知出口：
//
//	GET    /api/v1/me/notification-outlet  读取非敏感配置（key 只回末四位）
//	PUT    /api/v1/me/notification-outlet  保存/重建配置并建立推送基线
//	DELETE /api/v1/me/notification-outlet  删除配置
func (a *API) handleNotificationOutlet(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !a.cfg.Webhook.Enabled {
			writeJSON(w, http.StatusOK, notificationOutletView{Enabled: false})
			return
		}
		config, err := a.outlets.Get(r.Context(), viewer.Member.ID)
		if errors.Is(err, outlet.ErrOutletNotFound) {
			writeJSON(w, http.StatusOK, notificationOutletView{Enabled: true})
			return
		}
		if a.writeOutletError(w, err) {
			return
		}
		a.writeOutlet(w, http.StatusOK, config)
	case http.MethodPut:
		if !a.cfg.Webhook.Enabled {
			a.writeWebhookDisabled(w)
			return
		}
		var req struct {
			BotID string `json:"bot_id"`
			Key   string `json:"key"`
		}
		if !decodeJSON(w, r, &req, `{"bot_id": "bot01", "key": "只写不读的 webhook key"}`) {
			return
		}
		if !validOutletBotID(req.BotID) {
			WriteError(w, http.StatusUnprocessableEntity, CodeInvalidOutletBotID,
				outlet.ErrInvalidBotID.Error(), "bot_id 只用于服务端拼接白名单地址，不能填 URL")
			return
		}
		if req.Key == "" {
			WriteError(w, http.StatusUnprocessableEntity, CodeOutletWebhookKeyMissing,
				outlet.ErrInvalidWebhookKey.Error(), "key 只写不读；更换 bot_id 后必须重新粘贴")
			return
		}
		config, err := a.outlets.Save(r.Context(), viewer.Member.ID, req.BotID, req.Key)
		if a.writeOutletError(w, err) {
			return
		}
		a.writeOutlet(w, http.StatusOK, config)
	case http.MethodDelete:
		if !a.cfg.Webhook.Enabled {
			a.writeWebhookDisabled(w)
			return
		}
		if err := a.outlets.Delete(r.Context(), viewer.Member.ID); a.writeOutletError(w, err) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

// handleNotificationOutletAction 处理显式状态与测试动作；暂停/恢复都不重建基线。
func (a *API) handleNotificationOutletAction(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.webSessionFromRequest(w, r)
	if !ok {
		return
	}
	action := r.URL.Path[len("/api/v1/me/notification-outlet/"):]
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	if !a.cfg.Webhook.Enabled {
		a.writeWebhookDisabled(w)
		return
	}
	switch action {
	case "pause":
		config, err := a.outlets.Pause(r.Context(), viewer.Member.ID)
		if a.writeOutletError(w, err) {
			return
		}
		a.writeOutlet(w, http.StatusOK, config)
	case "resume":
		config, err := a.outlets.Resume(r.Context(), viewer.Member.ID)
		if a.writeOutletError(w, err) {
			return
		}
		a.writeOutlet(w, http.StatusOK, config)
	case "test":
		if _, err := a.outlets.Get(r.Context(), viewer.Member.ID); a.writeOutletError(w, err) {
			return
		}
		if retry := a.outletTests.allow(strconv.FormatInt(viewer.Member.ID, 10)); retry > 0 {
			seconds := retryAfterSeconds(retry)
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			WriteErrorExtra(w, http.StatusTooManyRequests, CodeOutletTestRateLimited,
				"测试通知一分钟只能发送一次", "每次测试都会真实触发目标 bot 一次 run",
				map[string]any{"retry_after_seconds": seconds})
			return
		}
		result, err := a.outlets.SendTest(r.Context(), viewer.Member.ID,
			a.outletBuilder, a.outletClient, a.clk.Now())
		if a.writeOutletError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Result     string `json:"result"`
			HTTPStatus int    `json:"http_status"`
		}{Result: result.Kind, HTTPStatus: result.HTTPStatus})
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个通知出口动作", "")
	}
}

type notificationOutletView struct {
	Enabled bool           `json:"enabled"`
	Outlet  *outletViewRow `json:"outlet"`
}

type outletViewRow struct {
	BotID           string  `json:"bot_id"`
	KeyLast4        string  `json:"key_last4"`
	LastPushedID    int64   `json:"last_pushed_id"`
	Generation      int64   `json:"generation"`
	Status          string  `json:"status"`
	FailureCount    int64   `json:"failure_count"`
	PauseReason     *string `json:"pause_reason,omitempty"`
	DeliveryState   string  `json:"delivery_state"`
	DeliveryStateAt *string `json:"delivery_state_at,omitempty"`
	TransientSince  *string `json:"transient_since,omitempty"`
}

func (a *API) writeWebhookDisabled(w http.ResponseWriter) {
	WriteError(w, http.StatusConflict, CodeWebhookDisabled,
		"这个部署没有开启通知出口", "请联系部署管理员确认 [webhook] enabled")
}

func (a *API) writeOutlet(w http.ResponseWriter, status int, config outlet.Configuration) {
	view := notificationOutletView{Enabled: a.cfg.Webhook.Enabled,
		Outlet: &outletViewRow{
			BotID: config.BotID, KeyLast4: config.KeyLast4,
			LastPushedID: config.LastPushedID, Generation: config.Generation,
			Status: config.Status, FailureCount: config.FailureCount, PauseReason: config.PauseReason,
			DeliveryState: config.DeliveryState, DeliveryStateAt: config.DeliveryStateAt,
			TransientSince: config.TransientSince,
		}}
	writeJSON(w, status, view)
}

func (a *API) writeOutletError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, outlet.ErrOutletNotFound):
		WriteError(w, http.StatusNotFound, CodeOutletNotFound, "还没有通知出口配置", "")
	case errors.Is(err, outlet.ErrInvalidBotID):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidOutletBotID, err.Error(), "")
	case errors.Is(err, outlet.ErrInvalidWebhookKey):
		WriteError(w, http.StatusUnprocessableEntity, CodeOutletWebhookKeyMissing, err.Error(), "")
	case errors.Is(err, outlet.ErrMasterKeyMissing):
		WriteError(w, http.StatusServiceUnavailable, CodeWebhookDisabled,
			"通知出口主密钥不可用", "请联系部署管理员重新注入 TASKBOARD_OUTLET_KEY")
	default:
		panic(err)
	}
	return true
}

func validOutletBotID(botID string) bool {
	return outlet.ValidBotID(botID)
}
