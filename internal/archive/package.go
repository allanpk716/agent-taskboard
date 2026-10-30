package archive

import (
	"archive/zip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
	"github.com/allanpk716/agent-taskboard/internal/version"
)

const (
	// FormatVersion 是归档包格式版本。导入端 (#237) 只认识不高于这个版本的包。
	FormatVersion = 1
	// Retention 是归档包在服务器上的暂存时长。
	Retention = 7 * 24 * time.Hour
)

// ErrPackageNotFound 表示暂存包不存在或已过期清除。
var ErrPackageNotFound = errors.New("archive package not found")

// PackageInfo 是暂存区里一个归档包的对外信息。
type PackageInfo struct {
	Name               string
	TicketNumbers      []string
	TicketCount        int
	AttachmentBytes    int64
	GeneratedAt        time.Time
	ExpiresAt          time.Time
	OperatorName       string
	RemovedTicketCount int
}

type manifest struct {
	FormatVersion   int              `json:"format_version"`
	PlatformVersion string           `json:"platform_version"`
	GeneratedAt     string           `json:"generated_at"`
	PackageName     string           `json:"package_name"`
	Operator        manifestActor    `json:"operator"`
	Tickets         []manifestTicket `json:"tickets"`
	Files           []manifestFile   `json:"files"`
}

type manifestActor struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Name     string `json:"name"`
}

type manifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type manifestTicket struct {
	Number          string `json:"number"`
	Title           string `json:"title"`
	ClosedAt        string `json:"closed_at"`
	AttachmentBytes int64  `json:"attachment_bytes"`
	ContentSHA256   string `json:"content_sha256"`
}

var attachmentRefPattern = regexp.MustCompile(`att:[A-Za-z0-9_-]+`)

func archiveAttachmentPathPrefix(number string, id int64) string {
	return "tickets/" + number + "/attachments/att_" + strconv.FormatInt(id, 10) + "/"
}

func archiveAttachmentPath(number string, id int64, filename string) string {
	return archiveAttachmentPathPrefix(number, id) + filename
}

type ticketExport struct {
	Ticket        map[string]any   `json:"ticket"`
	BriefVersions []map[string]any `json:"brief_versions"`
	Replies       []map[string]any `json:"replies"`
	Attachments   []map[string]any `json:"attachments"`
	Timeline      []map[string]any `json:"timeline"`
	Drafts        []map[string]any `json:"drafts"`
	AccessRecords []map[string]any `json:"access_records"`
	Labels        []map[string]any `json:"labels"`
	Collaborators []map[string]any `json:"collaborators"`
	GuestLinks    []map[string]any `json:"guest_links"`
	GuestDevices  []map[string]any `json:"guest_devices"`
	TicketReads   []map[string]any `json:"ticket_reads"`
}

