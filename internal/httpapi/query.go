// 查询接口（#37）：
//
//	GET /api/v1/tickets           条件列表（仅成员；访客 403 members_only）
//	GET /api/v1/tickets/counts    按一个维度计数（筛选条件与列表相同）
//
// 参数白名单、错误码、摘要字段、偏移量翻页与 format=markdown 的表格都照
// prototype/ticket-query；变更序号 cursor 由事件写入时同事务推进。
package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/labels"
	"github.com/allanpk716/agent-taskboard/internal/markdownview"
	"github.com/allanpk716/agent-taskboard/internal/templates"
	"github.com/allanpk716/agent-taskboard/internal/tickets"
)

// 列表接受的查询参数（写错 → 422 unknown_parameter）。
var queryAllowedParams = []string{
	"status", "kind", "template", "label", "no_label", "assignee", "creator", "waiting_on", "q",
	"actor_kind",
	"created_after", "created_before", "updated_after", "updated_before", "closed_after", "closed_before",
	"changed_after", "sort", "limit", "page_token", "include", "format",
}

// 计数不接受的列表参数（只做计数，不给排序与翻页）。
var countsRejectedParams = []string{"sort", "limit", "page_token", "include", "format"}

// 计数分组的内建维度（group_by 还可以是一个标签维度）。
var countsBuiltinGroups = []string{"status", "kind", "assignee", "close_reason", "waiting_on"}

// 支持的排序键（同键时一律按工单序号倒序）。
var querySorts = []string{"-updated", "updated", "-created", "created", "-closed", "change"}

// handleTicketList 处理 GET /api/v1/tickets：条件、排序、limit/翻页、摘要、
// include=brief、format=markdown；响应带 total/next/cursor。
func (a *API) handleTicketList(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.memberFromRequestFor(w, r, "访客用 GET /api/v1/session 找到自己的工单，再用它的地址读取")
	if !ok {
		return
	}
	q := r.URL.Query()
	if apiErr := rejectUnknownParams(q, queryAllowedParams, "group_by 只用于 GET /api/v1/tickets/counts"); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	if v := q.Get("include"); v != "" && v != "brief" {
		writeAPIError(w, NewError(http.StatusUnprocessableEntity, CodeInvalidInclude,
			"include 目前只支持 brief", "带说明用 include=brief"))
		return
	}
	fq, resolved, apiErr := a.parseQueryFilters(r.Context(), viewer, q)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}

	// 排序：默认 -updated；带 changed_after 时默认按变更顺序。
	sortKey := q.Get("sort")
	if sortKey == "" {
		sortKey = "-updated"
		if fq.ChangedAfter != nil {
			sortKey = "change"
		}
	}
	if !containsStringIn(querySorts, sortKey) {
		writeAPIError(w, NewError(http.StatusUnprocessableEntity, CodeInvalidSort,
			"不支持的排序 "+sortKey, "可用："+strings.Join(querySorts, "、")))
		return
	}
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			writeAPIError(w, NewError(http.StatusUnprocessableEntity, CodeInvalidLimit,
				"limit 必须是 1–200 的整数", "要拿全部就跟着 next 翻页"))
			return
		}
		limit = n
	}
	offset := 0
	if raw := q.Get("page_token"); raw != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(raw, "p"))
		if err != nil || n < 0 {
			writeAPIError(w, NewError(http.StatusUnprocessableEntity, CodeInvalidPageToken,
				"page_token 无效", "使用上一页响应里的 next 地址"))
			return
		}
		offset = n
	}

	rows, err := a.tickets.QueryList(r.Context(), fq)
	if err != nil {
		panic(err)
	}
	sortListRows(rows, sortKey)

	cursor, err := a.tickets.CurrentChange(r.Context())
	if err != nil {
		panic(err)
	}
	include := q.Get("include")
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	page := rows[offset:end]
	items := make([]ticketListItemView, 0, len(page))
	for i := range page {
		items = append(items, newTicketListItem(&page[i], include))
	}
	var next *string
	if offset+limit < len(rows) {
		nq := cloneQuery(q)
		nq.Set("page_token", "p"+strconv.Itoa(offset+limit))
		address := "/api/v1/tickets?" + nq.Encode()
		next = &address
	}

	if q.Get("format") == "markdown" {
		md := markdownview.List(markdownview.ListInput{
			Total: int64(len(rows)), Count: len(page), ResolvedAliases: resolved, Next: next, Cursor: cursor,
			Rows: listMarkdownRows(page),
		})
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(md))
		return
	}
	body := map[string]any{
		"total":   len(rows),
		"count":   len(page),
		"items":   items,
		"next":    next,
		"cursor":  cursor,
		"filters": queryFiltersEcho(fq, sortKey),
	}
	if len(resolved) > 0 {
		body["resolved_aliases"] = resolved
	}
	writeJSON(w, http.StatusOK, body)
}

