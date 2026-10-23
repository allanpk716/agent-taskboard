# Windows 桌面客户端

桌面客户端是单个 Windows exe，负责连接账号、订阅通知、弹出 Toast、维护托盘未读状态和标记已读。双击下载的 exe 后自动安装到用户目录，全程不需要管理员权限。

## 构建

```powershell
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build `
  -trimpath -ldflags "-s -w -H=windowsgui -X github.com/allanpk716/agent-taskboard/internal/version.Client=0.2.0" `
  -o bin/taskboard-client.exe ./cmd/taskboard-client
```

查看版本执行 `.\taskboard-client.exe -version`。

## 平台地址

发布产物本身不内置任何平台地址；所有部署下载同一个 exe，哈希也与 Release 清单一致。

客户端启动时按以下顺序解析平台地址：

1. `-server` 参数；
2. `TASKBOARD_SERVER_URL` 环境变量；
3. 已安装时保存在 `HKCU\Software\Taskboard\Client` 的地址；
4. 弹出原生输入框，让成员填写平台地址。

输入框标题为「Taskboard 平台地址」，说明文字为「请输入平台网址，例如 https://taskboard.example.com」。客户端把输入规范化后请求 `<地址>/api/v1/health`，只有返回 200 JSON 且 `server_version` 非空才保存到 HKCU；探测失败会在同一个对话框显示原因并允许重填。首次运行点取消时客户端退出。

## 安装与卸载

从安装目录（`%LOCALAPPDATA%\Programs\taskboard-client\`）以外启动时，客户端先把安装目录里的旧版改为 `taskboard-client.exe.old`，再复制新文件、通知正在运行的旧实例退出并从安装位置重启；下次启动时尽力删除 `.old`。安装后写入：

```text
HKCU\Software\Microsoft\Windows\CurrentVersion\Run
TaskboardClient = "C:\...\taskboard-client.exe" --autostart
```

然后从安装位置重新启动，下载目录里的进程随即退出。已在安装目录内启动时不会复制自己。

托盘菜单的「卸载」会撤销这台电脑的客户端凭证，并删除 Run 自启项、本机凭据、Toast AUMID 注册和 HKCU 里的平台地址，最后通过延迟命令删除程序自身。

托盘菜单另有「更改平台地址…」。弹窗会预填当前地址；新地址探测通过且与旧地址不同时，客户端尽力撤销旧凭证（失败也继续）、删除本机凭证、保存新地址，然后按新地址重新打开网页确认并签发客户端凭证。点取消则保持原地址不变。

## 自动更新与兼容性

客户端启动时、每次实时通道重连收到 hello 时，以及之后每 6 小时检查一次 `https://github.com/allanpk716/agent-taskboard/releases/download/channel-stable/latest.json` 的 `client` 条目。版本更高时弹出一次「Taskboard 客户端有新版本 x.y.z，点击更新」，托盘菜单也会出现「更新到 x.y.z」；同一版本本次运行只弹一次 Toast。

成员点击 Toast 或菜单后才下载该版本 Release 的 `manifest.json`，再下载 `taskboard-client.exe`，按清单里的 SHA256 和大小校验。下载或校验失败时 Toast 提示原因，菜单项保留可重试，通知继续使用当前版本。

校验通过后，客户端把正在运行的 exe 改名为 `taskboard-client.exe.prev`，放入新 exe 并带 `--after-update` 启动。新进程完成初始化后通过命名事件 `Local\TaskboardClientUpdateReady` 通知旧进程，旧进程随即退出，新进程弹出「客户端已更新到 x.y.z」。30 秒内没有收到就绪信号时，旧进程结束新进程、删除新 exe、把 `.prev` 换回原位置，继续以旧版本提醒。

客户端用到的平台接口只做增量修改，不再有服务端下发的最低版本门槛。0.1.4 及更早版本查不到旧更新接口时不会退出，通知照常可用；需要成员手动下载新版覆盖一次。

## 数据与系统能力

- 客户端凭证保存在当前用户的 Windows 凭据管理器，目标名为 `Taskboard Desktop Client`。
- Toast 的应用标识固定为 `com.urithub.TaskboardClient`，激活 GUID 注册在 HKCU。
- 同一会话用命名 mutex `Local\TaskboardDesktopClient` 保证单实例。
- 托盘图标区分正常、未读、需要重新登录和连接中断；菜单列出最近 10 条未读，并提供更改平台地址、更新、全部已读、查看全部、退出登录、卸载和退出。