// Generate 生成归档包：先完整写出 zip 并原子 rename，成功后才在一个事务里
// 丢弃未发出草稿，保证包生成失败时平台数据不变。
func (s *Service) Generate(ctx context.Context, ticketNumbers []string, closedMonths int, operator identity.Viewer) (PackageInfo, error) {
	if !s.generateMu.TryLock() {
		return PackageInfo{}, ErrGenerateInProgress
	}
	defer s.generateMu.Unlock()
	preview, err := s.Preview(ctx, ticketNumbers, closedMonths)
	if err != nil {
		return PackageInfo{}, err
	}
	if err := os.MkdirAll(s.stageDir(), 0o755); err != nil {
		return PackageInfo{}, err
	}
	now := s.clk.Now()
	name, err := newPackageName(now)
	if err != nil {
		return PackageInfo{}, err
	}
	temp, err := os.CreateTemp(s.stageDir(), ".generating-*.zip.tmp")
	if err != nil {
		return PackageInfo{}, err
	}
	tempName := temp.Name()
	defer func() {
		if temp != nil {
			_ = temp.Close()
		}
		if tempName != "" {
			_ = os.Remove(tempName)
		}
	}()

	exps := make(map[int64]ticketExport)
	files := make([]manifestFile, 0)
	zw := zip.NewWriter(temp)
	for i, summary := range preview.Tickets {
		export, err := s.exportTicket(ctx, summary)
		if err != nil {
			_ = zw.Close()
			return PackageInfo{}, err
		}
		exps[summary.Seq] = export
		encoded, err := json.MarshalIndent(export, "", "  ")
		if err != nil {
			_ = zw.Close()
			return PackageInfo{}, err
		}
		encoded = append(encoded, '\n')
		path := "tickets/" + summary.Number + "/ticket.json"
		if err := addZipFile(zw, &files, path, encoded); err != nil {
			_ = zw.Close()
			return PackageInfo{}, err
		}
		sum := sha256.Sum256(encoded)
		summary.ContentSHA256 = hex.EncodeToString(sum[:])
		preview.Tickets[i] = summary
	}
	for _, summary := range preview.Tickets {
		export := exps[summary.Seq]
		atts, err := s.packageAttachments(zw, &files, summary, export)
		if err != nil {
			_ = zw.Close()
			return PackageInfo{}, err
		}
		encoded := []byte(markdownOf(summary, export, atts))
		path := "tickets/" + summary.Number + "/README.md"
		if err := addZipFile(zw, &files, path, encoded); err != nil {
			_ = zw.Close()
			return PackageInfo{}, err
		}
	}

	m := manifest{FormatVersion: FormatVersion, PlatformVersion: version.Server,
		GeneratedAt: now.Format(time.RFC3339Nano), PackageName: name,
		Operator: manifestActor{ID: operator.Member.ID, Username: operator.Member.Username, Name: operator.Member.Name},
		Files:    files}
	for _, t := range preview.Tickets {
		m.Tickets = append(m.Tickets, manifestTicket{Number: t.Number, Title: t.Title,
			ClosedAt: t.ClosedAt.Format(time.RFC3339Nano), AttachmentBytes: t.AttachmentBytes,
			ContentSHA256: t.ContentSHA256})
	}
	manifestBytes, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		_ = zw.Close()
		return PackageInfo{}, err
	}
	if err := addZipEntry(zw, "manifest.json", append(manifestBytes, '\n')); err != nil {
		_ = zw.Close()
		return PackageInfo{}, err
	}
	if err := zw.Close(); err != nil {
		return PackageInfo{}, err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		temp = nil
		return PackageInfo{}, err
	}
	if err := temp.Close(); err != nil {
		temp = nil
		return PackageInfo{}, err
	}
	temp = nil
	final := filepath.Join(s.stageDir(), name)
	if err := os.Rename(tempName, final); err != nil {
		return PackageInfo{}, err
	}
	tempName = ""

	if err := s.discardPendingDrafts(ctx, preview.Tickets, preview.Drafts, closedMonths,
		operator, name, now); err != nil {
		// 包已完整写出，但「生成」这个操作还没有成功：移除暂存包，让管理员
		// 故障恢复后从原数据重试，避免列表里出现未完成丢弃动作的包。
		_ = os.Remove(final)
		return PackageInfo{}, err
	}
	return PackageInfo{Name: name, TicketNumbers: numbersOf(preview.Tickets), TicketCount: len(preview.Tickets),
		AttachmentBytes: preview.AttachmentBytes(), GeneratedAt: now, ExpiresAt: now.Add(Retention),
		OperatorName: operator.Member.Name}, nil
}

// List 列出未过期的暂存包（新包在前）。
func (s *Service) List(ctx context.Context) ([]PackageInfo, error) {
	if err := s.CleanupExpired(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.stageDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []PackageInfo{}, nil
		}
		return nil, err
	}
	removedCounts, err := s.removedTicketCounts(ctx)
	if err != nil {
		return nil, err
	}
	out := []PackageInfo{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".zip") {
			continue
		}
		info, err := s.infoFromZip(entry.Name())
		if errors.Is(err, ErrPackageNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if s.clk.Now().After(info.ExpiresAt) {
			continue
		}
		info.RemovedTicketCount = removedCounts[info.Name]
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GeneratedAt.After(out[j].GeneratedAt) })
	return out, nil
}

