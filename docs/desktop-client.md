# Windows 桌面客户端

桌面客户端是单个 Windows exe，负责连接账号、订阅通知、弹出 Toast、维护托盘未读状态和标记已读。安装、卸载、开机自启和自动更新由后续工单实现。

## 构建

```powershell
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build `
  -ldflags "-H=windowsgui -X github.com/allanpk716/agent-taskboard/cmd/taskboard-client.DefaultServerURL=https://taskboard.example.com -X github.com/allanpk716/agent-taskboard/internal/version.Client=0.2.0" `
  -o bin/taskboard-client.exe ./cmd/taskboard-client
```

`DefaultServerURL` 是构建时注入的默认平台地址。运行时可用参数覆盖：

```powershell
.\taskboard-client.exe -server https://taskboard.example.com
```

也可设置 `TASKBOARD_SERVER_URL`。两者同时存在时，环境变量覆盖参数注入的默认值，命令行参数显式传入时优先生效。查看版本执行 `.\taskboard-client.exe -version`。

## 数据与系统能力

- 客户端凭证保存在当前用户的 Windows 凭据管理器，目标名为 `Taskboard Desktop Client`。
- Toast 的应用标识固定为 `com.urithub.TaskboardClient`，激活 GUID 注册在 HKCU。
- 同一会话用命名 mutex `Local\TaskboardDesktopClient` 保证单实例。
- 托盘图标区分正常、未读、需要重新登录和连接中断；菜单列出最近 10 条未读，并提供全部已读、查看全部、重新登录、退出登录和退出。
