package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/allanpk716/agent-taskboard/internal/attachments"
	"github.com/allanpk716/agent-taskboard/internal/clock"
	"github.com/allanpk716/agent-taskboard/internal/identity"
)

var (
	// ErrPackageInvalid 表示上传的归档包损坏、被篡改或结构不完整。
	ErrPackageInvalid = errors.New("archive package invalid")
	// ErrFormatUnsupported 表示归档包格式版本高于当前平台支持版本。
	ErrFormatUnsupported = errors.New("archive format unsupported")
)

const maxArchiveJSONBytes int64 = 16 << 20
const maxArchiveUncompressedBytes int64 = 4 << 30

// ImportTicket 是检查归档包时的一张候选工单。
type ImportTicket struct {
	Seq             int64
	Number          string
	Title           string
	ClosedAt        time.Time
	AttachmentBytes int64
	Importable      bool
	Reason          string
}

// ImportInspection 是一次检查归档包的结果。
type ImportInspection struct {
	FormatVersion   int
	PlatformVersion string
	PackageName     string
	GeneratedAt     time.Time
	AttachmentBytes int64
	Tickets         []ImportTicket
}

// ImportResult 是一次导入的外部结果；Rejected 里的工单保持墓碑不变。
type ImportResult struct {
	PackageName string
	ImportedAt  time.Time
	Imported    []ImportTicket
	Rejected    []ImportTicket
}

// parsedImportPackage 保存已通过完整校验的包内容。
type parsedImportPackage struct {
	Manifest  manifest
	Tickets   map[string]ticketExport
	Files     map[string]*zip.File
	Generated time.Time
}

// InspectImport 校验上传包的格式、清单与全部文件校验和，并逐张判断能否导入。
func (s *Service) InspectImport(ctx context.Context, readerAt io.ReaderAt, size int64) (ImportInspection, error) {
	parsed, err := s.parseImportPackage(readerAt, size)
	if err != nil {
		return ImportInspection{}, err
	}
	out := ImportInspection{FormatVersion: parsed.Manifest.FormatVersion,
		PlatformVersion: parsed.Manifest.PlatformVersion, PackageName: parsed.Manifest.PackageName,
		GeneratedAt: parsed.Generated, Tickets: []ImportTicket{}}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportInspection{}, err
	}
	defer tx.Rollback()
	for _, meta := range parsed.Manifest.Tickets {
		export := parsed.Tickets[meta.Number]
		ticket := ImportTicket{Number: meta.Number, Title: meta.Title,
			ClosedAt: parseArchiveTime(meta.ClosedAt), AttachmentBytes: meta.AttachmentBytes}
		ticket.Seq = importInt(export.Ticket, "seq")
		reason, err := s.canImportTicketTx(ctx, tx, parsed, meta, export)
		if err != nil {
			return ImportInspection{}, err
		}
		if reason != "" {
			ticket.Importable, ticket.Reason = false, reason
		} else {
			ticket.Importable = true
		}
		out.Tickets = append(out.Tickets, ticket)
		out.AttachmentBytes += meta.AttachmentBytes
	}
	return out, nil
}