func (s *Service) removedTicketCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT package_name, COUNT(*) FROM archived_tickets GROUP BY package_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			return nil, err
		}
		out[name] = count
	}
	return out, rows.Err()
}

// Open 返回未过期包的文件句柄；调用方负责 Close。
func (s *Service) Open(name string) (*os.File, PackageInfo, error) {
	if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".zip") ||
		strings.HasPrefix(name, ".") {
		return nil, PackageInfo{}, ErrPackageNotFound
	}
	path := filepath.Join(s.stageDir(), name)
	file, err := os.Open(path)
	if err != nil {
		return nil, PackageInfo{}, ErrPackageNotFound
	}
	info, err := s.infoFromZip(name)
	if err != nil {
		file.Close()
		return nil, PackageInfo{}, err
	}
	if s.clk.Now().After(info.ExpiresAt) {
		file.Close()
		_ = os.Remove(path)
		return nil, PackageInfo{}, ErrPackageNotFound
	}
	return file, info, nil
}

// CleanupExpired 清除暂存超过 7 天的包与生成中断留下的临时文件。
func (s *Service) CleanupExpired() error {
	if !s.generateMu.TryLock() {
		// 正在生成时不能清理临时文件；下一轮清除任务会处理到期包。
		return nil
	}
	defer s.generateMu.Unlock()
	entries, err := os.ReadDir(s.stageDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(s.stageDir(), name)
		if strings.HasPrefix(name, ".generating-") && strings.HasSuffix(name, ".tmp") {
			_ = os.Remove(path)
			continue
		}
		if !strings.HasSuffix(name, ".zip") {
			continue
		}
		info, err := s.infoFromZip(name)
		if errors.Is(err, ErrPackageNotFound) {
			continue
		}
		if err != nil || s.clk.Now().After(info.ExpiresAt) {
			_ = os.Remove(path)
		}
	}
	return nil
}

func (s *Service) infoFromZip(name string) (PackageInfo, error) {
	file, err := os.Open(filepath.Join(s.stageDir(), name))
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	zr, err := zip.NewReader(file, info.Size())
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	rc, err := zr.Open("manifest.json")
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	defer rc.Close()
	var m manifest
	if err := json.NewDecoder(rc).Decode(&m); err != nil || m.FormatVersion < 1 {
		return PackageInfo{}, ErrPackageNotFound
	}
	generated, err := time.ParseInLocation(time.RFC3339Nano, m.GeneratedAt, clock.Beijing)
	if err != nil {
		return PackageInfo{}, ErrPackageNotFound
	}
	numbers := make([]string, 0, len(m.Tickets))
	var attachmentBytes int64
	for _, t := range m.Tickets {
		numbers = append(numbers, t.Number)
		attachmentBytes += t.AttachmentBytes
	}
	if m.PackageName != name {
		return PackageInfo{}, ErrPackageNotFound
	}
	return PackageInfo{Name: name, TicketNumbers: numbers, TicketCount: len(numbers),
		AttachmentBytes: attachmentBytes, GeneratedAt: generated, ExpiresAt: generated.Add(Retention),
		OperatorName: m.Operator.Name}, nil
}

type archiveQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (s *Service) exportTicket(ctx context.Context, summary TicketSummary) (ticketExport, error) {
	return exportTicket(ctx, s.db, summary)
}

