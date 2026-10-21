package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// handleLabelsRoot 处理标签字典集合：
//
//	GET /api/v1/labels[?dimension=]  字典列表（每个值带 aliases，另列出不带维度的标签）
func (a *API) handleLabelsRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		return
	}
	if _, ok := a.memberFromRequest(w, r); !ok {
		return
	}
	view, err := a.tickets.Labels().Dictionary(r.Context(), r.URL.Query().Get("dimension"))
	if err != nil {
		a.writeLabelsError(w, err)
		return
	}
	out := dictionaryView{Dimensions: []labelDimensionView{}}
	for _, d := range view.Dimensions {
		dv := labelDimensionView{Name: d.Name, Values: []labelValueView{}}
		for _, v := range d.Values {
			aliases := v.Aliases
			if aliases == nil {
				aliases = []string{}
			}
			dv.Values = append(dv.Values, labelValueView{
				Value: v.Value, Label: d.Name + ":" + v.Value, Aliases: aliases,
			})
		}
		out.Dimensions = append(out.Dimensions, dv)
	}
	if r.URL.Query().Get("dimension") == "" {
		plain := view.Plain
		if plain == nil {
			plain = []string{}
		}
		out.Plain = &plain
	}
	writeJSON(w, http.StatusOK, out)
}

// handleLabelsSub 处理 /api/v1/labels 下的子资源：
//
//	GET   /api/v1/labels/resolve?q=&dimension=  俗称换标准值（exact / alias / fuzzy）
//	POST  /api/v1/labels/aliases                添加别名（任何成员）
//	PATCH /api/v1/labels/aliases                改别名的指向（管理员）
//	POST  /api/v1/labels/dimensions             添加维度（管理员）
//	POST  /api/v1/labels/rename|merge|deactivate  标签治理（管理员）
func (a *API) handleLabelsSub(w http.ResponseWriter, r *http.Request) {
	sub := trimPathPrefix(r.URL.Path, "/api/v1/labels/")
	switch sub {
	case "resolve":
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
			return
		}
		a.labelResolve(w, r)
	case "aliases":
		switch r.Method {
		case http.MethodPost:
			a.addAlias(w, r)
		case http.MethodPatch:
			a.repointAlias(w, r)
		default:
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		}
	case "dimensions":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
			return
		}
		a.addDimension(w, r)
	case "rename", "merge", "deactivate":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
			return
		}
		a.governLabel(w, r, sub)
	default:
		WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口", "标签接口：GET /api/v1/labels、GET /api/v1/labels/resolve、POST /api/v1/labels/aliases")
	}
}

// labelResolve 处理 GET /api/v1/labels/resolve：把俗称换标准值并注明匹配方式。
func (a *API) labelResolve(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.memberFromRequest(w, r); !ok {
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		WriteError(w, http.StatusUnprocessableEntity, CodeQRequired, "缺少查询词 q",
			"用 ?q=俗称 或 ?q=维度:值 查；可用 dimension 参数限定维度")
		return
	}
	matches, err := a.tickets.Labels().Resolve(r.Context(), q, r.URL.Query().Get("dimension"))
	if err != nil {
		a.writeLabelsError(w, err)
		return
	}
	out := resolveView{Q: q, Matches: []matchView{}}
	for _, m := range matches {
		mv := matchView{Label: m.Label, Via: m.Via}
		if m.Via == "alias" {
			mv.Alias = &m.Alias
		}
		out.Matches = append(out.Matches, mv)
	}
	if len(out.Matches) == 0 {
		out.Hint = "没有匹配。打标签时带 allow_new: true 可以新建该值；" +
			"或先用 POST /api/v1/labels/aliases 把这个写法登记为某个标准值的别名"
	}
	writeJSON(w, http.StatusOK, out)
}

// aliasRequest 是 POST / PATCH /api/v1/labels/aliases 的请求体。
type aliasRequest struct {
	Label string `json:"label"`
	Alias string `json:"alias"`
}

