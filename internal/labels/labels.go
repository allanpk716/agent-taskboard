// Package labels 是「标签字典」模块：维度、值、别名与不带维度的标签；
// 解析（exact / alias / fuzzy）；停用、改名、合并（合并时改写历史工单上的标签）。
package labels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Manager 读写标签字典。
type Manager struct {
	db *sql.DB
}

func NewManager(db *sql.DB) *Manager { return &Manager{db: db} }

// DBTX 是能执行 SQL 的连接：*sql.DB 或事务里的 *sql.Tx 都满足它。
// allow_new 在工单事务里建字典值时用事务版方法，保证两边一起生效。
type DBTX interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// 字典操作的错误；HTTP 层把它们映射成错误码。
var (
	// ErrUnknownDimension 表示维度不存在（新维度只能由管理员添加）。
	ErrUnknownDimension = errors.New("标签维度不存在")
	// ErrWildcardNotAllowed 表示 维度:* 通配写法不能作为标签打到工单上。
	ErrWildcardNotAllowed = errors.New("维度:* 是查询用的通配写法，不能打到工单上")
	// ErrAliasTaken 表示这个写法已被同维度的另一个标准值占用。
	ErrAliasTaken = errors.New("这个写法已被同维度的另一个标准值占用")
	// ErrAliasExists 表示这个别名已经挂在这个标准值上。
	ErrAliasExists = errors.New("这个别名已经挂在这个标准值上")
	// ErrAliasRequired 表示别名为空。
	ErrAliasRequired = errors.New("别名不能为空")
	// ErrAliasNotFound 表示没有这个别名（改指向时）。
	ErrAliasNotFound = errors.New("没有这个别名")
	// ErrNameRequired 表示维度名为空。
	ErrNameRequired = errors.New("维度名不能为空")
	// ErrDimensionTaken 表示维度已存在。
	ErrDimensionTaken = errors.New("维度已存在")
	// ErrValueTaken 表示改名或合并的目标值已存在。
	ErrValueTaken = errors.New("目标标签值已存在")
	// ErrSameLabel 表示改名的目标就是原值，或合并的两个值相同。
	ErrSameLabel = errors.New("两个标签是同一个")
	// ErrDimensionMismatch 表示改名或合并的两端维度不一致（或不带维度的标签与维度值混用）。
	ErrDimensionMismatch = errors.New("两个标签的维度不一致")
	// ErrQRequired 表示 resolve 缺少查询词。
	ErrQRequired = errors.New("缺少查询词 q")
	// ErrLabelRequired 表示请求里的标签为空。
	ErrLabelRequired = errors.New("标签不能为空")
)

// UnknownValueError 表示一个标签不在字典中。
// Suggestions 是同维度（或不带维度标签）里的近似值，帮调用方自纠。
type UnknownValueError struct {
	Label       string
	Suggestions []string
}

func (e *UnknownValueError) Error() string {
	return fmt.Sprintf("标签字典中没有 %s", e.Label)
}

// LabelDeactivatedError 表示标签值已被管理员停用：不能新打，历史工单上仍保留。
type LabelDeactivatedError struct {
	Label string
}

func (e *LabelDeactivatedError) Error() string {
	return fmt.Sprintf("标签值 %s 已停用", e.Label)
}

// Validate 校验 labels 里的每个标签都是字典中「在用」的值：
// `维度:值` 要求维度和值都已存在且未停用；不带维度的标签要求在 plain 标签里。
// 建单（POST /api/v1/tickets）用这条严格校验；别名与近似写法经
// POST …/labels 的解析流程换成标准值。遇到第一个问题就返回。
func (m *Manager) Validate(ctx context.Context, labels []string) error {
	for _, label := range labels {
		dim, val, hasDim := SplitLabel(label)
		if !hasDim {
			var active, deactivated int
			if err := m.db.QueryRowContext(ctx,
				`SELECT COALESCE(SUM(deactivated = 0), 0), COALESCE(SUM(deactivated = 1), 0)
				 FROM plain_labels WHERE label = ?`, label).
				Scan(&active, &deactivated); err != nil {
				return err
			}
			if deactivated > 0 {
				return &LabelDeactivatedError{Label: label}
			}
			if active == 0 {
				plains, err := m.plainLabels(ctx)
				if err != nil {
					return err
				}
				return &UnknownValueError{Label: label, Suggestions: suggest(label, plains)}
			}
			continue
		}
		var active, deactivated int
		if err := m.db.QueryRowContext(ctx,
			`SELECT COALESCE(SUM(deactivated = 0), 0), COALESCE(SUM(deactivated = 1), 0)
			 FROM label_values WHERE dimension = ? AND value = ?`, dim, val).
			Scan(&active, &deactivated); err != nil {
			return err
		}
		if deactivated > 0 {
			return &LabelDeactivatedError{Label: label}
		}
		if active > 0 {
			continue
		}
		values, err := m.DimensionValues(ctx, dim)
		if err != nil {
			return err
		}
		q := make([]string, len(values))
		for i, v := range values {
			q[i] = dim + ":" + v
		}
		return &UnknownValueError{Label: label, Suggestions: suggest(label, q)}
	}
	return nil
}

