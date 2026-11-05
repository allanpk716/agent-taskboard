// Package attachments 是「附件存储」模块（spec「建议的模块划分」）：两步上传、
// 配额检查、按凭证鉴权下载（Content-Disposition 与 nosniff 在 HTTP 层）、
// 未被引用的附件 24 小时后清除。文件本体经 Storage 接口写入当前后端（默认
// data_dir/attachments/<id>），数据库里只有元数据；服务端只检查上限，不压缩图片。
package attachments

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"math"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

// 上传与认领的错误；HTTP 层把它们映射成 4xx 错误码（413/422）。
var (
	// ErrFileRequired 表示 multipart 里没有文件字段。
	ErrFileRequired = errors.New("缺少文件字段（multipart 字段名 file）")
	// ErrInvalidFilename 表示文件名缺失、带路径分隔符、过长或不是 UTF-8。
	ErrInvalidFilename = errors.New("文件名不能为空，不能带路径分隔符，须为 UTF-8，且不超过 255 个字符")
	// ErrNotFound 表示附件不存在（或不是这张工单上的）。
	ErrNotFound = errors.New("没有这个附件")
	// ErrNotUsable 表示引用的附件不存在、已挂在别处，或不是本次凭证上传的。
	ErrNotUsable = errors.New("附件不存在、已挂到别处，或不是你上传的")
	// ErrPendingNotDownloadable 表示待用（未引用）附件不能下载。
	ErrPendingNotDownloadable = errors.New("附件还在待用状态，先在说明或回复里引用它")
)

// TooLargeError 是单个文件超限（413 attachment_too_large）。
type TooLargeError struct {
	Size  int64
	Limit int64
}

func (e *TooLargeError) Error() string {
	return fmt.Sprintf("单个文件不能超过 %s（本次 %s）", mbText(e.Limit), mbText(e.Size))
}

// QuotaExceededError 是整张工单附件合计超限（413 ticket_quota_exceeded）。
type QuotaExceededError struct {
	Used  int64 // 已用字节（含待用附件）
	Size  int64 // 本次上传
	Limit int64
}

// IntakeQuotaExceededError 是入口内未认领附件合计超限（413
// intake_pending_quota_exceeded）。
type IntakeQuotaExceededError struct {
	Used  int64
	Size  int64
	Limit int64
}

func (e *IntakeQuotaExceededError) Error() string {
	return fmt.Sprintf("这个反馈入口待提交的附件合计不能超过 %s（已用 %s）",
		mbText(e.Limit), mbText(e.Used))
}

func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("这张工单的附件合计不能超过 %s（已用 %s，含待用附件）",
		mbText(e.Limit), mbText(e.Used))
}

// CountExceededError 是每条说明或回复的附件/插图个数超限（413 attachment_count_exceeded）。
type CountExceededError struct {
	Kind  string // "files"（附件）| "images"（插图）
	Count int
	Limit int
}

func (e *CountExceededError) Error() string {
	if e.Kind == "images" {
		return fmt.Sprintf("每条说明或回复最多 %d 张插图，本次引用了 %d 张", e.Limit, e.Count)
	}
	return fmt.Sprintf("每条说明或回复最多 %d 个附件，本次引用了 %d 个", e.Limit, e.Count)
}

// Limits 是换算成字节数的附件上限（config.toml [attachments]）。
type Limits struct {
	MaxFileBytes     int64
	MaxFilesPerPost  int
	MaxImagesPerPost int
	MaxTicketBytes   int64
}

// MaxFileMB 等字段按 MB 读取； LimitsOf 换算成字节。
func LimitsOf(maxFileMB, maxFilesPerPost, maxImagesPerPost, maxTicketMB int) Limits {
	return Limits{
		MaxFileBytes:     int64(maxFileMB) << 20,
		MaxFilesPerPost:  maxFilesPerPost,
		MaxImagesPerPost: maxImagesPerPost,
		MaxTicketBytes:   int64(maxTicketMB) << 20,
	}
}

// MaxFileMB / MaxTicketMB 是字节上限的 MB 表示（用量回显用）。
func (l Limits) MaxFileMB() int   { return int(l.MaxFileBytes >> 20) }
func (l Limits) MaxTicketMB() int { return int(l.MaxTicketBytes >> 20) }
func (l Limits) UsedMB(bytes int64) float64 {
	return math.Round(float64(bytes)/(1<<20)*10) / 10
}

