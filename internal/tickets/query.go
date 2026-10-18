// 查询（#37）：列表与计数共用的筛选、摘要元数据、关键词规范化匹配与全局
// 变更序号读取。筛选与等待方都从现有投影推导，不引入新的存储状态；
// 变更序号在事件写入时同事务推进（见 writes.go 的 appendEvent）。
package tickets

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// ListQuery 是一次列表/计数查询的全部筛选条件（HTTP 层解析好成员与标签后填入；
// 名字已换成标准值，通配写法 维度:* 原样保留）。零值字段表示「不过滤」。
type ListQuery struct {
	Status   string // open | closed | all（默认 open 由 HTTP 层填）
	Kind     string // task | report
	Template string // 模板 key

	Labels   []string // 必须全部带有（可含 维度:*）
	NoLabels []string // 必须一个都不带

	AssigneeSet  bool
	AssigneeID   int64  // AssigneeNone 为 false 时的成员 id
	AssigneeName string // 回显用
	AssigneeNone bool   // assignee=none：未指派

	CreatorSet   bool
	CreatorID    int64
	CreatorName  string // 回显用
	CreatorGuest bool   // creator=guest：经反馈入口提交

	WaitingOn      string // assignee | creator | guest | department | me
	ViewerMemberID int64  // waiting_on=me 时按谁的收件箱算

	// ActorKind 是「本人 / Agent」筛选（orchestrator 决议 5，评审 P2-12 修正）：
	// 按创建者的署名区分——member = 真人署名（成员本人或未自报 via_agent 的
	// 访客），agent = 成员 Agent 署名，或访客自报「由 Agent 写成」（via_agent）。
	ActorKind string

	CreatedAfter, CreatedBefore *time.Time // _after 包含该时刻，_before 不包含
	UpdatedAfter, UpdatedBefore *time.Time
	ClosedAfter, ClosedBefore   *time.Time

	ChangedAfter *int64 // 只返回变更序号大于它的工单
	Q            string // 关键词（标题、说明与回复，忽略大小写、空格和连字符）
}

// TicketMeta 是列表摘要需要的工单元数据：创建/更新/关闭时间与最后变更序号，
// 全部从时间线推导（更新时间 = 最后一条事件，关闭时间 = 最后一条 closed 事件）。
type TicketMeta struct {
	CreatedAt time.Time
	UpdatedAt time.Time
	ClosedAt  time.Time // 未关闭（或已重开）时为零值
	Change    int64     // 该单最后一条事件的变更序号
}

// SearchHit 是关键词命中的位置与片段（查重用）。
type SearchHit struct {
	MatchedIn []string // 标题 / 说明 / 回复 N
	Snippet   string   // 首个命中的前后片段
}

// ListRow 是筛选通过的一行：完整投影 + 元数据 + 等待方 + 命中。
type ListRow struct {
	Full      Full
	Meta      TicketMeta
	WaitingOn WaitingOn
	Hit       *SearchHit // 带 q 时非 nil
}

