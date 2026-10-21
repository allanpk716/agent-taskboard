// Package template 是「模板」模块的纯逻辑部分（spec「建议的模块划分」：解析
// 模板 Markdown、生成字段定义、格式错误报行号；把 fields 渲染成 brief_md；
// 必填校验；生成给 Agent 的填写指南）。没有 IO，不碰数据库和 HTTP。
//
// 模板写法（spec #26「模板语法」，来自 #9；分组标记见 #63）：YAML 头写 名称、
// 类型、预设标签；每个 ## 小节是一个字段，标题写成问句，小节正文是引导说明，
// > 示例： 是占位文字；花括号标记字段类型和是否必填：{必填}、{选填}、
// {单选: A | B}、{维度:X 必填}，不写时默认为多行文本、选填。模板里可以另写
// 一段「给 Agent 的说明」。#63 起正文可用 `# 组名` 给字段分组，组标题下、
// 第一个 ## 之前的第一段非空文字是组副标题。
package template

import (
	"fmt"
	"strings"
)

// 字段类型。
const (
	TypeText      = "text"      // 多行文本（默认）
	TypeRadio     = "radio"     // 单选
	TypeDimension = "dimension" // 从标签维度中选值
)

// UnsureOption 是维度字段里的「不确定」选项：算作已填，但不打标签
// （orchestrator 决议 6）。
const UnsureOption = "不确定"

// AgentNoteTitle 是「给 Agent 的说明」小节的标题；这段不是字段。
const AgentNoteTitle = "给 Agent 的说明"

// Group 是模板正文里的一个分组（#63）：`# 组名` 开一组，紧随其后、
// 在下一个 `##` 之前的第一段非空文字是副标题。分组只影响展示（访客填写页
// 按组折叠），不影响说明渲染、必填校验和给 Agent 的填写指南。
type Group struct {
	Title    string `json:"title"`              // 组名（# 后的原文）
	Subtitle string `json:"subtitle,omitempty"` // 组标题下的第一段非空文字
}

// Field 是模板里的一个字段（data-model「Template」的字段结构）。
// 键就是小节标题（问句）原文——模板语法里没有独立的键，问句即键。
type Field struct {
	Key       string   `json:"key"`                 // = Title
	Title     string   `json:"title"`               // 问句标题
	Guide     string   `json:"guide,omitempty"`     // 引导说明（小节正文）
	Example   string   `json:"example,omitempty"`   // > 示例： 的占位文字
	Required  bool     `json:"required"`            // 是否必填
	Type      string   `json:"type"`                // text | radio | dimension
	Options   []string `json:"options,omitempty"`   // radio 的选项；维度字段的候选值运行时取字典
	Dimension string   `json:"dimension,omitempty"` // 维度字段的维度名
	Group     *int     `json:"group,omitempty"`     // 所属分组的序号（从 0 起）；不分组时为 nil
}

// Definition 是一份模板的定义：YAML 头与字段小节解析的结果。
type Definition struct {
	Name         string   `json:"name"`                    // 名称
	Kind         string   `json:"kind"`                    // task | report
	Scenario     string   `json:"scenario,omitempty"`      // 适用场景（一句话）
	PresetLabels []string `json:"preset_labels,omitempty"` // 预设标签
	AgentNote    string   `json:"agent_note,omitempty"`    // 给 Agent 的说明
	Fields       []Field  `json:"fields"`
	Groups       []Group  `json:"groups,omitempty"` // 字段分组（#63）；不分组时为空
}

// Snapshot 是工单保存的「创建时模板版本」：key、版本号和当时的字段定义
// （含引导文字与示例，访客页按它渲染）。模板以后修改不影响已有工单。
type Snapshot struct {
	Key     string `json:"key"`
	Version int64  `json:"version"`
	Definition
}

// ParseError 是模板格式错误，带 1 起的行号；保存模板时格式错误就失败。
type ParseError struct {
	Line    int
	Message string
}

func (e *ParseError) Error() string { return fmt.Sprintf("第 %d 行：%s", e.Line, e.Message) }

func parseErr(line int, format string, args ...any) error {
	return &ParseError{Line: line, Message: fmt.Sprintf(format, args...)}
}