// Import 校验上传包后，在一个事务里逐张恢复选中的墓碑工单；附件先按原 ID
// 写回，再提交数据库，失败时回滚并清理这次写入的文件。
func (s *Service) Import(ctx context.Context, readerAt io.ReaderAt, size int64,
	selected []string, operator identity.Viewer) (ImportResult, error) {
	s.generateMu.Lock()
	defer s.generateMu.Unlock()

	parsed, err := s.parseImportPackage(readerAt, size)
	if err != nil {
		return ImportResult{}, err
	}
	want := make(map[string]bool, len(selected))
	for _, number := range selected {
		number = strings.TrimSpace(number)
		if number != "" {
			want[number] = true
		}
	}
	selectAll := len(want) == 0
	for number := range parsed.Tickets {
		if selectAll {
			want[number] = true
		}
	}

	now := s.clk.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `PRAGMA defer_foreign_keys = ON`); err != nil {
		return ImportResult{}, err
	}
	result := ImportResult{PackageName: parsed.Manifest.PackageName, ImportedAt: now,
		Imported: []ImportTicket{}, Rejected: []ImportTicket{}}
	written := []int64{}
	importedNumbers := []string{}
	for _, meta := range parsed.Manifest.Tickets {
		candidate := importCandidate(meta, parsed.Tickets[meta.Number])
		if !want[meta.Number] {
			continue
		}
		reason, err := s.canImportTicketTx(ctx, tx, parsed, meta, parsed.Tickets[meta.Number])
		if err != nil {
			s.removeImportFiles(ctx, written)
			return ImportResult{}, err
		}
		if reason != "" {
			candidate.Importable, candidate.Reason = false, reason
			result.Rejected = append(result.Rejected, candidate)
			continue
		}
		ids, err := s.writeImportAttachments(ctx, parsed, meta, parsed.Tickets[meta.Number])
		written = append(written, ids...)
		if err != nil {
			s.removeImportFiles(ctx, written)
			return ImportResult{}, err
		}
		if err := insertImportTicketTx(ctx, tx, parsed.Tickets[meta.Number]); err != nil {
			s.removeImportFiles(ctx, written)
			return ImportResult{}, fmt.Errorf("恢复工单 %s 失败：%w", meta.Number, err)
		}
		if err := deleteTombstoneTx(ctx, tx, importInt(parsed.Tickets[meta.Number].Ticket, "seq")); err != nil {
			s.removeImportFiles(ctx, written)
			return ImportResult{}, err
		}
		candidate.Importable = true
		result.Imported = append(result.Imported, candidate)
		importedNumbers = append(importedNumbers, candidate.Number)
	}
	for number := range want {
		if _, ok := parsed.Tickets[number]; !ok {
			result.Rejected = append(result.Rejected, ImportTicket{Number: number,
				Importable: false, Reason: "归档包里没有这张工单"})
		}
	}
	if len(result.Imported) > 0 {
		if err := insertArchiveOperation(ctx, tx, "archive_import", operator.Member.ID, now,
			importedNumbers, parsed.Manifest.PackageName); err != nil {
			s.removeImportFiles(ctx, written)
			return ImportResult{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		s.removeImportFiles(ctx, written)
		return ImportResult{}, err
	}
	return result, nil
}

func (s *Service) parseImportPackage(readerAt io.ReaderAt, size int64) (*parsedImportPackage, error) {
	zr, err := zip.NewReader(readerAt, size)
	if err != nil {
		return nil, fmt.Errorf("%w：不是合法 zip：%v", ErrPackageInvalid, err)
	}
	entries := make(map[string]*zip.File, len(zr.File))
	var manifestEntry *zip.File
	for _, entry := range zr.File {
		if entry.FileInfo().IsDir() {
			continue
		}
		if _, exists := entries[entry.Name]; exists {
			return nil, fmt.Errorf("%w：路径 %s 重复", ErrPackageInvalid, entry.Name)
		}
		if entry.Name == "manifest.json" {
			manifestEntry = entry
		}
		entries[entry.Name] = entry
	}
	if manifestEntry == nil {
		return nil, fmt.Errorf("%w：缺少 manifest.json", ErrPackageInvalid)
	}
	manifestBody, err := readBoundedZipEntry(manifestEntry, maxArchiveJSONBytes)
	if err != nil {
		return nil, err
	}
	var m manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&m); err != nil {
		return nil, fmt.Errorf("%w：清单不是合法 JSON：%v", ErrPackageInvalid, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w：清单后有多余内容", ErrPackageInvalid)
	}
	if m.FormatVersion < 1 {
		return nil, fmt.Errorf("%w：格式版本 %d 无效", ErrPackageInvalid, m.FormatVersion)
	}
	if m.FormatVersion > FormatVersion {
		return nil, fmt.Errorf("%w：包格式版本 %d 高于当前支持的 %d",
			ErrFormatUnsupported, m.FormatVersion, FormatVersion)
	}
	if m.PackageName == "" || len(m.Tickets) == 0 || len(m.Files) == 0 {
		return nil, fmt.Errorf("%w：清单内容不完整", ErrPackageInvalid)
	}
	if m.PlatformVersion == "" || m.Operator.Username == "" || m.Operator.Name == "" {
		return nil, fmt.Errorf("%w：清单缺少平台版本或操作人", ErrPackageInvalid)
	}
	if _, err := time.ParseInLocation(time.RFC3339Nano, m.GeneratedAt, clock.Beijing); err != nil {
		return nil, fmt.Errorf("%w：生成时间无效：%v", ErrPackageInvalid, err)
	}

	listed := make(map[string]manifestFile, len(m.Files))
	totalSize := int64(0)
	for _, file := range m.Files {
		if !safeImportPath(file.Path) || file.Path == "manifest.json" {
			return nil, fmt.Errorf("%w：文件路径 %q 不安全", ErrPackageInvalid, file.Path)
		}
		if len(file.SHA256) != sha256.Size*2 {
			return nil, fmt.Errorf("%w：%s 的 SHA-256 长度不对", ErrPackageInvalid, file.Path)
		}
		if _, err := hex.DecodeString(file.SHA256); err != nil {
			return nil, fmt.Errorf("%w：%s 的 SHA-256 不是十六进制", ErrPackageInvalid, file.Path)
		}
		if _, exists := listed[file.Path]; exists {
			return nil, fmt.Errorf("%w：清单路径 %s 重复", ErrPackageInvalid, file.Path)
		}
		listed[file.Path] = file
		if file.Size < 0 || file.Size > maxArchiveUncompressedBytes-totalSize {
			return nil, fmt.Errorf("%w：清单声明的解压总量超过上限 %d 字节",
				ErrPackageInvalid, maxArchiveUncompressedBytes)
		}
		totalSize += file.Size
	}
	if len(listed)+1 != len(entries) {
		return nil, fmt.Errorf("%w：清单列出 %d 个文件，包里有 %d 个", ErrPackageInvalid,
			len(listed), len(entries)-1)
	}
	for name, entry := range entries {
		if name == "manifest.json" {
			continue
		}
		file, ok := listed[name]
		if !ok {
			return nil, fmt.Errorf("%w：包里的 %s 不在清单中", ErrPackageInvalid, name)
		}
		if err := verifyZipEntry(entry, file); err != nil {
			return nil, err
		}
	}

	out := &parsedImportPackage{Manifest: m, Tickets: map[string]ticketExport{}, Files: entries}
	out.Generated, _ = time.ParseInLocation(time.RFC3339Nano, m.GeneratedAt, clock.Beijing)
	seen := make(map[string]bool, len(m.Tickets))
	for _, meta := range m.Tickets {
		if meta.Number == "" || seen[meta.Number] || strings.ContainsAny(meta.Number, `\/ .`) {
			return nil, fmt.Errorf("%w：清单工单编号为空或重复", ErrPackageInvalid)
		}
		seen[meta.Number] = true
		if parseArchiveTime(meta.ClosedAt).IsZero() {
			return nil, fmt.Errorf("%w：%s 的关闭时间无效", ErrPackageInvalid, meta.Number)
		}
		name := "tickets/" + meta.Number + "/ticket.json"
		body, err := readBoundedZipEntry(entries[name], maxArchiveJSONBytes)
		if err != nil {
			return nil, err
		}
		var export ticketExport
		exportDecoder := json.NewDecoder(bytes.NewReader(body))
		exportDecoder.UseNumber()
		if err := exportDecoder.Decode(&export); err != nil {
			return nil, fmt.Errorf("%w：%s 不是合法 JSON：%v", ErrPackageInvalid, name, err)
		}
		if exportDecoder.More() {
			return nil, fmt.Errorf("%w：%s 后有多余内容", ErrPackageInvalid, name)
		}
		normalizeImportTicket(&export)
		normalizeLegacyGuestCredentials(&export)
		if err := validateImportTicket(meta, export); err != nil {
			return nil, fmt.Errorf("%w：%v", ErrPackageInvalid, err)
		}
		out.Tickets[meta.Number] = export
	}
	if err := validateManifestFiles(out); err != nil {
		return nil, fmt.Errorf("%w：%v", ErrPackageInvalid, err)
	}
	return out, nil
}

func validateImportTicket(meta manifestTicket, export ticketExport) error {
	normalizeLegacyGuestCredentials(&export)
	if export.Ticket == nil || export.BriefVersions == nil || export.Replies == nil ||
		export.Attachments == nil || export.Timeline == nil || export.Drafts == nil ||
		export.AccessRecords == nil || export.Labels == nil || export.Collaborators == nil ||
		export.GuestLinks == nil || export.GuestDevices == nil || export.TicketReads == nil ||
		export.GuestCredentialHolders == nil || export.GuestCredentialDevices == nil {
		return fmt.Errorf("%s 的结构化数据不完整", meta.Number)
	}
	seq := importInt(export.Ticket, "seq")
	number, _ := export.Ticket["number"].(string)
	title, _ := export.Ticket["title"].(string)
	status, _ := export.Ticket["status"].(string)
	if seq <= 0 || number != meta.Number || title != meta.Title || status != "closed" {
		return fmt.Errorf("%s 的清单与结构化数据不一致", meta.Number)
	}
	draftRows, draftVersionRows := importDraftRows(export)
	for _, table := range importTableColumns {
		rows, err := importRowsForTable(export, table.name, draftRows, draftVersionRows)
		if err != nil {
			return err
		}
		for _, row := range rows {
			for _, column := range table.columns {
				if _, ok := row[column]; !ok {
					return fmt.Errorf("%s 缺少恢复列 %s.%s", meta.Number, table.name, column)
				}
			}
			if importInt(row, "ticket_seq") != 0 && importInt(row, "ticket_seq") != seq {
				return fmt.Errorf("%s 的 %s 关联到其他工单", meta.Number, table.name)
			}
		}
	}
	linkIDs := newIntSet()
	for _, row := range export.GuestLinks {
		linkIDs.add(row["id"])
	}
	for _, row := range export.GuestDevices {
		id := importInt(row, "guest_link_id")
		if !linkIDs.items[id] {
			return fmt.Errorf("%s 的访客设备关联到缺失链接", meta.Number)
		}
	}
	holderIDs := newIntSet()
	for _, row := range export.GuestCredentialHolders {
		holderIDs.add(row["id"])
		ownerType, _ := row["owner_type"].(string)
		if ownerType == "guest_link" && !linkIDs.items[importInt(row, "owner_id")] {
			return fmt.Errorf("%s 的访客凭证持有者关联到缺失链接", meta.Number)
		}
	}
	for _, row := range export.GuestCredentialDevices {
		if !holderIDs.items[importInt(row, "credential_holder_id")] {
			return fmt.Errorf("%s 的访客设备关联到缺失凭证持有者", meta.Number)
		}
	}
	var attachmentBytes int64
	slimPackages := map[string]bool{}
	for _, row := range export.Attachments {
		size := importInt(row, "size")
		if size < 0 {
			return fmt.Errorf("%s 的附件大小无效", meta.Number)
		}
		attachmentBytes += size
		removedAt, _ := row["slim_removed_at"]
		packageName := stringValue(row["slim_package_name"])
		if (removedAt != nil) != (packageName != "") {
			return fmt.Errorf("%s 的附件瘦身状态不完整", meta.Number)
		}
		if removedAt != nil {
			if _, err := time.ParseInLocation(time.RFC3339Nano, stringValue(removedAt), clock.Beijing); err != nil {
				return fmt.Errorf("%s 的附件瘦身时间无效", meta.Number)
			}
			slimPackages[packageName] = true
		}
	}
	if attachmentBytes != meta.AttachmentBytes {
		return fmt.Errorf("%s 的附件大小与清单不一致", meta.Number)
	}
	if !sameStringSet(slimPackages, meta.SlimPackages) {
		return fmt.Errorf("%s 的清单瘦身包与附件瘦身状态不一致", meta.Number)
	}
	return nil
}

func normalizeLegacyGuestCredentials(export *ticketExport) {
	if export.GuestCredentialHolders != nil {
		return
	}
	export.GuestCredentialHolders = []map[string]any{}
	export.GuestCredentialDevices = []map[string]any{}
	for _, link := range export.GuestLinks {
		link["credential_holder_id"] = int64(0)
	}
}

func sameStringSet(got map[string]bool, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for _, name := range want {
		if !got[name] {
			return false
		}
	}
	return true
}

func validateManifestFiles(pkg *parsedImportPackage) error {
	known := make(map[string]bool, len(pkg.Manifest.Tickets))
	for _, meta := range pkg.Manifest.Tickets {
		known[meta.Number] = true
	}
	for _, file := range pkg.Manifest.Files {
		if !strings.HasPrefix(file.Path, "tickets/") {
			return fmt.Errorf("文件 %s 不属于任何工单", file.Path)
		}
		rest := strings.TrimPrefix(file.Path, "tickets/")
		number := rest
		if index := strings.Index(rest, "/"); index >= 0 {
			number = rest[:index]
		}
		if !known[number] {
			return fmt.Errorf("文件 %s 不属于清单中的工单", file.Path)
		}
	}
	return nil
}

func readBoundedZipEntry(entry *zip.File, limit int64) ([]byte, error) {
	if entry == nil {
		return nil, fmt.Errorf("%w：缺少 zip 条目", ErrPackageInvalid)
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, fmt.Errorf("%w：读取 %s 失败：%v", ErrPackageInvalid, entry.Name, err)
	}
	body, err := io.ReadAll(io.LimitReader(rc, limit+1))
	closeErr := rc.Close()
	if err != nil {
		return nil, fmt.Errorf("%w：读取 %s 失败：%v", ErrPackageInvalid, entry.Name, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("%w：关闭 %s 失败：%v", ErrPackageInvalid, entry.Name, closeErr)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("%w：%s 超过单条目读取上限 %d 字节", ErrPackageInvalid, entry.Name, limit)
	}
	return body, nil
}

func verifyZipEntry(entry *zip.File, want manifestFile) error {
	rc, err := entry.Open()
	if err != nil {
		return fmt.Errorf("%w：读取 %s 失败：%v", ErrPackageInvalid, entry.Name, err)
	}
	sum := sha256.New()
	written, copyErr := io.Copy(sum, io.LimitReader(rc, want.Size+1))
	closeErr := rc.Close()
	if copyErr != nil {
		return fmt.Errorf("%w：读取 %s 失败：%v", ErrPackageInvalid, entry.Name, copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("%w：关闭 %s 失败：%v", ErrPackageInvalid, entry.Name, closeErr)
	}
	if written != want.Size {
		return fmt.Errorf("%w：%s 大小不符", ErrPackageInvalid, entry.Name)
	}
	if !strings.EqualFold(hex.EncodeToString(sum.Sum(nil)), want.SHA256) {
		return fmt.Errorf("%w：%s SHA-256 不符", ErrPackageInvalid, entry.Name)
	}
	return nil
}

func safeImportPath(name string) bool {
	if name == "" || path.IsAbs(name) || strings.ContainsAny(name, `\`) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func normalizeImportTicket(export *ticketExport) {
	for _, rows := range []*[]map[string]any{&export.BriefVersions, &export.Replies,
		&export.Attachments, &export.Timeline, &export.AccessRecords, &export.Labels,
		&export.Collaborators, &export.GuestLinks, &export.GuestDevices,
		&export.GuestCredentialHolders, &export.GuestCredentialDevices, &export.TicketReads} {
		for i, row := range *rows {
			(*rows)[i] = normalizeImportMap(row).(map[string]any)
		}
	}
	if export.Ticket != nil {
		export.Ticket = normalizeImportMap(export.Ticket).(map[string]any)
	}
	for i, item := range export.Drafts {
		normalized := normalizeImportMap(item).(map[string]any)
		export.Drafts[i] = normalized
	}
	// 旧格式的归档包没有瘦身列；统一补默认值，恢复时与「仍在服务器上」一致。
	for i, row := range export.Attachments {
		if _, ok := row["slim_removed_at"]; !ok {
			row["slim_removed_at"] = nil
		}
		if _, ok := row["slim_package_name"]; !ok {
			row["slim_package_name"] = ""
		}
		export.Attachments[i] = row
	}
}

func normalizeImportMap(value map[string]any) any {
	if value == nil {
		return map[string]any(nil)
	}
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = normalizeImportValue(item)
	}
	return out
}

func normalizeImportValue(value any) any {
	switch value := value.(type) {
	case json.Number:
		if parsed, err := strconv.ParseInt(value.String(), 10, 64); err == nil {
			return parsed
		}
		if parsed, err := value.Float64(); err == nil {
			return parsed
		}
		return value.String()
	case map[string]any:
		return normalizeImportMap(value)
	case []any:
		out := make([]any, len(value))
		for i, item := range value {
			out[i] = normalizeImportValue(item)
		}
		return out
	default:
		return value
	}
}

func importCandidate(meta manifestTicket, export ticketExport) ImportTicket {
	return ImportTicket{Seq: importInt(export.Ticket, "seq"), Number: meta.Number,
		Title: meta.Title, ClosedAt: parseArchiveTime(meta.ClosedAt), AttachmentBytes: meta.AttachmentBytes}
}

func parseArchiveTime(value string) time.Time {
	parsed, _ := time.ParseInLocation(time.RFC3339Nano, value, clock.Beijing)
	return parsed
}

func (s *Service) canImportTicketTx(ctx context.Context, tx *sql.Tx, pkg *parsedImportPackage,
	meta manifestTicket, export ticketExport) (string, error) {
	var seq, tombstoneSeq int64
	var number, title, packageName string
	err := tx.QueryRowContext(ctx, `
		SELECT seq, number, title, package_name FROM archived_tickets WHERE number = ?`,
		meta.Number).Scan(&tombstoneSeq, &number, &title, &packageName)
	if errors.Is(err, sql.ErrNoRows) {
		var existing int64
		if err := tx.QueryRowContext(ctx, `SELECT seq FROM tickets WHERE number = ?`,
			meta.Number).Scan(&existing); err == nil {
			return "工单已在平台上，不能重复导入", nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("读取工单 %s 状态失败：%w", meta.Number, err)
		}
		return "工单当前不在墓碑状态，不能导入", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取墓碑 %s 失败：%w", meta.Number, err)
	}
	seq = importInt(export.Ticket, "seq")
	if tombstoneSeq != seq || packageName != pkg.Manifest.PackageName {
		return "归档包与墓碑不匹配", nil
	}
	reason, err := s.checkImportIDsTx(ctx, tx, export)
	if err != nil || reason != "" {
		return reason, err
	}
	reason, err = s.checkImportReferencesTx(ctx, tx, pkg, export)
	if err != nil || reason != "" {
		return reason, err
	}
	if reason := checkImportAttachments(pkg, meta, export); reason != "" {
		return reason, nil
	}
	return "", nil
}

func (s *Service) checkImportIDsTx(ctx context.Context, tx *sql.Tx, export ticketExport) (string, error) {
	type idRef struct {
		table  string
		id     int64
		reason string
	}
	refs := []idRef{}
	for _, row := range export.Attachments {
		refs = append(refs, idRef{"attachments", importInt(row, "id"), "附件 ID 已被平台使用"})
	}
	for _, row := range export.AccessRecords {
		refs = append(refs, idRef{"access_records", importInt(row, "id"), "访问记录 ID 已被平台使用"})
	}
	for _, row := range export.GuestLinks {
		refs = append(refs, idRef{"guest_links", importInt(row, "id"), "访客链接 ID 已被平台使用"})
	}
	// 访客本体与访客级凭证持有者不随工单删除：同一访客的其他工单仍在平台
	// 上时，恢复按原 id 复用已有行（导入时用 INSERT OR IGNORE）。
	for _, row := range export.GuestCredentialHolders {
		if stringValue(row["owner_type"]) == "guest" {
			continue
		}
		refs = append(refs, idRef{"guest_credential_holders", importInt(row, "id"), "访客凭证持有者 ID 已被平台使用"})
	}
	guestHolderIDs := guestHolderIDSet(export)
	for _, row := range export.GuestCredentialDevices {
		if guestHolderIDs[importInt(row, "credential_holder_id")] {
			continue
		}
		refs = append(refs, idRef{"guest_credential_devices", importInt(row, "id"), "访客设备凭证 ID 已被平台使用"})
	}
	for _, item := range export.Drafts {
		draft, _ := item["draft"].(map[string]any)
		refs = append(refs, idRef{"drafts", importInt(draft, "id"), "草稿 ID 已被平台使用"})
	}
	for _, ref := range refs {
		if ref.id == 0 {
			continue
		}
		var exists int64
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM `+ref.table+` WHERE id = ?`, ref.id).Scan(&exists)
		if err == nil {
			return ref.reason, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("检查恢复表 %s 的 ID 冲突失败：%w", ref.table, err)
		}
	}
	return "", nil
}

func (s *Service) checkImportReferencesTx(ctx context.Context, tx *sql.Tx,
	pkg *parsedImportPackage, export ticketExport) (string, error) {
	memberIDs := newIntSet()
	agentIDs := newIntSet()
	intakeIDs := newIntSet()
	linkIDs := newIntSet()
	guestIDs := newIntSet()
	ticketSeqs := newIntSet()
	for _, row := range []*map[string]any{&export.Ticket} {
		memberIDs.addRows(*row, "creator_member_id", "assignee_id")
		agentIDs.addRows(*row, "creator_agent_id")
		linkIDs.addRows(*row, "creator_link_id")
		intakeIDs.addRows(*row, "creator_intake_id")
	}
	for _, row := range export.BriefVersions {
		memberIDs.addRows(row, "actor_member_id")
		agentIDs.addRows(row, "actor_agent_id")
		linkIDs.addRows(row, "actor_link_id")
	}
	for _, row := range export.Timeline {
		memberIDs.addRows(row, "actor_member_id", "collaborator_member_id")
		agentIDs.addRows(row, "actor_agent_id")
		linkIDs.addRows(row, "actor_link_id")
	}
	for _, row := range export.Attachments {
		memberIDs.addRows(row, "uploader_member_id")
		agentIDs.addRows(row, "uploader_agent_id")
		linkIDs.addRows(row, "uploader_link_id")
		intakeIDs.addRows(row, "uploader_intake_id")
	}
	for _, row := range export.AccessRecords {
		memberIDs.addRows(row, "actor_member_id")
		agentIDs.addRows(row, "actor_agent_id")
		linkIDs.addRows(row, "actor_link_id")
	}
	for _, row := range export.Collaborators {
		memberIDs.addRows(row, "member_id")
	}
	for _, row := range export.TicketReads {
		memberIDs.addRows(row, "member_id")
	}
	for _, row := range export.GuestLinks {
		guestIDs.add(row["guest_id"])
	}
	for _, row := range export.GuestLinks {
		memberIDs.addRows(row, "created_by_member_id")
		agentIDs.addRows(row, "created_by_agent_id")
		linkIDs.add(row["id"])
	}
	for _, item := range export.Drafts {
		draft, _ := item["draft"].(map[string]any)
		memberIDs.addRows(draft, "member_id")
		for _, version := range versionsOf(item) {
			memberIDs.addRows(version, "author_member_id")
			agentIDs.addRows(version, "author_agent_id")
		}
	}
	for _, row := range export.Timeline {
		if value := importInt(row, "duplicate_of"); value != 0 {
			ticketSeqs.add(value)
		}
	}
	if value := importInt(export.Ticket, "duplicate_of_seq"); value != 0 {
		ticketSeqs.add(value)
	}

	reason, err := missingTx(ctx, tx, "members", "id", memberIDs.values(), "成员")
	if err != nil || reason != "" {
		return reason, err
	}
	reason, err = missingTx(ctx, tx, "agent_tokens", "id", agentIDs.values(), "成员 Agent")
	if err != nil || reason != "" {
		return reason, err
	}
	reason, err = missingTx(ctx, tx, "intakes", "id", intakeIDs.values(), "反馈入口")
	if err != nil || reason != "" {
		return reason, err
	}
	for _, id := range linkIDs.values() {
		if pkgHasLink(export, id) {
			continue
		}
		var exists int64
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM guest_links WHERE id = ?`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return "引用的访客链接缺失", nil
		}
		if err != nil {
			return "", fmt.Errorf("检查访客链接引用失败：%w", err)
		}
	}
	for _, id := range guestIDs.values() {
		if pkgHasGuest(export, id) {
			continue
		}
		var exists int64
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM guests WHERE id = ?`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return "引用的访客缺失", nil
		}
		if err != nil {
			return "", fmt.Errorf("检查访客引用失败：%w", err)
		}
	}
	for _, seq := range ticketSeqs.values() {
		if pkgHasTicket(pkg, seq) {
			continue
		}
		var exists int64
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM tickets WHERE seq = ?`, seq).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return "引用的工单缺失", nil
		}
		if err != nil {
			return "", fmt.Errorf("检查工单引用失败：%w", err)
		}
	}
	for _, row := range export.Labels {
		label, _ := row["label"].(string)
		var exists int64
		err := tx.QueryRowContext(ctx, `
			SELECT 1 WHERE EXISTS (SELECT 1 FROM label_values WHERE dimension || ':' || value = ?)
			   OR EXISTS (SELECT 1 FROM plain_labels WHERE label = ?)`, label, label).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return "引用的标签缺失：" + label, nil
		}
		if err != nil {
			return "", fmt.Errorf("检查标签引用失败：%w", err)
		}
	}
	if key, _ := export.Ticket["template_key"].(string); key != "" {
		var exists int64
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM templates WHERE key = ?`, key).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return "引用的模板缺失：" + key, nil
		}
		if err != nil {
			return "", fmt.Errorf("检查模板引用失败：%w", err)
		}
	}
	return "", nil
}