// DimensionValues 返回维度下在用的值；维度不存在时返回空列表（由调用方决定怎么报错）。
func (m *Manager) DimensionValues(ctx context.Context, dimension string) ([]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT value FROM label_values WHERE dimension = ? AND deactivated = 0 ORDER BY rowid`, dimension)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		values = append(values, v)
	}
	return values, rows.Err()
}

// DimensionNames 返回全部维度（hint 示例用）。
func (m *Manager) DimensionNames(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT name FROM label_dimensions ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

// HasDimension 报告维度是否已在字典中（allow_new 造不出新维度时报 unknown_dimension 用）。
func (m *Manager) HasDimension(ctx context.Context, dimension string) (bool, error) {
	var known int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM label_dimensions WHERE name = ?`, dimension).Scan(&known); err != nil {
		return false, err
	}
	return known > 0, nil
}

func (m *Manager) plainLabels(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT label FROM plain_labels WHERE deactivated = 0 ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		labels = append(labels, l)
	}
	return labels, rows.Err()
}

// SplitLabel 把「维度:值」拆开；没有冒号时 hasDim 为 false。
// 值里还可能出现冒号（如时间），只按第一个冒号拆。
func SplitLabel(label string) (dim, val string, hasDim bool) {
	i := strings.Index(label, ":")
	if i < 0 {
		return "", label, false
	}
	return label[:i], label[i+1:], true
}