// handleTicketCounts 处理 GET /api/v1/tickets/counts?group_by=…：筛选条件与
// 列表相同；一张工单在该维度有多个值时每个值各算一次；没有该维度计入 (无)。
func (a *API) handleTicketCounts(w http.ResponseWriter, r *http.Request) {
	viewer, ok := a.memberFromRequestFor(w, r, "访客用 GET /api/v1/session 找到自己的工单，再用它的地址读取")
	if !ok {
		return
	}
	q := r.URL.Query()
	groupBy := q.Get("group_by")
	if groupBy == "" {
		writeAPIError(w, NewError(http.StatusUnprocessableEntity, CodeGroupByRequired,
			"需要 group_by", "可以是一个标签维度，或 "+strings.Join(countsBuiltinGroups, "、")))
		return
	}
	dims, err := a.tickets.Labels().DimensionNames(r.Context())
	if err != nil {
		panic(err)
	}
	if !containsStringIn(dims, groupBy) && !containsStringIn(countsBuiltinGroups, groupBy) {
		writeAPIError(w, NewError(http.StatusUnprocessableEntity, CodeInvalidGroupBy,
			"不能按「"+groupBy+"」分组",
			"可以是一个标签维度（"+strings.Join(dims, "、")+"），或 "+strings.Join(countsBuiltinGroups, "、")))
		return
	}
	for _, k := range countsRejectedParams {
		if q.Has(k) {
			writeAPIError(w, NewError(http.StatusUnprocessableEntity, CodeUnknownParameter,
				"计数接口不接受 "+k, "计数不做排序与翻页；要逐张看就查列表 GET /api/v1/tickets"))
			return
		}
	}
	allowed := append([]string{"group_by"}, queryAllowedParams...)
	if apiErr := rejectUnknownParams(q, allowed, ""); apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	fq, resolved, apiErr := a.parseQueryFilters(r.Context(), viewer, q)
	if apiErr != nil {
		writeAPIError(w, apiErr)
		return
	}
	rows, err := a.tickets.QueryList(r.Context(), fq)
	if err != nil {
		panic(err)
	}
	cursor, err := a.tickets.CurrentChange(r.Context())
	if err != nil {
		panic(err)
	}

	// 有序 tally：同数时保持首次出现的顺序（照原型 Object.entries 的插入序）。
	var order []string
	tally := map[string]int64{}
	bump := func(k string) {
		if _, ok := tally[k]; !ok {
			order = append(order, k)
		}
		tally[k]++
	}
	for i := range rows {
		row := &rows[i]
		if containsStringIn(dims, groupBy) {
			values := []string{}
			for _, l := range row.Full.Labels {
				if strings.HasPrefix(l, groupBy+":") {
					values = append(values, strings.TrimPrefix(l, groupBy+":"))
				}
			}
			if len(values) == 0 {
				bump("(无)")
			} else {
				for _, v := range values {
					bump(v)
				}
			}
			continue
		}
		switch groupBy {
		case "assignee":
			if row.Full.AssigneeID == 0 {
				bump("(未指派)")
			} else {
				bump(row.Full.AssigneeName)
			}
		case "close_reason":
			if row.Full.CloseReason == "" {
				bump("(未关闭)")
			} else {
				bump(markdownview.CloseReasonText(row.Full.CloseReason))
			}
		case "waiting_on":
			bump(row.WaitingOn.Text)
		case "status":
			bump(row.Full.Status)
		case "kind":
			bump(row.Full.Kind)
		}
	}
	groups := make([]groupView, 0, len(order))
	for _, k := range order {
		groups = append(groups, groupView{Value: k, Count: tally[k]})
	}
	sort.SliceStable(groups, func(i, j int) bool { return groups[i].Count > groups[j].Count })

	body := map[string]any{
		"group_by": groupBy,
		"total":    len(rows),
		"groups":   groups,
		"filters":  queryFiltersEcho(fq, ""),
		"cursor":   cursor,
	}
	if len(resolved) > 0 {
		body["resolved_aliases"] = resolved
	}
	if containsStringIn(dims, groupBy) {
		body["note"] = "同一工单在该维度有多个值时每个值各算一次，各组之和可能大于 total"
	}
	writeJSON(w, http.StatusOK, body)
}