// Principal 是上传者归属：成员（本人或其任意 Agent，算同一方）或访客链接。
// 认领（在回复/说明里引用待用附件）只允许上传者一方自己来。
type Principal struct {
	MemberID int64
	AgentID  int64 // 成员 Agent 上传时记下具名 Agent（署名用）
	LinkID   int64
}

// PrincipalOf 把 Viewer 转成上传者归属。
func PrincipalOf(v identity.Viewer) Principal {
	if v.Kind == identity.ViewerGuest && v.GuestLink != nil {
		return Principal{LinkID: v.GuestLink.ID}
	}
	p := Principal{MemberID: v.Member.ID}
	if v.Agent != nil {
		p.AgentID = v.Agent.ID
	}
	return p
}

func (p Principal) owns(uploaderMemberID, uploaderLinkID int64) bool {
	if p.MemberID != 0 {
		return uploaderMemberID == p.MemberID
	}
	if p.LinkID != 0 {
		return uploaderLinkID == p.LinkID
	}
	return false
}

// AttachmentInfo 是一条附件的元数据。
type AttachmentInfo struct {
	ID         int64
	TicketSeq  int64
	Filename   string
	Size       int64
	State      string // pending | attached
	AttachedTo string // 「说明」或「回复 N」；pending 为空
	DraftID    int64  // 非 0 表示被这份草稿的最新版占用
	// SlimRemovedAt / SlimPackageName：附件被瘦身移出时记录（#281）。
	// RemovedAt 为 nil 表示文件仍在服务器上；附件行本身保留作占位。
	SlimRemovedAt   *time.Time
	SlimPackageName string
	Uploader        Principal
	UploadedAt      time.Time
	ClaimedAt       *time.Time
}

// Ref 返回对外的附件引用（att_N）。
func (a AttachmentInfo) Ref() string { return "att_" + strconv.FormatInt(a.ID, 10) }

// Manager 是附件存储的入口。
type Manager struct {
	db                 *sql.DB
	clk                clock.Clock
	dir                string // data_dir/attachments
	store              Storage
	limits             Limits
	intakePendingBytes int64
}

// SetIntakeLimits 设置入口附件的临时配额（[intake] pending_quota_mb）。
func (m *Manager) SetIntakeLimits(pendingBytes int64) { m.intakePendingBytes = pendingBytes }

// NewManager 用本地后端建管理器。dataDir 是 config.toml 的 data_dir。
func NewManager(db *sql.DB, dataDir string, c clock.Clock, limits Limits) (*Manager, error) {
	local, err := NewLocalStorage(dataDir)
	if err != nil {
		return nil, err
	}
	return NewManagerWithStorage(db, dataDir, local, c, limits)
}

// NewManagerWithStorage 用指定后端建管理器（测试注入内存假存储用）。本地
// attachments 目录仍会创建：Dir() 供统计、备份镜像和包服务沿用，附件对象
// 读写一律走 store。
func NewManagerWithStorage(db *sql.DB, dataDir string, store Storage, c clock.Clock, limits Limits) (*Manager, error) {
	dir := filepath.Join(dataDir, "attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建附件目录 %s 失败：%w", dir, err)
	}
	return &Manager{db: db, clk: c, dir: dir, store: store, limits: limits}, nil
}

// Limits 返回生效的上限。
func (m *Manager) Limits() Limits { return m.limits }

// Dir 返回附件目录（备份镜像用）。
func (m *Manager) Dir() string { return m.dir }

// Storage 返回附件对象的后端。上传、下载、删除都从这里走（#315）。
func (m *Manager) Storage() Storage { return m.store }

// ValidateFilename 校验客户端文件名（存储时不用它做路径，按 id 落盘）。
func ValidateFilename(name string) error {
	name = strings.TrimSpace(name)
	if !utf8.ValidString(name) {
		return ErrInvalidFilename
	}
	if name == "" || len(name) > 255 {
		return ErrInvalidFilename
	}
	if strings.ContainsAny(name, "/\\\x00") {
		return ErrInvalidFilename
	}
	return nil
}

// ContentTypeOf 按文件名推断对象元数据里的 Content-Type。文本类补 charset；
// 未知类型用 application/octet-stream，避免依赖存储后端嗅探。
func ContentTypeOf(filename string) string {
	ext := ""
	if i := strings.LastIndexByte(filename, '.'); i >= 0 {
		ext = strings.ToLower(filename[i:])
	}
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".txt", ".log":
		return "text/plain; charset=utf-8"
	}
	if typ := mime.TypeByExtension(ext); typ != "" {
		return typ
	}
	return "application/octet-stream"
}