// norm 是标签匹配用的规范化：忽略大小写、空格、连字符和下划线
// （与 ticket-query 原型一致，E-203 能对上 E203）。
func norm(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch r {
		case ' ', '-', '_', '\t':
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// suggest 挑出与 q 规范化后相等或互相包含的字典值（最多 5 个）。
func suggest(q string, values []string) []string {
	nq := norm(q)
	var out []string
	for _, v := range values {
		nv := norm(v)
		if nq == "" || nv == "" {
			continue
		}
		if nv == nq || strings.Contains(nv, nq) || strings.Contains(nq, nv) {
			out = append(out, v)
			if len(out) >= 5 {
				break
			}
		}
	}
	return out
}

// ---- 字典数据的一次性快照（解析、列表共用） ----

// dictionaryValue 是字典里的一个值：别名与停用状态。
type dictionaryValue struct {
	value       string
	aliases     []string
	deactivated bool
}

// plainValue 是一个不带维度的标签。
type plainValue struct {
	label       string
	deactivated bool
}

// dictionaryData 是一次读取得到的字典快照。字典只有几百行，整读后做纯解析。
type dictionaryData struct {
	dimensionOrder []string
	values         map[string][]dictionaryValue
	plain          []plainValue
}

func (m *Manager) loadDictionary(ctx context.Context) (*dictionaryData, error) {
	d := &dictionaryData{values: map[string][]dictionaryValue{}}
	dims, err := m.DimensionNames(ctx)
	if err != nil {
		return nil, err
	}
	d.dimensionOrder = dims
	// 先读别名（按「维度\x00值」分组），再读值并挂上：避免往可能因 append
	// 重新分配的切片里写数据。
	aliasesByValue := map[string][]string{}
	rows2, err := m.db.QueryContext(ctx,
		`SELECT dimension, alias, value FROM label_aliases ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows2.Close()
	for rows2.Next() {
		var dim, alias, val string
		if err := rows2.Scan(&dim, &alias, &val); err != nil {
			return nil, err
		}
		aliasesByValue[dim+"\x00"+val] = append(aliasesByValue[dim+"\x00"+val], alias)
	}
	if err := rows2.Err(); err != nil {
		return nil, err
	}
	rows, err := m.db.QueryContext(ctx,
		`SELECT dimension, value, deactivated FROM label_values ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var dim, val string
		var deact int
		if err := rows.Scan(&dim, &val, &deact); err != nil {
			return nil, err
		}
		d.values[dim] = append(d.values[dim], dictionaryValue{
			value: val, deactivated: deact == 1, aliases: aliasesByValue[dim+"\x00"+val],
		})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows3, err := m.db.QueryContext(ctx,
		`SELECT label, deactivated FROM plain_labels ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows3.Close()
	for rows3.Next() {
		var label string
		var deact int
		if err := rows3.Scan(&label, &deact); err != nil {
			return nil, err
		}
		d.plain = append(d.plain, plainValue{label: label, deactivated: deact == 1})
	}
	return d, rows3.Err()
}

// activeDimension 返回维度下在用的值。
func (d *dictionaryData) activeDimension(dim string) []dictionaryValue {
	var out []dictionaryValue
	for _, v := range d.values[dim] {
		if !v.deactivated {
			out = append(out, v)
		}
	}
	return out
}

func (d *dictionaryData) allDimension(dim string) []dictionaryValue {
	return d.values[dim]
}

func (d *dictionaryData) hasDimension(dim string) bool {
	for _, n := range d.dimensionOrder {
		if n == dim {
			return true
		}
	}
	return false
}

func (d *dictionaryData) activePlain() []string {
	var out []string
	for _, p := range d.plain {
		if !p.deactivated {
			out = append(out, p.label)
		}
	}
	return out
}

// ---- 打标签 / 摘标签时的解析 ----

// ApplyCandidate 是「一个写法解析成要打到工单上的标准值」的结果：
// Via 为 exact / alias 表示已换成标准值；Via 为空表示字典里没有
// （带 allow_new 时可以新建），Ambiguous 表示有多个候选、即便 allow_new 也不能新建。
type ApplyCandidate struct {
	Requested   string
	Resolved    string
	Via         string
	Suggestions []string
	Ambiguous   bool
}

// ResolveAdd 把一个写法（标准值、近似写法或别名）解析成要打到工单上的标准值。
// 停用值与通配写法返回错误；「字典里没有」不是错误，由调用方配合 allow_new 决定。
func (m *Manager) ResolveAdd(ctx context.Context, q string) (ApplyCandidate, error) {
	d, err := m.loadDictionary(ctx)
	if err != nil {
		return ApplyCandidate{}, err
	}
	return d.resolveAdd(q)
}

func (d *dictionaryData) resolveAdd(q string) (ApplyCandidate, error) {
	q = strings.TrimSpace(q)
	cand := ApplyCandidate{Requested: q}
	if q == "*" {
		return cand, ErrWildcardNotAllowed
	}
	dim, val, hasDim := SplitLabel(q)
	if hasDim && val == "*" {
		return cand, ErrWildcardNotAllowed
	}
	if hasDim {
		cand.Requested = dim + ":" + val
		if !d.hasDimension(dim) {
			// 维度不存在：不带 allow_new 时按「字典里没有」报，带了由调用方报 unknown_dimension。
			return cand, nil
		}
		actives := d.activeDimension(dim)
		// exact：规范化后与值相同（忽略大小写、空格、连字符、下划线）。
		var exact []string
		for _, v := range actives {
			if norm(v.value) == norm(val) {
				exact = append(exact, v.value)
			}
		}
		if len(exact) == 1 {
			cand.Resolved, cand.Via = dim+":"+exact[0], "exact"
			return cand, nil
		}
		if len(exact) > 1 {
			return ApplyCandidate{Requested: cand.Requested, Suggestions: withDim(dim, exact), Ambiguous: true}, nil
		}
		// alias：同维度里恰好有一个在用的值认领这个写法时换成标准值，多义不换。
		switch target, values := d.aliasTarget(dim, val); target {
		case 1:
			cand.Resolved, cand.Via = dim+":"+values[0], "alias"
			return cand, nil
		case -1:
			return ApplyCandidate{Requested: cand.Requested, Suggestions: withDim(dim, values), Ambiguous: true}, nil
		}
		// 停用值：不能新打。
		for _, v := range d.allDimension(dim) {
			if v.deactivated && norm(v.value) == norm(val) {
				return cand, &LabelDeactivatedError{Label: dim + ":" + v.value}
			}
		}
		full := make([]string, len(actives))
		for i, v := range actives {
			full[i] = dim + ":" + v.value
		}
		cand.Suggestions = suggest(q, full)
		return cand, nil
	}
	// 不带维度：先对上不带维度的标签，再把跨维度只有一个值认领的裸别名换成标准值。
	for _, p := range d.plain {
		if !p.deactivated && norm(p.label) == norm(q) {
			cand.Resolved, cand.Via = p.label, "exact"
			return cand, nil
		}
	}
	switch target, labels := d.crossDimensionAliasTarget(q); target {
	case 1:
		cand.Resolved, cand.Via = labels[0], "alias"
		return cand, nil
	case -1:
		return ApplyCandidate{Requested: q, Suggestions: labels, Ambiguous: true}, nil
	}
	for _, p := range d.plain {
		if p.deactivated && norm(p.label) == norm(q) {
			return cand, &LabelDeactivatedError{Label: p.label}
		}
	}
	cand.Suggestions = suggest(q, d.activePlain())
	return cand, nil
}

// withDim 给候选值补上「维度:」前缀。
func withDim(dim string, values []string) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = dim + ":" + v
	}
	return out
}

// aliasTarget 统计维度 dim 里认领写法 q 的在用值：返回 1 时 values 是那个值，
// -1 时是多义候选，0 时没有。停用值的别名不参与。
func (d *dictionaryData) aliasTarget(dim, q string) (int, []string) {
	nq := norm(q)
	var hits []string
	for _, v := range d.activeDimension(dim) {
		for _, a := range v.aliases {
			if norm(a) == nq {
				hits = appendUnique(hits, v.value)
			}
		}
	}
	switch len(hits) {
	case 0:
		return 0, nil
	case 1:
		return 1, hits
	default:
		return -1, hits
	}
}

// crossDimensionAliasTarget 统计整个字典里认领裸写法 q 的在用值（跨维度）。
func (d *dictionaryData) crossDimensionAliasTarget(q string) (int, []string) {
	nq := norm(q)
	var hits []string
	for _, dim := range d.dimensionOrder {
		for _, v := range d.activeDimension(dim) {
			for _, a := range v.aliases {
				if norm(a) == nq {
					hits = appendUnique(hits, dim+":"+v.value)
				}
			}
		}
	}
	switch len(hits) {
	case 0:
		return 0, nil
	case 1:
		return 1, hits
	default:
		return -1, hits
	}
}

func appendUnique(list []string, s string) []string {
	for _, x := range list {
		if x == s {
			return list
		}
	}
	return append(list, s)
}

// ResolveRemove 把要摘掉的写法解析成标准值。工单上可能还带着停用值
// （管理员停用或合并后），所以解析时包含停用值。解析不到返回 UnknownValueError。
func (m *Manager) ResolveRemove(ctx context.Context, q string) (string, error) {
	d, err := m.loadDictionary(ctx)
	if err != nil {
		return "", err
	}
	q = strings.TrimSpace(q)
	if q == "*" {
		return "", ErrWildcardNotAllowed
	}
	dim, val, hasDim := SplitLabel(q)
	if hasDim && val == "*" {
		return "", ErrWildcardNotAllowed
	}
	if hasDim {
		if !d.hasDimension(dim) {
			return "", &UnknownValueError{Label: q}
		}
		var exact []string
		for _, v := range d.allDimension(dim) { // 含停用值：工单上可能还带着
			if norm(v.value) == norm(val) {
				exact = append(exact, v.value)
			}
		}
		if len(exact) == 1 {
			return dim + ":" + exact[0], nil
		}
		if len(exact) > 1 {
			return "", &UnknownValueError{Label: q, Suggestions: withDim(dim, exact)}
		}
		switch target, values := d.aliasTarget(dim, val); target {
		case 1:
			return dim + ":" + values[0], nil
		case -1:
			return "", &UnknownValueError{Label: q, Suggestions: withDim(dim, values)}
		}
		all := make([]string, len(d.values[dim]))
		for i, v := range d.values[dim] {
			all[i] = dim + ":" + v.value
		}
		return "", &UnknownValueError{Label: q, Suggestions: suggest(q, all)}
	}
	for _, p := range d.plain {
		if norm(p.label) == norm(q) {
			return p.label, nil
		}
	}
	switch target, labels := d.crossDimensionAliasTarget(q); target {
	case 1:
		return labels[0], nil
	case -1:
		return "", &UnknownValueError{Label: q, Suggestions: labels}
	}
	var all []string
	for _, p := range d.plain {
		all = append(all, p.label)
	}
	return "", &UnknownValueError{Label: q, Suggestions: suggest(q, all)}
}

// CreateValueTx 在维度下新建一个字典值（allow_new）。在工单打标签的事务里调用，
// 保证「值进字典」和「标签上工单」一起生效。
func (m *Manager) CreateValueTx(ctx context.Context, q DBTX, dimension, value string) error {
	_, err := q.ExecContext(ctx,
		`INSERT INTO label_values (dimension, value) VALUES (?, ?)`, dimension, value)
	return err
}

// CreatePlainTx 新建一个不带维度的标签（allow_new）。
func (m *Manager) CreatePlainTx(ctx context.Context, q DBTX, label string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO plain_labels (label) VALUES (?)`, label)
	return err
}

// ---- 字典列表与 resolve ----

// ValueView 是字典列表里的一个值。
type ValueView struct {
	Value   string
	Aliases []string
}

// DimensionView 是字典列表里的一个维度。
type DimensionView struct {
	Name   string
	Values []ValueView
}

// DictionaryView 是 GET /api/v1/labels 的投影；不带 dimension 时附带不带维度的标签。
type DictionaryView struct {
	Dimensions []DimensionView
	Plain      []string
}

// Dictionary 列出字典：每个值带别名；dimension 非空时只列该维度（不带维度的标签省略）。
// 停用的值不列出（它不能再被新打，历史工单上仍保留）。
func (m *Manager) Dictionary(ctx context.Context, dimension string) (*DictionaryView, error) {
	d, err := m.loadDictionary(ctx)
	if err != nil {
		return nil, err
	}
	view := &DictionaryView{}
	dims := d.dimensionOrder
	if dimension != "" {
		if !d.hasDimension(dimension) {
			return nil, ErrUnknownDimension
		}
		dims = []string{dimension}
	} else {
		view.Plain = d.activePlain()
	}
	for _, dim := range dims {
		dv := DimensionView{Name: dim}
		for _, v := range d.activeDimension(dim) {
			dv.Values = append(dv.Values, ValueView{Value: v.value, Aliases: v.aliases})
		}
		view.Dimensions = append(view.Dimensions, dv)
	}
	return view, nil
}

// Match 是 resolve 的一个候选：匹配方式 exact / alias / fuzzy。
type Match struct {
	Label string
	Via   string
	Alias string
}

// Resolve 按三级匹配（exact → alias → fuzzy）返回候选，同级按字典顺序。
// q 可以是裸写法、别名或「维度:值」；dimension 参数限定范围。停用值不参与。
// fuzzy 要求规范化后至少 2 个字符且互相包含。
func (m *Manager) Resolve(ctx context.Context, q, dimension string) ([]Match, error) {
	d, err := m.loadDictionary(ctx)
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, ErrQRequired
	}
	dim, val, hasDim := SplitLabel(q)
	scope := dimension
	if scope == "" && hasDim {
		scope = dim
	}
	if scope != "" && !d.hasDimension(scope) {
		return nil, ErrUnknownDimension
	}
	if hasDim && scope != dim {
		return nil, nil // 写法与限定的维度不一致：没有候选
	}
	type scopedValue struct{ dim, value string }
	var values []scopedValue
	var plains []string
	if scope != "" {
		for _, v := range d.activeDimension(scope) {
			values = append(values, scopedValue{scope, v.value})
		}
	} else {
		for _, dim := range d.dimensionOrder {
			for _, v := range d.activeDimension(dim) {
				values = append(values, scopedValue{dim, v.value})
			}
		}
		plains = d.activePlain()
	}
	nq := norm(val)
	seen := map[string]bool{}
	var exact, aliasM, fuzzy []Match
	push := func(tier *[]Match, label, via, alias string) {
		if seen[label] {
			return
		}
		seen[label] = true
		*tier = append(*tier, Match{Label: label, Via: via, Alias: alias})
	}
	for _, v := range values {
		if norm(v.value) == nq {
			push(&exact, v.dim+":"+v.value, "exact", "")
		}
	}
	for _, p := range plains {
		if norm(p) == nq {
			push(&exact, p, "exact", "")
		}
	}
	for _, dim := range d.dimensionOrder {
		if scope != "" && dim != scope {
			continue
		}
		for _, v := range d.activeDimension(dim) {
			label := dim + ":" + v.value
			for _, a := range v.aliases {
				if norm(a) == nq {
					push(&aliasM, label, "alias", a)
				}
			}
		}
	}
	if len(nq) >= 2 {
		for _, v := range values {
			nv := norm(v.value)
			if nv != "" && (strings.Contains(nv, nq) || strings.Contains(nq, nv)) {
				push(&fuzzy, v.dim+":"+v.value, "fuzzy", "")
			}
		}
		for _, p := range plains {
			np := norm(p)
			if np != "" && (strings.Contains(np, nq) || strings.Contains(nq, np)) {
				push(&fuzzy, p, "fuzzy", "")
			}
		}
	}
	return append(append(exact, aliasM...), fuzzy...), nil
}

// ---- 别名的维护 ----

// AddAlias 给字典中的标准值（维度:值）添加别名；任何成员都可以。
// label 会先按规范化解析到在用的标准值。返回该标准值现在的全部别名。
func (m *Manager) AddAlias(ctx context.Context, label, alias string) (string, []string, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "", nil, ErrAliasRequired
	}
	d, err := m.loadDictionary(ctx)
	if err != nil {
		return "", nil, err
	}
	dim, val, hasDim := SplitLabel(strings.TrimSpace(label))
	if !hasDim {
		return "", nil, &UnknownValueError{Label: label}
	}
	standard, err := d.standardValue(dim, val)
	if err != nil {
		return "", nil, err
	}
	target := strings.TrimPrefix(standard, dim+":")
	// 与该维度在用的值、已有别名冲突（规范化比较；别名只能挂 维度:值）。
	na := norm(alias)
	for _, v := range d.activeDimension(dim) {
		if norm(v.value) == na {
			if v.value == target {
				return "", nil, ErrAliasExists // 别名与标准值写法相同
			}
			return "", nil, ErrAliasTaken
		}
		for _, a := range v.aliases {
			if norm(a) == na {
				if v.value == target {
					return "", nil, ErrAliasExists
				}
				return "", nil, ErrAliasTaken
			}
		}
	}
	if _, err := m.db.ExecContext(ctx,
		`INSERT INTO label_aliases (dimension, alias, value) VALUES (?, ?, ?)`, dim, alias, target); err != nil {
		return "", nil, err
	}
	aliases, err := m.valueAliases(ctx, dim, target)
	return standard, aliases, err
}

// standardValue 在维度 dim 里按规范化找到唯一的在用标准值。
func (d *dictionaryData) standardValue(dim, val string) (string, error) {
	if !d.hasDimension(dim) {
		return "", &UnknownValueError{Label: dim + ":" + val}
	}
	target := ""
	for _, v := range d.activeDimension(dim) {
		if norm(v.value) == norm(val) {
			if target != "" {
				return "", &UnknownValueError{Label: dim + ":" + val}
			}
			target = v.value
		}
	}
	if target == "" {
		for _, v := range d.allDimension(dim) {
			if v.deactivated && norm(v.value) == norm(val) {
				return "", &LabelDeactivatedError{Label: dim + ":" + v.value}
			}
		}
		return "", &UnknownValueError{Label: dim + ":" + val}
	}
	return dim + ":" + target, nil
}

func (m *Manager) valueAliases(ctx context.Context, dim, value string) ([]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT alias FROM label_aliases WHERE dimension = ? AND value = ? ORDER BY rowid`, dim, value)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RepointAlias 把维度里已有的别名改指向另一个标准值；仅管理员。
func (m *Manager) RepointAlias(ctx context.Context, alias, label string) (string, []string, error) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "", nil, ErrAliasRequired
	}
	d, err := m.loadDictionary(ctx)
	if err != nil {
		return "", nil, err
	}
	dim, val, hasDim := SplitLabel(strings.TrimSpace(label))
	if !hasDim {
		return "", nil, &UnknownValueError{Label: label}
	}
	standard, err := d.standardValue(dim, val)
	if err != nil {
		return "", nil, err
	}
	// 找到这个维度里已有的别名行（规范化比较），改它的指向。
	row := ""
	for _, v := range d.allDimension(dim) {
		for _, a := range v.aliases {
			if norm(a) == norm(alias) {
				row = a
			}
		}
	}
	if row == "" {
		return "", nil, ErrAliasNotFound
	}
	if _, err := m.db.ExecContext(ctx,
		`UPDATE label_aliases SET value = ? WHERE dimension = ? AND alias = ?`,
		strings.TrimPrefix(standard, dim+":"), dim, row); err != nil {
		return "", nil, err
	}
	aliases, err := m.valueAliases(ctx, dim, strings.TrimPrefix(standard, dim+":"))
	return standard, aliases, err
}

// ---- 管理员治理：加维度、停用、改名、合并 ----

// AddDimension 添加一个新维度；仅管理员。
func (m *Manager) AddDimension(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrNameRequired
	}
	if strings.Contains(name, ":") {
		return ErrDimensionMismatch // 维度名里不能有冒号（会破坏 维度:值 的写法）
	}
	var known int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM label_dimensions WHERE name = ?`, name).Scan(&known); err != nil {
		return err
	}
	if known > 0 {
		return ErrDimensionTaken
	}
	_, err := m.db.ExecContext(ctx, `INSERT INTO label_dimensions (name) VALUES (?)`, name)
	return err
}

