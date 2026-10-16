// 反馈入口的 HTTP 侧（#35）：成员创建/停用/轮换 slug；匿名读取入口（模板
// 最新版与维度候选值）与匿名提交（严格校验，成功返回工单 + 访客链接）。
// 管理端点仅限成员（访客 403 members_only）；匿名端点不需要任何凭证。
package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// handleIntakesRoot 处理入口集合：
//
//	GET  /api/v1/intakes  列表（任何成员，含已停用）
//	POST /api/v1/intakes  创建（任何成员；绑定反馈类型的模板）
func (a *API) handleIntakesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listIntakes(w, r)
	case http.MethodPost:
		a.createIntake(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

// handleIntakeRef 处理单个入口：
//
//	GET  /api/v1/intakes/{slug}            匿名读取（模板最新版与维度候选值；
//	                                       ?format=markdown 给 Agent 的填写指南）
//	POST /api/v1/intakes/{slug}/tickets    匿名提交（严格校验，返回工单与访客链接）
//	POST /api/v1/intakes/{slug}/deactivate 停用（成员；幂等）
//	POST /api/v1/intakes/{slug}/rotate     轮换 slug（成员；旧 slug 立即失效）
func (a *API) handleIntakeRef(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/intakes/")
	slug, sub, hasSub := strings.Cut(rest, "/")
	if !hasSub {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
			return
		}
		a.readIntake(w, r, slug)
		return
	}
	switch sub {
	case "tickets":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "提交反馈用 POST")
			return
		}
		a.submitToIntake(w, r, slug)
	case "deactivate":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "停用入口用 POST")
			return
		}
		a.deactivateIntake(w, r, slug)
	case "rotate":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "轮换 slug 用 POST")
			return
		}
		a.rotateIntake(w, r, slug)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
			"入口接口：GET /api/v1/intakes/{slug}、POST …/tickets、POST …/deactivate、POST …/rotate")
	}
}

// intakeCreateRequest 是 POST /api/v1/intakes 的请求体。
type intakeCreateRequest struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Template string `json:"template"`
}

// intakeRotateRequest 是 POST /api/v1/intakes/{slug}/rotate 的请求体：新 slug。
type intakeRotateRequest struct {
	Slug string `json:"slug"`
}

// intakeSubmitRequest 是 POST /api/v1/intakes/{slug}/tickets 的请求体（匿名）。
// author_name 是提交者自报的署名（ADR 0003：不经平台核实）。
type intakeSubmitRequest struct {
	Title      string            `json:"title"`
	Fields     map[string]string `json:"fields"`
	AuthorName string            `json:"author_name"`
}

// intakeSummaryView 是入口的管理侧视图（列表项）。
type intakeSummaryView struct {
	Slug          string     `json:"slug"`
	Name          string     `json:"name"`
	Template      string     `json:"template"`
	Active        bool       `json:"active"`
	URL           string     `json:"url"`
	DeactivatedAt *string    `json:"deactivated_at,omitempty"`
	CreatedBy     authorView `json:"created_by"`
	CreatedAt     string     `json:"created_at"`
}

// intakeTemplateView 是匿名读取时回显的模板（始终最新版本）。
type intakeTemplateView struct {
	Key          string              `json:"key"`
	Name         string              `json:"name"`
	Version      int64               `json:"version"`
	Kind         string              `json:"kind"`
	PresetLabels []string            `json:"preset_labels"`
	AgentNote    string              `json:"agent_note"`
	Fields       []templateFieldView `json:"fields"`
}

// intakePublicView 是 GET /api/v1/intakes/{slug}（匿名）的响应。
type intakePublicView struct {
	Slug     string             `json:"slug"`
	Name     string             `json:"name"`
	URL      string             `json:"url"`
	Kind     string             `json:"kind"`
	Template intakeTemplateView `json:"template"`
}

// intakeTicketView 是提交响应里的工单摘要。
type intakeTicketView struct {
	Number string `json:"number"`
	Seq    int64  `json:"seq"`
	Title  string `json:"title"`
	URL    string `json:"url"`
}