func checkImportAttachments(pkg *parsedImportPackage, meta manifestTicket, export ticketExport) string {
	for _, row := range export.Attachments {
		if importAttachmentSlimmed(row) {
			// 已瘦身的附件文件不在服务器上，也不在归档包里；要还原它需要瘦身包。
			continue
		}
		id := importInt(row, "id")
		prefix := archiveAttachmentPathPrefix(meta.Number, id)
		found := []string{}
		for _, file := range pkg.Manifest.Files {
			if strings.HasPrefix(file.Path, prefix) {
				found = append(found, file.Path)
			}
		}
		if len(found) != 1 {
			return "归档包里附件文件缺失或不唯一：" + strconv.FormatInt(id, 10)
		}
	}
	return ""
}

func importAttachmentSlimmed(row map[string]any) bool {
	return row["slim_removed_at"] != nil || stringValue(row["slim_package_name"]) != ""
}

type intSet struct{ items map[int64]bool }

func newIntSet() *intSet { return &intSet{items: map[int64]bool{}} }

func (s *intSet) add(value any) {
	switch value := value.(type) {
	case int64:
		if value != 0 {
			s.items[value] = true
		}
	}
}

func (s *intSet) addRows(row map[string]any, keys ...string) {
	if row == nil {
		return
	}
	for _, key := range keys {
		s.add(row[key])
	}
}