// UploadStream 存一个待用附件：校验单文件与整单上限后流式写入存储接口，
// 元数据入库（state=pending）。大小来自 multipart 头，内容交给限长 reader，
// 不整读进内存。写入顺序是「插 DB 行 → Put → 提交」；Put 失败回滚 DB 行，
// 提交失败删除已写入的对象。
func (m *Manager) UploadStream(ctx context.Context, ticketSeq int64, filename string, size int64,
	src io.Reader, meta PutMeta, principal Principal) (AttachmentInfo, string, error) {
	filename = strings.TrimSpace(filename)
	if err := ValidateFilename(filename); err != nil {
		return AttachmentInfo{}, "", err
	}
	if size > m.limits.MaxFileBytes {
		return AttachmentInfo{}, "", &TooLargeError{Size: size, Limit: m.limits.MaxFileBytes}
	}
	used, err := m.usedBytes(ctx, ticketSeq)
	if err != nil {
		return AttachmentInfo{}, "", err
	}
	if used+size > m.limits.MaxTicketBytes {
		return AttachmentInfo{}, "", &QuotaExceededError{Used: used, Size: size, Limit: m.limits.MaxTicketBytes}
	}

	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return AttachmentInfo{}, "", err
	}
	defer tx.Rollback()

	var agentID, linkID any
	if principal.AgentID != 0 {
		agentID = principal.AgentID
	}
	if principal.LinkID != 0 {
		linkID = principal.LinkID
	}
	var memberID any
	if principal.MemberID != 0 {
		memberID = principal.MemberID
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO attachments (ticket_seq, filename, size, state,
		                         uploader_member_id, uploader_agent_id, uploader_link_id, uploaded_at)
		VALUES (?, ?, ?, 'pending', ?, ?, ?, ?)
		RETURNING id`,
		ticketSeq, filename, size, memberID, agentID, linkID,
		now.Format(time.RFC3339Nano)).Scan(&id); err != nil {
		return AttachmentInfo{}, "", fmt.Errorf("写入附件元数据失败：%w", err)
	}
	// 先写对象再提交：Put 失败就回滚这一行。
	if err := m.store.Put(ctx, id, src, size, meta); err != nil {
		return AttachmentInfo{}, "", fmt.Errorf("写附件文件失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = m.store.Delete(context.WithoutCancel(ctx), id)
		return AttachmentInfo{}, "", err
	}

	info := AttachmentInfo{
		ID: id, TicketSeq: ticketSeq, Filename: filename, Size: size,
		State: "pending", Uploader: principal, UploadedAt: now,
	}
	return info, MarkdownSnippet(info), nil
}

// UploadToIntakeStream 匿名流式上传到反馈入口：附件暂不挂工单，等提交时
// 按入口和 Idempotency-Key 认领；24 小时未认领由 CleanupUnreferenced 清除。
func (m *Manager) UploadToIntakeStream(ctx context.Context, intakeID int64, uploadKey, filename string,
	size int64, src io.Reader, meta PutMeta) (AttachmentInfo, string, error) {
	filename = strings.TrimSpace(filename)
	if err := ValidateFilename(filename); err != nil {
		return AttachmentInfo{}, "", err
	}
	if uploadKey == "" {
		return AttachmentInfo{}, "", ErrNotUsable
	}
	if size > m.limits.MaxFileBytes {
		return AttachmentInfo{}, "", &TooLargeError{Size: size, Limit: m.limits.MaxFileBytes}
	}
	var used int64
	if err := m.db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(size), 0) FROM attachments
		WHERE uploader_intake_id = ? AND ticket_seq IS NULL`, intakeID).Scan(&used); err != nil {
		return AttachmentInfo{}, "", err
	}
	if used+size > m.intakePendingBytes {
		return AttachmentInfo{}, "", &IntakeQuotaExceededError{Used: used, Size: size, Limit: m.intakePendingBytes}
	}
	now := m.clk.Now()
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return AttachmentInfo{}, "", err
	}
	defer tx.Rollback()
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO attachments (ticket_seq, filename, size, state,
		                         uploader_intake_id, intake_upload_key, uploaded_at)
		VALUES (NULL, ?, ?, 'pending', ?, ?, ?) RETURNING id`,
		filename, size, intakeID, uploadKey, now.Format(time.RFC3339Nano)).Scan(&id); err != nil {
		return AttachmentInfo{}, "", fmt.Errorf("写入入口附件元数据失败：%w", err)
	}
	if err := m.store.Put(ctx, id, src, size, meta); err != nil {
		return AttachmentInfo{}, "", fmt.Errorf("写附件文件失败：%w", err)
	}
	if err := tx.Commit(); err != nil {
		_ = m.store.Delete(context.WithoutCancel(ctx), id)
		return AttachmentInfo{}, "", err
	}
	info := AttachmentInfo{ID: id, Filename: filename, Size: size, State: "pending", UploadedAt: now}
	return info, MarkdownSnippet(info), nil
}

// MarkdownSnippet 返回现成的引用片段：插图用图片语法，其余用普通链接。
func MarkdownSnippet(a AttachmentInfo) string {
	if IsImage(a.Filename) {
		return "![" + a.Filename + "](att:" + a.Ref() + ")"
	}
	return "[" + a.Filename + "](att:" + a.Ref() + ")"
}

// ByTicket 列出这张工单的全部附件（含待用），按 id 排序。
func (m *Manager) ByTicket(ctx context.Context, ticketSeq int64) ([]AttachmentInfo, error) {
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, ticket_seq, filename, size, state, attached_to, draft_id,
		       uploader_member_id, uploader_agent_id, uploader_link_id, uploaded_at, claimed_at,
		       slim_removed_at, slim_package_name
		FROM attachments WHERE ticket_seq = ? ORDER BY id`, ticketSeq)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAttachments(rows)
}

