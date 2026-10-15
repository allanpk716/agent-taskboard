# 反馈模板与提问规范：热门项目怎么引导人写好 bug 反馈

> 调研工单：[#12](https://github.com/allanpk716/agent-taskboard/issues/12)。服务于 [#9 工单模板模型](https://github.com/allanpk716/agent-taskboard/issues/9) 与 [#13 提交前的完整性检查](https://github.com/allanpk716/agent-taskboard/issues/13)。
> 调研日期：2026-10-15。凡标注「推断」的条目是基于来源对本平台的推演，不是来源原话。

## 结论速览

1. **字段**：所有被调研的大项目都强制要求三件套：**复现步骤、期望结果、实际结果**；其次是**版本/环境**。日志、截图、最小复现多为选填但被反复强调。
2. **引导手法**：靠「字段标签用提问句」「placeholder 给出示例」「description 解释为什么要这个信息、以及去哪里找」「把症状和猜测分开」「说明不填的后果」。
3. **检查机制**：成熟项目普遍是**提交时只做规则校验（必填），提交后再由机器人/AI 判定是否缺信息**，缺了就打标签、追问，超时关闭。LLM 质检（GitHub 官方的 `ai-assessment-comment-labeler`、gh-aw 的 issue triage agent）都是**在平台外、提交后**以 Action/Agent 形式运行，平台本身只提供表单和标签。
4. **知识库**：LLM 分诊工具用项目文档、历史 issue、标签字典作上下文；小于约 20 万 token（约 500 页）的知识库可以整份放进提示词，不必做 RAG。对本平台而言，标签字典（机型等）就是最小可用的“标准叫法表”。
5. **对现有地图的影响（推断）**：证据支持维持「平台不内置 AI」——内置规则校验 + 外部 Agent 提交后质检，已能覆盖主流做法；唯一缺口是“访客还在页面上时的实时 AI 提示”，主流项目也没有做这件事。

---

## 1. 模板长什么样

### 1.1 GitHub Issue Forms（YAML 表单）语法要点

来源：[Syntax for issue forms](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/syntax-for-issue-forms)、[Syntax for GitHub's form schema](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/syntax-for-githubs-form-schema)

- 顶层键：`name`、`description`（必填），`body`（必填，字段数组），可选 `title`（预填标题，如 `"[DevTools Bug]: "`）、`labels`、`type`、`assignees`、`projects`。
- 字段类型：`markdown`（只展示不提交）、`input`（单行）、`textarea`（多行，可 `render` 成代码块）、`dropdown`（可 `multiple`、`default`）、`checkboxes`（每个选项可单独 `required`）、`upload`（文件上传，可 `accept` 限制类型）。
- 字段属性：`label`、`description`（说明，支持 Markdown 链接）、`placeholder`（灰字示例）、`value`（预填内容，如 Kubernetes 预填 `<details>` 和命令）。
- 校验：`required`、`min_length`（input/textarea）、`accept`（upload）。**注意**：文档原文说 `required` "Prevents form submission until element is completed. Only for public repositories." —— 私有仓库不强制。
- 限制：dropdown 设了 `default` 时不能含 "None"/"n/a" 选项；表单处于 public preview。Rust 的 ICE 模板注释里直言 "GitHub forms have strict limits on the size of fields so backtraces cannot be pasted directly"，因此仍保留 Markdown 版 ICE 模板。
- 配置：`config.yml` 里 `blank_issues_enabled: false` 可禁止跳过模板，`contact_links` 把“提问”导流到别处（VS Code 把提问导到 Stack Overflow）。

### 1.2 Issue Forms 与旧式 Markdown 模板的取舍

| | Issue Forms（YAML） | Markdown 模板 |
|---|---|---|
| 结构 | 每个字段独立输入框，提交后渲染为 `### 标题` + 内容，便于机器解析 | 一整块文本，提示写在 `<!-- -->` 注释里，用户可以整段删掉 |
| 校验 | 必填、最小长度、上传类型 | 无，只能靠提交后机器人用正则检查（VS Code 用注释 `<!-- ⚠️⚠️ Do Not Delete This! bug_report_template ⚠️⚠️ -->` 作为模板标记） |
| 长内容 | 字段有长度上限，长日志不便 | 无限制（Rust 因此保留 Markdown ICE 模板） |
| 适合谁 | 不熟悉格式的人：逐项填空，不用懂 Markdown | 熟手：自由排版、大段代码与回溯 |

**本平台推断**：访客是非开发人员，应采用“表单式”模板（每字段独立、可必填），提交后再拼成 Markdown 说明——这和 Issue Forms 的做法一致，也让外部 Agent 可以按小标题解析。

### 1.3 五个大项目的 bug 模板（原文取自各仓库 `.github/ISSUE_TEMPLATE/`，2026-10-15 获取）

| 项目 | 格式 | 必填/核心字段 | 引导手法 |
|---|---|---|---|
| [VS Code `bug_report.md`](https://github.com/microsoft/vscode/blob/main/.github/ISSUE_TEMPLATE/bug_report.md) | Markdown | 是否在禁用扩展时复现（Yes/No）、VS Code 版本、OS 版本、复现步骤 | 注释里逐条提示：先搜重复、先试 Insiders、"use 'Report Issue' from the 'Help' menu in VS Code to pre-fill useful information"、用 `--disable-extensions` 自查 |
| [React `bug_report.md`](https://github.com/facebook/react/blob/main/.github/ISSUE_TEMPLATE/bug_report.md) | Markdown | React 版本、复现步骤、代码示例链接、当前行为、期望行为 | 说后果："Issues without reproduction steps or code examples may be immediately closed as not actionable."；说好处："Your bug will get fixed much faster if we can run your code" |
| [React `devtools_bug_report.yml`](https://github.com/facebook/react/blob/main/.github/ISSUE_TEMPLATE/devtools_bug_report.yml) | Form | 网站/应用（必填）、复现步骤（必填）、**出现频率**下拉（Every time / Often / Sometimes / Only once，必填）；版本、错误信息、调用栈由工具**自动填入**（"Please do not edit this field."） | placeholder 给出完整示例步骤（"1. Log in… 2. Click 'Messages'…"）；"Issues filed without repro steps will be closed." |
| [Rust `bug_report.md`](https://github.com/rust-lang/rust/blob/master/.github/ISSUE_TEMPLATE/bug_report.md) / [`ice.yaml`](https://github.com/rust-lang/rust/blob/master/.github/ISSUE_TEMPLATE/ice.yaml) | 两种都有 | “I tried this code / I expected to see this happen / Instead, this happened”、`rustc --version --verbose`、Backtrace；ICE 表单里版本和回溯必填，受影响渠道用复选框 | 以填空句式引导（“我尝试了…我期望…结果却…”）；placeholder 给出版本输出的样子；告诉人怎么拿回溯（`RUST_BACKTRACE=1`） |
| [Kubernetes `bug-report.yaml`](https://github.com/kubernetes/kubernetes/blob/master/.github/ISSUE_TEMPLATE/bug-report.yaml) | Form | What happened?、What did you expect to happen?、How can we reproduce it (as minimally and precisely as possible)?、Kubernetes 版本、云厂商（均必填）；OS、安装工具、运行时等选填 | 字段标签直接是问句；`value` 预填折叠块和要执行的命令；"Not doing so may result in your bug not being addressed in a timely manner." |
| [Electron `bug_report.yml`](https://github.com/electron/electron/blob/main/.github/ISSUE_TEMPLATE/bug_report.yml) | Form | 预检复选框（读过贡献指南、同意行为准则、已搜索重复，均必填）、版本、OS（多选）、OS 版本、架构、是否在 Chrome 也出现、期望行为、实际行为；**最后正常版本**、测试用例 Gist 选填 | OS 版本的 description 手把手告诉在哪里看（"On Windows, click Start button > Settings > System > About"）；下拉提供 "I don't know how to test" 选项；文件头有一段**专门写给 AI Agent 的说明**：要简洁、先写症状、把调查分析放进 `<details>` |

**共同字段（出现频次从高到低）**

1. 复现步骤（5/5，都必填或首要）
2. 实际结果 / 期望结果（5/5）
3. 版本与环境（5/5）
4. 最小复现 / 测试用例（React、Rust、K8s、Electron）
5. 错误信息、日志、回溯（React DevTools、Rust、K8s）
6. 出现频率（React DevTools），最后正常的版本（Electron，用于定位回归）
7. 补充信息（Anything else?）

**共同的引导手法**

- 标签写成问题（K8s: "What happened?"），而不是名词（“问题描述”）。
- placeholder 给一份**完整的好例子**，而不是“请输入”。
- description 解释“为什么要”和“在哪里找”（Electron 的 OS 版本路径、Rust 的回溯命令）。
- 说明后果和收益：缺复现会被关闭 / 有复现修得更快。
- 给“不知道”的出口（Electron 的 "I don't know how to test"），避免用户乱选。
- 能自动采集的就不让人填（VS Code Issue Reporter、React DevTools 自动字段）。
- 症状与分析分开（Electron 给 Agent 的说明；ESR、Tatham 同样强调）。

---

## 2. 提问/反馈指南：可以变成检查项的规则

来源：Simon Tatham, [How to Report Bugs Effectively](https://www.chiark.greenend.org.uk/~sgtatham/bugs.html)；Eric S. Raymond《提问的智慧》中文译本 [ryanhanwu/How-To-Ask-Questions-The-Smart-Way](https://github.com/ryanhanwu/How-To-Ask-Questions-The-Smart-Way/blob/main/README-zh_CN.md)；Mozilla [Bug Writing Guidelines](https://bugzilla.mozilla.org/page.cgi?id=bug-writing.html)

| # | 规则 | 出处 | 能否机器检查 |
|---|---|---|---|
| R1 | 标题要具体，写“在哪里、出了什么现象”，不写“救命/急/不能用” | ESR“使用有意义且描述明确的标题”；Mozilla：约 10 个词，好例 "Cancelling a File Copy dialog crashes File Manager"，坏例 "Software crashes" | 规则：长度下限 + 黑名单词；LLM：判断是否含对象和现象 |
| R2 | 按时间顺序写出操作步骤 | ESR“按发生时间先后列出问题症状”；Tatham "tell them exactly what you did … which buttons you pressed and what order"；Mozilla 称复现步骤是 "the most important part of any bug report" | 规则：步骤字段必填、最小长度；LLM：判断是否是可执行的步骤 |
| R3 | 写清看到了什么，**以及**原本期望看到什么 | Tatham "State what you saw, and also state what you expected to see" | 规则：两个字段分别必填 |
| R4 | 错误信息原样照抄，**尤其是里面的数字** | Tatham "Write down the error messages, especially if they have numbers in" | LLM：现象提到“报错/报警”但没给原文或编号时追问 |
| R5 | 只写症状，猜测可以有但要分开 | Tatham "The diagnosis is an optional extra, and not an alternative to giving the symptoms"；ESR“描述问题症状而非你的猜测”“别动辄声称找到 Bug” | 模板层面：单独设“你的猜测（选填）”字段 |
| R6 | 说明出现频率；偶发问题说出规律 | Tatham "That's funny, it did it a moment ago" 一节；Mozilla 要求说明每次/偶尔/不出现 | 规则：频率下拉必填 |
| R7 | 给版本号和环境 | Tatham "Have version numbers at your fingertips"；ESR“精确地描述问题并言之有物” | 规则：机型必填、版本选填但有“在哪里看”说明 |
| R8 | 出错后先停手，不要乱试 | Tatham "When your computer does something unexpected, freeze." | 不可检查，放进入口页的提示语 |
| R9 | 一个反馈只说一件事 | Mozilla "Open a new bug report for each issue!"；VS Code wiki "File a single issue per problem" | LLM：识别多问题合一并建议拆分 |
| R10 | 避免指代不清（“它”“那个窗口”） | Tatham "Be careful of pronouns" | LLM 可提示 |
| R11 | 说清楚希望对方做什么 | ESR“清楚明确地表达你的问题以及需求” | 模板：可选的“你希望我们做什么” |
| R12 | 截图/录屏只是辅助，不能代替文字步骤 | VS Code wiki [Submitting Bugs and Suggestions](https://github.com/microsoft/vscode/wiki/Submitting-Bugs-and-Suggestions)："images and animations illustrate repro-steps but _do not_ replace them" | 规则：有附件也不能免填步骤 |

两份指南都承认“非技术用户也能写好”：Tatham 说道歉英语不好的人往往写得最清楚，最不清楚的是自以为对方能懂的人。

---

## 3. 提交前/提交后的检查机制

### 3.1 已有做法

| 机制 | 实例 | 时机 | 效果与代价 |
|---|---|---|---|
| **表单必填/格式校验** | Issue Forms `required`、`min_length` | 提交前，同步 | 零成本、即时。只能保证“填了”，不能保证“填对”；GitHub 的 `required` 甚至只在公开仓库生效 |
| **自动采集环境信息** | VS Code 内置 Issue Reporter（Help > Report Issue）预填版本、平台、扩展；React DevTools 自动填版本/错误/调用栈（"Please do not edit this field."） | 提交前 | 最可靠的环境信息来源，但需要被测软件自身集成上报功能。VS Code 还用 regex-labeler 检测“用了 Reporter 却没粘贴剪贴板内容”的空 issue 并自动关闭（[Automated Issue Triaging](https://github.com/microsoft/vscode/wiki/Automated-Issue-Triaging)） |
| **规则机器人：缺项打标签、超时关闭** | VS Code：维护者评论 `/needsMoreInfo` 或打 `~needs more info`，机器人换成 `needs more info` 并贴出指南链接；"If we don't receive the needed information within 7 days the bot closes the issue."（[Issues Triaging](https://github.com/microsoft/vscode/wiki/Issues-Triaging)）；若最后一条是非团队成员且 60 天无人理，则提醒指派人 | 提交后，人触发、机器执行 | 维护者省心，信息不全的问题能自然收敛。代价见 3.2 |
| **“无复现即关闭”的明文政策** | React 模板 "may be immediately closed as not actionable"；React DevTools "will be closed"；Electron "might be closed without a maintainer taking a closer look" | 提交前告知，提交后执行 | 提前告知后果，督促用户补全；对新人不友好 |
| **传统 ML 分类** | VS Code 每半小时用两个模型把 issue 映射到功能区/负责人，置信度阈值默认 0.75，达不到就留给人工；每月用全部 issue 重训 | 提交后 | 只做分派，不判断质量 |
| **LLM 质量评估（打标签+评论）** | GitHub 官方 [`github/ai-assessment-comment-labeler`](https://github.com/github/ai-assessment-comment-labeler)（[2025-09 changelog](https://github.blog/changelog/2025-09-05-github-actions-ai-labeler-and-moderator-with-the-github-models-inference-api/)）：打上触发标签后，按 `.prompt.yml` 里的系统提示评估“问题描述、复现步骤、期望与实际、视觉证据”，输出 `ai:bug-report-review:missing details` / `ready for review` 等标签并可评论 | 提交后，在平台外的 Action 中运行 | 结构化、可配置、多提示并行；只看 issue 正文，不读仓库其他资料 |
| **LLM 分诊 Agent** | gh-aw [agentics `issue-triage.md`](https://github.com/githubnext/agentics/blob/main/workflows/issue-triage.md)：读 issue、仓库标签、相似 issue 和**相关文档**；判断完整性，缺信息就打 `needs-info` 并 "ask only the specific questions needed to proceed"，"do not guess"；通过 `safe-outputs` 白名单限制只能打指定标签、最多评论 1 条 | 提交后，在平台外运行 | 能追问具体问题、查重；写权限受白名单约束，降低提示注入风险 |
| **知识库型助手** | [Dosu](https://dosu.dev/blog/automating-github-issue-triage)：从代码、issue、PR、Slack、Confluence 等建知识库，起草回复，默认交维护者批准 | 提交后 | 能回答“已知问题”；需要接入数据源 |

### 3.2 用户反感点

- **超时自动关闭**：对 20 个大项目的研究（Khatoonabadi 等，[arXiv 2305.18150](https://arxiv.org/abs/2305.18150)，ACM TOSEM 2023）发现 Stale bot 帮助清理积压，但它“更多地介入新手贡献者的条目”，项目采用后活跃贡献者明显减少。
- **机器人噪音**：Wessel 等访谈 21 名从业者（[arXiv 2103.13950](https://arxiv.org/abs/2103.13950)，CSCW 2021），发现噪音是核心问题：话太多、动作太多、做没人要求的事。
- **对策**（来自上述项目实践）：VS Code 的关闭评论写明原因和下一步（"Please take the time to review these and update the issue"）；gh-aw 要求“只问必要的问题”“不确定就不打标签”；ai-assessment 支持抑制评论只打标签。

### 3.3 三条路线对比（对应 #13）

| | 平台内置规则校验 | 外部 Agent 提交后质检 | 平台内置 AI 提交前审核 |
|---|---|---|---|
| 主流项目是否这样做 | 是（Issue Forms） | 是（ai-assessment、gh-aw、Dosu 都在平台外运行） | **未见**主流开源项目在提交按钮前跑 LLM |
| 能查什么 | 必填、长度、格式、选项合法 | 是否可复现、症状与猜测是否混杂、叫法是否标准、是否重复 | 同外部 Agent |
| 访客体验 | 即时，但只能提示“没填” | 访客提交后才收到追问（以回复形式，访客链接里能看到） | 当场提示，体验最好 |
| 平台复杂度 | 低 | 低：平台只需 API、标签、回复（已具备） | 高：模型接入、延迟、费用、失败降级、提示注入 |
| 与地图的一致性 | 一致 | 一致（“初筛由外部 Agent 完成”） | 与 Out of scope 冲突 |

**推断的建议**：MVP 采用「内置规则校验 + 外部 Agent 提交后质检」。规则校验覆盖 R2、R3、R6、R7、R12；外部 Agent 负责 R1、R4、R5、R9、R10，以回复形式追问，并打一个“待补充”类的普通标签。注意术语：GLOSSARY 里“等待方”一词的 _Avoid_ 列有“需补充”，所以标签名应另取（例如沿用 `待初筛` 体系），等待方仍由最后一条回复推导——Agent 回复后，等待方自然变成访客。不建议做超时自动关闭（研究显示会劝退新手；而且我们的访客是同事）。

---

## 4. AI 检查怎么理解领域术语

- **整份放进提示词**：Anthropic [Contextual Retrieval](https://www.anthropic.com/news/contextual-retrieval)："If your knowledge base is smaller than 200,000 tokens (about 500 pages of material), you can just include the entire knowledge base in the prompt"，配合提示缓存降低成本。一个部门的产品线术语表、机型清单、常见报警码，远小于这个量级。
- **超过后用 RAG + 关键词检索**：同一文章指出纯向量检索会漏掉精确标识符（例子是错误码 "TS-999"），BM25 擅长匹配 "unique identifiers or technical terms"。机型号 `BH-7600CS`、报警码正属于这一类。
- **分诊工具实际用的上下文**：
  - gh-aw triage agent：仓库已有标签、相似 issue、"relevant repository documentation"。
  - Dosu：代码、issue、PR、讨论，以及 Slack、Confluence、Notion；"Confluence and other documentation provide higher-level product context"；从维护者的修改中学习；草稿默认需维护者批准。
  - ai-assessment-comment-labeler：只有静态系统提示 + issue 正文；术语只能写进 `.prompt.yml`。
- **标准叫法来源**：标签字典本身就是“受控词表”。gh-aw 要求 "Choose only labels that already exist"。本平台的标签维度（产品线、机型……）由平台维护且可查询（见 #4、#6），外部 Agent 读字典即可把“7600”“BH7600CS”归一到 `机型:BH-7600CS`。
- **知识库由谁维护（推断）**：起步阶段只需要两样东西：①平台标签字典（已有，管理员维护）；②一份“俗称/错别字 → 标准名”的别名表 + 产品线简介（成员维护，Markdown 即可，几十到几百条）。放在平台外（如一个 Git 仓库或 Wiki），由外部 Agent 读取，这与地图“知识库可能落在本地图之外”的判断一致。纠错应以“建议”形式出现（回复或打标签），不直接改写访客的说明，以免丢失原始证据。

---

## 5. 对非技术用户（如检验科）的适配

| 做法 | 适用于非开发人员？ | 说明 |
|---|---|---|
| 表单逐项填写、标签用问句 | 适用 | 不需要懂 Markdown |
| placeholder 给领域内的完整示例 | 适用，且最关键 | 示例应使用检验科熟悉的场景（例：“1. 开机自检完成 2. 放入质控品 3. 点击‘开始检测’”） |
| 下拉选机型/频率，提供“不知道” | 适用 | 参照 Electron 的 "I don't know how to test" |
| “在哪里看版本/序列号”的说明 | 适用 | 参照 Electron 的 OS 版本路径说明 |
| 照片/截图/录像上传 | 适用 | 拍屏幕照片即可；但不能代替步骤文字（R12） |
| 报警/错误原文与编号 | 适用 | 只要求照抄，不要求理解 |
| 最小复现、代码 Gist、日志命令、回溯 | **不适用** | 只适合开发者；改为“附上仪器导出的日志（如会导出）” |
| 预检复选框（已读贡献指南、已搜索重复） | **不适用** | 访客通过反馈入口看不到其他工单，无法查重；查重应交给外部 Agent |
| 自动采集环境信息 | 适用但需仪器软件配合 | 长期方向，不在 MVP 内 |
| 超时自动关闭 | 不建议 | 同事关系，且研究显示劝退新手 |
| 中文以外语言要求 | 不相关 | — |

### 推荐的反馈入口默认模板字段（供 #9 使用，推断）

| 字段 | 类型 | 必填 | 提示写法（description / placeholder） |
|---|---|---|---|
| 标题 | 单行 | 是 | “一句话：哪台仪器、哪个功能、出了什么现象”；例：“BH-7600CS 质控测试时报警 E-203 并停止吸样” |
| 机型 | 下拉（取自标签维度 `机型`，含“不确定”） | 是 | 访客可选值的标签维度（地图已允许入口指定维度） |
| 仪器序列号 / 软件版本 | 单行 | 否 | 写明在仪器哪个界面能看到 |
| 发生时间 | 日期时间 | 是 | “大概时间即可，便于我们查仪器日志” |
| 你做了什么（操作步骤） | 多行 | 是，最少约 20 字 | 示例三步；“按顺序写，每步一行” |
| 你看到了什么（实际现象） | 多行 | 是 | “屏幕上的报警或提示请**原样照抄，包括编号**” |
| 你原本期望看到什么 | 多行 | 是 | “例：正常出结果” |
| 出现频率 | 下拉：每次 / 经常 / 偶尔 / 只出现过一次 | 是 | — |
| 对工作的影响 | 下拉：无法出报告 / 能绕过 / 不影响 | 否 | 帮助判断优先级 |
| 照片、截图、日志 | 附件 | 否 | “用手机拍屏幕即可；照片不能代替上面的步骤” |
| 你的猜测或补充 | 多行 | 否 | “如果你觉得是什么原因，写在这里；上面只写看到的现象” |
| 署名与联系方式 | 单行 | 是 | 平台已有“访客自报署名” |

入口页顶部的固定提示（markdown 字段，不提交）：出错后先别乱按、拍下屏幕；一个反馈只写一件事；写得越具体我们处理越快。

---

## 来源

- GitHub Docs: [Syntax for issue forms](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/syntax-for-issue-forms) / [Syntax for GitHub's form schema](https://docs.github.com/en/communities/using-templates-to-encourage-useful-issues-and-pull-requests/syntax-for-githubs-form-schema)
- 模板原文：[VS Code](https://github.com/microsoft/vscode/tree/main/.github/ISSUE_TEMPLATE)、[React](https://github.com/facebook/react/tree/main/.github/ISSUE_TEMPLATE)、[Rust](https://github.com/rust-lang/rust/tree/master/.github/ISSUE_TEMPLATE)、[Kubernetes](https://github.com/kubernetes/kubernetes/tree/master/.github/ISSUE_TEMPLATE)、[Electron](https://github.com/electron/electron/tree/main/.github/ISSUE_TEMPLATE)
- VS Code wiki: [Issues Triaging](https://github.com/microsoft/vscode/wiki/Issues-Triaging)、[Automated Issue Triaging](https://github.com/microsoft/vscode/wiki/Automated-Issue-Triaging)、[Submitting Bugs and Suggestions](https://github.com/microsoft/vscode/wiki/Submitting-Bugs-and-Suggestions)、[vscode-github-triage-actions](https://github.com/microsoft/vscode-github-triage-actions)
- Simon Tatham: [How to Report Bugs Effectively](https://www.chiark.greenend.org.uk/~sgtatham/bugs.html)
- Eric S. Raymond《提问的智慧》中文译本：[ryanhanwu/How-To-Ask-Questions-The-Smart-Way](https://github.com/ryanhanwu/How-To-Ask-Questions-The-Smart-Way/blob/main/README-zh_CN.md)
- Mozilla: [Bug Writing Guidelines](https://bugzilla.mozilla.org/page.cgi?id=bug-writing.html)
- GitHub: [ai-assessment-comment-labeler](https://github.com/github/ai-assessment-comment-labeler)、[Changelog 2025-09-05](https://github.blog/changelog/2025-09-05-github-actions-ai-labeler-and-moderator-with-the-github-models-inference-api/)、[gh-aw](https://github.github.com/gh-aw/)、[agentics issue-triage.md](https://github.com/githubnext/agentics/blob/main/workflows/issue-triage.md)
- Dosu: [Automating GitHub Issue Triage](https://dosu.dev/blog/automating-github-issue-triage)
- Anthropic: [Introducing Contextual Retrieval](https://www.anthropic.com/news/contextual-retrieval)
- Khatoonabadi et al., [Understanding the Helpfulness of Stale Bot for Pull-based Development](https://arxiv.org/abs/2305.18150)
- Wessel et al., [Don't Disturb Me: Challenges of Interacting with Software Bots on OSS Projects](https://arxiv.org/abs/2103.13950)
