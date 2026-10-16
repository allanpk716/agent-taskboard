package updatehub

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"net/http/httptest"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/release"
)

// publish-updatehub 通过 POST /api/apps/{appId}/channels/{channel}/releases
// 上传 nupkg 和 releases.win.json。这里的 fakeHub 复刻 UpdateHub 上传接口
// 的校验逻辑，保证我们发出的请求正是它接受的那种。

func startHub(t *testing.T, token string) *fakeHub {
	t.Helper()
	hub := newFakeHub(token)
	srv := httptest.NewServer(hub)
	t.Cleanup(srv.Close)
	hub.baseURL = srv.URL
	return hub
}

func TestUpload把nupkg和feed登记到应用和通道(t *testing.T) {
	hub := startHub(t, "secret-token")
	c := New(hub.baseURL, "secret-token")

	appID, version := "agent-taskboard-server", "1.2.3"
	nupkg := []byte("a zip pretending to be a nupkg")
	feed, err := release.ReleaseFeedJSON(appID, version, nupkg)
	if err != nil {
		t.Fatal(err)
	}

	res, err := c.Upload(context.Background(), appID, "stable", []UploadFile{
		{Name: "releases.win.json", Content: feed},
		{Name: release.NupkgFileName(appID, version), Content: nupkg},
	}, "server 1.2.3")
	if err != nil {
		t.Fatalf("Upload：%v", err)
	}
	if res.Channel != "stable" || res.FilesReceived != 2 {
		t.Fatalf("上传结果不对：%+v", res)
	}
	if len(res.VersionsAdded) != 1 || res.VersionsAdded[0] != "1.2.3" {
		t.Fatalf("应登记版本 1.2.3：%+v", res.VersionsAdded)
	}

	// UpdateHub 侧：nupkg 按原名入库，notes 记录在案。
	stored := hub.file(appID, "stable", release.NupkgFileName(appID, version))
	if string(stored) != string(nupkg) {
		t.Fatalf("nupkg 入库内容不对：%q", stored)
	}
	if len(hub.notes) != 1 || hub.notes[0] != "server 1.2.3" {
		t.Fatalf("notes 未上传：%v", hub.notes)
	}

	// 上传后用公开的 feed 地址能查到刚登记的版本（PackageId 与 appId 一致）。
	gotFeed, err := c.FetchFeed(context.Background(), appID, "stable", "win")
	if err != nil {
		t.Fatalf("FetchFeed：%v", err)
	}
	if len(gotFeed.Assets) != 1 {
		t.Fatalf("feed 应有一条：%+v", gotFeed.Assets)
	}
	a := gotFeed.Assets[0]
	sum := sha1.Sum(nupkg)
	if a.PackageId != appID || a.Version != version {
		t.Fatalf("feed 条目不对：%+v", a)
	}
	if a.SHA1 != hex.EncodeToString(sum[:]) || a.Size != int64(len(nupkg)) {
		t.Fatalf("feed 的 SHA1/Size 与 nupkg 不符：%+v", a)
	}
}

func TestUpload带Bearer认证(t *testing.T) {
	hub := startHub(t, "secret-token")
	c := New(hub.baseURL, "secret-token")
	feed, err := release.ReleaseFeedJSON("agent-taskboard-web", "1.4.0", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(context.Background(), "agent-taskboard-web", "stable",
		[]UploadFile{{Name: "releases.win.json", Content: feed}}, ""); err != nil {
		t.Fatalf("Upload：%v", err)
	}
	if len(hub.auths) != 1 || hub.auths[0] != "Bearer secret-token" {
		t.Fatalf("应发送 Bearer token：%v", hub.auths)
	}
}

func TestUpload没有token时被拒绝(t *testing.T) {
	hub := startHub(t, "secret-token")
	c := New(hub.baseURL, "") // 忘了配 UPDATEHUB_TOKEN
	if _, err := c.Upload(context.Background(), "agent-taskboard-web", "stable",
		[]UploadFile{{Name: "releases.win.json", Content: []byte(`{"Assets":[]}`)}}, ""); err == nil {
		t.Fatal("缺少 token 应该失败")
	}
}

func TestUpload在feed的PackageId与appId不一致时被拒绝(t *testing.T) {
	hub := startHub(t, "secret-token")
	c := New(hub.baseURL, "secret-token")
	// feed 写的是别的应用名：UpdateHub 会拒绝登记。
	feed, err := release.ReleaseFeedJSON("some-other-app", "1.0.0", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Upload(context.Background(), "agent-taskboard-web", "stable",
		[]UploadFile{{Name: "releases.win.json", Content: feed}}, ""); err == nil {
		t.Fatal("PackageId 与 appId 不一致时应失败")
	}
	if hub.uploads != 0 || len(hub.files) != 0 {
		t.Fatal("被拒绝的请求不应登记任何文件")
	}
}