// addAlias 处理 POST /api/v1/labels/aliases：任何成员都能给标准值添加别名。
func (a *API) addAlias(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.memberFromRequest(w, r); !ok {
		return
	}
	var req aliasRequest
	if !decodeJSON(w, r, &req, `{"label": "维度:值", "alias": "俗称"}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		label, aliases, err := a.tickets.Labels().AddAlias(r.Context(), req.Label, req.Alias)
		if err != nil {
			a.writeLabelsError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, aliasView{Label: label, Aliases: aliases})
	})
}

// repointAlias 处理 PATCH /api/v1/labels/aliases：把别名改指向另一个标准值（管理员）。
func (a *API) repointAlias(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.adminFromRequestFor(w, r, "访客不能查看或维护标签",
		"浏览字典（GET /api/v1/labels）和添加别名（POST /api/v1/labels/aliases）对成员开放"); !ok {
		return
	}
	var req aliasRequest
	if !decodeJSON(w, r, &req, `{"alias": "俗称", "label": "维度:值"}`) {
		return
	}
	label, aliases, err := a.tickets.Labels().RepointAlias(r.Context(), req.Alias, req.Label)
	if err != nil {
		a.writeLabelsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, aliasView{Label: label, Aliases: aliases})
}

// addDimension 处理 POST /api/v1/labels/dimensions：新维度只能由管理员添加。
func (a *API) addDimension(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.adminFromRequestFor(w, r, "访客不能查看或维护标签",
		"浏览字典（GET /api/v1/labels）和添加别名（POST /api/v1/labels/aliases）对成员开放"); !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &req, `{"name": "新维度"}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		if err := a.tickets.Labels().AddDimension(r.Context(), req.Name); err != nil {
			a.writeLabelsError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"name": req.Name, "values": []string{}})
	})
}

// governRequest 是标签治理（停用 / 改名 / 合并）的请求体。
type governRequest struct {
	Label string `json:"label"` // deactivate 用
	To    string `json:"to"`    // rename 用
	From  string `json:"from"`  // merge 用
	Into  string `json:"into"`  // merge 用
}

// governLabel 处理 POST /api/v1/labels/{rename|merge|deactivate}（管理员）。
// 合并时改写历史工单上的标签；时间线事件保持当时记下的原文。
func (a *API) governLabel(w http.ResponseWriter, r *http.Request, action string) {
	if _, ok := a.adminFromRequestFor(w, r, "访客不能查看或维护标签",
		"浏览字典（GET /api/v1/labels）和添加别名（POST /api/v1/labels/aliases）对成员开放"); !ok {
		return
	}
	var req governRequest
	if !decodeJSON(w, r, &req, `{"label": "维度:值"}`) {
		return
	}
	switch action {
	case "rename":
		rewritten, err := a.tickets.Labels().Rename(r.Context(), req.Label, req.To)
		if err != nil {
			a.writeLabelsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"from": req.Label, "to": req.To, "tickets_updated": rewritten})
	case "merge":
		rewritten, err := a.tickets.Labels().Merge(r.Context(), req.From, req.Into)
		if err != nil {
			a.writeLabelsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"from": req.From, "into": req.Into, "tickets_updated": rewritten})
	case "deactivate":
		changed, err := a.tickets.Labels().Deactivate(r.Context(), req.Label)
		if err != nil {
			a.writeLabelsError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"label": req.Label, "deactivated": changed})
	}
}

// setTicketLabelsRequest 是 POST /api/v1/tickets/{id}/labels 的请求体。
type setTicketLabelsRequest struct {
	Add      []string `json:"add"`
	Remove   []string `json:"remove"`
	AllowNew bool     `json:"allow_new"`
}

// setTicketLabels 处理 POST /api/v1/tickets/{编号或序号}/labels：
// 打标签 / 摘标签（仅成员与成员 Agent；访客 403 members_only）。
func (a *API) setTicketLabels(w http.ResponseWriter, r *http.Request, ref string) {
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	if _, err := a.tickets.ForViewer(r.Context(), viewer, ref); err != nil {
		a.writeTicketErrorForViewer(w, r, ref, viewer, err)
		return
	}
	var req setTicketLabelsRequest
	if !decodeJSON(w, r, &req, `{"add": ["维度:值"], "remove": [], "allow_new": false}`) {
		return
	}
	result, err := a.tickets.SetLabels(r.Context(), tickets.SetLabelsInput{
		Ref:      ref,
		Add:      req.Add,
		Remove:   req.Remove,
		AllowNew: req.AllowNew,
		Actor:    viewer,
	})
	if err != nil {
		if errors.Is(err, tickets.ErrNotFound) {
			a.writeTicketErrorForViewer(w, r, ref, viewer, err)
			return
		}
		a.writeLabelsError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, setLabelsResponse{
		Labels:                  result.Labels,
		Changed:                 changedView{Added: result.Added, Removed: result.Removed, AliasResolved: result.AliasResolved},
		CreatedDictionaryValues: result.CreatedDictionaryValues,
	})
}

// ---- 视图结构 ----

type labelValueView struct {
	Value   string   `json:"value"`
	Label   string   `json:"label"`
	Aliases []string `json:"aliases"`
}

type labelDimensionView struct {
	Name   string           `json:"name"`
	Values []labelValueView `json:"values"`
}

