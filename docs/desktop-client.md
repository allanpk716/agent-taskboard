# Windows 桌面客户端

桌面客户端是单个 Windows exe，负责连接账号、订阅通知、弹出 Toast、维护托盘未读状态和标记已读。双击下载的 exe 后自动安装到用户目录，全程不需要管理员权限。

## 构建

```powershell
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build `
  -ldflags "-H=windowsgui -X github.com/allanpk716/agent-taskboard/internal/version.Client=0.2.0" `
  -o bin/taskboard-client.exe ./cmd/taskboard-client
```

查看版本执行 `.\taskboard-client.exe -version`。

## 平台地址

发布产物本身不内置平台地址。服务端的下载接口会在原始 exe 后追加一个 `TBSTAMP1` 尾部戳，内容是本次下载使用的平台地址；`/api/v1/client/latest` 里的 SHA256 和大小也按附加后的下载内容计算。

客户端启动时按以下顺序解析平台地址：

1. `-server` 参数；
2. `TASKBOARD_SERVER_URL` 环境变量；
3. 已安装时保存在 `HKCU\Software\Taskboard\Client` 的地址；
4. 自身 exe 尾部戳。

四个来源都为空时报错退出，不再回退到本机默认地址。解析成功后，安装流程会把地址保存到 HKCU。

## 安装与卸载

从安装目录（`%LOCALAPPDATA%\Programs\taskboard-client\`）以外启动时，客户端把自己复制到安装目录并覆盖旧版本，写入：

```text
HKCU\Software\Microsoft\Windows\CurrentVersion\Run
TaskboardClient = "C:\...\taskboard-client.exe" --autostart
```

然后从安装位置重新启动，下载目录里的进程随即退出。已在安装目录内启动时不会复制自己。

托盘菜单的「卸载」会撤销这台电脑的客户端凭证，并删除 Run 自启项、本机凭据、Toast AUMID 注册和 HKCU 里的平台地址，最后通过延迟命令删除程序自身。

## 数据与系统能力

- 客户端凭证保存在当前用户的 Windows 凭据管理器，目标名为 `Taskboard Desktop Client`。
- Toast 的应用标识固定为 `com.urithub.TaskboardClient`，激活 GUID 注册在 HKCU。
- 同一会话用命名 mutex `Local\TaskboardDesktopClient` 保证单实例。
- 托盘图标区分正常、未读、需要重新登录和连接中断；菜单列出最近 10 条未读，并提供全部已读、查看全部、重新登录、退出登录、卸载和退出。
