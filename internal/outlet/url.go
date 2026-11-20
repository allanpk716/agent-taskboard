package outlet

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrInvalidBase 表示部署级出口白名单地址不安全。
var ErrInvalidBase = errors.New("webhook allowed_bases 中的地址无效")

const webhookPathTemplate = "/api/v1/bots/%s/webhook"

// URLBuilder 只用部署白名单 base 和已校验 bot_id 拼出口地址。
type URLBuilder struct {
	bases []*url.URL
}

// NewURLBuilder 校验并规范化出口白名单。多基地时使用第一个作为当前目标基地。
func NewURLBuilder(allowedBases []string) (*URLBuilder, error) {
	if len(allowedBases) == 0 {
		return nil, fmt.Errorf("%w：至少需要一个 https 根地址", ErrInvalidBase)
	}
	bases := make([]*url.URL, 0, len(allowedBases))
	for _, raw := range allowedBases {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" ||
			parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
			parsed.RawFragment != "" || !safeBasePath(parsed.Path) {
			return nil, fmt.Errorf("%w：%s", ErrInvalidBase, raw)
		}
		if parsed.Path == "" || parsed.Path == "/" {
			parsed.Path = ""
		} else if !strings.HasSuffix(parsed.Path, "/") {
			parsed.Path += "/"
		}
		bases = append(bases, parsed)
	}
	return &URLBuilder{bases: bases}, nil
}

// Build 拼接固定 webhook 路径。bot_id 只允许小写字母和数字，不能影响 URL 结构。
func (b *URLBuilder) Build(botID string) (string, error) {
	if !botIDPattern.MatchString(botID) {
		return "", ErrInvalidBotID
	}
	endpoint := *b.bases[0]
	endpoint.Path += strings.TrimPrefix(fmt.Sprintf(webhookPathTemplate, botID), "/")
	raw := endpoint.String()
	built, err := url.Parse(raw)
	if err != nil || !insideBase(built, b.bases[0]) {
		return "", fmt.Errorf("%w：拼接结果越出了白名单 base", ErrInvalidBase)
	}
	return raw, nil
}

func safeBasePath(path string) bool {
	if path == "" || path == "/" {
		return true
	}
	if !strings.HasPrefix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(strings.Trim(path, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." || strings.ContainsAny(segment, "?#") {
			return false
		}
	}
	return true
}

func insideBase(candidate, base *url.URL) bool {
	return candidate.Scheme == base.Scheme && candidate.Host == base.Host &&
		strings.HasPrefix(candidate.EscapedPath(), base.EscapedPath())
}
