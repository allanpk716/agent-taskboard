// 模板的 HTTP 接口（#34）：列表、读取（JSON 或 Markdown 填写指南）、
// 新建、修改（产生新版本）与停用。任何成员（含成员 Agent）都能维护模板。
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/allanpk716/agent-taskboard/internal/template"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// handleTemplatesRoot 处理模板集合：
//
//	GET  /api/v1/templates        列表（含已停用，带 active 标记）
//	POST /api/v1/templates        新建（key + 模板 Markdown）
func (a *API) handleTemplatesRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.listTemplates(w, r)
	case http.MethodPost:
		a.createTemplate(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
	}
}

// handleTemplateRef 处理单个模板：
//
//	GET   /api/v1/templates/{key}[?format=markdown]        读取（JSON 或填写指南）
//	GET   /api/v1/templates/{key}/versions                历史版本（#42 模板维护页）
//	PATCH /api/v1/templates/{key}                          修改（产生新版本）
//	POST  /api/v1/templates/{key}/deactivate               停用（只能停用，不能删除）
func (a *API) handleTemplateRef(w http.ResponseWriter, r *http.Request) {
	key, sub, hasSub := templateRefAndSub(r.URL.Path)
	if !hasSub {
		switch r.Method {
		case http.MethodGet:
			a.readTemplate(w, r, key)
		case http.MethodPatch:
			a.updateTemplate(w, r, key)
		default:
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "")
		}
		return
	}
	if sub == "versions" {
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "版本列表用 GET 读取")
			return
		}
		a.listTemplateVersions(w, r, key)
		return
	}
	if sub == "deactivate" {
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "不支持的请求方法", "停用模板用 POST")
			return
		}
		a.deactivateTemplate(w, r, key)
		return
	}
	WriteError(w, http.StatusNotFound, CodeNotFound, "没有这个接口",
		"模板接口：GET/PATCH /api/v1/templates/{key}、POST …/deactivate")
}

// templateRefAndSub 从路径 /api/v1/templates/{key}[/{子资源}] 里取出 key 与子资源。
func templateRefAndSub(path string) (key, sub string, hasSub bool) {
	rest := strings.TrimPrefix(path, "/api/v1/templates/")
	key, sub, hasSub = strings.Cut(rest, "/")
	return key, sub, hasSub
}

// templateGroupView 是模板分组的对外结构（#63）：组名与副标题；不分组时
// groups 为空（省略）。
type templateGroupView struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

// templateFieldView 是字段定义的对外结构；维度字段的 options 在读取时
// 从标签字典现取（字典会变，不能缓存在模板里），末尾带「不确定」；
// option_aliases 是各候选值的别名（#39：下拉框按别名搜索，随字典现取）；
// group 是所属分组的序号（#63，从 0 起），不分组时不出现。
type templateFieldView struct {
	Key           string              `json:"key"`
	Title         string              `json:"title"`
	Guide         string              `json:"guide"`
	Example       string              `json:"example"`
	Required      bool                `json:"required"`
	Type          string              `json:"type"` // text | radio | dimension
	Options       []string            `json:"options"`
	Dimension     string              `json:"dimension,omitempty"`
	OptionAliases map[string][]string `json:"option_aliases,omitempty"`
	Group         *int                `json:"group,omitempty"`
}

