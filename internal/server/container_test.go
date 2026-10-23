// 容器模式（#231）：平台升级由替换镜像完成，管理页不再触发现地更新；
// 镜像自带的桌面客户端安装包在启动时装入数据目录。
package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/clientstamp"
	"github.com/allanpk716/agent-taskboard/internal/release"
	"github.com/allanpk716/agent-taskboard/internal/tbtest"
	"github.com/allanpk716/agent-taskboard/internal/updater"
)

func Test容器模式下更新接口提示升级镜像(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithContainer("https://taskboard.example.com", ""))
	s.CreateAdmin("admin", "pass-word")
	admin := s.Login("admin", "pass-word")

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		res := s.Do(t, admin, method, "/api/v1/admin/update", nil, nil)
		if res.StatusCode != http.StatusConflict {
			t.Fatalf("%s 状态码 = %d, 想 409", method, res.StatusCode)
		}
		var got errorBody
		tbtest.DecodeJSON(t, res, &got)
		if got.Error.Code != "container_update_unsupported" {
			t.Fatalf("%s 错误码 = %q, 想 container_update_unsupported", method, got.Error.Code)
		}
		if got.Error.Message != "容器部署，请升级镜像" {
			t.Fatalf("%s 提示 = %q, 想 容器部署，请升级镜像", method, got.Error.Message)
		}
	}
}

func Test容器模式缺public_url时启动失败(t *testing.T) {
	var err error
	tbtest.Start(t, tbtest.WithContainer("", ""), tbtest.WithStartupError(&err))
	if err == nil {
		t.Fatal("容器模式缺少 public_url 时不应启动成功")
	}
	if want := "容器模式必须配置 [server] public_url"; !strings.Contains(err.Error(), want) {
		t.Fatalf("启动错误 = %q, 应包含 %q", err.Error(), want)
	}
}

func writeImageClient(t *testing.T, dir, version string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := release.Manifest{
		Component: "client",
		Version:   version,
		Files:     fileEntriesShared(map[string][]byte{"taskboard-client.exe": data}),
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "taskboard-client.exe"), data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func Test容器模式启动时装入镜像里更新的客户端(t *testing.T) {
	imageDir := t.TempDir()
	data := append(testClientExe(t), []byte("image 1.3.0")...)
	writeImageClient(t, imageDir, "1.3.0", data)
	s := tbtest.Start(t, tbtest.WithContainer("https://taskboard.example.com", imageDir))
	s.CreateMember("wang", "pass-word")
	web := s.Login("wang", "pass-word")

	var latest struct {
		Version string `json:"version"`
	}
	res := s.Do(t, web, http.MethodGet, "/api/v1/client/latest", nil, &latest)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("latest 状态码 = %d", res.StatusCode)
	}
	if latest.Version != "1.3.0" {
		t.Fatalf("latest 版本 = %q, 想 1.3.0", latest.Version)
	}
	res = s.Do(t, web, http.MethodGet, "/api/v1/client/download", nil, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("download 状态码 = %d", res.StatusCode)
	}
	got, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	stamped, err := clientstamp.Append(data, "https://taskboard.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(stamped) {
		t.Fatal("下载内容应是镜像里的客户端安装包（带对外地址尾部戳）")
	}
}

func Test容器模式镜像客户端不新时不替换已安装版本(t *testing.T) {
	data := append(testClientExe(t), []byte("installed 1.3.0")...)
	dataDir := t.TempDir()
	if err := updater.InstallClient(dataDir, data, updater.ClientPackage{
		Version: "1.3.0", Sha256: sha256Sum(data), Size: int64(len(data)),
	}); err != nil {
		t.Fatal(err)
	}
	imageDir := t.TempDir()
	writeImageClient(t, imageDir, "1.2.0", append(testClientExe(t), []byte("image 1.2.0")...))
	s := tbtest.Start(t, tbtest.WithDataDir(dataDir),
		tbtest.WithContainer("https://taskboard.example.com", imageDir))
	s.CreateMember("wang", "pass-word")
	web := s.Login("wang", "pass-word")

	var latest struct {
		Version string `json:"version"`
	}
	res := s.Do(t, web, http.MethodGet, "/api/v1/client/latest", nil, &latest)
	if res.StatusCode != http.StatusOK || latest.Version != "1.3.0" {
		t.Fatalf("latest = %d/%+v, 想 200/1.3.0", res.StatusCode, latest)
	}
}