// Deactivate 停用一个标签值（或不带维度的标签）：历史工单上保留，不能再新打。
// 已停用时幂等成功，返回 false。仅管理员。
func (m *Manager) Deactivate(ctx context.Context, label string) (bool, error) {
	d, err := m.loadDictionary(ctx)
	if err != nil {
		return false, err
	}
	dim, val, hasDim := SplitLabel(strings.TrimSpace(label))
	if hasDim {
		if v := d.findValue(dim, val); v != nil {
			if v.deactivated {
				return false, nil
			}
			_, err := m.db.ExecContext(ctx,
				`UPDATE label_values SET deactivated = 1 WHERE dimension = ? AND value = ?`, dim, val)
			return true, err
		}
		return false, &UnknownValueError{Label: label}
	}
	for _, p := range d.plain {
		if p.label == val {
			if p.deactivated {
				return false, nil
			}
			_, err := m.db.ExecContext(ctx,
				`UPDATE plain_labels SET deactivated = 1 WHERE label = ?`, val)
			return true, err
		}
	}
	return false, &UnknownValueError{Label: label}
}

// Rename 把标签值改名（或不带维度的标签改名），并改写历史工单上的标签。
// 返回被改写的工单数。仅管理员。
func (m *Manager) Rename(ctx context.Context, from, to string) (int64, error) {
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	if from == "" || to == "" {
		return 0, ErrLabelRequired
	}
	fromDim, fromVal, fromHasDim := SplitLabel(from)
	toDim, toVal, toHasDim := SplitLabel(to)
	if fromHasDim != toHasDim || fromDim != toDim {
		return 0, ErrDimensionMismatch
	}
	if from == to {
		return 0, ErrSameLabel
	}
	return m.rewrite(ctx, fromHasDim, fromDim, fromVal, toVal, false)
}