func (s *intSet) values() []int64 {
	out := make([]int64, 0, len(s.items))
	for value := range s.items {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func versionsOf(item map[string]any) []map[string]any {
	raw, _ := item["versions"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, value := range raw {
		if row, ok := value.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func pkgHasLink(export ticketExport, id int64) bool {
	for _, row := range export.GuestLinks {
		if importInt(row, "id") == id {
			return true
		}
	}
	return false
}

func pkgHasGuest(export ticketExport, id int64) bool {
	for _, row := range export.Guests {
		if importInt(row, "id") == id {
			return true
		}
	}
	return false
}

func pkgHasTicket(pkg *parsedImportPackage, seq int64) bool {
	for _, export := range pkg.Tickets {
		if importInt(export.Ticket, "seq") == seq {
			return true
		}
	}
	return false
}

func missingTx(ctx context.Context, tx *sql.Tx, table, column string, ids []int64, noun string) (string, error) {
	for _, id := range ids {
		var exists int64
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM `+table+` WHERE `+column+` = ?`, id).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return "引用的" + noun + "缺失", nil
		}
		if err != nil {
			return "", fmt.Errorf("检查%s引用失败：%w", noun, err)
		}
	}
	return "", nil
}

var importTableColumns = []struct {
	name    string
	columns []string
}{
	{"tickets", []string{"seq", "number", "kind", "title", "status", "close_reason",
		"brief_md", "brief_revision", "creator_member_id", "creator_agent_id", "assignee_id",
		"template_key", "created_at", "last_change", "duplicate_of_seq", "template_version",
		"template_snapshot", "fields_json", "creator_link_id", "creator_intake_id",
		"creator_author_name", "brief_attachment_ids"}},
	{"brief_versions", []string{"ticket_seq", "revision", "title", "brief_md", "fields_json",
		"attachment_ids", "actor_member_id", "actor_agent_id", "actor_link_id", "actor_link_note",
		"actor_guest_name", "actor_author_name", "actor_via_agent", "at"}},
	{"timeline_events", []string{"ticket_seq", "seq", "change_seq", "type", "actor_member_id",
		"actor_agent_id", "at", "body_md", "reply_no", "deliver", "revision", "reason",
		"duplicate_of", "label", "actor_link_id", "actor_link_note", "actor_guest_name",
		"actor_author_name", "actor_via_agent", "note", "link_expires_at", "assign_from", "assign_to",
		"attachment_ids", "collaborator_member_id", "collaborator_name", "actor_system"}},
	{"attachments", []string{"id", "ticket_seq", "filename", "size", "state", "attached_to",
		"uploader_member_id", "uploader_agent_id", "uploader_link_id", "uploader_intake_id",
		"intake_upload_key", "uploaded_at", "claimed_at", "draft_id",
		"slim_removed_at", "slim_package_name"}},
	{"drafts", []string{"id", "kind", "status", "ticket_seq", "member_id", "request_json",
		"version", "base_timeline_seq", "member_edited", "created_at", "updated_at", "sent_at",
		"sent_timeline_seq"}},
	{"draft_versions", []string{"draft_id", "version", "request_json", "author_member_id",
		"author_agent_id", "at"}},
	{"access_records", []string{"id", "ticket_seq", "actor_kind", "actor_name", "actor_member_id",
		"actor_agent_id", "actor_link_id", "actor_link_note", "entry_method", "action",
		"attachment_id", "attachment_ref", "attachment_filename", "first_at", "last_at", "count"}},
	{"ticket_labels", []string{"ticket_seq", "label"}},
	{"ticket_collaborators", []string{"ticket_seq", "member_id", "added_at"}},
	{"guests", []string{"id", "name", "created_by_member_id", "created_by_agent_id",
		"created_at", "disabled_at", "home_key_hash", "home_key_ciphertext"}},
	{"guest_credential_holders", []string{"id", "owner_type", "owner_id", "claimed_at",
		"claim_name", "access_code_hash", "access_code_failures", "access_code_locked_at", "created_at"}},
	{"guest_links", []string{"id", "ticket_seq", "key_hash", "note", "expires_at", "revoked_at",
		"can_edit_brief", "created_by_member_id", "created_by_agent_id", "created_at",
		"guest_id", "credential_holder_id", "key_ciphertext"}},
	{"guest_credential_devices", []string{"id", "credential_holder_id", "token_hash",
		"created_at", "entry_method"}},
	{"ticket_reads", []string{"member_id", "ticket_seq", "last_read_seq"}},
}

func insertImportTicketTx(ctx context.Context, tx *sql.Tx, export ticketExport) error {
	if len(export.GuestLinks) > 0 && len(export.GuestCredentialHolders) == 0 {
		if err := insertLegacyGuestCredentialsTx(ctx, tx, export); err != nil {
			return err
		}
	}
	draftRows, draftVersionRows := importDraftRows(export)
	for _, table := range importTableColumns {
		rows, err := importRowsForTable(export, table.name, draftRows, draftVersionRows)
		if err != nil {
			return err
		}
		// 访客与访客级持有者跨工单共享：已存在（含本次导入早前工单写入）时复用。
		ignoreConflict := table.name == "guests" ||
			(table.name == "guest_credential_holders" && onlyGuestHolders(rows)) ||
			(table.name == "guest_credential_devices" && onlyGuestHolderDevices(export, rows))
		if err := insertRowsTx(ctx, tx, table.name, table.columns, rows, ignoreConflict); err != nil {
			return err
		}
	}
	return nil
}

func guestHolderIDSet(export ticketExport) map[int64]bool {
	out := map[int64]bool{}
	for _, row := range export.GuestCredentialHolders {
		if stringValue(row["owner_type"]) == "guest" {
			out[importInt(row, "id")] = true
		}
	}
	return out
}

func onlyGuestHolderDevices(export ticketExport, rows []map[string]any) bool {
	guestHolders := guestHolderIDSet(export)
	for _, row := range rows {
		if !guestHolders[importInt(row, "credential_holder_id")] {
			return false
		}
	}
	return true
}

func onlyGuestHolders(rows []map[string]any) bool {
	for _, row := range rows {
		if stringValue(row["owner_type"]) != "guest" {
			return false
		}
	}
	return true
}

func insertLegacyGuestCredentialsTx(ctx context.Context, tx *sql.Tx, export ticketExport) error {
	for _, link := range export.GuestLinks {
		var holderID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO guest_credential_holders
				(owner_type, owner_id, claimed_at, claim_name, access_code_hash,
				 access_code_failures, access_code_locked_at, created_at)
			VALUES ('guest_link', ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
			link["id"], link["claimed_at"], link["claim_name"], link["access_code_hash"],
			link["access_code_failures"], link["access_code_locked_at"], link["created_at"]).
			Scan(&holderID); err != nil {
			return fmt.Errorf("恢复旧格式的访客凭证持有者：%w", err)
		}
		link["credential_holder_id"] = holderID
		for _, device := range export.GuestDevices {
			if importInt(device, "guest_link_id") != importInt(link, "id") {
				continue
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO guest_credential_devices
					(credential_holder_id, token_hash, entry_method, created_at)
				VALUES (?, ?, ?, ?)`,
				holderID, device["token_hash"], device["entry_method"], device["created_at"]); err != nil {
				return fmt.Errorf("恢复旧格式的访客设备凭证：%w", err)
			}
		}
	}
	return nil
}

func importDraftRows(export ticketExport) ([]map[string]any, []map[string]any) {
	draftRows := make([]map[string]any, 0, len(export.Drafts))
	draftVersionRows := make([]map[string]any, 0, len(export.Drafts))
	for _, item := range export.Drafts {
		draft, _ := item["draft"].(map[string]any)
		draftRows = append(draftRows, draft)
		for _, version := range versionsOf(item) {
			draftVersionRows = append(draftVersionRows, version)
		}
	}
	return draftRows, draftVersionRows
}

func importRowsForTable(export ticketExport, table string, draftRows, draftVersionRows []map[string]any) (
	[]map[string]any, error) {
	switch table {
	case "tickets":
		return []map[string]any{export.Ticket}, nil
	case "brief_versions":
		return export.BriefVersions, nil
	case "timeline_events":
		return export.Timeline, nil
	case "attachments":
		return export.Attachments, nil
	case "drafts":
		return draftRows, nil
	case "draft_versions":
		return draftVersionRows, nil
	case "access_records":
		return export.AccessRecords, nil
	case "ticket_labels":
		return export.Labels, nil
	case "ticket_collaborators":
		return export.Collaborators, nil
	case "guests":
		return export.Guests, nil
	case "guest_credential_holders":
		return export.GuestCredentialHolders, nil
	case "guest_links":
		return export.GuestLinks, nil
	case "guest_credential_devices":
		return export.GuestCredentialDevices, nil
	case "ticket_reads":
		return export.TicketReads, nil
	default:
		return nil, fmt.Errorf("未知恢复表 %s", table)
	}
}

func insertRowsTx(ctx context.Context, tx *sql.Tx, table string, columns []string, rows []map[string]any, ignoreConflict bool) error {
	for _, row := range rows {
		args := make([]any, 0, len(columns))
		for _, column := range columns {
			value, ok := row[column]
			if !ok {
				return fmt.Errorf("缺少恢复列 %s.%s", table, column)
			}
			args = append(args, value)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
		verb := "INSERT INTO "
		if ignoreConflict {
			verb = "INSERT OR IGNORE INTO "
		}
		if _, err := tx.ExecContext(ctx, verb+table+` (`+strings.Join(columns, ",")+
			`) VALUES (`+placeholders+`)`, args...); err != nil {
			return fmt.Errorf("写入 %s：%w", table, err)
		}
	}
	return nil
}

func (s *Service) writeImportAttachments(ctx context.Context, pkg *parsedImportPackage, meta manifestTicket,
	export ticketExport) ([]int64, error) {
	written := []int64{}
	for _, row := range export.Attachments {
		if importAttachmentSlimmed(row) {
			continue
		}
		id := importInt(row, "id")
		filename, _ := row["filename"].(string)
		prefix := archiveAttachmentPathPrefix(meta.Number, id)
		var packagePath string
		for _, file := range pkg.Manifest.Files {
			if strings.HasPrefix(file.Path, prefix) {
				packagePath = file.Path
				break
			}
		}
		if packagePath == "" {
			return written, fmt.Errorf("归档包里缺少附件 %d", id)
		}
		size := importInt(row, "size")
		if err := s.streamImportAttachment(ctx, pkg.Files[packagePath], id, filename, size); err != nil {
			return written, err
		}
		written = append(written, id)
	}
	return written, nil
}

func (s *Service) streamImportAttachment(ctx context.Context, entry *zip.File, id int64, filename string, size int64) error {
	rc, err := entry.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	meta := attachments.PutMeta{ContentType: attachments.ContentTypeOf(filename)}
	if err := s.atts.Storage().Put(ctx, id, rc, size, meta); err != nil {
		return fmt.Errorf("附件 %s 写回失败：%w", entry.Name, err)
	}
	return nil
}

func (s *Service) removeImportFiles(ctx context.Context, ids []int64) {
	objects := s.atts.Storage()
	ctx = context.WithoutCancel(ctx)
	for _, id := range ids {
		_ = objects.Delete(ctx, id)
	}
}

func deleteTombstoneTx(ctx context.Context, tx *sql.Tx, seq int64) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM archived_tickets WHERE seq = ?`, seq)
	return err
}

func importInt(row map[string]any, key string) int64 {
	switch value := row[key].(type) {
	case int64:
		return value
	case json.Number:
		parsed, _ := strconv.ParseInt(value.String(), 10, 64)
		return parsed
	default:
		return 0
	}
}