// ByTicketState 列出这张工单上处于某个状态的附件。
func (m *Manager) ByTicketState(ctx context.Context, ticketSeq int64, state string) ([]AttachmentInfo, error) {
	atts, err := m.ByTicket(ctx, ticketSeq)
	if err != nil {
		return nil, err
	}
	out := []AttachmentInfo{}
	for _, a := range atts {
		if a.State == state {
			out = append(out, a)
		}
	}
	return out, nil
}

// Get 按引用（att_N 或数字）取一条附件元数据。未认领的入口附件还没有工单，
// 当作不存在。
func (m *Manager) Get(ctx context.Context, ref string) (AttachmentInfo, error) {
	id, err := ParseRef(ref)
	if err != nil {
		return AttachmentInfo{}, ErrNotFound
	}
	rows, err := m.db.QueryContext(ctx, `
		SELECT id, ticket_seq, filename, size, state, attached_to, draft_id,
		       uploader_member_id, uploader_agent_id, uploader_link_id, uploaded_at, claimed_at,
		       slim_removed_at, slim_package_name
		FROM attachments WHERE id = ? AND ticket_seq IS NOT NULL`, id)
	if err != nil {
		return AttachmentInfo{}, err
	}
	defer rows.Close()
	atts, err := scanAttachments(rows)
	if err != nil {
		return AttachmentInfo{}, err
	}
	if len(atts) != 1 {
		return AttachmentInfo{}, ErrNotFound
	}
	return atts[0], nil
}

// ParseRef 把 att_N（或纯数字）解析成数字 id。
func ParseRef(ref string) (int64, error) {
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "att_")
	return strconv.ParseInt(ref, 10, 64)
}