// templateSummaryView 是列表项。
type templateSummaryView struct {
	Key          string   `json:"key"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Scenario     string   `json:"scenario"`
	Version      int64    `json:"version"`
	Active       bool     `json:"active"`
	PresetLabels []string `json:"preset_labels"`
	URL          string   `json:"url"`
}

// templateDetailView 是 GET /api/v1/templates/{key} 的响应。
type templateDetailView struct {
	templateSummaryView
	AgentNote string              `json:"agent_note"`
	Fields    []templateFieldView `json:"fields"`
	Groups    []templateGroupView `json:"groups,omitempty"` // 字段分组（#63）；不分组时省略
	Markdown  string              `json:"markdown"`
	UpdatedBy *authorView         `json:"updated_by"`
	UpdatedAt string              `json:"updated_at"`
}

func (a *API) listTemplates(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.memberFromRequest(w, r); !ok {
		return
	}
	list, err := a.templates.List(r.Context())
	if err != nil {
		panic(err)
	}
	out := struct {
		Templates []templateSummaryView `json:"templates"`
	}{Templates: []templateSummaryView{}}
	for _, t := range list {
		out.Templates = append(out.Templates, templateSummaryView{
			Key: t.Key, Name: t.Name, Kind: t.Kind, Scenario: t.Scenario, Version: t.Version, Active: t.Active,
			PresetLabels: nonNil(t.PresetLabels), URL: "/api/v1/templates/" + t.Key,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// templateFieldsView 把模板字段定义转成对外结构；维度字段的 options 从标签字典
// 现取（字典会变，不能缓存在模板里），末尾带「不确定」。反馈入口的匿名读取
// （#35）复用同一结构。
func (a *API) templateFieldsView(r *http.Request, t templates.Template) []templateFieldView {
	fields := []templateFieldView{}
	for _, f := range t.Fields {
		fv := templateFieldView{
			Key: f.Key, Title: f.Title, Guide: f.Guide, Example: f.Example,
			Required: f.Required, Type: f.Type, Options: nonNil(f.Options), Dimension: f.Dimension,
			Group: f.Group,
		}
		if f.Type == template.TypeDimension {
			// 维度字段带上字典中的候选值和「不确定」选项（spec「读取」）。
			a.fillDimensionOptions(r.Context(), &fv)
		}
		fields = append(fields, fv)
	}
	return fields
}

// fillDimensionOptions 给维度字段补上字典候选值（末尾带「不确定」）与各候选值
// 的别名。候选值随字典变化，读取时现取（#39：补全说明与反馈入口的表单下拉框
// 按别名搜索；字典端点仅成员可用，别名随字段定义一并下发）。
func (a *API) fillDimensionOptions(ctx context.Context, fv *templateFieldView) {
	dict, err := a.tickets.Labels().Dictionary(ctx, fv.Dimension)
	if err != nil {
		panic(err)
	}
	values := []string{}
	var aliases map[string][]string
	for _, d := range dict.Dimensions {
		for _, v := range d.Values {
			values = append(values, v.Value)
			if len(v.Aliases) > 0 {
				if aliases == nil {
					aliases = map[string][]string{}
				}
				aliases[v.Value] = append([]string{}, v.Aliases...)
			}
		}
	}
	fv.Options = append(values, template.UnsureOption)
	fv.OptionAliases = aliases
}

func (a *API) readTemplate(w http.ResponseWriter, r *http.Request, key string) {
	if _, ok := a.memberFromRequest(w, r); !ok {
		return
	}
	t, err := a.templates.Get(r.Context(), key)
	if err != nil {
		a.writeTemplatesError(w, err)
		return
	}
	if r.URL.Query().Get("format") == "markdown" {
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(a.guide(r, t)))
		return
	}
	view := templateDetailView{
		templateSummaryView: templateSummaryView{
			Key: t.Key, Name: t.Name, Kind: t.Kind, Scenario: t.Scenario, Version: t.Version, Active: t.Active,
			PresetLabels: nonNil(t.PresetLabels), URL: "/api/v1/templates/" + t.Key,
		},
		AgentNote: t.AgentNote,
		Groups:    groupViewsOf(t.Groups),
		Markdown:  t.Markdown,
		UpdatedAt: t.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	if u := t.UpdatedBy; u.MemberID != 0 {
		av := newAuthorView(authorOfUpdater(u), false)
		view.UpdatedBy = &av
	}
	view.Fields = a.templateFieldsView(r, t)
	writeJSON(w, http.StatusOK, view)
}

// groupViewsOf 把模板分组转成对外结构；不分组时返回 nil（JSON 里省略）。
func groupViewsOf(groups []template.Group) []templateGroupView {
	if len(groups) == 0 {
		return nil
	}
	out := make([]templateGroupView, 0, len(groups))
	for _, g := range groups {
		out = append(out, templateGroupView{Title: g.Title, Subtitle: g.Subtitle})
	}
	return out
}

// guide 组装填写指南：各维度的候选值现取字典。
func (a *API) guide(r *http.Request, t templates.Template) string {
	candidates := map[string][]string{}
	for _, f := range t.Fields {
		if f.Type != template.TypeDimension {
			continue
		}
		if _, done := candidates[f.Dimension]; done {
			continue
		}
		values, err := a.tickets.Labels().DimensionValues(r.Context(), f.Dimension)
		if err != nil {
			panic(err)
		}
		candidates[f.Dimension] = values
	}
	return template.Guide(t.Key, t.Definition, t.Version, candidates)
}

// listTemplateVersions 处理 GET /api/v1/templates/{key}/versions：列出历史版本
// （新→旧），每版带修改人、时间与模板原文（#42 模板维护页）。
type templateVersionView struct {
	Version   int64       `json:"version"`
	Name      string      `json:"name"`
	Markdown  string      `json:"markdown"`
	UpdatedBy *authorView `json:"updated_by"`
	UpdatedAt string      `json:"updated_at"`
}

func (a *API) listTemplateVersions(w http.ResponseWriter, r *http.Request, key string) {
	if _, ok := a.memberFromRequest(w, r); !ok {
		return
	}
	versions, err := a.templates.Versions(r.Context(), key)
	if err != nil {
		a.writeTemplatesError(w, err)
		return
	}
	out := struct {
		Versions []templateVersionView `json:"versions"`
	}{Versions: []templateVersionView{}}
	for _, v := range versions {
		vv := templateVersionView{
			Version: v.Version, Name: v.Name, Markdown: v.Markdown,
			UpdatedAt: v.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if v.UpdatedBy.MemberID != 0 {
			av := newAuthorView(authorOfUpdater(v.UpdatedBy), false)
			vv.UpdatedBy = &av
		}
		out.Versions = append(out.Versions, vv)
	}
	writeJSON(w, http.StatusOK, out)
}

// templateWriteRequest 是 POST /api/v1/templates 与 PATCH /api/v1/templates/{key}
// 的请求体。
type templateWriteRequest struct {
	Key      string `json:"key"`      // 新建时必填；修改时用路径里的 key
	Markdown string `json:"markdown"` // 模板原文
}

func (a *API) createTemplate(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	var req templateWriteRequest
	if !decodeJSON(w, r, &req, `{"key": "bug-report", "markdown": "---\n名称: …\n---\n## 问句？ {必填}\n引导说明"}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		t, err := a.templates.Create(r.Context(), req.Key, req.Markdown, viewer)
		if err != nil {
			a.writeTemplatesError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, templateSummaryView{
			Key: t.Key, Name: t.Name, Kind: t.Kind, Scenario: t.Scenario, Version: t.Version, Active: t.Active,
			PresetLabels: nonNil(t.PresetLabels), URL: "/api/v1/templates/" + t.Key,
		})
	})
}