// intakeSubmitView 是 POST /api/v1/intakes/{slug}/tickets 成功（201）的响应：
// 新工单 + 交给提交者的访客链接（带「可填写说明」权限，30 天）。
type intakeSubmitView struct {
	Ticket    intakeTicketView `json:"ticket"`
	GuestLink struct {
		URL          string `json:"url"`
		Note         string `json:"note"`
		ExpiresAt    string `json:"expires_at"`
		CanEditBrief bool   `json:"can_edit_brief"`
	} `json:"guest_link"`
	Next string `json:"next"`
}

func (a *API) createIntake(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.memberFromRequestFor(w, r,
		"反馈入口由成员创建和维护；其他部门的人直接使用入口地址提交")
	if !ok {
		return
	}
	var req intakeCreateRequest
	if !decodeJSON(w, r, &req, `{"slug": "bug", "name": "部门 Bug 反馈", "template": "bug-report"}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		in, err := a.tickets.CreateIntake(r.Context(), viewer, tickets.IntakeSpec{
			Slug: req.Slug, Name: req.Name, TemplateKey: req.Template,
		})
		if err != nil {
			a.writeIntakesError(w, r, err)
			return
		}
		writeJSON(w, http.StatusCreated, newIntakeSummaryView(in))
	})
}

func (a *API) listIntakes(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.memberFromRequestFor(w, r,
		"反馈入口由成员创建和维护；其他部门的人直接使用入口地址提交"); !ok {
		return
	}
	list, err := a.tickets.Intakes(r.Context())
	if err != nil {
		panic(err)
	}
	out := struct {
		Intakes []intakeSummaryView `json:"intakes"`
	}{Intakes: []intakeSummaryView{}}
	for _, in := range list {
		out.Intakes = append(out.Intakes, newIntakeSummaryView(in))
	}
	writeJSON(w, http.StatusOK, out)
}

// readIntake 匿名读取入口：返回入口信息和绑定模板的当前版本（入口始终使用
// 模板的最新版本），维度字段带字典候选值与「不确定」。不存在或已停用 →
// 404 intake_not_found（轮换后的旧 slug 同样）。
func (a *API) readIntake(w http.ResponseWriter, r *http.Request, slug string) {
	in, err := a.tickets.ActiveIntakeBySlug(r.Context(), slug)
	if err != nil {
		a.writeIntakesError(w, r, err)
		return
	}
	t, err := a.templates.Get(r.Context(), in.TemplateKey)
	if err != nil {
		a.writeIntakesError(w, r, err)
		return
	}
	if r.URL.Query().Get("format") == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(a.guide(r, t)))
		return
	}
	writeJSON(w, http.StatusOK, intakePublicView{
		Slug: in.Slug, Name: in.Name, URL: "/i/" + in.Slug, Kind: t.Kind,
		Template: intakeTemplateView{
			Key: t.Key, Name: t.Name, Version: t.Version, Kind: t.Kind,
			PresetLabels: nonNil(t.PresetLabels), AgentNote: t.AgentNote,
			Fields: a.templateFieldsView(r, t),
		},
	})
}

func (a *API) deactivateIntake(w http.ResponseWriter, r *http.Request, slug string) {
	if _, ok := a.memberFromRequestFor(w, r,
		"反馈入口由成员创建和维护；其他部门的人直接使用入口地址提交"); !ok {
		return
	}
	changed, err := a.tickets.DeactivateIntake(r.Context(), slug)
	if err != nil {
		a.writeIntakesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"slug": slug, "active": false, "deactivated": changed})
}

func (a *API) rotateIntake(w http.ResponseWriter, r *http.Request, slug string) {
	if _, ok := a.memberFromRequestFor(w, r,
		"反馈入口由成员创建和维护；其他部门的人直接使用入口地址提交"); !ok {
		return
	}
	var req intakeRotateRequest
	if !decodeJSON(w, r, &req, `{"slug": "bug-2027"}`) {
		return
	}
	in, err := a.tickets.RotateIntakeSlug(r.Context(), slug, req.Slug)
	if err != nil {
		a.writeIntakesError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newIntakeSummaryView(in))
}

// submitToIntake 匿名提交一张反馈：严格校验，缺必填 422 missing_fields
// （hint 逐项说明）；成功 201 返回新工单和一条带「可填写说明」权限的
// 30 天访客链接。支持 Idempotency-Key（匿名凭证同一 key 重放返回首次结果）。
func (a *API) submitToIntake(w http.ResponseWriter, r *http.Request, slug string) {
	var req intakeSubmitRequest
	if !decodeJSON(w, r, &req, `{"title": "BH-5100 打印报告乱码", "fields": {"哪台仪器？": "BH-5100", "…": "…"}, "author_name": "赵六"}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		result, err := a.tickets.SubmitToIntake(r.Context(), slug, tickets.SubmissionInput{
			Title: req.Title, Fields: req.Fields, AuthorName: req.AuthorName,
		})
		if err != nil {
			a.writeIntakesError(w, r, err)
			return
		}
		view := intakeSubmitView{
			Ticket: intakeTicketView{
				Number: result.Ticket.Number, Seq: result.Ticket.Seq,
				Title: result.Ticket.Title, URL: "/t/" + result.Ticket.Number,
			},
			Next: "之后用这条访客链接查看这张反馈、补充说明和回复；请收藏链接（也可以把它交给你的 Agent 作 Bearer token）",
		}
		view.GuestLink.URL = a.absURL("/g/" + result.LinkKey)
		view.GuestLink.Note = result.LinkNote
		view.GuestLink.ExpiresAt = result.ExpiresAt.Format(time.RFC3339Nano)
		view.GuestLink.CanEditBrief = true
		writeJSON(w, http.StatusCreated, view)
	})
}

