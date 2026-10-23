// 容器模式（#231）：平台升级由替换镜像完成，管理页不再触发现地更新。
package server_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/allanpk716/agent-taskboard/internal/tbtest"
)

func Test容器模式下更新接口提示升级镜像(t *testing.T) {
	s := tbtest.Start(t, tbtest.WithContainer("https://taskboard.example.com"))
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
	tbtest.Start(t, tbtest.WithContainer(""), tbtest.WithStartupError(&err))
	if err == nil {
		t.Fatal("容器模式缺少 public_url 时不应启动成功")
	}
	if want := "容器模式必须配置 [server] public_url"; !strings.Contains(err.Error(), want) {
		t.Fatalf("启动错误 = %q, 应包含 %q", err.Error(), want)
	}
}