type dictionaryView struct {
	Dimensions []labelDimensionView `json:"dimensions"`
	// Plain 列出不带维度的标签；指定 dimension 时省略。
	Plain *[]string `json:"plain,omitempty"`
}

type matchView struct {
	Label string  `json:"label"`
	Via   string  `json:"via"`
	Alias *string `json:"alias,omitempty"` // via == alias 时是命中的那个别名
}

type resolveView struct {
	Q       string      `json:"q"`
	Matches []matchView `json:"matches"`
	Hint    string      `json:"hint,omitempty"`
}

type aliasView struct {
	Label   string   `json:"label"`
	Aliases []string `json:"aliases"`
}

// changedView 是 POST …/labels 响应里「这次实际改了什么」：加上的、摘掉的、
// 以及写别名被换成标准值的回执。
type changedView struct {
	Added         []string                  `json:"added"`
	Removed       []string                  `json:"removed"`
	AliasResolved []tickets.AliasResolution `json:"alias_resolved"`
}

// setLabelsResponse 是 POST /api/v1/tickets/{id}/labels 的响应（200）。
type setLabelsResponse struct {
	Labels                  []string    `json:"labels"`
	Changed                 changedView `json:"changed"`
	CreatedDictionaryValues []string    `json:"created_dictionary_values"`
}

// writeLabelsError 把标签字典模块的错误映射成 HTTP 错误。
func (a *API) writeLabelsError(w http.ResponseWriter, err error) {
	var unknown *labels.UnknownValueError
	var deactivated *labels.LabelDeactivatedError
	switch {
	case errors.As(err, &unknown):
		WriteErrorExtra(w, http.StatusUnprocessableEntity, CodeUnknownLabelValue,
			unknown.Error(), "改用字典中已有的值，或带 allow_new: true 新建；完整字典见 GET /api/v1/labels",
			map[string]any{"suggestions": unknown.Suggestions})
	case errors.As(err, &deactivated):
		WriteError(w, http.StatusUnprocessableEntity, CodeLabelDeactivated, deactivated.Error(),
			"这个值已停用，不能再新打；历史工单上仍保留。如需恢复找管理员")
	case errors.Is(err, labels.ErrUnknownDimension):
		WriteError(w, http.StatusUnprocessableEntity, CodeUnknownDimension, "标签维度不存在",
			"新维度只能由管理员添加（POST /api/v1/labels/dimensions）；现有维度见 GET /api/v1/labels")
	case errors.Is(err, labels.ErrWildcardNotAllowed):
		WriteError(w, http.StatusUnprocessableEntity, CodeWildcardNotAllowed, err.Error(),
			"维度:* 只用于查询筛选（如 label=机型:*），不能作为标签打到工单上")
	case errors.Is(err, labels.ErrAliasTaken):
		WriteError(w, http.StatusConflict, CodeAliasTaken, err.Error(),
			"给两个值用不同的写法，或请管理员用 PATCH /api/v1/labels/aliases 改指向")
	case errors.Is(err, labels.ErrAliasExists):
		WriteError(w, http.StatusConflict, CodeAliasExists, err.Error(), "")
	case errors.Is(err, labels.ErrAliasRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeAliasRequired, err.Error(), "")
	case errors.Is(err, labels.ErrAliasNotFound):
		WriteError(w, http.StatusNotFound, CodeAliasNotFound, err.Error(),
			"先 POST /api/v1/labels/aliases 添加这个别名，或检查别名和维度是否写对")
	case errors.Is(err, labels.ErrNameRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeNameRequired, err.Error(), "")
	case errors.Is(err, labels.ErrDimensionTaken):
		WriteError(w, http.StatusConflict, CodeDimensionTaken, err.Error(), "")
	case errors.Is(err, labels.ErrValueTaken):
		WriteError(w, http.StatusConflict, CodeValueTaken, err.Error(),
			"如果本意是把两个值合成一个，改用 POST /api/v1/labels/merge")
	case errors.Is(err, labels.ErrSameLabel):
		WriteError(w, http.StatusUnprocessableEntity, CodeSameLabel, err.Error(), "")
	case errors.Is(err, labels.ErrDimensionMismatch):
		WriteError(w, http.StatusUnprocessableEntity, CodeDimensionMismatch, err.Error(),
			"改名或合并的两端要么都是同一个维度的值，要么都是不带维度的标签")
	case errors.Is(err, labels.ErrQRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeQRequired, err.Error(), "用 ?q=俗称 或 ?q=维度:值 查")
	case errors.Is(err, labels.ErrLabelRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeLabelRequired, err.Error(), "")
	default:
		panic(err)
	}
}

func trimPathPrefix(p, prefix string) string {
	return strings.TrimPrefix(p, prefix)
}