func exportTicket(ctx context.Context, q archiveQueryer, summary TicketSummary) (ticketExport, error) {
	var out ticketExport
	var err error
	if out.Ticket, err = queryObject(ctx, q, `
		SELECT seq, number, kind, title, status, close_reason, brief_md, brief_revision,
		       creator_member_id, creator_agent_id, assignee_id, template_key, created_at,
		       last_change, duplicate_of_seq, template_version, template_snapshot, fields_json,
		       creator_link_id, creator_intake_id, creator_author_name, brief_attachment_ids
		FROM tickets WHERE seq = ?`, summary.Seq); err != nil {
		return out, err
	}
	if out.BriefVersions, err = queryObjects(ctx, q, `
		SELECT ticket_seq, revision, title, brief_md, fields_json, attachment_ids,
		       actor_member_id, actor_agent_id, actor_link_id, actor_link_note,
		       actor_author_name, actor_via_agent, at
		FROM brief_versions WHERE ticket_seq = ? ORDER BY revision`, summary.Seq); err != nil {
		return out, err
	}
	if out.Replies, err = queryObjects(ctx, q, timelineColumns+`
		FROM timeline_events WHERE ticket_seq = ? AND type = 'reply' ORDER BY seq`, summary.Seq); err != nil {
		return out, err
	}
	// 草稿附件会随未发出草稿一起丢弃，不进入归档包。
	if out.Attachments, err = queryObjects(ctx, q, attachmentColumns+`
		FROM attachments WHERE ticket_seq = ? AND draft_id IS NULL ORDER BY id`, summary.Seq); err != nil {
		return out, err
	}
	if out.Timeline, err = queryObjects(ctx, q, timelineColumns+`
		FROM timeline_events WHERE ticket_seq = ? ORDER BY seq`, summary.Seq); err != nil {
		return out, err
	}
	if out.AccessRecords, err = queryObjects(ctx, q, accessRecordColumns+`
		FROM access_records WHERE ticket_seq = ? ORDER BY first_at, id`, summary.Seq); err != nil {
		return out, err
	}
	if out.Labels, err = queryObjects(ctx, q, `
		SELECT ticket_seq, label FROM ticket_labels WHERE ticket_seq = ? ORDER BY label`, summary.Seq); err != nil {
		return out, err
	}
	if out.Collaborators, err = queryObjects(ctx, q, `
		SELECT ticket_seq, member_id, added_at FROM ticket_collaborators
		WHERE ticket_seq = ? ORDER BY member_id`, summary.Seq); err != nil {
		return out, err
	}
	if out.GuestLinks, err = queryObjects(ctx, q, guestLinkColumns+`
		FROM guest_links WHERE ticket_seq = ? ORDER BY id`, summary.Seq); err != nil {
		return out, err
	}
	if out.TicketReads, err = queryObjects(ctx, q, `
		SELECT member_id, ticket_seq, last_read_seq FROM ticket_reads
		WHERE ticket_seq = ? ORDER BY member_id`, summary.Seq); err != nil {
		return out, err
	}
	sentRows, err := queryObjects(ctx, q, draftColumns+`
		FROM drafts WHERE ticket_seq = ? AND status = 'sent' ORDER BY id`, summary.Seq)
	if err != nil {
		return out, err
	}
	out.Drafts = make([]map[string]any, 0, len(sentRows))
	for _, row := range sentRows {
		id, _ := row["id"].(int64)
		versions, err := queryObjects(ctx, q, draftVersionColumns+`
			FROM draft_versions WHERE draft_id = ? ORDER BY version`, id)
		if err != nil {
			return out, err
		}
		if versions == nil {
			versions = []map[string]any{}
		}
		out.Drafts = append(out.Drafts, map[string]any{"draft": row, "versions": versions})
	}
	out.GuestDevices, err = guestDevices(ctx, q, out.GuestLinks)
	return out, err
}

func queryObject(ctx context.Context, q archiveQueryer, query string, args ...any) (map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, sql.ErrNoRows
	}
	out, err := scanObject(rows)
	if err != nil {
		return nil, err
	}
	return out, rows.Err()
}

func queryObjects(ctx context.Context, q archiveQueryer, query string, args ...any) ([]map[string]any, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		row, err := scanObject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func scanObject(rows *sql.Rows) (map[string]any, error) {
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	values := make([]any, len(types))
	dest := make([]any, len(types))
	for i := range values {
		dest[i] = &values[i]
	}
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(types))
	for i, typ := range types {
		out[typ.Name()] = normalizeSQLValue(values[i])
	}
	return out, nil
}

func normalizeSQLValue(value any) any {
	switch v := value.(type) {
	case []byte:
		if utf8.Valid(v) {
			return string(v)
		}
		return v
	default:
		return value
	}
}