// QueryList 按条件筛出全部工单（工单序号顺序；排序由调用方做）。
// 遍历用 ByRef 逐张取完整投影，等待方、标签、时间都从时间线推导——
// 与收件箱同一套真相，不另存会漂移的状态。
func (m *Manager) QueryList(ctx context.Context, q ListQuery) ([]ListRow, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT seq FROM tickets ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var seqs []int64
	for rows.Next() {
		var seq int64
		if err := rows.Scan(&seq); err != nil {
			return nil, err
		}
		seqs = append(seqs, seq)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []ListRow{}
	for _, seq := range seqs {
		full, err := m.ByRef(ctx, strconv.FormatInt(seq, 10))
		if err != nil {
			return nil, err
		}
		turn, err := m.WaitingOn(ctx, full)
		if err != nil {
			return nil, err
		}
		if !q.matches(&full, turn) {
			continue
		}
		var hit *SearchHit
		if q.Q != "" {
			if hit = SearchIn(q.Q, full.Title, full.BriefMD, full.Timeline); hit == nil {
				continue
			}
		}
		out = append(out, ListRow{
			Full:      full,
			Meta:      MetaOf(full),
			WaitingOn: turn,
			Hit:       hit,
		})
	}
	return out, nil
}

// MetaOf 从时间线推导列表摘要需要的元数据。
func MetaOf(full Full) TicketMeta {
	meta := TicketMeta{CreatedAt: full.CreatedAt, Change: full.LastChange}
	if n := len(full.Timeline); n > 0 {
		last := full.Timeline[n-1]
		meta.UpdatedAt = last.At
		meta.Change = last.Change
	}
	if full.Status == "closed" {
		for i := len(full.Timeline) - 1; i >= 0; i-- {
			if full.Timeline[i].Type == "closed" {
				meta.ClosedAt = full.Timeline[i].At
				break
			}
		}
	}
	return meta
}

// matches 报告工单是否通过全部筛选条件（照 prototype/ticket-query 的 select）。
func (q *ListQuery) matches(t *Full, turn WaitingOn) bool {
	if q.Status != "all" && t.Status != q.Status {
		return false
	}
	if q.Kind != "" && t.Kind != q.Kind {
		return false
	}
	if q.Template != "" && t.TemplateKey != q.Template {
		return false
	}
	for _, l := range q.Labels {
		if !hasWildcardLabel(t.Labels, l) {
			return false
		}
	}
	for _, l := range q.NoLabels {
		if hasWildcardLabel(t.Labels, l) {
			return false
		}
	}
	if q.AssigneeSet {
		if q.AssigneeNone {
			if t.AssigneeID != 0 {
				return false
			}
		} else if t.AssigneeID != q.AssigneeID {
			return false
		}
	}
	if q.CreatorSet {
		if q.CreatorGuest {
			if t.Creator.MemberID != 0 {
				return false
			}
		} else if t.Creator.MemberID != q.CreatorID {
			return false
		}
	}
	if q.WaitingOn != "" {
		if q.WaitingOn == "me" {
			if turn.MemberID != q.ViewerMemberID {
				return false
			}
		} else if turn.Party != q.WaitingOn {
			return false
		}
	}
	switch q.ActorKind {
	case "member":
		if t.Creator.AgentID != 0 || t.Creator.ViaAgent {
			return false
		}
	case "agent":
		if t.Creator.AgentID == 0 && !t.Creator.ViaAgent {
			return false
		}
	}
	meta := MetaOf(*t)
	if q.CreatedAfter != nil && meta.CreatedAt.Before(*q.CreatedAfter) ||
		q.CreatedBefore != nil && !meta.CreatedAt.Before(*q.CreatedBefore) {
		return false
	}
	if q.UpdatedAfter != nil && meta.UpdatedAt.Before(*q.UpdatedAfter) ||
		q.UpdatedBefore != nil && !meta.UpdatedAt.Before(*q.UpdatedBefore) {
		return false
	}
	if (q.ClosedAfter != nil || q.ClosedBefore != nil) && meta.ClosedAt.IsZero() {
		return false
	}
	if q.ClosedAfter != nil && meta.ClosedAt.Before(*q.ClosedAfter) ||
		q.ClosedBefore != nil && !meta.ClosedAt.Before(*q.ClosedBefore) {
		return false
	}
	if q.ChangedAfter != nil && meta.Change <= *q.ChangedAfter {
		return false
	}
	return true
}

// hasWildcardLabel 报告工单是否带有该标签；`维度:*` 表示这个维度有任何一个值。
func hasWildcardLabel(labels []string, l string) bool {
	if strings.HasSuffix(l, ":*") {
		prefix := l[:len(l)-1] // "维度:"
		for _, x := range labels {
			if strings.HasPrefix(x, prefix) {
				return true
			}
		}
		return false
	}
	for _, x := range labels {
		if x == l {
			return true
		}
	}
	return false
}

// CurrentChange 返回当前全局变更序号（列表/计数响应的 cursor；无事件时为 0）。
func (m *Manager) CurrentChange(ctx context.Context) (int64, error) {
	var c int64
	if err := m.db.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(change_seq), 0) FROM timeline_events`).Scan(&c); err != nil {
		return 0, err
	}
	return c, nil
}

// ResolveMemberRef 把查询参数里的成员写法（成员 id、姓名或用户名）解析成成员；
// 查无此人返回 UnknownMemberError（与指派不同：这里不把成员 Agent 当特例）。
func (m *Manager) ResolveMemberRef(ctx context.Context, ref string) (int64, string, error) {
	ref = strings.TrimSpace(ref)
	var (
		id   int64
		name string
		err  error
	)
	if n, convErr := strconv.ParseInt(ref, 10, 64); convErr == nil {
		err = m.db.QueryRowContext(ctx, `SELECT id, name FROM members WHERE id = ?`, n).Scan(&id, &name)
	} else {
		err = m.db.QueryRowContext(ctx,
			`SELECT id, name FROM members WHERE name = ? OR username = ?`, ref, ref).Scan(&id, &name)
	}
	if err == nil {
		return id, name, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, "", err
	}
	return 0, "", &UnknownMemberError{Ref: ref}
}

// ---- 关键词匹配：忽略大小写、空格和连字符（E203 能找到 E-203）----

// NormQueryText 是搜索与规范化的匹配规则：小写、去掉空白、连字符和下划线。
func NormQueryText(s string) string {
	var b strings.Builder
	for _, r := range s {
		if unicode.IsSpace(r) || r == '-' || r == '_' {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// SearchIn 在标题、说明（含字段拼接的 Markdown）与回复正文里查找关键词，
// 返回命中位置与首个命中的片段；没命中返回 nil。照 prototype/ticket-query
// 的 search：保留「原文 → 规范化」的索引，用它在原文上切出片段。
func SearchIn(query, title, briefMD string, timeline []Event) *SearchHit {
	needle := NormQueryText(query)
	if needle == "" {
		return nil
	}
	runes := []rune(needle)
	hit := &SearchHit{MatchedIn: []string{}}
	look := func(label, body string) {
		str := []rune(body)
		var flat []rune
		idx := make([]int, 0, len(str)) // flat 里第 k 个字符在原文里的下标
		for k, c := range str {
			if n := NormQueryText(string(c)); n != "" {
				flat = append(flat, []rune(n)[0])
				idx = append(idx, k)
			}
		}
		p := indexOfRunes(flat, runes)
		if p < 0 {
			return
		}
		hit.MatchedIn = append(hit.MatchedIn, label)
		if hit.Snippet != "" {
			return // 片段只取第一个命中
		}
		start, end := idx[p], idx[p+len(runes)-1]+1
		lo, hi := start-15, end+25
		var sb strings.Builder
		if lo > 0 {
			sb.WriteString("…")
		} else {
			lo = 0
		}
		if hi > len(str) {
			hi = len(str)
		}
		sb.WriteString(string(str[lo:hi]))
		if hi < len(str) {
			sb.WriteString("…")
		}
		hit.Snippet = sb.String()
	}
	look("标题", title)
	look("说明", briefMD)
	for _, e := range timeline {
		if e.Type == "reply" {
			look("回复 "+strconv.FormatInt(e.ReplyNo, 10), e.BodyMD)
		}
	}
	if len(hit.MatchedIn) == 0 {
		return nil
	}
	return hit
}

// indexOfRunes 在 haystack 里找 needle（子串位置，没有返回 -1）。
func indexOfRunes(haystack, needle []rune) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}
