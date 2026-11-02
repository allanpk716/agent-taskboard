// Package packagefile 提供归档包与瘦身包共用的 zip 写入、路径安全化
// 和清单文件哈希逻辑。
package packagefile

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// ManifestFile 是机器可读清单里的一个文件条目。
type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

func AddFile(zw *zip.Writer, files *[]ManifestFile, path string, body []byte) error {
	if err := AddEntry(zw, path, body); err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	*files = append(*files, ManifestFile{Path: path, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(body))})
	return nil
}

func AddEntry(zw *zip.Writer, path string, body []byte) error {
	path, err := SafePath(path)
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

func AddStream(zw *zip.Writer, files *[]ManifestFile, path string, src io.Reader, size int64) error {
	path, err := SafePath(path)
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
		return fmt.Errorf("包内文件 %s 大小不一致：数据库 %d，实际 %d", path, size, written)
	}
	*files = append(*files, ManifestFile{Path: path, SHA256: hex.EncodeToString(sum.Sum(nil)), Size: written})
	return nil
}

func SafePath(path string) (string, error) {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		part = SanitizeSegment(part)
		if part == "" {
			return "", fmt.Errorf("包内路径 %q 包含空路径段", path)
		}
		parts[i] = part
	}
	return strings.Join(parts, "/"), nil
}

func SanitizeSegment(part string) string {
	if part == "" || part == "." || part == ".." || strings.ContainsAny(part, `\/`) ||
		strings.ContainsFunc(part, unicode.IsControl) {
		return "_"
	}
	return part
}