const timelineColumns = `SELECT ticket_seq, seq, change_seq, type, actor_member_id, actor_agent_id, at,
	       body_md, reply_no, deliver, revision, reason, duplicate_of, label, actor_link_id,
	       actor_link_note, actor_author_name, actor_via_agent, note, link_expires_at,
	       assign_from, assign_to, attachment_ids, collaborator_member_id, collaborator_name,
	       actor_system`

const attachmentColumns = `SELECT id, ticket_seq, filename, size, state, attached_to,
	       uploader_member_id, uploader_agent_id, uploader_link_id, uploader_intake_id,
	       intake_upload_key, uploaded_at, claimed_at, draft_id`

const accessRecordColumns = `SELECT id, ticket_seq, actor_kind, actor_name, actor_member_id,
	       actor_agent_id, actor_link_id, actor_link_note, entry_method, action, attachment_id,
	       attachment_ref, attachment_filename, first_at, last_at, count`

const guestLinkColumns = `SELECT id, ticket_seq, key_hash, note, expires_at, revoked_at,
	       can_edit_brief, created_by_member_id, created_by_agent_id, created_at, claimed_at,
	       claim_name, access_code_hash, access_code_failures, access_code_locked_at, key_ciphertext`

const draftColumns = `SELECT id, kind, status, ticket_seq, member_id, request_json, version,
	       base_timeline_seq, member_edited, created_at, updated_at, sent_at, sent_timeline_seq`

const draftVersionColumns = `SELECT draft_id, version, request_json, author_member_id,
	       author_agent_id, at`

func guestDevices(ctx context.Context, q archiveQueryer, links []map[string]any) ([]map[string]any, error) {
	ids := make([]any, 0, len(links))
	for _, link := range links {
		id, _ := link["id"].(int64)
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return []map[string]any{}, nil
	}
	return queryObjects(ctx, q, `SELECT id, guest_link_id, token_hash, created_at, entry_method
		FROM guest_devices WHERE guest_link_id IN (`+inPlaceholders(len(ids))+`)
		ORDER BY guest_link_id, id`, ids...)
}

type packagedAttachment struct {
	Ref      string
	Filename string
	Relative string
}

func (s *Service) packageAttachments(zw *zip.Writer, files *[]manifestFile, summary TicketSummary, export ticketExport) ([]packagedAttachment, error) {
	out := make([]packagedAttachment, 0, len(export.Attachments))
	for _, row := range export.Attachments {
		id, _ := row["id"].(int64)
		filename, _ := row["filename"].(string)
		ref := fmt.Sprintf("att_%d", id)
		relative := "attachments/" + ref + "/" + filename
		path := archiveAttachmentPath(summary.Number, id, filename)
		src, err := os.Open(filepath.Join(s.attachmentsDir, strconv.FormatInt(id, 10)))
		if err != nil {
			return nil, fmt.Errorf("读取附件 %s 失败：%w", ref, err)
		}
		size, _ := row["size"].(int64)
		if err := addZipStream(zw, files, path, src, size); err != nil {
			_ = src.Close()
			return nil, err
		}
		if err := src.Close(); err != nil {
			return nil, err
		}
		out = append(out, packagedAttachment{Ref: ref, Filename: filename, Relative: relative})
	}
	return out, nil
}

func addZipFile(zw *zip.Writer, files *[]manifestFile, path string, body []byte) error {
	if err := addZipEntry(zw, path, body); err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	*files = append(*files, manifestFile{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))})
	return nil
}

func addZipEntry(zw *zip.Writer, path string, body []byte) error {
	path, err := safeZipPath(path)
	if err != nil {
		return err
	}
	entry, err := zw.Create(path)
	if err != nil {
		return err
	}
	_, err = entry.Write(body)
	return err
}

func addZipStream(zw *zip.Writer, files *[]manifestFile, path string, src io.Reader, size int64) error {
	path, err := safeZipPath(path)
	if err != nil {
		return err
	}
	entry, err := zw.Create(path)
	if err != nil {
		return err
	}
	sum := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(entry, sum), src)
	if copyErr != nil {
		return copyErr
	}
	if written != size {
		return fmt.Errorf("附件 %s 大小不一致：数据库 %d，实际 %d", path, size, written)
	}
	*files = append(*files, manifestFile{Path: path, SHA256: hex.EncodeToString(sum.Sum(nil)), Size: written})
	return nil
}