// groupView 是计数响应里的一个组。
type groupView struct {
	Value string `json:"value"`
	Count int64  `json:"count"`
}

// rejectUnknownParams 检查查询参数都在白名单里，否则返回 unknown_parameter。
// extraHint 追加在可用参数列表后（如列表接口拒绝 group_by）。
func rejectUnknownParams(q url.Values, allowed []string, extraHint string) *Error {
	for k := range q {
		if !containsStringIn(allowed, k) {
			hint := "可用参数：" + strings.Join(allowed, "、")
			if extraHint != "" {
				hint += "；" + extraHint
			}
			return NewError(http.StatusUnprocessableEntity, CodeUnknownParameter,
				"不认识的参数 "+k, hint)
		}
	}
	return nil
}

// cloneQuery 复制一份查询参数（构造 next 地址用）。
func cloneQuery(q url.Values) url.Values {
	nq := url.Values{}
	for k, vs := range q {
		for _, v := range vs {
			nq.Add(k, v)
		}
	}
	return nq
}

// parseQueryFilters 解析列表与计数共用的筛选参数（status/kind/template、
// label/no_label、assignee/creator/waiting_on、时间范围、q、changed_after）。
// 返回解析结果、别名回显（"写法 → 标准值"）与错误。
func (a *API) parseQueryFilters(ctx context.Context, viewer identity.Viewer, q url.Values) (tickets.ListQuery, []string, *Error) {
	fq := tickets.ListQuery{Status: "open", ViewerMemberID: viewer.Member.ID}
	if s := q.Get("status"); s != "" {
		if !containsStringIn([]string{"open", "closed", "all"}, s) {
			return fq, nil, NewError(http.StatusUnprocessableEntity, CodeInvalidStatus,
				"status 只能是 open、closed 或 all", "")
		}
		fq.Status = s
	}
	if k := q.Get("kind"); k != "" {
		if !containsStringIn([]string{"task", "report"}, k) {
			return fq, nil, NewError(http.StatusUnprocessableEntity, CodeInvalidKind,
				"kind 只能是 task 或 report", "")
		}
		fq.Kind = k
	}
	if tpl := q.Get("template"); tpl != "" {
		if _, err := a.templates.Get(ctx, tpl); err != nil {
			if errors.Is(err, templates.ErrNotFound) {
				hint := ""
				if list, lerr := a.templates.List(ctx); lerr == nil {
					keys := make([]string, 0, len(list))
					for _, tp := range list {
						keys = append(keys, tp.Key)
					}
					hint = "可用：" + strings.Join(keys, "、")
				}
				return fq, nil, NewError(http.StatusUnprocessableEntity, CodeTemplateNotFound,
					"没有模板 "+tpl, hint)
			}
			panic(err)
		}
		fq.Template = tpl
	}
	var resolved []string
	resolveLabels := func(key string, dst *[]string) *Error {
		for _, raw := range q[key] {
			if strings.TrimSpace(raw) == "" {
				continue
			}
			res, err := a.tickets.Labels().ResolveFilter(ctx, raw)
			if err != nil {
				var unknown *labels.UnknownValueError
				switch {
				case errors.As(err, &unknown):
					return NewErrorExtra(http.StatusUnprocessableEntity, CodeUnknownLabelValue,
						unknown.Error(), "改用字典中的标准值；完整字典见 GET /api/v1/labels",
						map[string]any{"suggestions": unknown.Suggestions})
				case errors.Is(err, labels.ErrUnknownDimension):
					dim := raw
					if i := strings.Index(raw, ":"); i >= 0 {
						dim = raw[:i]
					}
					return NewError(http.StatusUnprocessableEntity, CodeUnknownDimension,
						"没有标签维度「"+dim+"」", "新维度只能由管理员添加")
				default:
					panic(err)
				}
			}
			if res.Via == "alias" {
				resolved = append(resolved, res.Alias+" → "+res.Label)
			}
			*dst = append(*dst, res.Label)
		}
		return nil
	}
	if err := resolveLabels("label", &fq.Labels); err != nil {
		return fq, nil, err
	}
	if err := resolveLabels("no_label", &fq.NoLabels); err != nil {
		return fq, nil, err
	}

	if v := q.Get("assignee"); v != "" {
		fq.AssigneeSet = true
		switch v {
		case "none":
			fq.AssigneeNone = true
			fq.AssigneeName = "none"
		case "me":
			fq.AssigneeID = viewer.Member.ID
			fq.AssigneeName = viewer.Member.Name
		default:
			id, name, err := a.tickets.ResolveMemberRef(ctx, v)
			if err != nil {
				return fq, nil, a.unknownMemberQueryError(ctx, v)
			}
			fq.AssigneeID, fq.AssigneeName = id, name
		}
	}
	if v := q.Get("creator"); v != "" {
		fq.CreatorSet = true
		switch v {
		case "guest":
			fq.CreatorGuest = true
			fq.CreatorName = "guest"
		case "me":
			fq.CreatorID = viewer.Member.ID
			fq.CreatorName = viewer.Member.Name
		default:
			id, name, err := a.tickets.ResolveMemberRef(ctx, v)
			if err != nil {
				return fq, nil, a.unknownMemberQueryError(ctx, v)
			}
			fq.CreatorID, fq.CreatorName = id, name
		}
	}
	if v := q.Get("waiting_on"); v != "" {
		if !containsStringIn([]string{"assignee", "creator", "guest", "department", "me"}, v) {
			return fq, nil, NewError(http.StatusUnprocessableEntity, CodeInvalidWaitingOn,
				"waiting_on 只能是 assignee、creator、guest、department 或 me", "")
		}
		fq.WaitingOn = v
	}
	if v := q.Get("actor_kind"); v != "" {
		if !containsStringIn([]string{"member", "agent"}, v) {
			return fq, nil, NewError(http.StatusUnprocessableEntity, CodeInvalidActorKind,
				"actor_kind 只能是 member（本人或访客署名）或 agent（成员 Agent 署名）", "")
		}
		fq.ActorKind = v
	}
	ranges := []struct {
		prefix string
		after  **time.Time
		before **time.Time
	}{
		{"created", &fq.CreatedAfter, &fq.CreatedBefore},
		{"updated", &fq.UpdatedAfter, &fq.UpdatedBefore},
		{"closed", &fq.ClosedAfter, &fq.ClosedBefore},
	}
	for _, rg := range ranges {
		if v := q.Get(rg.prefix + "_after"); v != "" {
			t, apiErr := parseQueryTime(rg.prefix+"_after", v)
			if apiErr != nil {
				return fq, nil, apiErr
			}
			*rg.after = &t
		}
		if v := q.Get(rg.prefix + "_before"); v != "" {
			t, apiErr := parseQueryTime(rg.prefix+"_before", v)
			if apiErr != nil {
				return fq, nil, apiErr
			}
			*rg.before = &t
		}
	}
	if v := q.Get("changed_after"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return fq, nil, NewError(http.StatusUnprocessableEntity, CodeInvalidCursor,
				"changed_after 必须是上次响应里的 cursor（整数）", "")
		}
		fq.ChangedAfter = &n
	}
	fq.Q = q.Get("q")
	return fq, resolved, nil
}