func newIntakeSummaryView(in tickets.Intake) intakeSummaryView {
	v := intakeSummaryView{
		Slug: in.Slug, Name: in.Name, Template: in.TemplateKey,
		Active: in.Active, URL: "/i/" + in.Slug,
		CreatedBy: newAuthorView(in.CreatedBy),
		CreatedAt: in.CreatedAt.Format(time.RFC3339Nano),
	}
	if in.DeactivatedAt != nil {
		t := in.DeactivatedAt.Format(time.RFC3339Nano)
		v.DeactivatedAt = &t
	}
	return v
}

// writeIntakesError 把反馈入口的错误映射成 HTTP 错误；入口特有的错误在这里，
// 建单共有的（标题、字段、标签、模板）复用 writeTicketError。
func (a *API) writeIntakesError(w http.ResponseWriter, r *http.Request, err error) {
	var missing *tickets.MissingFieldsError
	switch {
	case errors.Is(err, tickets.ErrIntakeNotFound):
		WriteError(w, http.StatusNotFound, CodeIntakeNotFound, err.Error(),
			"入口地址形如 /i/<slug>；入口可能已停用，或 slug 已轮换，请向部门成员索取当前地址")
	case errors.Is(err, tickets.ErrSlugRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeSlugRequired, err.Error(),
			`slug 是入口地址的一部分，如 "bug-report"`)
	case errors.Is(err, tickets.ErrInvalidSlug):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidSlug, err.Error(),
			`slug 只用小写字母、数字和连字符，如 "bug-report"`)
	case errors.Is(err, tickets.ErrSlugTaken):
		WriteError(w, http.StatusConflict, CodeSlugTaken, err.Error(),
			"换一个 slug；已占用的 slug 不能复用（历史工单和成员还引用着旧地址），可换用轮换保留旧入口")
	case errors.Is(err, tickets.ErrNameRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeNameRequired, err.Error(),
			"名称会显示在提交页上，如「部门 Bug 反馈」")
	case errors.Is(err, tickets.ErrTemplateNotReport):
		WriteError(w, http.StatusUnprocessableEntity, CodeTemplateNotReport, err.Error(),
			"入口提交的是反馈，换一份类型为「反馈」的模板（如 bug-report），或先修改模板类型")
	case errors.As(err, &missing):
		hints := make([]string, 0, len(missing.Fields))
		for _, f := range missing.Fields {
			h := "【" + f.Field + "】" + f.Hint
			if f.Example != "" {
				h += "（示例：" + f.Example + "）"
			}
			hints = append(hints, h)
		}
		extra := map[string]any{"fields": missing.Fields}
		WriteErrorExtra(w, http.StatusUnprocessableEntity, CodeMissingFields,
			missing.Error(), "逐项补充后再提交：\n"+strings.Join(hints, "\n"), extra)
	default:
		// 入口提交还没有工单引用可填（建单在提交内部完成）。
		a.writeTicketError(w, r, "", err)
	}
}
