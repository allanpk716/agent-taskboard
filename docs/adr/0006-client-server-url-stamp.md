# 下载的客户端 exe 尾部附带平台地址

桌面客户端需要知道自己该连哪个平台，但发布的 exe 是公开 GitHub Release 上的同一份产物，不能把某个部署的内网地址编进去。服务端在 `/api/v1/client/download` 返回代管的 exe 时，在文件末尾附加一段尾部数据：平台地址、4 字节小端长度、8 字节魔数 `TBSTAMP1`。PE 文件末尾的附加数据不影响运行。地址取配置 `public_url`，为空时由请求的 scheme 和 host 推出。客户端首次运行时从自身 exe 尾部读出地址，安装时存进 HKCU；之后从安装位置重启、开机自启、自动更新都读 HKCU 里保存的这份地址。

地址的解析顺序：`-server` 参数 > `TASKBOARD_SERVER_URL` 环境变量 > HKCU 里保存的地址 > exe 尾部。都没有时报错退出，不再默认连本机。

## Considered Options

- **构建时用 `-ldflags -X` 注入**：最简单，但公开 Release 会带上内网地址；换地址要重发客户端，别的部署也得自己构建。
- **读下载文件的 Mark-of-the-Web（`Zone.Identifier` 里的 `HostUrl`）**：服务端不用改，但浏览器、经共享盘拷贝等情况会丢掉这个标记，不可靠。

## Consequences

- 服务端下载的字节和 manifest 里的原始产物不同。`/client/latest` 的 `sha256` 和 `size` 按附加尾部后的字节计算，与下载内容一致；客户端自动更新按它校验。服务端从更新源拉包时仍按 manifest 校验原始字节。
- 同一个版本，不同部署下载到的 exe 哈希不同。客户端本来就不签名（ADR 0005），不影响 SmartScreen 的处理方式。
