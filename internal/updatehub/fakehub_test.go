package updatehub

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// fakeHub 按 UpdateHub（allanpk716/update-hub 的 handler/upload.go 与
// middleware.BearerAuth）的行为校验上传请求：Bearer token、appId/channel
// 命名、multipart 解析、feed 的 PackageId 与 appId 一致（不区分大小写）、
// 从 .nupkg 文件名提取版本，并按 releases.{os}.json 提供查询。
// 它是测试里 UpdateHub 的替身，字段名与断言都对着那份源码写。

var fakeHubNamePattern = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

type fakeHub struct {
	token   string
	baseURL string
	mu      sync.Mutex
	files   map[string][]byte // "appId/channel/name" -> content
	feeds   map[string][]byte // "appId/channel/releases.win.json" -> content
	notes   []string
	auths   []string // 收到的 Authorization 头
	uploads int
}

func newFakeHub(token string) *fakeHub {
	return &fakeHub{
		token: token,
		files: map[string][]byte{},
		feeds: map[string][]byte{},
	}
}

func (h *fakeHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/apps/"):
		h.serveUpload(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, ".json") &&
		strings.Contains(r.URL.Path, "/releases."):
		h.serveFeed(w, r)
	default:
		http.NotFound(w, r)
	}
}

// serveFeed 对应 UpdateHub 的 StaticHandler：GET /{appId}/{channel}/releases.{os}.json，不需要认证。
func (h *fakeHub) serveFeed(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/"), "/", 3)
	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}
	h.mu.Lock()
	feed := h.feeds[parts[0]+"/"+parts[1]+"/"+parts[2]]
	h.mu.Unlock()
	if feed == nil {
		feed = []byte(`{"Assets":[]}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(feed)
}

// serveUpload 对应 handler/upload.go 的 UploadHandler + BearerAuth 中间件。
func (h *fakeHub) serveUpload(w http.ResponseWriter, r *http.Request) {
	// BearerAuth：POST /api/* 必须带有效的 Bearer token。
	auth := r.Header.Get("Authorization")
	h.mu.Lock()
	h.auths = append(h.auths, auth)
	h.mu.Unlock()
	if auth != "Bearer "+h.token {
		writeJSONError(w, http.StatusUnauthorized, "invalid token")
		return
	}

	rest := strings.TrimPrefix(r.URL.Path, "/api/apps/")
	parts := strings.SplitN(rest, "/channels/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeJSONError(w, http.StatusBadRequest, "missing appId or channel in path")
		return
	}
	appID, channel := parts[0], strings.TrimSuffix(parts[1], "/releases")
	if !fakeHubNamePattern.MatchString(appID) {
		writeJSONError(w, http.StatusBadRequest, "invalid appId: use alphanumeric and hyphen only")
		return
	}
	if !fakeHubNamePattern.MatchString(channel) {
		writeJSONError(w, http.StatusBadRequest, "invalid channel name: use alphanumeric and hyphen only")
		return
	}
	if err := r.ParseMultipartForm(200 << 20); err != nil {
		writeJSONError(w, http.StatusBadRequest, "multipart parse error: "+err.Error())
		return
	}

	versionsAdded := map[string]bool{}
	filesReceived := 0
	for _, formFiles := range r.MultipartForm.File {
		for _, fh := range formFiles {
			file, err := fh.Open()
			if err != nil {
				continue
			}
			data, err := io.ReadAll(file)
			file.Close()
			if err != nil {
				continue
			}
			if isFeedFilename(fh.Filename) {
				added, err := h.mergeReleaseFeed(appID, channel, fh.Filename, data)
				if err != nil {
					writeJSONError(w, http.StatusInternalServerError, "merge releases: "+err.Error())
					return
				}
				for _, v := range added {
					versionsAdded[v] = true
				}
			} else {
				h.mu.Lock()
				h.files[appID+"/"+channel+"/"+fh.Filename] = data
				h.mu.Unlock()
				if ver := extractVersionFromNupkg(fh.Filename); ver != "" {
					versionsAdded[ver] = true
				}
			}
			filesReceived++
		}
	}
	if vals := r.MultipartForm.Value["notes"]; len(vals) > 0 {
		h.mu.Lock()
		h.notes = append(h.notes, vals[0])
		h.mu.Unlock()
	}
	h.mu.Lock()
	h.uploads++
	h.mu.Unlock()

	versionList := make([]string, 0, len(versionsAdded))
	for v := range versionsAdded {
		versionList = append(versionList, v)
	}
	sort.Strings(versionList)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"channel":        channel,
		"files_received": filesReceived,
		"versions_added": versionList,
	})
}

// mergeReleaseFeed 对应 upload.go：校验 PackageId 与 appId 一致（不区分大小写），整份替换 feed。
func (h *fakeHub) mergeReleaseFeed(appID, channel, filename string, uploadedData []byte) ([]string, error) {
	var uploaded release.ReleaseFeed
	if err := json.Unmarshal(uploadedData, &uploaded); err != nil {
		return nil, err
	}
	if len(uploaded.Assets) > 0 {
		packageId := ""
		for _, a := range uploaded.Assets {
			if a.PackageId != "" {
				packageId = a.PackageId
				break
			}
		}
		if packageId != "" && !strings.EqualFold(packageId, appID) {
			return nil, errPackageIDMismatch("feed has " + packageId + ", URL has " + appID)
		}
	}
	var added []string
	for _, a := range uploaded.Assets {
		if a.Version != "" {
			added = append(added, a.Version)
		}
	}
	h.mu.Lock()
	h.feeds[appID+"/"+channel+"/"+filename] = uploadedData
	h.mu.Unlock()
	return added, nil
}

type errPackageIDMismatch string

func (e errPackageIDMismatch) Error() string { return string(e) }

// file 返回 UpdateHub 侧已入库的文件内容。
func (h *fakeHub) file(appID, channel, name string) []byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.files[appID+"/"+channel+"/"+name]
}

// isFeedFilename 对应 model.IsFeedFilename。
func isFeedFilename(filename string) bool {
	ok, _ := regexp.MatchString(`^releases\.[a-zA-Z0-9_]+\.json$`, filename)
	return ok
}

// extractVersionFromNupkg 对应 upload.go：PackageId-1.2.3-full.nupkg 的倒数第二段是版本。
func extractVersionFromNupkg(filename string) string {
	if !strings.HasSuffix(filename, ".nupkg") {
		return ""
	}
	base := filename[:len(filename)-len(".nupkg")]
	parts := strings.Split(base, "-")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": message})
}