func (a *API) updateTemplate(w http.ResponseWriter, r *http.Request, key string) {
	viewer, ok := a.memberFromRequest(w, r)
	if !ok {
		return
	}
	var req templateWriteRequest
	if !decodeJSON(w, r, &req, `{"markdown": "---\n名称: …\n---\n## 问句？ {必填}\n引导说明"}`) {
		return
	}
	a.withIdempotency(w, r, func(w http.ResponseWriter, r *http.Request) {
		t, err := a.templates.Update(r.Context(), key, req.Markdown, viewer)
		if err != nil {
			a.writeTemplatesError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, templateSummaryView{
			Key: t.Key, Name: t.Name, Kind: t.Kind, Version: t.Version, Active: t.Active,
			PresetLabels: nonNil(t.PresetLabels), URL: "/api/v1/templates/" + t.Key,
		})
	})
}

func (a *API) deactivateTemplate(w http.ResponseWriter, r *http.Request, key string) {
	if _, ok := a.memberFromRequest(w, r); !ok {
		return
	}
	changed, err := a.templates.Deactivate(r.Context(), key)
	if err != nil {
		a.writeTemplatesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "active": false, "deactivated": changed})
}

// writeTemplatesError 把模板模块的错误映射成 HTTP 错误；格式错误带行号。
func (a *API) writeTemplatesError(w http.ResponseWriter, err error) {
	var parseErr *template.ParseError
	switch {
	case errors.As(err, &parseErr):
		WriteError(w, http.StatusUnprocessableEntity, CodeTemplateFormatError,
			"模板格式错误："+parseErr.Error(), "按报错行号修正模板后重新保存；写法见 docs/api.md 的模板语法")
	case errors.Is(err, templates.ErrKeyRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeTemplateKeyRequired, err.Error(),
			`key 用小写字母、数字和连字符，如 "check-report"`)
	case errors.Is(err, templates.ErrInvalidKey):
		WriteError(w, http.StatusUnprocessableEntity, CodeInvalidTemplateKey, err.Error(),
			`key 用小写字母、数字和连字符，如 "check-report"`)
	case errors.Is(err, templates.ErrMarkdownRequired):
		WriteError(w, http.StatusUnprocessableEntity, CodeMarkdownRequired, err.Error(), "")
	case errors.Is(err, templates.ErrKeyTaken):
		WriteError(w, http.StatusConflict, CodeTemplateKeyTaken, err.Error(),
			"模板 key 不能复用（历史工单还引用着）；换一个 key，或用 PATCH 修改现有模板")
	case errors.Is(err, templates.ErrNotFound):
		WriteError(w, http.StatusNotFound, CodeTemplateNotFound, "没有这个模板",
			"模板列表见 GET /api/v1/templates")
	case errors.Is(err, templates.ErrInactive):
		WriteError(w, http.StatusUnprocessableEntity, CodeTemplateInactive, err.Error(),
			"已停用的模板不能再用来建单；换用其他模板或新建一份")
	default:
		panic(err)
	}
}

// authorOfUpdater 把模板版本的修改人转成作者结构。
func authorOfUpdater(u templates.Updater) tickets.Author {
	return tickets.Author{
		MemberID:   u.MemberID,
		MemberName: u.MemberName,
		AgentID:    u.AgentID,
		AgentName:  u.AgentName,
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