func safeZipPath(path string) (string, error) {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		part = sanitizeZipSegment(part)
		if part == "" {
			return "", fmt.Errorf("归档包路径 %q 包含空路径段", path)
		}
		parts[i] = part
	}
	return strings.Join(parts, "/"), nil
}

func sanitizeZipSegment(part string) string {
	if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `\/`) ||
		strings.ContainsFunc(part, unicode.IsControl) {
		return "_"
	}
	return part
}

func markdownOf(summary TicketSummary, export ticketExport, atts []packagedAttachment) string {
	refs := map[string]packagedAttachment{}
	for _, att := range atts {
		refs[att.Ref] = att
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# %s %s\n\n", summary.Number, stringValue(export.Ticket["title"]))
	fmt.Fprintf(&b, "- 状态：%s\n", stringValue(export.Ticket["status"]))
	fmt.Fprintf(&b, "- 创建时间：%s\n", stringValue(export.Ticket["created_at"]))
	fmt.Fprintf(&b, "- 关闭时间：%s\n", summary.ClosedAt.Format(time.RFC3339Nano))
	if labels := stringValues(export.Labels, "label"); len(labels) > 0 {
		fmt.Fprintf(&b, "- 标签：%s\n", strings.Join(labels, "、"))
	}
	b.WriteString("\n## 说明\n\n")
	rewrite := func(body string) string {
		return attachmentRefPattern.ReplaceAllStringFunc(body, func(token string) string {
			att, ok := refs[token[len("att:"):]]
			if !ok {
				return token
			}
			return escapeMarkdownPath(att.Relative)
		})
	}
	brief := rewrite(stringValue(export.Ticket["brief_md"]))
	if brief == "" {
		brief = stringValue(export.Ticket["fields_json"])
	}
	b.WriteString(brief)
	b.WriteString("\n\n### 附件\n")
	if len(atts) == 0 {
		b.WriteString("无\n")
	}
	for _, att := range atts {
		fmt.Fprintf(&b, "- [%s](%s)\n", att.Filename, escapeMarkdownPath(att.Relative))
	}
	if len(export.Replies) > 0 {
		b.WriteString("\n## 回复\n")
		for _, reply := range export.Replies {
			fmt.Fprintf(&b, "\n### 回复 %v\n\n%s\n", reply["reply_no"], rewrite(stringValue(reply["body_md"])))
		}
	}
	b.WriteString("\n## 时间线\n\n")
	for _, event := range export.Timeline {
		fmt.Fprintf(&b, "- %v · %s · %s\n", event["type"], stringValue(event["at"]), timelineDetail(event))
	}
	b.WriteString("\n## 起草过程\n\n见 `ticket.json` 的 `drafts` 字段。\n")
	b.WriteString("\n## 访问记录与访客链接\n\n见 `ticket.json` 的 `access_records`、`guest_links`、`guest_devices` 字段。\n")
	return b.String()
}

func timelineDetail(event map[string]any) string {
	for _, key := range []string{"body_md", "reason", "note", "assign_to", "label", "collaborator_name"} {
		if value := stringValue(event[key]); value != "" {
			return value
		}
	}
	return "—"
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func stringValues(rows []map[string]any, key string) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if value := stringValue(row[key]); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func escapeMarkdownPath(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = strings.ReplaceAll(url.QueryEscape(part), "+", "%20")
	}
	return strings.Join(parts, "/")
}

func (s *Service) discardPendingDrafts(ctx context.Context, tickets []TicketSummary, drafts []DraftSummary,
	closedMonths int, operator identity.Viewer, packageName string, generatedAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := revalidateSelectionTx(ctx, tx, tickets, closedMonths, s.clk.Now()); err != nil {
		return err
	}
	if err := revalidateDraftsTx(ctx, tx, drafts); err != nil {
		return err
	}
	draftIDs := make([]any, 0, len(drafts))
	for _, draft := range drafts {
		draftIDs = append(draftIDs, draft.seq)
	}
	fileIDs := []int64{}
	if len(draftIDs) > 0 {
		rows, err := tx.QueryContext(ctx, `
			SELECT a.id FROM attachments a
			WHERE a.draft_id IN (`+inPlaceholders(len(draftIDs))+`)`, draftIDs...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			fileIDs = append(fileIDs, id)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	if len(draftIDs) > 0 {
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM attachments WHERE draft_id IN (`+inPlaceholders(len(draftIDs))+`)`, draftIDs...); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			DELETE FROM drafts WHERE id IN (`+inPlaceholders(len(draftIDs))+`)
			AND status = 'pending'`, draftIDs...); err != nil {
			return err
		}
	}
	if err := insertArchiveOperation(ctx, tx, "archive_generate", operator.Member.ID,
		generatedAt, numbersOf(tickets), packageName); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.atts.RemoveFiles(fileIDs)
	return nil
}

func inPlaceholders(n int) string {
	if n == 0 {
		return ""
	}
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func revalidateSelectionTx(ctx context.Context, tx *sql.Tx, tickets []TicketSummary,
	closedMonths int, now time.Time) error {
	args := make([]any, 0, len(tickets))
	for _, ticket := range tickets {
		args = append(args, ticket.Seq)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT t.seq, t.status, (
			SELECT e.at FROM timeline_events e
			WHERE e.ticket_seq = t.seq AND e.type = 'closed'
			ORDER BY e.seq DESC LIMIT 1
		)
		FROM tickets t WHERE t.seq IN (`+inPlaceholders(len(tickets))+`)`, args...)
	if err != nil {
		return err
	}
	statuses := map[int64]string{}
	closedAts := map[int64]sql.NullString{}
	for rows.Next() {
		var seq int64
		var status string
		var closedAt sql.NullString
		if err := rows.Scan(&seq, &status, &closedAt); err != nil {
			rows.Close()
			return err
		}
		statuses[seq] = status
		closedAts[seq] = closedAt
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	cutoff := now.AddDate(0, -closedMonths, 0)
	for _, ticket := range tickets {
		if statuses[ticket.Seq] != "closed" || !closedAts[ticket.Seq].Valid {
			return fmt.Errorf("%w：%s", ErrNotArchivable, ticket.Number)
		}
		at, err := time.ParseInLocation(time.RFC3339Nano, closedAts[ticket.Seq].String, clock.Beijing)
		if err != nil || at.After(cutoff) {
			return fmt.Errorf("%w：%s", ErrNotArchivable, ticket.Number)
		}
	}
	return nil
}

func revalidateDraftsTx(ctx context.Context, tx *sql.Tx, drafts []DraftSummary) error {
	if len(drafts) == 0 {
		return nil
	}
	args := make([]any, 0, len(drafts))
	for _, draft := range drafts {
		args = append(args, draft.seq)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT id, status, ticket_seq, version, updated_at FROM drafts
		WHERE id IN (`+inPlaceholders(len(drafts))+`)`, args...)
	if err != nil {
		return err
	}
	type currentDraft struct {
		status  string
		ticket  int64
		version int64
		updated string
	}
	current := map[int64]currentDraft{}
	for rows.Next() {
		var id int64
		var draft currentDraft
		if err := rows.Scan(&id, &draft.status, &draft.ticket, &draft.version, &draft.updated); err != nil {
			rows.Close()
			return err
		}
		current[id] = draft
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, draft := range drafts {
		got, ok := current[draft.seq]
		if !ok || got.status != "pending" || got.ticket == 0 {
			return fmt.Errorf("待丢弃草稿 %s 已变化", draft.ID)
		}
		if got.ticket != draft.ticketSeq || got.version != draft.Version || got.updated != draft.UpdatedAt {
			return fmt.Errorf("待丢弃草稿 %s 已变化", draft.ID)
		}
	}
	return nil
}

func newPackageName(now time.Time) (string, error) {
	buf := make([]byte, 5)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("archive-%d-%s.zip", now.UnixNano(), hex.EncodeToString(buf)), nil
}

func numbersOf(tickets []TicketSummary) []string {
	out := make([]string, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, t.Number)
	}
	return out
}
