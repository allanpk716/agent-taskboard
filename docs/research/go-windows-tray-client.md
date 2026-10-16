# 用 Go 开发 Windows 托盘客户端的技术选型

调研工单：[#20](https://github.com/allanpk716/agent-taskboard/issues/20)（属于地图 [#17 成员通知](https://github.com/allanpk716/agent-taskboard/issues/17)）。调研日期：2026-10-16。

## 结论

**推荐：纯 Go、不用 GUI 框架。用 `fyne.io/systray` 做托盘，`git.sr.ht/~jackmordaunt/go-toast/v2` 弹 Toast，`golang.org/x/sys/windows/registry` 写开机自启和 AUMID。界面能放进托盘菜单的就放进托盘菜单，放不下的交给浏览器打开网页。**

- 两个库在 Windows 上都是纯 Go 实现（syscall + COM），`CGO_ENABLED=0` 可以直接交叉编译，产物是一个单文件 exe，不依赖 WebView2，也不需要安装程序。
- 实测：一个带托盘菜单、Toast（点击后用 protocol 激活打开 URL）、写 Run 键的最小程序，`CGO_ENABLED=0 go build -ldflags "-s -w -H=windowsgui"` 编出来是 **4.5 MB**（go1.24.13，windows/amd64）。
- 「登录界面和未读列表」这个小窗口不用原生 GUI 来做：
  - 登录：客户端用系统浏览器打开平台的登录页，凭证通过 `127.0.0.1` 回环地址回传给客户端，做法见 RFC 8252。具体凭证模型由 [#19](https://github.com/allanpk716/agent-taskboard/issues/19) 决定。
  - 未读列表：在托盘菜单里放最近 N 条未读（菜单项可以动态增删），最后一项是「查看全部」，用浏览器打开网页上的通知列表。
  - 这样客户端里没有任何界面需要维护。按 #17 的定义，客户端「只负责提醒」，回复和处理都在网页上完成，二者一致。
- 如果以后确实需要一个原生的小窗口，再考虑 Wails v3。它的托盘和通知后端用的就是上面这个 go-toast（见下文），迁移代价不大。

## 方案对比

| | 纯 systray + go-toast（推荐） | Wails v3 | Wails v2 | Fyne v2 |
|---|---|---|---|---|
| 维护状态 | `fyne.io/systray` v1.12.2，2026-06；`go-toast/v2` v2.0.3，2025-01 | **仍是 beta**：v3.0.0-beta.30，2026-10-15，几乎每隔几天发一版 | v2.14.0，2026-08，稳定 | v2.8.2，2026-10-12，稳定 |
| 托盘 | 有 | 有，内置 systray API，可以把窗口挂到托盘上 | **没有内置托盘** | 有，`desktop.App.SetSystemTrayMenu`，底层就是 `fyne.io/systray` |
| Toast 及点击回调 | 有，进程内 COM 激活回调，也支持 protocol 激活直接打开 URL | 有，Windows 后端就是 go-toast 的 `wintoast` | 没有内置，要自己接 | Windows 上用 PowerShell 脚本弹，**没有点击回调** |
| 小窗口 | 不做，交给浏览器 | WebView2 渲染的 HTML 窗口 | WebView2 | OpenGL 自绘 |
| CGO | 不需要 | Windows 上不需要 | Windows 上不需要 | **需要**，要装 C 编译器（MSYS2/MinGW），因为依赖 go-gl/glfw |
| 运行时依赖 | 无 | WebView2 Runtime | WebView2 Runtime | 显卡的 OpenGL 驱动 |
| 体积 | 实测 4.5 MB | 大约 10 MB 以上（未实测） | 同左 | 大约 20 MB 以上（未实测） |
| 前端技术栈 | 无 | 要再维护一套 HTML/JS | 同左 | Go 写 UI |

上表的体积估算来自经验，没有实测，量级可以参考，具体数字不要引用；只有 4.5 MB 是实测数据。

### 维护状态（2026-10-16 查询 GitHub API 和 proxy.golang.org）

- `wailsapp/wails`：最新稳定版 v2.14.0（2026-08-10），v3 处于 `v3.0.0-beta.30`（2026-10-15，prerelease）。
- `fyne-io/fyne`：v2.8.2（2026-10-12）。
- `fyne-io/systray`：v1.12.2（2026-06-09），最近一次提交在 2026-09。这是 getlantern/systray 的 fork，去掉了 GTK 依赖，Fyne 自己也依赖它。
- `getlantern/systray`：最新 tag v1.2.2（2023-03），最近一次提交在 2023-11，**基本停止维护**，不选。
- `energye/systray`：v1.0.3（2026-01），用户较少。
- `github.com/go-toast/toast`：最近一次提交在 2019-02，**已废弃**，而且是靠调 PowerShell 弹通知的，不选。
- `git.sr.ht/~jackmordaunt/go-toast/v2`：v2.0.3（2025-01-17）。发版频率不高，但 Wails v3 的 `v3/go.mod` 直接依赖 `git.sr.ht/~jackmordaunt/go-toast/v2 v2.0.3`，相当于有一个活跃的大项目在背书。
- `lxn/walk`（原生 Win32 GUI）：最近一次提交在 2024-01，基本停止维护，所以不用它来画小窗口。

## 逐项说明

### 托盘图标和菜单

`fyne.io/systray` 提供 `systray.Run(onReady, onExit)`、`SetIcon`/`SetTooltip`、`AddMenuItem`、子菜单、可勾选和可禁用的菜单项，并且可以在任意 goroutine 里调用。Windows 实现 `systray_windows.go` 只 import 了 `golang.org/x/sys/windows`，整个包只有 `systray_darwin.go` 用了 cgo。README 里写着「this package requires cgo」，但那是针对 macOS 的，Windows 上用 `CGO_ENABLED=0` 已经实测编译通过。另外要加 `-ldflags -H=windowsgui`，否则启动时会弹出控制台窗口（README 原文）。

### Toast 通知、点击后打开 URL、AppUserModelID

**要不要注册 AUMID**：未打包（非 MSIX）的桌面程序必须让 Windows 认得自己的 AUMID，否则 Toast 不显示，或者显示不出应用名。有两种做法：

1. 旧做法：在开始菜单放一个带 `System.AppUserModel.ID` 属性的快捷方式。Microsoft 文档的原话是："Without a valid shortcut installed in the Start screen or in All Programs, you cannot raise a toast notification from a desktop app."（[How to enable desktop toast notifications through an AppUserModelID](https://learn.microsoft.com/en-us/windows/win32/shell/enable-desktop-toast-with-appusermodelid)）
2. 新做法：写注册表 `Software\Classes\AppUserModelId\<AUMID>`，填 `DisplayName`、`IconUri`、`CustomActivator`。Microsoft 的「Send a local toast notification from other types of unpackaged apps」介绍的就是这种做法；Edge 的 Toast 也是用 `HKLM\SOFTWARE\Classes\AppUserModelId\...` 下的 `DisplayName` 和 `CustomActivator` 注册的（[Microsoft Q&A](https://learn.microsoft.com/en-us/answers/questions/997401/windows-toast-notifications-issue.html)）。

go-toast v2 用的是第 2 种，而且写的是 **HKCU**，不需要管理员权限。源码 `wintoast/registry.go` 里：`appKeyRoot = SOFTWARE\Classes\AppUserModelId`，`registry.CreateKey(registry.CURRENT_USER, ...)`，会写入 `DisplayName`、`CustomActivator`（COM 激活器的 CLSID）、可选的 `IconUri`、`IconBackgroundColor`。如果设置了 `ActivationExe`，还会写 `SOFTWARE\Classes\CLSID\{GUID}\LocalServer32`，这样即使进程已经退出，点击通知也能把 exe 拉起来。程序启动时调一次 `toast.SetAppData(AppData{AppID, GUID, IconPath})` 就够了，**不需要开始菜单快捷方式，也不需要安装程序**。`GUID` 要用我们自己生成的固定值，库的注释里说："It's preferable for the application to override this value with its own generated GUID."

**点击通知后打开浏览器跳转**：有两种方式。

- `ActivationType: toast.Protocol` 加上 `ActivationArguments: "https://.../tickets/xxx"`：Windows 直接用默认浏览器打开这个 URL，不经过我们的进程。库的注释里有例子（"opens gmail.com when they click the notification"）。模板生成的 XML 是 `<toast activationType="protocol" launch="https://...">`，`launch` 和 `activationType` 是 Toast schema 定义的属性（[toast element](https://learn.microsoft.com/en-us/uwp/schemas/tiles/toastschema/element-toast)）。
- `toast.Foreground` 加上 `toast.SetActivationCallback(func(args, data))`：点击时回调到我们的进程，适合「打开 URL 的同时把这条通知标为已读」。Windows 的激活是通过 COM（`INotificationActivationCallback`）进来的，go-toast 用纯 Go 实现了这个 COM 对象（见其 `ARCHITECTURE.md`）。

**推荐用 Foreground 回调**：在回调里先调 API 标记已读，再用 `rundll32 url.dll,FileProtocolHandler <url>`，或者 `windows.ShellExecute` 打开浏览器。客户端没在运行时，Protocol 方式也能用，可以作为退路。

注意事项：

- go-toast 有一个 `PowershellFallback` 选项，COM 方式失败时改用 PowerShell 弹通知，但这种情况下**激活回调不生效**（`SetActivationCallback` 的注释："This will do nothing if the powershell fallback is in-effect."）。
- Wails 文档提到，wintoast 后端目前不支持按 `tag`/`group` 原地替换通知，`UpdateNotification` 在 Windows 上会重新弹一条新的（[Wails v3 notifications 文档](https://github.com/wailsapp/wails/blob/master/docs/mpress/content/features/notifications/overview.md)）。如果以后要做「通知聚合」（#17 的「Not yet specified」里列了这一项），需要自己扩展模板，加上 `tag`。

### 开机自启：Run 键还是启动文件夹

推荐用 **`HKCU\Software\Microsoft\Windows\CurrentVersion\Run`**，写一个值，比如 `AgentTaskboard = "C:\Users\...\agent-taskboard-tray.exe" --autostart`。依据（[Run and RunOnce Registry Keys](https://learn.microsoft.com/en-us/windows/win32/setupapi/run-and-runonce-registry-keys)）：

- 用户级的 Run 键不需要管理员权限，"The Run key makes the program run every time the user logs on"；
- 命令行不能超过 260 个字符；
- "A program that is run from any of these keys should not write to the key during its execution"。所以开关自启只在用户点击设置时写，不要每次启动都重写；
- 系统不保证 Run 键里的程序马上启动，可能会延迟。启动文件夹（Startup group）也一样，两者在这一点上没有区别。

相比启动文件夹，Run 键用 `x/sys/windows/registry` 一行代码就能写，不用通过 COM 创建 `.lnk`；用户也能在「任务管理器 → 启动应用」里看到并关闭它。exe 换位置后，启动时检查一下 Run 键里的路径，不一致就更新（只在用户开启了自启时才做）。

### 小窗口：登录和未读列表

除了上面推荐的「托盘菜单 + 浏览器」做法，还评估了几种需要一个真正的窗口时的选择：

- **Wails v3**：最顺手，有托盘、通知、窗口挂到托盘（`systray.AttachWindow`），甚至自带 updater。但它还是 beta，几乎每隔几天发一版，API 还可能变；而且要多维护一套前端，并依赖 WebView2。
- **Fyne**：Windows 上要 CGO 和 MinGW 工具链（Fyne Quick Start 原文："a C compiler (to connect with system graphics drivers)"，Windows 推荐 MSYS2），交叉编译和 CI 都变麻烦；而且它在 Windows 上的通知是用 PowerShell 弹的，没有点击回调，满足不了「点击后跳到工单」这个需求。不选。
- **walk 等原生 Win32 库**：停止维护了。

### 产物体积、CGO、WebView2、单文件

- 推荐方案：单个 exe，大约 4.5 MB（实测），不需要 CGO，也没有运行时依赖。
- WebView2：Microsoft 文档说 "The Evergreen WebView2 Runtime will be included as part of the Windows 11 operating system"，"The vast majority of Windows 10 devices have the WebView2 Runtime installed already"，但还是要求应用自己检测，缺了就安装（bootstrapper 大约 2 MB，要联网；离线要用 standalone installer；Fixed Version "over 250 MB"）（[Distribute your app and the WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution)）。选 Wails 的话，就得多处理这一层，Wails 的 Windows 构建也会生成 WebView2 bootstrapper 和 NSIS 安装包（[Wails Windows build 文档](https://github.com/wailsapp/wails/blob/master/docs/mpress/content/guides/build/windows.md)）。

### SmartScreen、杀软和代码签名成本

依据：[SmartScreen reputation for Windows app developers](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation)、[Code signing options](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options)。

- SmartScreen 检查的是**下载来的文件**（"checks the reputation of downloaded files"）。未签名的 exe 首次运行会弹 "Windows protected your PC"，要点「仍要运行」；"Enterprise policy can prevent continuation entirely"。未签名的文件每出一个新版本，信誉都要从零开始积累。
- **签了名也不能马上免提示**：OV/EV 证书一样会先提示"unrecognized"，要等信誉积累起来，"it can take several weeks and hundreds of clean installs from a wide audience"。部门内部只有几十人安装，**签名了也很难积累到足够的信誉**。"EV certificates no longer bypass SmartScreen"（2024 年起）。
- 成本：Azure Artifact Signing（原 Trusted Signing）大约 9.99 美元/月，但**只面向美国、加拿大、欧盟、英国的组织**，国内公司用不了；OV 证书每年 150–300 美元，私钥必须放在 HSM 或硬件令牌里；EV 每年 400 美元以上，已经没有 SmartScreen 上的优势。
- 内网的出路：Microsoft 文档说 "Enterprises may distribute files from Trusted Intranet locations not subject to SmartScreen review"，IT 也可以把文件提交到 Microsoft Security Intelligence 加快建立信任；自签名证书经过组策略下发为受信任根证书之后，也可以用于企业内部分发。
- **Windows 11 的智能应用控制（Smart App Control）**："will block execution of unsigned files unless the file has a positive reputation"，而且这个检查 "apply to all executable files, not just those downloaded from the Internet"。新装的 Windows 11 如果开着它，未签名的 exe **会被直接拦下，没有「仍要运行」可点**。部门电脑大多是升级上来或者域管理的，一般处于关闭状态，但需要抽查确认。
- 杀软误报：Go 程序未签名、单文件，还会写 Run 键、注册 COM 激活器、替换自身 exe，这几样都是启发式检测常盯的行为，被国产杀软报毒的概率不低。这一条没有一手资料能量化，只能实测。

建议：**MVP 先不签名**。exe 从内网 HTTPS 地址（平台自身或 UpdateHub）下载；首次安装时让用户点一次「仍要运行」。用户真正下载的只有首次安装那一次，之后客户端自我更新时，自己下载的新 exe 不会被打上 Mark of the Web，正常不会再触发 SmartScreen；这一点没有找到 Microsoft 的明文说明，属于推断，需要实测。如果实测发现杀软或智能应用控制拦得厉害，再看两条路：一是请 IT 用组策略下发自签名的根证书，二是买 OV 证书。详细的分发方案留给 [#23](https://github.com/allanpk716/agent-taskboard/issues/23)。

### exe 自我替换式的自动更新

推荐方案对自我更新**最友好**：只有一个 exe，没有 DLL、没有前端资源目录，也没有 WebView2 用户数据目录需要迁移。Windows 不允许删除或覆盖正在运行的 exe，但允许改名，所以可以沿用服务端在 ADR 0002 里的做法：

1. 下载新版本，按 SHA256 校验（沿用 ADR 0002 的 `manifest.json` 和多更新源回退）；
2. 把正在运行的 `xxx.exe` 改名为 `xxx.exe.prev`，把新文件放到 `xxx.exe`；
3. 启动新的 exe，旧进程退出；新进程启动后清理 `.prev`，起不来就改回来。

现成的库有 `github.com/minio/selfupdate`（v0.6.0，2022-10，停更了，但做的事情就是上面这几步），这几步自己写也只有几十行，不值得引入依赖。

## 风险与待验证项

1. 首次安装时 SmartScreen、国产杀软、Windows 11 智能应用控制的实际表现：找两三台典型的部门电脑实测。
2. 自我更新下载的新 exe 是否会触发 SmartScreen 或杀软。
3. 托盘客户端用什么凭证调 API。ADR 0003 规定 "通过 API token 的操作，一律记为签发这个 token 的那个成员 Agent"。客户端只读通知、标记已读，不发回复，但它的凭证类型仍需要 [#19](https://github.com/allanpk716/agent-taskboard/issues/19) 来定。
4. go-toast 发版不频繁。真出了问题，可以 fork，代码量很小，Wails 也在依赖它。

## 主要来源

- fyne-io/systray：<https://github.com/fyne-io/systray>（README，以及 v1.12.2 的 `systray_windows.go` 源码）
- go-toast v2：<https://git.sr.ht/~jackmordaunt/go-toast>（v2.0.3 源码：`toast.go`、`wintoast/registry.go`、`wintoast/bind_windows.go`、`tmpl/xml.go.tmpl`、`ARCHITECTURE.md`）
- Wails：<https://github.com/wailsapp/wails>（releases，`v3/go.mod`，`docs/mpress/content/features/notifications/overview.md`、`features/menus/systray.md`、`guides/build/windows.md`、`guides/updater.md`、`getting-started/installation.md`）
- Fyne：<https://github.com/fyne-io/fyne>（`app/app_windows.go`、`go.mod`），<https://docs.fyne.io/started/quick/>
- Microsoft：Run and RunOnce Registry Keys；How to enable desktop toast notifications through an AppUserModelID；toast schema；Distribute your app and the WebView2 Runtime；SmartScreen reputation for Windows app developers；Code signing options for Windows app developers（链接见正文）
- RFC 8252 OAuth 2.0 for Native Apps（回环地址重定向）：<https://www.rfc-editor.org/rfc/rfc8252>
