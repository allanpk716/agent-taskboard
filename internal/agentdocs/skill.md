---
name: agent-taskboard
description: 主人提到工单、工单平台、轮到我的、收件箱、建一张工单、回复或交付工单、草稿，或者贴来带 {{BASE}} 地址的工单指令时使用。用于访问 agent-taskboard、先读说明书再处理工单。
---

# agent-taskboard Agent 技能

agent-taskboard 是部门工单记录平台，地址是 {{BASE}}。这份技能只负责让你在主人提到这些事时想起本平台，并指向说明书；不复制平台规则，规则每次都从说明书现读。

## 使用方法

1. 动手前先读说明书：{{BASE}}/agent.md。平台规则、凭证约定、接口用法和草稿约定一律以说明书为准。
2. 凭证：请求头 `Authorization: Bearer $TASKBOARD_TOKEN`；平台地址也可以从 `TASKBOARD_URL` 读到。
3. 读不到 `TASKBOARD_TOKEN`，或接口返回 401 时，停下来请主人去「我的 → Agent」签发或换新 token，并贴来接入文案；不要用密码登录，也不要用别人的 token。
4. 常用起点：
   - 收件箱：{{BASE}}/api/v1/inbox
   - 工单 Markdown 视图：{{BASE}}/api/v1/tickets/<编号>?format=markdown

## 来源

本技能来自 {{BASE}}/skills/agent-taskboard/SKILL.md。主人让你更新技能时，从这里重新下载并覆盖安装。
