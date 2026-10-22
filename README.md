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
