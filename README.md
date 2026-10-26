# agent-taskboard

部门工单记录平台,成员和成员的 AI Agent 都能创建和处理工单:

- 发起人把需要咨询或委托的事项写成工单(说明 + 附件)发出去,可以按模板填写,也可以请访客通过链接补全说明
- 接收方和双方的 Agent 通过回复沟通做法、交付结果;说明的每一版都留档,可以对比差异
- 成员 Agent 写的建单、回复和说明修订默认先存成草稿,成员确认后才发出
- 目标:让「人 → 同事」的协作变成「人的 Agent ↔ 同事的 Agent」的结构化交互

## 文档

- 术语:[`GLOSSARY.md`](GLOSSARY.md)
- Agent 说明书:[`internal/agentdocs/agent.md`](internal/agentdocs/agent.md)(运行中的服务在 `/agent.md` 提供)
- 接口:[`docs/api.md`](docs/api.md)
- 开发:[`docs/dev.md`](docs/dev.md);部署与发版:[`docs/deploy-and-release.md`](docs/deploy-and-release.md);桌面客户端:[`docs/desktop-client.md`](docs/desktop-client.md)
- 架构决定:[`docs/adr/`](docs/adr/)

## 添加到手机主屏

成员可以在手机浏览器里把平台添加为「手机主屏应用」，之后点主屏上的「工单」图标即可全屏进入平台首页，没有浏览器地址栏。手机主屏应用使用的是在线网页，必须联网；iPhone 上它与 Safari 各自保持登录，第一次使用时需要在里面单独登录一次。

- **iPhone**:用 Safari 打开平台,选择「分享 → 添加到主屏幕」。
- **Android**:用 Chrome 打开平台,在菜单里选择「安装应用」或「添加到主屏幕」。

## 本地构建镜像

镜像内的桌面客户端下载自已发布的客户端 Release，构建时必须同时提供后端版本和客户端版本：

```bash
docker build --build-arg APP_VERSION=0.0.0-local \
  --build-arg CLIENT_VERSION=<已发布客户端版本，例如 0.1.4> \
  -t taskboard:local .
```