// Parse 解析模板 Markdown，生成字段定义。格式错误返回 *ParseError（带行号）。
func Parse(md string) (Definition, error) {
	lines := strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Definition{}, parseErr(1, "模板要以 YAML 头开始：第一行写 ---")
	}
	headerEnd := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			headerEnd = i
			break
		}
	}
	if headerEnd < 0 {
		return Definition{}, parseErr(len(lines), "YAML 头没有结束：缺少收尾的 ---")
	}
	def, err := parseHeader(lines[1:headerEnd])
	if err != nil {
		return Definition{}, err
	}
	if err := parseBody(lines[headerEnd+1:], headerEnd+2, &def); err != nil {
		return Definition{}, err
	}
	return def, nil
}

// parseHeader 解析 YAML 头：名称、类型、预设标签；其余键报错。
func parseHeader(lines []string) (Definition, error) {
	def := Definition{PresetLabels: []string{}}
	nameLine := 0
	for i, raw := range lines {
		line := i + 2 // YAML 头从第 2 行开始
		if strings.TrimSpace(raw) == "" {
			continue
		}
		key, val, ok := strings.Cut(raw, ":")
		if !ok {
			return Definition{}, parseErr(line, "YAML 头的每一行都要写成「键: 值」")
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		switch key {
		case "名称":
			if val == "" {
				return Definition{}, parseErr(line, "名称不能为空")
			}
			def.Name = val
			nameLine = line
		case "类型":
			switch val {
			case "任务":
				def.Kind = "task"
			case "反馈":
				def.Kind = "report"
			default:
				return Definition{}, parseErr(line, "类型只能是 任务 或 反馈")
			}
		case "预设标签":
			if !strings.HasPrefix(val, "[") || !strings.HasSuffix(val, "]") {
				return Definition{}, parseErr(line, "预设标签要写成 [标签1, 标签2]")
			}
			for _, p := range strings.Split(val[1:len(val)-1], ",") {
				if p = strings.TrimSpace(p); p != "" {
					def.PresetLabels = append(def.PresetLabels, p)
				}
			}
		case "适用场景":
			if val == "" {
				return Definition{}, parseErr(line, "适用场景不能为空；不写就删掉这一行")
			}
			def.Scenario = val
		default:
			return Definition{}, parseErr(line, "YAML 头里没有 %q 这个键（可用：名称、类型、适用场景、预设标签）", key)
		}
	}
	if def.Name == "" {
		if nameLine != 0 {
			return Definition{}, parseErr(nameLine, "名称不能为空")
		}
		return Definition{}, parseErr(1, "YAML 头缺少 名称")
	}
	if def.Kind == "" {
		return Definition{}, parseErr(1, "YAML 头缺少 类型（任务 / 反馈）")
	}
	return def, nil
}

// parseBody 解析正文：`# 组名` 开一组（可选，#63），每个 ## 小节是一个字段
// （或「给 Agent 的说明」）。bodyStart 是正文第一行的行号（1 起），用于报行号。
// 分组规则：要么完全不分组，要么所有字段都在分组里（字段写在第一个 # 之前
// 报带行号的错）；空组报错；「给 Agent 的说明」不属于任何组，写在哪里都行；
// 除 # 与 ## 外，其他级别的标题仍然报错。
func parseBody(lines []string, bodyStart int, def *Definition) error {
	type section struct {
		line    int    // 小节标题行号
		title   string // 问句标题
		marker  string // 花括号标记（可为空）
		body    []string
		isNote  bool // 给 Agent 的说明
		note    string
		example []string
		guide   []string
		group   int // 所属分组的序号；-1 = 不在任何分组（未用分组，或「给 Agent 的说明」）
	}
	type group struct {
		line         int
		title        string
		subtitle     []string // 副标题：第一段非空文字（可跨行）
		subtitleDone bool     // 已取到第一段（空行分段）；之后的非空文字不进副标题
	}
	var sections []*section
	var groups []*group
	curGroup := -1        // 当前分组序号；-1 = 还没遇到 #
	inGroupIntro := false // 正在分组标题与第一个 ## 之间：非空文字是副标题

	endSubtitlePara := func() {
		if inGroupIntro && curGroup >= 0 && len(groups[curGroup].subtitle) > 0 {
			groups[curGroup].subtitleDone = true
		}
	}
	for i, raw := range lines {
		line := bodyStart + i
		t := strings.TrimSpace(raw)
		if t == "" {
			if n := len(sections); n > 0 {
				sections[n-1].body = append(sections[n-1].body, "")
			}
			endSubtitlePara()
			continue
		}
		if strings.HasPrefix(t, "## ") {
			title, marker, err := splitHeading(t, line)
			if err != nil {
				return err
			}
			g := -1
			if title != AgentNoteTitle && curGroup >= 0 {
				g = curGroup
			}
			sections = append(sections, &section{line: line, title: title, marker: marker,
				isNote: title == AgentNoteTitle, group: g})
			inGroupIntro = false
			continue
		}
		// # 组名 开一组；组标题下、第一个 ## 之前的第一段非空文字是副标题。
		if strings.HasPrefix(t, "# ") || t == "#" {
			title := strings.TrimSpace(strings.TrimPrefix(t, "#"))
			if title == "" {
				return parseErr(line, "组名不能为空：# 加一个空格再写组名")
			}
			groups = append(groups, &group{line: line, title: title})
			curGroup = len(groups) - 1
			inGroupIntro = true
			continue
		}
		if strings.HasPrefix(t, "#") {
			return parseErr(line, "模板正文里的标题只能用「# 」（分组）或「## 」（字段小节）")
		}
		if inGroupIntro {
			// 分组标题与第一个 ## 之间的第一段非空文字是副标题，其余忽略。
			g := groups[curGroup]
			if !g.subtitleDone {
				g.subtitle = append(g.subtitle, t)
			}
			continue
		}
		if len(sections) == 0 {
			return parseErr(line, "模板正文从分组（# ）或字段小节（## ）开始（YAML 头之后不要写别的文字）")
		}
		cur := sections[len(sections)-1]
		if cur.isNote {
			cur.note += raw + "\n"
			continue
		}
		// > 示例： 开始占位文字；紧随其后的 > 行续写示例，其余是引导说明。
		if strings.HasPrefix(t, "> 示例：") || strings.HasPrefix(t, "> 示例:") {
			colon := strings.Index(t, "：")
			if colon < 0 {
				colon = strings.Index(t, ":")
			}
			cur.example = append(cur.example, strings.TrimSpace(t[colon+len("："):]))
			continue
		}
		if len(cur.example) > 0 && strings.HasPrefix(t, ">") {
			cur.example = append(cur.example, strings.TrimSpace(strings.TrimPrefix(t, ">")))
			continue
		}
		cur.guide = append(cur.guide, raw)
	}

	seenTitles := map[string]int{}
	groupFields := make([]int, len(groups))
	for _, sec := range sections {
		if sec.isNote {
			if sec.marker != "" {
				return parseErr(sec.line, "「%s」不是字段，不能带花括号标记", AgentNoteTitle)
			}
			if def.AgentNote != "" {
				return parseErr(sec.line, "「%s」只能有一段", AgentNoteTitle)
			}
			def.AgentNote = strings.TrimSpace(sec.note)
			continue
		}
		if len(groups) > 0 && sec.group < 0 {
			return parseErr(sec.line, "字段「%s」写在第一个分组（# ）之前；用了分组时，所有字段都要写在分组里", sec.title)
		}
		f, err := parseField(sec.title, sec.marker, sec.line)
		if err != nil {
			return err
		}
		f.Guide = strings.TrimSpace(strings.Join(sec.guide, "\n"))
		f.Example = strings.Join(sec.example, "\n")
		if prev, dup := seenTitles[f.Key]; dup {
			return parseErr(sec.line, "字段标题重复：%s（上一段在第 %d 行）", f.Key, prev)
		}
		seenTitles[f.Key] = sec.line
		if len(groups) > 0 {
			g := sec.group
			f.Group = &g
			groupFields[g]++
		}
		def.Fields = append(def.Fields, f)
	}
	for i, g := range groups {
		if groupFields[i] == 0 {
			return parseErr(g.line, "分组「%s」是空的：下面要至少有一个 ## 字段小节", g.title)
		}
		def.Groups = append(def.Groups, Group{Title: g.title, Subtitle: strings.Join(g.subtitle, "\n")})
	}
	if len(def.Fields) == 0 {
		return parseErr(bodyStart, "模板至少要有一个字段小节（## 问句），「%s」不算字段", AgentNoteTitle)
	}
	return nil
}

// splitHeading 把「## 问句 {标记}」拆成标题与标记；标记必须写在行末。
func splitHeading(t string, line int) (title, marker string, err error) {
	rest := strings.TrimSpace(t[len("## "):])
	if i := strings.LastIndex(rest, "{"); i >= 0 {
		if !strings.HasSuffix(rest, "}") {
			return "", "", parseErr(line, "花括号标记 {…} 要写在小节标题行的末尾")
		}
		marker = strings.TrimSpace(rest[i+1 : len(rest)-1])
		title = strings.TrimSpace(rest[:i])
		if title == "" {
			return "", "", parseErr(line, "小节标题（问句）不能为空")
		}
		if strings.Contains(title, "{") {
			return "", "", parseErr(line, "小节标题里不能出现 {")
		}
		return title, marker, nil
	}
	return rest, "", nil
}

// parseField 把标题行的花括号标记解析成字段属性。不写时默认多行文本、选填。
func parseField(title, marker string, line int) (Field, error) {
	f := Field{Key: title, Title: title, Type: TypeText}
	if marker == "" {
		return f, nil
	}
	var (
		dimension    string
		radio        bool
		optTokens    []string
		inOptions    bool
		dimensionSet bool
	)
	for _, tok := range strings.Fields(marker) {
		switch {
		case tok == "必填":
			f.Required = true
			inOptions = false
		case tok == "选填":
			inOptions = false // 保持选填（默认）
		case strings.HasPrefix(tok, "维度:"):
			if dimensionSet {
				return Field{}, parseErr(line, "维度标记只能写一个")
			}
			d := tok[len("维度:"):]
			if d == "" {
				return Field{}, parseErr(line, "维度: 后面要写维度名，如 {维度:机型 必填}")
			}
			if strings.ContainsAny(d, ":|") {
				return Field{}, parseErr(line, "维度名里不能有 : 或 |")
			}
			dimension, dimensionSet = d, true
			inOptions = false
		case tok == "单选:" || strings.HasPrefix(tok, "单选:"):
			if radio {
				return Field{}, parseErr(line, "单选标记只能写一个")
			}
			radio = true
			inOptions = true
			if rest := strings.TrimPrefix(tok, "单选:"); rest != "" {
				optTokens = append(optTokens, rest)
			}
		case inOptions:
			optTokens = append(optTokens, tok)
		default:
			return Field{}, parseErr(line, "标记里出现无法识别的写法 %q（可用：必填、选填、维度:X、单选: A | B）", tok)
		}
	}
	switch {
	case radio && dimensionSet:
		return Field{}, parseErr(line, "维度字段从标签字典里选值，不能再标 单选")
	case radio:
		f.Type = TypeRadio
		for _, opt := range strings.Split(strings.Join(optTokens, " "), "|") {
			if opt = strings.TrimSpace(opt); opt != "" {
				f.Options = append(f.Options, opt)
			}
		}
		if len(f.Options) == 0 {
			return Field{}, parseErr(line, "单选: 后面要用 | 分隔写出选项，如 {单选: A | B}")
		}
	case dimensionSet:
		f.Type = TypeDimension
		f.Dimension = dimension
	}
	return f, nil
}

// RenderBrief 把字段值拼成说明的 Markdown（brief_md）：只拼已填的字段，
// 问句作三级标题——与 prototype/member-handoff 的 briefMd 一致。
// 维度字段的值也因此以文字写进说明（访客看不到标签）。
func RenderBrief(fields map[string]string, defs []Field) string {
	parts := make([]string, 0, len(defs))
	for _, f := range defs {
		v := strings.TrimSpace(fields[f.Key])
		if v == "" {
			continue
		}
		parts = append(parts, "### "+f.Title+"\n\n"+v)
	}
	return strings.Join(parts, "\n\n")
}

// MissingRequired 报出没填的必填字段（键按模板顺序）。选「不确定」的维度字段
// 算已填。每项带一句给调用方拼 warnings / missing_fields 的话。
func MissingRequired(fields map[string]string, defs []Field) []string {
	var missing []string
	for _, f := range defs {
		if !f.Required {
			continue
		}
		if strings.TrimSpace(fields[f.Key]) == "" {
			missing = append(missing, f.Key)
		}
	}
	return missing
}

// Guide 生成给 Agent 的填写指南（GET /templates/{key}?format=markdown）：
// 模板信息、建单请求示例、逐字段的问句/引导/示例/候选值，以及「给 Agent 的说明」。
// candidates 是各维度当前的候选值（运行时从标签字典取，不缓存在模板里），
// 每个维度字段的候选值末尾都附上「不确定」。
func Guide(key string, def Definition, version int64, candidates map[string][]string) string {
	var L []string
	L = append(L,
		"# 模板 "+def.Name+"（"+key+"）", "",
		"- 类型："+kindText(def.Kind)+" · 版本："+fmt.Sprintf("%d", version))
	if def.Scenario != "" {
		L = append(L, "- 适用场景："+def.Scenario)
	}
	if len(def.PresetLabels) > 0 {
		L = append(L, "- 预设标签："+strings.Join(def.PresetLabels, "、"))
	}
	L = append(L, "",
		"按这份模板建单：`POST /api/v1/tickets`，`template` 写 `"+key+"`，`fields` 的键用下面每个小节的问句原文：",
		"",
		"```http",
		"POST /api/v1/tickets",
		"Authorization: Bearer <你的 token>",
		"Content-Type: application/json",
		"",
		`{"template": "`+key+`", "title": "…", "fields": {"`+firstQuestion(def)+`": "…"}, "labels": []}`,
		"```", "",
		"缺必填项不会拦下建单，响应的 `warnings` 会逐项列出；维度字段选中的值会自动成为工单的标签。",
		"日志这类不属于某个字段的文件，上传后放进说明修订的 `attachment_ids`，不要拼进任何字段。", "")
	for _, f := range def.Fields {
		L = append(L, "## "+f.Title+markerText(f))
		if f.Guide != "" {
			L = append(L, f.Guide)
		}
		switch f.Type {
		case TypeDimension:
			values := candidates[f.Dimension]
			values = append(append([]string{}, values...), UnsureOption)
			L = append(L, "- 候选值（维度 "+f.Dimension+"）："+strings.Join(values, "、"))
		case TypeRadio:
			L = append(L, "- 选项："+strings.Join(f.Options, "、"))
		}
		if f.Example != "" {
			L = append(L, "> 示例："+f.Example)
		}
		L = append(L, "")
	}
	if def.AgentNote != "" {
		L = append(L, "## "+AgentNoteTitle, "", def.AgentNote, "")
	}
	return strings.Join(L, "\n")
}

// firstQuestion 取模板第一个字段的问句，给请求示例用。
func firstQuestion(def Definition) string {
	if len(def.Fields) > 0 {
		return def.Fields[0].Key
	}
	return "问句"
}

// markerText 把字段属性还原成花括号标记的写法（指南里回显用）。
func markerText(f Field) string {
	var inner []string
	switch f.Type {
	case TypeDimension:
		inner = append(inner, "维度:"+f.Dimension)
	case TypeRadio:
		inner = append(inner, "单选: "+strings.Join(f.Options, " | "))
	}
	if f.Required {
		inner = append(inner, "必填")
	} else if len(inner) == 0 {
		return "" // 完全缺省时不回显 {选填}
	}
	if len(inner) == 0 {
		return ""
	}
	return " {" + strings.Join(inner, " ") + "}"
}

// kindText 把类型码翻成中文。
func kindText(kind string) string {
	if kind == "report" {
		return "反馈"
	}
	return "任务"
}