// unknownMemberQueryError 组装筛选里 unknown_member 的错误（hint 列出可选成员）。
func (a *API) unknownMemberQueryError(ctx context.Context, ref string) *Error {
	hint := ""
	if names := a.tickets.MemberNames(ctx); len(names) > 0 {
		hint = "可写成员名、me；成员：" + strings.Join(names, "、")
	}
	return NewError(http.StatusUnprocessableEntity, CodeUnknownMember, "没有成员「"+ref+"」", hint)
}

// parseQueryTime 解析时间筛选：日期（默认当天 00:00）或日期加时分，北京时间。
// _after 包含该时刻、_before 不包含（由比较处保证）。
func parseQueryTime(key, val string) (time.Time, *Error) {
	bad := func() *Error {
		return NewError(http.StatusUnprocessableEntity, CodeInvalidTime,
			key+" 的格式不对："+val, "写 2026-10-01 或 2026-10-01T09:30；_after 包含该时刻，_before 不包含")
	}
	normalized := ""
	switch {
	case queryDateOnly.MatchString(val):
		normalized = val + " 00:00"
	case queryDateMinute.MatchString(val):
		normalized = strings.Replace(val, "T", " ", 1)
	default:
		return time.Time{}, bad()
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", normalized, clock.Beijing)
	if err != nil {
		return time.Time{}, bad()
	}
	return t, nil
}

var (
	queryDateOnly   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	queryDateMinute = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}$`)
)

// queryFiltersEcho 组装响应里的 filters 回显：解析后的标准条件 + 实际排序。
func queryFiltersEcho(fq tickets.ListQuery, sortKey string) map[string]any {
	labels := fq.Labels
	if labels == nil {
		labels = []string{}
	}
	noLabels := fq.NoLabels
	if noLabels == nil {
		noLabels = []string{}
	}
	echo := map[string]any{
		"status":   fq.Status,
		"label":    labels,
		"no_label": noLabels,
	}
	if fq.Kind != "" {
		echo["kind"] = fq.Kind
	}
	if fq.Template != "" {
		echo["template"] = fq.Template
	}
	if fq.AssigneeSet {
		echo["assignee"] = fq.AssigneeName
	}
	if fq.CreatorSet {
		echo["creator"] = fq.CreatorName
	}
	if fq.WaitingOn != "" {
		echo["waiting_on"] = fq.WaitingOn
	}
	if fq.ActorKind != "" {
		echo["actor_kind"] = fq.ActorKind
	}
	timeEcho := func(name string, t *time.Time) {
		if t != nil {
			echo[name] = t.Format("2006-01-02 15:04")
		}
	}
	timeEcho("created_after", fq.CreatedAfter)
	timeEcho("created_before", fq.CreatedBefore)
	timeEcho("updated_after", fq.UpdatedAfter)
	timeEcho("updated_before", fq.UpdatedBefore)
	timeEcho("closed_after", fq.ClosedAfter)
	timeEcho("closed_before", fq.ClosedBefore)
	if fq.ChangedAfter != nil {
		echo["changed_after"] = *fq.ChangedAfter
	}
	if fq.Q != "" {
		echo["q"] = fq.Q
	}
	if sortKey != "" {
		echo["sort"] = sortKey
	}
	return echo
}

// sortListRows 按排序键排序列表行；同键时按工单序号倒序（照原型）。
func sortListRows(rows []tickets.ListRow, sortKey string) {
	cmpTime := func(a, b time.Time) int {
		switch {
		case a.Before(b):
			return -1
		case b.Before(a):
			return 1
		}
		return 0
	}
	sort.SliceStable(rows, func(i, j int) bool {
		var c int
		switch sortKey {
		case "-updated":
			c = cmpTime(rows[j].Meta.UpdatedAt, rows[i].Meta.UpdatedAt)
		case "updated":
			c = cmpTime(rows[i].Meta.UpdatedAt, rows[j].Meta.UpdatedAt)
		case "-created":
			c = cmpTime(rows[j].Meta.CreatedAt, rows[i].Meta.CreatedAt)
		case "created":
			c = cmpTime(rows[i].Meta.CreatedAt, rows[j].Meta.CreatedAt)
		case "-closed":
			c = cmpTime(rows[j].Meta.ClosedAt, rows[i].Meta.ClosedAt)
		case "change":
			c = cmpInt64(rows[i].Meta.Change, rows[j].Meta.Change)
		}
		if c == 0 {
			c = cmpInt64(rows[j].Full.Seq, rows[i].Full.Seq)
		}
		return c < 0
	})
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// ticketListItemView 是列表项的摘要（spec「查询」：每项只返回摘要，时间线
// 不在列表里给，要看就读单张工单）。
type ticketListItemView struct {
	Number              string        `json:"number"`
	Seq                 int64         `json:"seq"`
	URL                 string        `json:"url"`
	Kind                string        `json:"kind"`
	Title               string        `json:"title"`
	Status              string        `json:"status"`
	CloseReason         *string       `json:"close_reason"`
	Template            *string       `json:"template"`
	Creator             any           `json:"creator"`
	Assignee            any           `json:"assignee"`
	CreatorDeactivated  *bool         `json:"creator_deactivated,omitempty"`
	AssigneeDeactivated *bool         `json:"assignee_deactivated,omitempty"`
	Labels              []string      `json:"labels"`
	WaitingOn           waitingOnView `json:"waiting_on"`
	CreatedAt           time.Time     `json:"created_at"`
	UpdatedAt           time.Time     `json:"updated_at"`
	ClosedAt            *time.Time    `json:"closed_at"`
	ReplyCount          int           `json:"reply_count"`
	LastChange          int64         `json:"last_change"`
	ReadFull            string        `json:"read_full"`
	MatchedIn           []string      `json:"matched_in,omitempty"`
	Snippet             string        `json:"snippet,omitempty"`
	Brief               *briefView    `json:"brief,omitempty"` // include=brief 时附带说明
}

// newTicketListItem 把一行投影组装成摘要；q 命中时带 matched_in/snippet。
func newTicketListItem(row *tickets.ListRow, include string) ticketListItemView {
	t := &row.Full
	v := ticketListItemView{
		Number:     t.Number,
		Seq:        t.Seq,
		URL:        "/t/" + t.Number,
		Kind:       t.Kind,
		Title:      t.Title,
		Status:     t.Status,
		Labels:     t.Labels,
		WaitingOn:  newWaitingOnView(row.WaitingOn),
		CreatedAt:  row.Meta.CreatedAt,
		UpdatedAt:  row.Meta.UpdatedAt,
		LastChange: row.Meta.Change,
		ReadFull:   "/api/v1/tickets/" + t.Number + "?format=markdown",
	}
	if t.Creator.MemberID != 0 {
		v.Creator = t.Creator.MemberName
		v.CreatorDeactivated = deactivatedFlag(t.Creator.MemberDeactivated)
	} else {
		v.Creator = "访客"
	}
	if t.AssigneeID != 0 {
		v.Assignee = t.AssigneeName
		v.AssigneeDeactivated = deactivatedFlag(t.AssigneeDeactivated)
	}
	if t.CloseReason != "" {
		v.CloseReason = &t.CloseReason
	}
	if t.TemplateKey != "" {
		v.Template = &t.TemplateKey
	}
	if !row.Meta.ClosedAt.IsZero() {
		closed := row.Meta.ClosedAt
		v.ClosedAt = &closed
	}
	for _, e := range t.Timeline {
		if e.Type == "reply" {
			v.ReplyCount++
		}
	}
	if row.Hit != nil {
		v.MatchedIn = row.Hit.MatchedIn
		v.Snippet = row.Hit.Snippet
	}
	if include == "brief" {
		b := briefView{Markdown: t.BriefMD, Revision: t.BriefRevision}
		if t.TemplateKey != "" {
			key := t.TemplateKey
			b.Template = &key
			fields := t.Fields
			b.Fields = &fields
		}
		v.Brief = &b
	}
	return v
}

func deactivatedFlag(deactivated bool) *bool {
	if !deactivated {
		return nil
	}
	value := true
	return &value
}

// listMarkdownRows 把本页行转成 Markdown 表格的行输入。
func listMarkdownRows(page []tickets.ListRow) []markdownview.ListRow {
	out := make([]markdownview.ListRow, 0, len(page))
	for i := range page {
		row := &page[i]
		item := markdownview.ListRow{
			Number:        row.Full.Number,
			Title:         row.Full.Title,
			Status:        row.Full.Status,
			CloseReason:   row.Full.CloseReason,
			Assignee:      row.Full.AssigneeName + markdownDeactivated(row.Full.AssigneeDeactivated),
			WaitingOnText: row.WaitingOn.Text,
			Labels:        row.Full.Labels,
			UpdatedAt:     row.Meta.UpdatedAt,
		}
		if row.Hit != nil {
			item.MatchedIn = row.Hit.MatchedIn
			item.Snippet = row.Hit.Snippet
		}
		out = append(out, item)
	}
	return out
}

func markdownDeactivated(deactivated bool) string {
	if deactivated {
		return "（已停用）"
	}
	return ""
}

func containsStringIn(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
