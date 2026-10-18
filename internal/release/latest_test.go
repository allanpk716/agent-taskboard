package release

import (
	"encoding/json"
	"testing"
)

// channel-stable Release 里的 latest.json 记录前后端各自的最新版本。
// 前后端分开打 tag、分开发布，CI 每次「读改写」：只更新自己那一半，
// 另一个组件的条目必须原样保留，不能整份覆盖。

func TestMergeLatest分别发布前后端时互不覆盖(t *testing.T) {
	serverEntry := LatestEntry{
		Version:     "1.2.0",
		Tag:         "server/v1.2.0",
		ManifestURL: "https://github.com/acme/board/releases/download/server/v1.2.0/manifest.json",
	}
	webEntry := LatestEntry{
		Version:     "1.4.0",
		Tag:         "web/v1.4.0",
		ManifestURL: "https://github.com/acme/board/releases/download/web/v1.4.0/manifest.json",
	}

	// 第一次发布后端：从空的 latest.json 开始。
	data, err := MergeLatestJSON(nil, ComponentServer, serverEntry)
	if err != nil {
		t.Fatalf("合并 server：%v", err)
	}
	// 再发布前端：server 条目必须原样保留。
	data, err = MergeLatestJSON(data, ComponentWeb, webEntry)
	if err != nil {
		t.Fatalf("合并 web：%v", err)
	}
	var l Latest
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatalf("解析 latest.json：%v\n%s", err, data)
	}
	if l.Server == nil || *l.Server != serverEntry {
		t.Fatalf("server 条目被改动：%+v", l.Server)
	}
	if l.Web == nil || *l.Web != webEntry {
		t.Fatalf("web 条目不正确：%+v", l.Web)
	}

	// 又发布了新版后端：web 条目必须原样保留。
	newServer := serverEntry
	newServer.Version, newServer.Tag, newServer.ManifestURL =
		"1.3.0", "server/v1.3.0", "https://github.com/acme/board/releases/download/server/v1.3.0/manifest.json"
	data, err = MergeLatestJSON(data, ComponentServer, newServer)
	if err != nil {
		t.Fatalf("再次合并 server：%v", err)
	}
	l = Latest{}
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatalf("解析 latest.json：%v", err)
	}
	if l.Web == nil || *l.Web != webEntry {
		t.Fatalf("更新 server 时 web 被覆盖：%+v", l.Web)
	}
	if l.Server == nil || l.Server.Version != "1.3.0" {
		t.Fatalf("server 未更新：%+v", l.Server)
	}
}

func TestMergeLatest拒绝损坏的latestjson(t *testing.T) {
	bad := []byte(`{"server": `)
	if _, err := MergeLatestJSON(bad, ComponentServer, LatestEntry{}); err == nil {
		t.Fatal("损坏的 JSON 应报错，避免把另一组件的条目悄悄抹掉")
	}
}

func TestMergeLatest发布客户端时保留前后端(t *testing.T) {
	existing := []byte(`{
	  "server": {"version":"1.2.0","tag":"server/v1.2.0","manifest_url":"https://github.com/acme/board/releases/download/server/v1.2.0/manifest.json"},
	  "web": {"version":"1.4.0","tag":"web/v1.4.0","manifest_url":"https://github.com/acme/board/releases/download/web/v1.4.0/manifest.json"}
	}`)
	entry := LatestEntry{
		Version: "1.0.0", Tag: "client/v1.0.0",
		ManifestURL: "https://github.com/acme/board/releases/download/client/v1.0.0/manifest.json",
	}
	data, err := MergeLatestJSON(existing, ComponentClient, entry)
	if err != nil {
		t.Fatalf("合并 client：%v", err)
	}
	var l Latest
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatalf("解析 latest.json：%v\n%s", err, data)
	}
	if l.Client == nil || *l.Client != entry {
		t.Fatalf("client 条目不对：%+v", l.Client)
	}
	if l.Server == nil || l.Server.Version != "1.2.0" || l.Web == nil || l.Web.Version != "1.4.0" {
		t.Fatalf("server/web 条目应保留：%+v %+v", l.Server, l.Web)
	}
}

func TestMergeLatest拒绝未知组件(t *testing.T) {
	if _, err := MergeLatestJSON(nil, "cli", LatestEntry{}); err == nil {
		t.Fatal("只有 server 和 web 两个组件")
	}
}

func TestManifestURL使用固定下载地址(t *testing.T) {
	got := ManifestURL("acme/board", "server/v1.2.0")
	want := "https://github.com/acme/board/releases/download/server/v1.2.0/manifest.json"
	if got != want {
		t.Fatalf("期望 %s，得到 %s", want, got)
	}
}