// Merge 把 from 并入 into：历史工单上的标签改写成 into，from 的别名改指向 into，
// from 停用（字典不物理删除）。返回被改写的工单数。仅管理员。
func (m *Manager) Merge(ctx context.Context, from, into string) (int64, error) {
	from = strings.TrimSpace(from)
	into = strings.TrimSpace(into)
	if from == "" || into == "" {
		return 0, ErrLabelRequired
	}
	fromDim, fromVal, fromHasDim := SplitLabel(from)
	intoDim, intoVal, intoHasDim := SplitLabel(into)
	if fromHasDim != intoHasDim || fromDim != intoDim {
		return 0, ErrDimensionMismatch
	}
	if from == into {
		return 0, ErrSameLabel
	}
	return m.rewrite(ctx, fromHasDim, fromDim, fromVal, intoVal, true)
}

// rewrite 是改名与合并共用的改写逻辑：字典更新（改名或停用）+ 工单当前标签
// 改写（一张工单同时有两个值时去重）。时间线事件只追加，保持当时记下的原文。
func (m *Manager) rewrite(ctx context.Context, hasDim bool, dim, fromVal, toVal string, merge bool) (int64, error) {
	d, err := m.loadDictionary(ctx)
	if err != nil {
		return 0, err
	}
	// 两端都必须是字典里在用的值；改名时目标不能已存在（已存在应改用合并）。
	var fromDeactivated bool
	var toExists, toDeactivated bool
	if hasDim {
		fromV, toV := d.findValue(dim, fromVal), d.findValue(dim, toVal)
		if fromV == nil {
			return 0, &UnknownValueError{Label: dim + ":" + fromVal}
		}
		fromDeactivated = fromV.deactivated
		toExists, toDeactivated = toV != nil, toV != nil && toV.deactivated
	} else {
		fromV, toV := d.plainByKey(fromVal), d.plainByKey(toVal)
		if fromV == nil {
			return 0, &UnknownValueError{Label: fromVal}
		}
		fromDeactivated = fromV.deactivated
		toExists, toDeactivated = toV != nil, toV != nil && toV.deactivated
	}
	if fromDeactivated {
		if hasDim {
			return 0, &LabelDeactivatedError{Label: dim + ":" + fromVal}
		}
		return 0, &LabelDeactivatedError{Label: fromVal}
	}
	if !merge {
		if toExists {
			return 0, ErrValueTaken
		}
	} else if !toExists {
		if hasDim {
			return 0, &UnknownValueError{Label: dim + ":" + toVal}
		}
		return 0, &UnknownValueError{Label: toVal}
	} else if toDeactivated {
		if hasDim {
			return 0, &LabelDeactivatedError{Label: dim + ":" + toVal}
		}
		return 0, &LabelDeactivatedError{Label: toVal}
	}

	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if hasDim {
		if merge {
			// from 的别名改指向 into，然后停用 from。
			if _, err := tx.ExecContext(ctx,
				`UPDATE label_aliases SET value = ? WHERE dimension = ? AND value = ?`, toVal, dim, fromVal); err != nil {
				return 0, err
			}
			if _, err := tx.ExecContext(ctx,
				`UPDATE label_values SET deactivated = 1 WHERE dimension = ? AND value = ?`, dim, fromVal); err != nil {
				return 0, err
			}
		} else {
			// 改名：label_aliases 有 ON UPDATE CASCADE，会跟着挪到新值上。
			if _, err := tx.ExecContext(ctx,
				`UPDATE label_values SET value = ? WHERE dimension = ? AND value = ?`, toVal, dim, fromVal); err != nil {
				return 0, err
			}
		}
	} else {
		if merge {
			if _, err := tx.ExecContext(ctx,
				`UPDATE plain_labels SET deactivated = 1 WHERE label = ?`, fromVal); err != nil {
				return 0, err
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				`UPDATE plain_labels SET label = ? WHERE label = ?`, toVal, fromVal); err != nil {
				return 0, err
			}
		}
	}
	rewritten, err := rewriteTicketLabels(ctx, tx, hasDim, dim, fromVal, toVal)
	if err != nil {
		return 0, err
	}
	return int64(rewritten), tx.Commit()
}