// Usage 返回这张工单的附件用量：合计字节（含待用附件；已瘦身移出服务器的
// 附件文件不在服务器上，不占配额也不计入）与个数。
func (m *Manager) Usage(ctx context.Context, ticketSeq int64) (usedBytes int64, count int, err error) {
	if err = m.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(CASE WHEN slim_removed_at IS NULL THEN size ELSE 0 END), 0), COUNT(*)
		FROM attachments WHERE ticket_seq = ?`,
		ticketSeq).Scan(&usedBytes, &count); err != nil {
		return 0, 0, err
	}
	return usedBytes, count, nil
}

func (m *Manager) usedBytes(ctx context.Context, ticketSeq int64) (int64, error) {
	used, _, err := m.Usage(ctx, ticketSeq)
	return used, err
}

func scanAttachments(rows *sql.Rows) ([]AttachmentInfo, error) {
	out := []AttachmentInfo{}
	for rows.Next() {
		var (
			a          AttachmentInfo
			ticket     sql.NullInt64
			attachedTo sql.NullString
			memberID   sql.NullInt64
			agentID    sql.NullInt64
			linkID     sql.NullInt64
			uploadedAt string
			claimedAt  sql.NullString
			draftID    sql.NullInt64
			slimmedAt  sql.NullString
		)
		if err := rows.Scan(&a.ID, &ticket, &a.Filename, &a.Size, &a.State, &attachedTo,
			&draftID, &memberID, &agentID, &linkID, &uploadedAt, &claimedAt,
			&slimmedAt, &a.SlimPackageName); err != nil {
			return nil, err
		}
		a.TicketSeq = ticket.Int64
		a.AttachedTo = attachedTo.String
		a.DraftID = draftID.Int64
		a.Uploader = Principal{MemberID: memberID.Int64, AgentID: agentID.Int64, LinkID: linkID.Int64}
		var err error
		if a.UploadedAt, err = parseTime(uploadedAt); err != nil {
			return nil, err
		}
		if claimedAt.Valid {
			t, err := parseTime(claimedAt.String)
			if err != nil {
				return nil, err
			}
			a.ClaimedAt = &t
		}
		if slimmedAt.Valid {
			t, err := parseTime(slimmedAt.String)
			if err != nil {
				return nil, err
			}
			a.SlimRemovedAt = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func parseTime(s string) (time.Time, error) {
	t, err := time.ParseInLocation(time.RFC3339Nano, s, clock.Beijing)
	if err != nil {
		return time.Time{}, fmt.Errorf("解析时间 %q 失败：%w", s, err)
	}
	return t, nil
}

func mbText(bytes int64) string {
	return strconv.FormatFloat(float64(bytes)/(1<<20), 'f', 1, 64) + " MB"
}

// ---- 文件类型判定与正文引用的提取（纯逻辑） ----

var (
	imageExt    = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp)$`)
	inlineExt   = regexp.MustCompile(`(?i)\.(png|jpe?g|gif|webp|pdf|txt|log)$`)
	anyAttRef   = regexp.MustCompile(`att:(att_\d+)`)
	imageAttRef = regexp.MustCompile(`!\[[^\]]*\]\(att:(att_\d+)\)`)
)

// IsImage 报告文件名是否是图片（插图语法与插图计数按它判定；SVG 不是图片——
// HTML 和 SVG 只能下载）。
func IsImage(filename string) bool { return imageExt.MatchString(filename) }

// IsInline 报告这个文件是否允许 inline 返回：图片、PDF、.txt 和 .log；
// 其余（含 HTML、SVG）一律 Content-Disposition: attachment。
func IsInline(filename string) bool { return inlineExt.MatchString(filename) }

// PostRef 是一条说明或回复里的一个附件引用：正文里的 att: 引用（区分插图
// 语法 ![…](att:…)）与显式 attachment_ids 合并去重后的结果。
type PostRef struct {
	ID          string
	ImageSyntax bool // 以插图语法 ![…](att:…) 引用（插图按扩展名判定）
	Explicit    bool // 通过 attachment_ids 显式挂上
}

// ParsePostRefs 解析一条说明或回复里的全部附件引用：正文的 att: 引用
// （插图语法单独标记）加上显式的 attachment_ids，按 id 去重（标记取并集）。
func ParsePostRefs(bodyMD string, attachmentIDs []string) []PostRef {
	images := map[string]bool{}
	for _, m := range imageAttRef.FindAllStringSubmatch(bodyMD, -1) {
		images[m[1]] = true
	}
	seen := map[string]*PostRef{}
	order := []string{}
	add := func(id string, imageSyntax, explicit bool) {
		if r, ok := seen[id]; ok {
			r.ImageSyntax = r.ImageSyntax || imageSyntax
			r.Explicit = r.Explicit || explicit
			return
		}
		r := &PostRef{ID: id, ImageSyntax: imageSyntax, Explicit: explicit}
		seen[id] = r
		order = append(order, id)
	}
	for _, m := range anyAttRef.FindAllStringSubmatch(bodyMD, -1) {
		add(m[1], images[m[1]], false)
	}
	for _, id := range attachmentIDs {
		id = strings.TrimSpace(id)
		if id != "" {
			add(id, false, true)
		}
	}
	out := make([]PostRef, 0, len(order))
	for _, id := range order {
		out = append(out, *seen[id])
	}
	return out
}