func (d *dictionaryData) plainByKey(label string) *plainValue {
	for i := range d.plain {
		if d.plain[i].label == label {
			return &d.plain[i]
		}
	}
	return nil
}

// findValue 按字节精确找维度下的值（含停用值）。快照建好后不再 append，
// 取元素指针是安全的。
func (d *dictionaryData) findValue(dim, val string) *dictionaryValue {
	for i := range d.values[dim] {
		if d.values[dim][i].value == val {
			return &d.values[dim][i]
		}
	}
	return nil
}

// rewriteTicketLabels 把工单上的 from 改写成 to：先去掉「同时有两个值」的重复行，
// 再批量替换。返回被改写的工单数（含去重的）。
func rewriteTicketLabels(ctx context.Context, tx *sql.Tx, hasDim bool, dim, fromVal, toVal string) (int, error) {
	from, to := fromVal, toVal
	if hasDim {
		from, to = dim+":"+fromVal, dim+":"+toVal
	}
	// 先数会受影响的工单（去重删除的也算）。
	var affected int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(DISTINCT ticket_seq) FROM ticket_labels WHERE label = ?`, from).Scan(&affected); err != nil {
		return 0, err
	}
	// 同时挂着两个值的工单：去掉旧值那一行，保留目标值。
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM ticket_labels WHERE label = ? AND ticket_seq IN
		 (SELECT ticket_seq FROM ticket_labels WHERE label = ?)`, from, to); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE ticket_labels SET label = ? WHERE label = ?`, to, from); err != nil {
		return 0, err
	}
	return affected, nil
}
