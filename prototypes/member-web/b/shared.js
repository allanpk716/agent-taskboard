/* ============================================================
   mock #40 · 成员网页原型 —— 共享假数据与交互内核（a/b/c 三方向通用）
   全部数据为演示用假数据，所有“写操作”只在内存里假装生效。
   术语以 GLOSSARY.md 为准。
   ============================================================ */

const BASE = 'https://taskboard.lan';
const ME = { id: 'li', name: '李工', admin: true };
const NOW = '10-16 17:25';

/* ---------------- 假数据 ---------------- */

const MEMBERS = [
  { id: 'li',    name: '李工', role: '管理员', active: true,  since: '2026-06' },
  { id: 'wang',  name: '王工', role: '成员',   active: true,  since: '2026-06' },
  { id: 'chen',  name: '陈工', role: '成员',   active: true,  since: '2026-07' },
  { id: 'zhou',  name: '周工', role: '成员',   active: true,  since: '2026-08' },
  { id: 'wu',    name: '吴工', role: '管理员', active: true,  since: '2026-06' },
  { id: 'zheng', name: '郑工', role: '成员',   active: false, since: '2026-07' },
];

/* 标签字典：维度 → 值（带别名）。初始值从日报系统导入。 */
const DICT = {
  '产品线': [
    { v: '血液', a: [] }, { v: '尿液', a: [] }, { v: '生免', a: ['生化免疫'] },
    { v: '流式', a: [] }, { v: '人工智能相关', a: ['AI 相关'] },
  ],
  '系统': [
    { v: 'LIS', a: [] }, { v: '中间件', a: [] }, { v: '智慧实验室', a: [] },
    { v: '质控平台-溯源', a: ['溯源平台'] }, { v: '动物云平台', a: [] }, { v: '算法数据生命周期', a: [] },
  ],
  '机型': [
    { v: '7000VET', a: [] }, { v: '7300', a: [] }, { v: 'BH-7000VET', a: [] },
    { v: 'BH-7300', a: [] }, { v: 'BH-7680', a: [] }, { v: 'BM-10vet', a: ['BM10', '小血球'] },
    { v: 'RM-11', a: [] }, { v: 'SL-1000', a: ['SL1000'] }, { v: 'UD-1290', a: [] },
    { v: 'UD-1300', a: ['1300'] }, { v: 'UD-2300', a: ['UD2300', '2300'] },
    { v: 'US-1680', a: ['1680'] }, { v: 'US-1000', a: [] }, { v: 'US-500', a: [] },
  ],
  '部门': [
    { v: '事业一部', a: [] }, { v: '事业二部', a: [] }, { v: '事业三部', a: [] },
    { v: '事业四部', a: [] }, { v: '事业五部', a: [] }, { v: '事业七部', a: [] },
    { v: '事业八部', a: [] }, { v: '人工智能研究部', a: ['AI 部', '算法部'] }, { v: '集团', a: [] },
    { v: '研发部', a: [] }, { v: '国际营销中心', a: [] }, { v: '销售公司', a: [] },
    { v: '集团软件技术组', a: ['软件组'] },
  ],
  '算法': [
    { v: 'anomaly-det', a: ['异常检测'] },  // 维度初始为空，此值由成员 allow_new 新建
  ],
  '立项': [ { v: 'PRJ-2389', a: [] }, { v: 'PRJ-2411', a: [] }, { v: 'PRJ-2436', a: [] } ],
  '子项目': [ { v: 'SUB-2301', a: [] }, { v: 'SUB-2312', a: [] }, { v: 'SUB-2344', a: [] } ],
};
const PLAIN = ['待初筛', '加急', '客户演示'];

/* 模板（Markdown + 约定语法，#9） */
const TEMPLATES = [
  {
    key: 'bug-feedback', name: 'Bug 反馈', kind: '反馈', ver: 3, active: true,
    preset: ['待初筛'], updated: '10-12 15:40', by: '李工', used: 46,
    agentNote: '缺信息时先以普通回复向访客逐项追问，不要改写访客的说明；初筛完成后摘掉「待初筛」，补打维度标签并指派给对应同事。',
    hist: [
      { ver: 3, at: '10-12 15:40', by: '李工', note: '「出现得频繁吗」改为单选' },
      { ver: 2, at: '10-08 09:12', by: '王工', note: '补「影响程度」字段' },
      { ver: 1, at: '09-30 16:20', by: '李工', note: '初始版本（预置）' },
    ],
    source: '---\n名称: Bug 反馈\n类型: 反馈\n预设标签: [待初筛]\n---\n## 哪台仪器？ {维度:机型 必填}\n在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。\n## 序列号和软件版本是多少？ {选填}\n在“设置 → 关于本机”里可以看到。\n> 示例：序列号 7600CS-2023-0412，软件 V3.2.1\n## 什么时候发生的？ {必填}\n大概时间即可。\n> 示例：今天上午 9 点左右\n## 你做了哪些操作？ {必填}\n按顺序写，一步一行。\n> 示例：1. 开机自检完成 2. 进入质控界面 3. 点击“开始”\n## 实际看到了什么？ {必填}\n报警请把原文连同编号照抄，能拍照最好。\n> 示例：弹出“E-203 试剂余量不足”\n## 你期望看到什么？ {必填}\n本来应该发生什么。\n> 示例：正常开始质控测试\n## 出现得频繁吗？ {单选: 每次 | 经常 | 偶尔 | 仅一次 必填}\n## 影响程度 {选填}\n是否影响出报告、影响多少标本。\n## 你的猜测 {选填}\n和上面的现象分开写，猜错也没关系。\n\n（给 Agent 的说明）缺信息时先以普通回复向访客逐项追问，不要改写访客的说明；初筛完成后摘掉「待初筛」，补打维度标签并指派。',
    fields: [
      { key: 'model',  q: '哪台仪器？', type: 'dim', dim: '机型', req: true, guide: '在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。' },
      { key: 'ver',    q: '序列号和软件版本是多少？', type: 'text', req: false, guide: '在“设置 → 关于本机”里可以看到。', ex: '序列号 7600CS-2023-0412，软件 V3.2.1' },
      { key: 'when',   q: '什么时候发生的？', type: 'text', req: true, guide: '大概时间即可。', ex: '今天上午 9 点左右' },
      { key: 'steps',  q: '你做了哪些操作？', type: 'text', req: true, guide: '按顺序写，一步一行。', ex: '1. 开机自检完成 2. 进入质控界面 3. 点击“开始”' },
      { key: 'actual', q: '实际看到了什么？', type: 'text', req: true, guide: '报警请把原文连同编号照抄，能拍照最好。', ex: '弹出“E-203 试剂余量不足”' },
      { key: 'expect', q: '你期望看到什么？', type: 'text', req: true, guide: '本来应该发生什么。', ex: '正常开始质控测试' },
      { key: 'freq',   q: '出现得频繁吗？', type: 'radio', req: true, options: ['每次', '经常', '偶尔', '仅一次'], guide: '' },
      { key: 'impact', q: '影响程度', type: 'text', req: false, guide: '是否影响出报告、影响多少标本。', ex: '上午的 20 个标本全部延迟' },
      { key: 'guess',  q: '你的猜测', type: 'text', req: false, guide: '和上面的现象分开写，猜错也没关系。', ex: '可能是试剂瓶的液位传感器没识别到' },
    ],
  },
  {
    key: 'assign-task', name: '分配任务', kind: '任务', ver: 2, active: true,
    preset: [], updated: '10-10 11:05', by: '王工', used: 128,
    agentNote: '收到任务先核对验收标准；完成后用带「交付」标记的回复交回，等待创建者验收。',
    hist: [
      { ver: 2, at: '10-10 11:05', by: '王工', note: '验收标准改为必填' },
      { ver: 1, at: '09-30 16:20', by: '李工', note: '初始版本（预置）' },
    ],
    source: '---\n名称: 分配任务\n类型: 任务\n预设标签: []\n---\n## 目标 {必填}\n一两句话讲清要做什么、为什么。\n## 背景 {选填}\n对方需要知道的来龙去脉。\n## 交付物 {必填}\n做完后交回什么东西。\n> 示例：代码 + 单测 + 一页说明\n## 验收标准 {必填}\n写成可逐条核对的句子。\n## 期限 {选填}\n> 示例：10-24 前交付\n## 相关资料 {选填}\n链接或 NAS 路径。\n\n（给 Agent 的说明）收到任务先核对验收标准；完成后用带「交付」标记的回复交回，等待创建者验收。',
    fields: [
      { key: 'goal', q: '目标', type: 'text', req: true, guide: '一两句话讲清要做什么、为什么。', ex: '把中间件断线重连改成指数退避' },
      { key: 'background', q: '背景', type: 'text', req: false, guide: '对方需要知道的来龙去脉。', ex: '现场 LIS 网络抖动频繁，固定 5 秒重连会把服务打满' },
      { key: 'deliverables', q: '交付物', type: 'text', req: true, guide: '做完后交回什么东西。', ex: '代码 + 单测 + 一页说明' },
      { key: 'acceptance', q: '验收标准', type: 'text', req: true, guide: '写成可逐条核对的句子。', ex: '断网恢复后 30 秒内重连成功；连续运行一晚无内存增长' },
      { key: 'due', q: '期限', type: 'text', req: false, guide: '', ex: '10-24 前交付' },
      { key: 'refs', q: '相关资料', type: 'text', req: false, guide: '链接或 NAS 路径。', ex: '\\\\nas01\\共享\\PRJ-2411\\设计.docx' },
    ],
  },
  {
    key: 'inquiry', name: '问题咨询', kind: '任务', ver: 1, active: true,
    preset: [], updated: '09-30 16:20', by: '李工', used: 61,
    agentNote: '回答后请对方确认是否解决；需要当面沟通时约时间并写回工单。',
    hist: [ { ver: 1, at: '09-30 16:20', by: '李工', note: '初始版本（预置）' } ],
    source: '---\n名称: 问题咨询\n类型: 任务\n预设标签: []\n---\n## 想问什么 {必填}\n一次问一件事。\n## 背景 {选填}\n在哪里遇到的、影响什么。\n## 已经试过什么 {选填}\n## 期望回复时间 {选填}\n> 示例：本周五前\n\n（给 Agent 的说明）回答后请对方确认是否解决；需要当面沟通时约时间并写回工单。',
    fields: [
      { key: 'what', q: '想问什么', type: 'text', req: true, guide: '一次问一件事。', ex: 'BM-10vet 的质控品开瓶后能用多久？' },
      { key: 'background', q: '背景', type: 'text', req: false, guide: '在哪里遇到的、影响什么。', ex: '客户冰箱停电一晚' },
      { key: 'tried', q: '已经试过什么', type: 'text', req: false, guide: '', ex: '重新定标一次，仍然偏低' },
      { key: 'expect_reply', q: '期望回复时间', type: 'text', req: false, guide: '', ex: '本周五前' },
    ],
  },
  {
    key: 'install-check', name: '装机验收单', kind: '任务', ver: 1, active: false,
    preset: ['客户演示'], updated: '10-01 10:00', by: '吴工', used: 9,
    agentNote: '', hist: [ { ver: 1, at: '09-28 10:00', by: '吴工', note: '初始版本' } ],
    source: '---\n名称: 装机验收单\n类型: 任务\n预设标签: [客户演示]\n---\n## 现场信息 {必填}\n## 验收项目 {必填}\n## 遗留问题 {选填}\n',
    fields: [
      { key: 'site', q: '现场信息', type: 'text', req: true, guide: '', ex: '' },
      { key: 'items', q: '验收项目', type: 'text', req: true, guide: '', ex: '' },
      { key: 'open', q: '遗留问题', type: 'text', req: false, guide: '', ex: '' },
    ],
  },
];

const TOKENS = [
  { name: '初筛',   created: '09-28', last: '今天 09:41',  revoked: false },
  { name: '笔记本', created: '08-14', last: '10-02 09:40', revoked: true, revokedAt: '10-02' },
  { name: '工位机', created: '10-11', last: '昨天 18:55',  revoked: false },
];

const S = {
  members: MEMBERS, dict: DICT, plain: PLAIN, templates: TEMPLATES, tokens: TOKENS,
  backup: { state: 'ok', target: '\\\\nas01\\共享\\taskboard\\backup' },
  update: { server: 'v1.2.3', web: 'v1.4.0', webAvail: 'v1.4.1', webNote: '修复访客链接延期后未恢复可回复的问题；建单页维度标签支持别名输入。' },
};

/* 工单。waiting 由时间线推导（这里直接给出推导结果）。 */
const TICKETS = [
  {
    no: '20261016-008', seq: 1245, kind: '任务', title: '更新智慧实验室部署文档', status: 'open',
    template: null, creator: { member: '周工' }, assignee: '李工',
    labels: ['系统:智慧实验室'], waiting: { party: 'assignee', member: '李工', reason: 'sent_back' },
    created: '10-15 15:00', updated: '10-16 11:47', lastKind: 'member', deliverBefore: true, briefMd: '',
    attachments: [], guestLinks: [],
    timeline: [
      { ev: true, at: '10-15 15:00', actor: { member: '周工' }, text: '周工 创建了工单并指派给 李工' },
      { at: '10-16 09:12', actor: { member: '李工' }, deliver: true,
        body: '部署文档已更新到 v2.3，补全了双机热备和证书续期两节，请查收。' },
      { at: '10-16 11:47', actor: { member: '周工' },
        body: '两个问题：\n\n1. **双机切换**那步少了回切验证，要补上。\n2. 新加的**备份一节**没提 NAS 路径从 `\\\\nas02` 换到 `\\\\nas01` 这件事。\n\n麻烦改完再交付。' },
    ],
  },
  {
    no: '20261016-007', seq: 1244, kind: '反馈', title: 'BM-10vet 白细胞通道质控一直偏低', status: 'open',
    template: 'bug-feedback', creator: { guest: '钱敏', link: '事业五部-钱敏', intake: true }, assignee: null,
    labels: ['待初筛', '机型:BM-10vet'], waiting: { party: 'department', member: null, reason: 'unassigned' },
    created: '10-16 11:55', updated: '10-16 11:55', lastKind: 'guest',
    fields: [
      { key: 'model', q: '哪台仪器？', val: 'BM-10vet', type: 'dim', dim: '机型', req: true, guide: '在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。' },
      { key: 'ver', q: '序列号和软件版本是多少？', val: '', type: 'text', req: false, guide: '在“设置 → 关于本机”里可以看到。', ex: '序列号 7600CS-2023-0412，软件 V3.2.1' },
      { key: 'when', q: '什么时候发生的？', val: '今天早上 6:40 晨检开始', type: 'text', req: true, guide: '大概时间即可。', ex: '今天上午 9 点左右' },
      { key: 'steps', q: '你做了哪些操作？', val: '1. 做白细胞质控\n2. 连续三次都偏低\n3. 换了一瓶新质控品，仍然偏低', type: 'text', req: true, guide: '按顺序写，一步一行。', ex: '' },
      { key: 'actual', q: '实际看到了什么？', val: '质控结果 WBC 值比靶值低 12%，仪器提示“质控失控”。', type: 'text', req: true, guide: '', ex: '' },
      { key: 'expect', q: '你期望看到什么？', val: '质控在控，可以开始测标本。', type: 'text', req: true, guide: '', ex: '' },
      { key: 'freq', q: '出现得频繁吗？', val: '经常', type: 'radio', req: true, options: ['每次', '经常', '偶尔', '仅一次'], guide: '' },
      { key: 'impact', q: '影响程度', val: '上午的常规标本先放到备机上做', type: 'text', req: false, guide: '', ex: '' },
      { key: 'guess', q: '你的猜测', val: '', type: 'text', req: false, guide: '', ex: '' },
    ],
    attachments: [{ name: '质控曲线.png', size: '640 KB' }],
    guestLinks: [{ note: '事业五部-钱敏', expires: '11月15日', canEdit: true, state: '有效', key: 'kQ7x2W' }],
    timeline: [
      { ev: true, at: '10-16 11:55', actor: { guest: '钱敏', link: '事业五部-钱敏' },
        text: '钱敏（访客）通过反馈入口提交了这张工单，自动获得访客链接「事业五部-钱敏」（可填写说明）' },
    ],
  },
  {
    no: '20261016-005', seq: 1242, kind: '任务', title: '评审异常检测方案初稿', status: 'open',
    template: null, creator: { member: '李工' }, assignee: '周工',
    labels: ['算法:anomaly-det', '部门:人工智能研究部'], waiting: { party: 'creator', member: '李工', reason: 'assignee_replied' },
    created: '10-16 11:10', updated: '10-16 15:10', lastKind: 'agent', briefMd: '',
    attachments: [], guestLinks: [],
    timeline: [
      { ev: true, at: '10-16 11:10', actor: { member: '李工' }, text: '李工 创建了工单并指派给 周工' },
      { at: '10-16 15:10', actor: { member: '周工', agent: '深度' },
        body: '方案初稿在 `\\\\nas01\\共享\\PRJ-2436\\异常检测方案v0.3.docx`，有两个问题想先确认：\n\n1. 训练数据用哪个时间窗？最近 30 天还是全部历史？\n2. 阈值要不要做成**每个机型可配**？现在写死在全局配置里。' },
    ],
  },
  {
    no: '20261016-003', seq: 1240, kind: '任务', title: '整理 SL-1000 遗留问题清单', status: 'open',
    template: null, creator: { member: '李工' }, assignee: '陈工',
    labels: ['机型:SL-1000', '立项:PRJ-2389'], waiting: { party: 'creator', member: '李工', reason: 'delivered' },
    created: '10-16 10:02', updated: '10-16 14:22', lastKind: 'member', deliverBefore: true, briefMd: '',
    attachments: [], guestLinks: [],
    timeline: [
      { ev: true, at: '10-16 10:02', actor: { member: '李工' }, text: '李工 创建了工单并指派给 陈工' },
      { at: '10-16 14:22', actor: { member: '陈工' }, deliver: true,
        body: '清单整理完了，共 **23 项**，已按模块分组，标了责任人和当前状态：\n\n`\\\\nas01\\共享\\PRJ-2389\\遗留问题清单.xlsx`\n\n其中 4 项建议直接关闭，理由写在备注列，请过目。' },
    ],
  },
  {
    no: '20261015-009', seq: 1236, kind: '任务', title: '给中间件加断线重连的指数退避', status: 'open',
    template: 'assign-task', creator: { member: '王工', agent: '工位' }, assignee: '李工',
    labels: ['系统:中间件', '立项:PRJ-2411'], waiting: { party: 'assignee', member: '李工', reason: 'new' },
    created: '10-16 09:05', updated: '10-16 09:05', lastKind: 'agent',
    fields: [
      { key: 'goal', q: '目标', val: '中间件断线后目前是固定 5 秒重连，高峰期会把服务打满。改成指数退避（1s 起，封顶 60s），加随机抖动。', type: 'text', req: true, guide: '一两句话讲清要做什么、为什么。', ex: '' },
      { key: 'background', q: '背景', val: '现场 LIS 网络抖动频繁，一家医院高峰期出现连接风暴，影响了取号速度。', type: 'text', req: false, guide: '', ex: '' },
      { key: 'deliverables', q: '交付物', val: '代码 + 单测 + 一页说明', type: 'text', req: true, guide: '', ex: '' },
      { key: 'acceptance', q: '验收标准', val: '1. 断网恢复后 30 秒内重连成功\n2. 连续运行一晚无内存增长\n3. 退避参数可配置', type: 'text', req: true, guide: '', ex: '' },
      { key: 'due', q: '期限', val: '10-24 前交付', type: 'text', req: false, guide: '', ex: '' },
      { key: 'refs', q: '相关资料', val: '\\\\nas01\\共享\\PRJ-2411\\设计.docx', type: 'text', req: false, guide: '', ex: '' },
    ],
    attachments: [], guestLinks: [],
    timeline: [
      { ev: true, at: '10-16 09:05', actor: { member: '王工', agent: '工位' },
        text: '王工 的 Agent · 工位 用模板「分配任务」创建了工单并指派给 李工' },
    ],
  },
  {
    no: '20261015-007', seq: 1234, kind: '反馈', title: 'UD-2300 质控时报警 E-203，无法开始测试', status: 'open',
    template: 'bug-feedback', creator: { guest: '张三', link: '检验科-张三', intake: true }, assignee: '李工',
    labels: ['机型:UD-2300', '产品线:尿液'], waiting: { party: 'assignee', member: '李工', reason: 'guest_replied' },
    created: '10-15 09:32', updated: '10-16 16:40', lastKind: 'guest',
    fields: [
      { key: 'model', q: '哪台仪器？', val: 'UD-2300', type: 'dim', dim: '机型', req: true, guide: '在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。' },
      { key: 'ver', q: '序列号和软件版本是多少？', val: '', type: 'text', req: false, guide: '在“设置 → 关于本机”里可以看到。', ex: '序列号 7600CS-2023-0412，软件 V3.2.1' },
      { key: 'when', q: '什么时候发生的？', val: '10月15日上午 9:10 左右', type: 'text', req: true, guide: '大概时间即可。', ex: '' },
      { key: 'steps', q: '你做了哪些操作？', val: '1. 开机自检完成\n2. 进入质控界面\n3. 点击“开始”', type: 'text', req: true, guide: '', ex: '' },
      { key: 'actual', q: '实际看到了什么？', val: '屏幕弹出“E-203 试剂余量不足”，但试剂是早上刚换的新瓶。', type: 'text', req: true, guide: '', ex: '弹出“E-203 试剂余量不足”' },
      { key: 'expect', q: '你期望看到什么？', val: '正常开始质控测试。', type: 'text', req: true, guide: '', ex: '' },
      { key: 'freq', q: '出现得频繁吗？', val: '每次', type: 'radio', req: true, options: ['每次', '经常', '偶尔', '仅一次'], guide: '' },
      { key: 'impact', q: '影响程度', val: '', type: 'text', req: false, guide: '', ex: '' },
      { key: 'guess', q: '你的猜测', val: '', type: 'text', req: false, guide: '', ex: '' },
    ],
    attachments: [{ name: 'E203报警.jpg', size: '1.2 MB' }, { name: '仪器日志.zip', size: '8.4 MB' }],
    guestLinks: [
      { note: '检验科-张三', expires: '11月14日', canEdit: true, state: '有效', key: 'kX9f2Q' },
      { note: '检验科-刘主任', expires: '2027-01-14', canEdit: false, state: '有效', key: 'm3Rt8p' },
      { note: '检验科-实习同学', expires: '10月20日', canEdit: false, state: '已撤销', key: 'zZ04kL' },
    ],
    timeline: [
      { ev: true, at: '10-15 09:32', actor: { guest: '张三', link: '检验科-张三' },
        text: '张三（访客）通过反馈入口提交了这张工单，自动打上标签 待初筛、机型:UD-2300，自动获得访客链接「检验科-张三」（可填写说明）' },
      { at: '10-15 09:40', actor: { member: '李工', agent: '初筛' },
        body: '感谢反馈，已转给仪器组。为了尽快定位，还需要两项信息：\n\n1. **软件版本**：在“设置 → 关于本机”里，例如 `V3.2.1`\n2. **试剂瓶照片**：报警时试剂瓶在仪器上的样子，拍清楚瓶身标签\n\n可以直接在下方回复，或者点“补全说明”填进对应的栏目。' },
      { ev: true, at: '10-15 10:12', actor: { member: '李工', agent: '初筛' },
        text: '李工 的 Agent · 初筛 摘掉了标签 待初筛，补打了标签 产品线:尿液' },
      { ev: true, at: '10-15 10:12', actor: { member: '李工', agent: '初筛' }, text: '指派给 李工' },
      { ev: true, at: '10-16 08:30', actor: { member: '李工' }, text: '李工 创建了访客链接「检验科-刘主任」（90 天）' },
      { at: '10-16 16:40', actor: { guest: '张三', link: '检验科-张三' }, atts: ['试剂瓶.jpg'],
        body: '软件版本是 **V3.2.1**。照片在下面，瓶身标签朝外放的。另外今天下午又出现了两次，都是重新开始质控就恢复正常。' },
    ],
  },
  {
    no: '20261014-006', seq: 1229, kind: '任务', title: '支援 LIS 接口联调（周三上午）', status: 'open',
    template: null, creator: { member: '王工' }, assignee: '李工',
    labels: ['系统:LIS', '子项目:SUB-2301'], waiting: { party: 'assignee', member: '李工', reason: 'creator_replied' },
    created: '10-14 14:30', updated: '10-15 17:20', lastKind: 'member', briefMd: '',
    attachments: [], guestLinks: [],
    timeline: [
      { ev: true, at: '10-14 14:30', actor: { member: '王工' }, text: '王工 创建了工单并指派给 李工' },
      { at: '10-15 09:58', actor: { member: '李工' },
        body: '周三上午可以，我腾两小时。需要对方提前准备好**测试账号**和**报文样例**，不然到了现场干等。' },
      { at: '10-15 17:20', actor: { member: '王工' },
        body: '账号已经向对方申请，样例让对方今天给。联调就定**周三 10:00**，地点在他们机房。' },
    ],
  },
  {
    no: '20261012-002', seq: 1201, kind: '反馈', title: 'US-1680 开机自检卡在 60%', status: 'closed', closeReason: 'done',
    template: 'bug-feedback', creator: { guest: '赵总', link: '销售公司-赵总', intake: true }, assignee: '王工',
    labels: ['机型:US-1680', '产品线:血液'], waiting: { party: 'none', member: null, reason: '' },
    created: '10-12 10:14', updated: '10-14 08:50', lastKind: 'member',
    fields: [], briefMd: '',
    attachments: [], guestLinks: [{ note: '销售公司-赵总', expires: '11月11日', canEdit: false, state: '有效', key: 'pP5nD2' }],
    timeline: [
      { ev: true, at: '10-12 10:14', actor: { guest: '赵总', link: '销售公司-赵总' }, text: '赵总（访客）通过反馈入口提交了这张工单' },
      { at: '10-12 11:02', actor: { member: '王工', agent: '工位' }, body: '请提供仪器序列号，以及卡住时屏幕的照片。' },
      { at: '10-13 09:30', actor: { guest: '赵总', link: '销售公司-赵总' }, body: '序列号 1680-2024-0311，照片已上传。' },
      { at: '10-13 16:45', actor: { member: '王工' }, deliver: true, body: '定位到光路电机驱动板电容老化，今天已上门更换，自检恢复正常。请确认。' },
      { ev: true, at: '10-14 08:50', actor: { member: '王工' }, text: '王工 关闭了工单（完成）' },
    ],
  },
  {
    no: '20261010-004', seq: 1189, kind: '反馈', title: '希望报表支持自定义配色', status: 'closed', closeReason: 'wontfix',
    template: null, creator: { guest: 'Sam', link: '国际营销中心-Sam', intake: true }, assignee: '陈工',
    labels: ['产品线:生免'], waiting: { party: 'none', member: null, reason: '' },
    created: '10-10 13:20', updated: '10-11 09:05', lastKind: 'member', briefMd: '',
    attachments: [], guestLinks: [],
    timeline: [
      { ev: true, at: '10-10 13:20', actor: { guest: 'Sam', link: '国际营销中心-Sam' }, text: 'Sam（访客）通过反馈入口提交了这张工单' },
      { at: '10-10 16:40', actor: { member: '陈工' }, body: '这个要动底层渲染引擎，影响面比较大。目前的主题色遵循公司 VI，暂不开放自定义。' },
      { ev: true, at: '10-11 09:05', actor: { member: '陈工' }, text: '陈工 关闭了工单（不处理）' },
    ],
  },
  {
    no: '20261008-011', seq: 1156, kind: '反馈', title: '询问 UD-1300 试剂开放参数的范围', status: 'closed', closeReason: 'duplicate',
    template: null, creator: { guest: '张三', link: '检验科-张三' }, assignee: '李工',
    labels: ['机型:UD-1300'], waiting: { party: 'none', member: null, reason: '' },
    created: '10-08 15:02', updated: '10-08 15:40', lastKind: 'member', briefMd: '',
    attachments: [], guestLinks: [],
    timeline: [
      { ev: true, at: '10-08 15:02', actor: { guest: '张三', link: '检验科-张三' }, text: '张三（访客）提交了这张工单' },
      { ev: true, at: '10-08 15:40', actor: { member: '李工' }, text: '李工 关闭了工单（重复，重复于 20261005-002）' },
    ],
  },
];
S.tickets = TICKETS;

/* ---------------- 可变 UI 状态 ---------------- */
const UI = {
  cur: null,          // 当前打开的工单
  deliver: false,     // composer 的交付标记
  tlWho: 'all',       // 时间线 本人/Agent 筛选
  adminTab: 'members',
  list: { status: 'open', kind: '', assignee: '', waiting: '', who: 'all', q: '' },
  new: { step: 1, tpl: null, created: null, warnings: [], labels: [] },
};
window.UI = UI; window.S = S;

/* ---------------- 小工具 ---------------- */
function esc(s) { return String(s == null ? '' : s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])); }
function md(s) {
  return esc(s).split(/\n\n+/).map(p => {
    p = p.replace(/\*\*(.+?)\*\*/g, '<b>$1</b>').replace(/`(.+?)`/g, '<code>$1</code>');
    p = p.replace(/\\\\nas01\\[^<\s]+/g, m => `<span class="unc">${m}</span>`);
    if (/^\d+\. /.test(p)) return '<ol>' + p.split('\n').map(l => '<li>' + l.replace(/^\d+\. /, '') + '</li>').join('') + '</ol>';
    return '<p>' + p.replace(/\n/g, '<br>') + '</p>';
  }).join('');
}
function norm(s) { return String(s).toLowerCase().replace(/[\s\-_]/g, ''); }
function toast(msg, ms) {
  let t = document.getElementById('toast');
  if (!t) { t = document.createElement('div'); t.id = 'toast'; document.body.appendChild(t); }
  t.innerHTML = msg; t.className = 'show';
  clearTimeout(t._h); t._h = setTimeout(() => t.className = '', ms || 2600);
}
function copyText(txt) {
  const done = () => toast('✓ 已复制到剪贴板');
  if (navigator.clipboard) navigator.clipboard.writeText(txt).then(done, () => fallback());
  else fallback();
  function fallback() {
    const ta = document.createElement('textarea'); ta.value = txt; document.body.appendChild(ta);
    ta.select(); try { document.execCommand('copy'); done(); } catch (e) { toast('复制失败，请长按选择文字复制'); }
    ta.remove();
  }
}
function modal(html, opts) {
  closeModal();
  const m = document.createElement('div'); m.className = 'mask';
  m.innerHTML = `<div class="modal${opts && opts.wide ? ' wide' : ''}">${html}</div>`;
  m.addEventListener('click', e => { if (e.target === m) closeModal(); });
  document.body.appendChild(m);
  return m;
}
function closeModal() { document.querySelectorAll('.mask').forEach(m => m.remove()); }
function askConfirm(title, body, yesLabel, onYes, danger) {
  const m = modal(`<div class="mtitle">${esc(title)}</div><div class="mbody">${body}</div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span>
    <button class="btn${danger ? ' danger' : ''}" data-x="yes">${esc(yesLabel)}</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelector('[data-x=yes]').onclick = () => { closeModal(); onYes(); };
}
function nav(h) { if (location.hash === h) renderApp(); else location.hash = h; }
window.addEventListener('hashchange', () => { renderApp(); window.scrollTo(0, 0); });

/* ---------------- 领域工具 ---------------- */
const REASONS = {
  new: '新指派给你', delivered: '已交付，待你验收', assignee_replied: '指派人回复了，待你答复',
  sent_back: '发起人退回修改', creator_replied: '发起人回复了', guest_replied: '访客回复了',
  unassigned: '尚未指派',
};
const CLOSE_TEXT = { done: '完成', wontfix: '不处理', duplicate: '重复' };
function whoName(a) {
  if (!a) return '系统';
  if (a.guest !== undefined) return a.agent ? `${a.guest}的 Agent（自报）` : `${a.guest}（访客）`;
  if (a.member) return a.agent ? `${a.member} 的 Agent · ${a.agent}` + (a.dead ? '（已停用）' : '') : a.member;
  return '系统';
}
function actorKind(a) {
  if (!a) return 'system';
  if (a.guest !== undefined) return 'guest';
  if (a.member) return a.agent ? 'agent' : 'member';
  return 'system';
}
function creatorName(t) {
  if (t.creator.guest !== undefined) return t.creator.intake ? `访客 ${t.creator.link}` : `访客 ${t.creator.link}`;
  return whoName(t.creator);
}
function creatorMember(t) { return t.creator.member || null; }
function waitText(t) {
  if (t.status === 'closed') return '无（已关闭）';
  switch (t.waiting.party) {
    case 'assignee': return `${t.assignee}（指派人）`;
    case 'creator': return `${creatorMember(t) || '创建者'}（发起人）`;
    case 'guest': return '访客';
    case 'department': return '本部门（未指派）';
    default: return '—';
  }
}
function findTicket(no) { return S.tickets.find(t => t.no === no || '#' + t.seq === no || String(t.seq) === no); }
function inboxItems() { return S.tickets.filter(t => t.status === 'open' && t.waiting.member === ME.name); }
function tplByKey(k) { return S.templates.find(x => x.key === k); }
function memberActive() { return S.members.filter(m => m.active); }

/* 标签解析：exact → alias → fuzzy */
function resolveLabel(dim, q) {
  const list = S.dict[dim]; if (!list) return null;
  const nq = norm(q);
  for (const it of list) if (it.v === q) return { value: it.v, via: 'exact' };
  for (const it of list) if (it.a.some(al => norm(al) === nq)) return { value: it.v, via: 'alias', alias: it.a.find(al => norm(al) === nq) };
  for (const it of list) { const nv = norm(it.v); if ((nv.includes(nq) || nq.includes(nv)) && nq.length >= 2 && nv.length >= 2) return { value: it.v, via: 'fuzzy' }; }
  return null;
}
function suggestValues(dim, q) {
  const nq = norm(q); if (!nq) return [];
  const pref = (a, b) => { let i = 0; while (i < a.length && i < b.length && a[i] === b[i]) i++; return i; };
  return (S.dict[dim] || []).map(it => it.v).filter(v => {
    const nv = norm(v);
    if (nv.includes(nq) || nq.includes(nv)) return true;
    return pref(nv, nq) >= 2;  // 前两三个字符对得上也算近似
  }).slice(0, 4);
}
function chipClass(l) {
  const dims = Object.keys(S.dict);
  const d = l.includes(':') ? l.split(':')[0] : '';
  const i = dims.indexOf(d);
  return 'chip c' + (i < 0 ? 0 : (i % 6) + 1);
}
function labelChips(t, removable) {
  return t.labels.map(l => removable
    ? `<span class="${chipClass(l)}" data-l="${esc(l)}">${esc(l)}<i data-act="label-rm" data-l="${esc(l)}" title="移除">×</i></span>`
    : `<span class="${chipClass(l)}">${esc(l)}</span>`).join('');
}

/* 加标签（带字典校验的假逻辑） */
function tryAddLabel(t, input, allowNew) {
  const s = String(input).trim(); if (!s) return { ok: false, msg: '请输入标签，例如 机型:UD-2300 或 待初筛' };
  const i = s.indexOf(':');
  const dim = i < 0 ? null : s.slice(0, i), val = i < 0 ? s : s.slice(i + 1);
  if (!dim) {
    if (!S.plain.includes(val)) { S.plain.push(val); t.labels.push(val); return { ok: true, note: `已新建不带维度的标签「${val}」` }; }
    if (t.labels.includes(val)) return { ok: false, msg: '这张工单已经有这个标签了' };
    t.labels.push(val); return { ok: true };
  }
  if (!S.dict[dim]) return { ok: false, code: 'unknown_dimension', msg: `422 unknown_dimension：维度「${dim}」不存在`, hint: '新维度只能由管理员添加（管理页 → 标签治理）' };
  const r = resolveLabel(dim, val);
  if (r) {
    const full = `${dim}:${r.value}`;
    if (t.labels.includes(full)) return { ok: false, msg: '这张工单已经有这个标签了' };
    t.labels.push(full);
    t.timeline.push({ ev: true, at: NOW, actor: { member: ME.name }, text: `${ME.name} 打上了标签 ${full}${r.via === 'alias' ? `（由别名「${r.alias}」匹配）` : r.via === 'fuzzy' ? '（模糊匹配）' : ''}` });
    return { ok: true, note: r.via === 'alias' ? `别名「${r.alias}」已换成标准值 ${full}` : r.via === 'fuzzy' ? `按近似值匹配到 ${full}` : null };
  }
  if (allowNew) {
    S.dict[dim].push({ v: val, a: [] });
    t.labels.push(`${dim}:${val}`);
    t.timeline.push({ ev: true, at: NOW, actor: { member: ME.name }, text: `${ME.name} 新建了字典值 ${dim}:${val} 并打上标签` });
    return { ok: true, note: `已新建字典值 ${dim}:${val}` };
  }
  return {
    ok: false, code: 'unknown_label_value',
    msg: `422 unknown_label_value：字典里没有 ${dim}:${val}`,
    hint: '勾选「允许新建字典值」后重试，或从下面的建议中选择',
    suggestions: suggestValues(dim, val),
    dim,
  };
}

/* ---------------- 写操作（假装生效） ---------------- */
function pushEvent(t, text, actor) { t.timeline.push({ ev: true, at: NOW, actor: actor || { member: ME.name }, text }); }
function recomputeAfterReply(t, deliver) {
  const me = ME.name;
  if (deliver) { t.deliverBefore = true; t.waiting = { party: 'creator', member: creatorMember(t), reason: 'delivered' }; return; }
  const c = creatorMember(t);
  if (t.assignee === me && c && c !== me) { t.waiting = { party: 'creator', member: c, reason: 'assignee_replied' }; }
  else if (c === me && t.assignee && t.assignee !== me) { t.waiting = { party: 'assignee', member: t.assignee, reason: t.deliverBefore ? 'sent_back' : 'creator_replied' }; }
  else if (c === me && !t.assignee) { t.waiting = { party: 'department', member: null, reason: 'unassigned' }; }
  else if (!c) { t.waiting = { party: 'guest', member: null, reason: '' }; }
  /* 第三位成员回复：不改变轮次 */
}
function sendReply() {
  const t = UI.cur; if (!t) return;
  const body = (document.getElementById('reply-body') || {}).value || '';
  if (!body.trim()) { toast('422 empty_reply：空回复会被拒绝（演示）'); return; }
  if (UI.deliver && t.assignee !== ME.name) { toast('403 deliver_assignee_only：只有指派人一方能交付（演示）'); return; }
  const wasDeliver = UI.deliver;
  t.timeline.push({ at: NOW, actor: { member: ME.name }, body, deliver: UI.deliver });
  t.updated = NOW; t.lastKind = 'member';
  recomputeAfterReply(t, wasDeliver);
  UI.deliver = false;
  refresh();
  toast(wasDeliver ? '✓ 已交付，轮到创建者验收' : '✓ 已发送');
}
function acceptAndClose() {
  const t = UI.cur; if (!t) return;
  pushEvent(t, `${ME.name} 验收通过，关闭了工单（完成）`);
  t.status = 'closed'; t.closeReason = 'done'; t.waiting = { party: 'none', member: null, reason: '' }; t.updated = NOW;
  refresh(); toast('✓ 已验收并关闭');
}
function closeTicket(t, reason, dupOf) {
  pushEvent(t, `${ME.name} 关闭了工单（${CLOSE_TEXT[reason]}${reason === 'duplicate' ? `，重复于 ${dupOf || '?'}` : ''}）`);
  t.status = 'closed'; t.closeReason = reason; t.waiting = { party: 'none', member: null, reason: '' }; t.updated = NOW;
  refresh(); toast('✓ 已关闭');
}
function reopenTicket(t) {
  pushEvent(t, `${ME.name} 重新开启了工单`);
  t.status = 'open'; t.closeReason = null; t.updated = NOW;
  t.waiting = t.assignee ? { party: 'assignee', member: t.assignee, reason: t.lastKind === 'guest' ? 'guest_replied' : 'new' } : { party: 'department', member: null, reason: 'unassigned' };
  refresh(); toast('已重新开启。请回复说明原因，让对方知道下一步做什么。');
}
function assignTicket(t, name) {
  const from = t.assignee;
  pushEvent(t, from ? `改派：${from} → ${name}` : `指派给 ${name}`);
  t.assignee = name; t.updated = NOW;
  t.waiting = { party: 'assignee', member: name, reason: 'new' };
  return `${ME.name}：工单「${t.title}」转给你了，让你的 Agent 读一下 ${BASE}/t/${t.no}（它也会出现在你的收件箱里）`;
}
function createTicket(d) {
  const seq = Math.max(...S.tickets.map(t => t.seq)) + 1;
  const date = '20261016';
  const maxN = Math.max(0, ...S.tickets.filter(t => t.no.startsWith(date)).map(t => +t.no.slice(9)));
  const n = maxN + 1;
  const no = `${date}-${String(n).padStart(3, '0')}`;
  const tpl = d.template ? tplByKey(d.template) : null;
  const labels = [...d.labels];
  if (tpl) { for (const p of tpl.preset) if (!labels.includes(p)) labels.push(p); }
  const fields = (tpl ? tpl.fields.map(f => ({ ...f, val: d.fields[f.key] || '' })) : []);
  if (tpl) for (const f of fields) if (f.type === 'dim' && f.val && f.val !== '不确定') { const l = `${f.dim}:${f.val}`; if (!labels.includes(l)) labels.push(l); }
  const warnings = (tpl ? tpl.fields.filter(f => f.req && !(d.fields[f.key] || '').trim()).map(f => f.q) : []);
  const t = {
    no, seq, kind: tpl ? tpl.kind : d.kind, title: d.title, status: 'open', closeReason: null,
    template: d.template || null, creator: { member: ME.name }, assignee: d.assignee || null,
    labels, waiting: (d.assignee && d.assignee !== ME.name) ? { party: 'assignee', member: d.assignee, reason: 'new' } : { party: 'department', member: null, reason: 'unassigned' },
    created: NOW, updated: NOW, lastKind: 'member', deliverBefore: false,
    fields, briefMd: tpl ? '' : (d.briefMd || ''), attachments: [], guestLinks: [],
    timeline: [{ ev: true, at: NOW, actor: { member: ME.name }, text: `${ME.name} ${tpl ? `用模板「${tpl.name}」创建了工单` : '创建了工单'}${d.assignee ? `并指派给 ${d.assignee}` : ''}` }],
  };
  S.tickets.unshift(t);
  const share = (d.assignee && d.assignee !== ME.name)
    ? `${ME.name}：给你派了一张工单「${d.title}」，让你的 Agent 读一下 ${BASE}/t/${no}（它也会出现在你的收件箱里）` : null;
  return { ticket: t, warnings, share };
}

/* ---------------- 对话流渲染（三方向共用结构，样式各自定） ---------------- */
function avHTML(a) {
  const k = actorKind(a);
  const label = k === 'agent' ? '🤖' : k === 'guest' ? (a.link || '访').slice(-2, ) : (a.member || '系').slice(0, 1);
  const cls = k === 'agent' ? 'a-av agent' : k === 'guest' ? 'a-av guest' : 'a-av member';
  return `<div class="${cls}">${esc(k === 'agent' ? 'AI' : label.slice(0, 2))}</div>`;
}
function tlFilterHTML() {
  const f = UI.tlWho;
  return `<div class="tlfilter"><span class="lbl">操作方</span>
    ${[['all', '全部'], ['member', '本人'], ['agent', 'Agent']].map(([v, n]) =>
      `<button class="fchip${f === v ? ' on' : ''}" data-act="tl-who" data-v="${v}">${n}</button>`).join('')}
    <span class="hint">（访客与系统事件始终显示；成员 Agent 的回复算它所属成员一方）</span></div>`;
}
function briefHTML(t) {
  const who = creatorName(t);
  let inner;
  if (t.fields && t.fields.length) {
    inner = t.fields.map(f => `<div class="bf"><div class="bq">${esc(f.q)}${f.req ? '<i class="req">必填</i>' : ''}</div>
      <div class="bv${f.val ? '' : ' empty'}">${f.val ? md(f.val) : '（未填写）'}</div></div>`).join('');
  } else {
    inner = md(t.briefMd || '（无说明）');
  }
  return `<div class="tl-row brief">${avHTML(t.creator)}
    <div class="tl-card"><div class="who"><b>${esc(who)}</b> · 说明${t.template ? ' · 模板 ' + esc(tplByKey(t.template).name) : ''}</div>
    ${inner}
    ${t.attachments.length ? `<div class="atts">${t.attachments.map(a => `<span class="att">📎 ${esc(a.name)} · ${esc(a.size)}</span>`).join('')}</div>` : ''}
    </div></div>`;
}
function timelineHTML(t) {
  const who = UI.tlWho;
  return t.timeline.map(e => {
    if (e.ev) {
      if (who !== 'all' && actorKind(e.actor) !== 'system' && false) return '';
      return `<div class="tl-event"><span>${esc(whoName(e.actor))} · ${esc(e.text)} · ${esc(e.at)}</span></div>`;
    }
    const k = actorKind(e.actor);
    if (who !== 'all' && (k === 'member' ? 'member' : k) !== who) return '';
    return `<div class="tl-row">${avHTML(e.actor)}
      <div class="tl-card"><div class="who"><b>${esc(whoName(e.actor))}</b> · ${esc(e.at)}${e.deliver ? ' <span class="badge deliver">交付</span>' : ''}</div>
      ${md(e.body || '')}
      ${e.atts && e.atts.length ? `<div class="atts">${e.atts.map(a => `<span class="att">📎 ${esc(a)}</span>`).join('')}</div>` : ''}
      </div></div>`;
  }).join('');
}
function conversationHTML(t) { return tlFilterHTML() + briefHTML(t) + timelineHTML(t); }

function statusBadge(t) {
  if (t.status === 'closed') return `<span class="badge closed">已关闭 · ${CLOSE_TEXT[t.closeReason] || ''}</span>`;
  if (t.waiting.member === ME.name) return `<span class="badge myturn">● 现在轮到你</span>`;
  return `<span class="badge waiting">等待 ${esc(waitText(t))}</span>`;
}
function composerHTML(t) {
  const canDeliver = t.status === 'open' && t.assignee === ME.name;
  return `<div class="composer">
    <textarea id="reply-body" placeholder="写回复（Markdown），可以直接粘贴截图…"></textarea>
    <div class="crow">
      <button class="btn ghost sm" data-act="att-demo">📎 附件</button>
      <button class="btn ghost sm${UI.deliver ? ' on' : ''}" data-act="deliver-toggle" ${canDeliver ? '' : 'disabled title="只有指派人一方能交付"'}>${UI.deliver ? '✓ 作为交付发送' : '标记为交付'}</button>
      <span class="hint">回复会记入时间线</span><span class="sp"></span>
      <button class="btn" data-act="reply-send">发送</button>
    </div></div>`;
}
function acceptPanelHTML(t) {
  if (t.status !== 'open' || t.waiting.member !== ME.name || t.waiting.reason !== 'delivered') return '';
  const dl = [...t.timeline].reverse().find(e => e.deliver);
  return `<div class="accept">
    <div>📦 <b>${esc(dl ? whoName(dl.actor) : '指派人')}</b> 已交付，等待你的验收${dl ? ' · ' + esc(dl.at) : ''}</div>
    <div class="btns"><button class="btn" data-act="accept">验收通过，关闭（完成）</button>
    <span class="hint">验收不通过：直接在下面回复说明问题，工单会退回指派人。</span></div></div>`;
}

/* ---------------- 弹窗：打标签 / 指派 / 访客链接 / 关闭 ---------------- */
function openLabelDialog(t) {
  const m = modal(`<div class="mtitle">打标签 · ${esc(t.no)}</div>
    <div class="mbody">
      <div class="lblrow" id="lbl-cur">${labelChips(t, true) || '<span class="hint">还没有标签</span>'}</div>
      <div class="inrow"><input id="lbl-input" placeholder="机型:UD-2300 或 待初筛（试试别名 2300 / BM10）">
      <button class="btn" id="lbl-add">添加</button></div>
      <label class="ckrow"><input type="checkbox" id="lbl-new"> 允许新建字典值（allow_new）</label>
      <div class="hint">值不在字典会被拒绝并给出近似值；写别名会自动换成标准值；不带维度的标签可随手新建；新维度只能由管理员添加。</div>
      <div id="lbl-err"></div>
    </div>`);
  const err = m.querySelector('#lbl-err');
  const doAdd = () => {
    err.innerHTML = '';
    const r = tryAddLabel(t, m.querySelector('#lbl-input').value, m.querySelector('#lbl-new').checked);
    if (!r.ok) {
      let sugg = '';
      if (r.suggestions && r.suggestions.length) sugg = `<div class="sugg">建议：${r.suggestions.map(v =>
        `<button class="fchip" data-s="${esc(r.dim + ':' + v)}">${esc(r.dim + ':' + v)}</button>`).join('')}</div>`;
      err.innerHTML = `<div class="errbox">${esc(r.msg)}${r.hint ? `<div class="hint">${esc(r.hint)}</div>` : ''}${sugg}</div>`;
      err.querySelectorAll('[data-s]').forEach(b => b.onclick = () => { m.querySelector('#lbl-input').value = b.dataset.s; doAdd(); });
      return;
    }
    m.querySelector('#lbl-input').value = '';
    m.querySelector('#lbl-cur').innerHTML = labelChips(t, true) || '<span class="hint">还没有标签</span>';
    wireRemove();
    refresh();
    if (r.note) toast(r.note);
  };
  const wireRemove = () => {
    m.querySelectorAll('[data-act=label-rm]').forEach(x => x.onclick = () => {
      const l = x.dataset.l;
      t.labels = t.labels.filter(y => y !== l);
      pushEvent(t, `${ME.name} 摘掉了标签 ${l}`);
      x.closest('.chip').remove();
      refresh();
    });
  };
  m.querySelector('#lbl-add').onclick = doAdd;
  m.querySelector('#lbl-input').addEventListener('keydown', e => { if (e.key === 'Enter') doAdd(); });
  wireRemove();
}
function openAssignDialog(t) {
  const opts = memberActive().map(mm => `<option value="${esc(mm.name)}">${esc(mm.name)}（${mm.role}）</option>`).join('');
  const m = modal(`<div class="mtitle">${t.assignee ? '改派' : '指派'} · ${esc(t.no)}</div>
    <div class="mbody">
      <label class="fldlab">指派给（只能指派成员，成员 Agent 不能被指派）</label>
      <select id="as-sel" class="sel">${t.assignee ? `<option value="">— 选择新指派人 —</option>` : ''}${opts}</select>
      <div class="hint">改派后，原指派人和新指派人的收件箱都会变化。</div>
    </div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span><button class="btn" data-x="yes" disabled>确认${t.assignee ? '改派' : '指派'}</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  const yes = m.querySelector('[data-x=yes]');
  m.querySelector('#as-sel').onchange = e => { yes.disabled = !e.target.value; };
  yes.onclick = () => {
    const name = m.querySelector('#as-sel').value;
    const share = assignTicket(t, name);
    closeModal(); refresh();
    const mm = modal(`<div class="mtitle">已指派给 ${esc(name)}</div>
      <div class="mbody"><div class="hint">改派响应里带一句可直接转发的 share_text：</div>
      <div class="linkbox">${esc(share)}</div></div>
      <div class="mrow"><button class="btn ghost" data-x="c">关闭</button><span class="sp"></span><button class="btn" data-x="cp">复制 share_text</button></div>`);
    mm.querySelector('[data-x=c]').onclick = closeModal;
    mm.querySelector('[data-x=cp]').onclick = () => copyText(share);
  };
}
function openLinksDialog(t) {
  const rows = t.guestLinks.map((g, i) => `<tr>
    <td>${esc(g.note)}${g.canEdit ? ' <span class="mini">可填写说明</span>' : ''}</td>
    <td>${esc(g.expires)}${g.state !== '有效' ? ` · ${esc(g.state)}` : ''}</td>
    <td>${g.state === '有效'
      ? `<button class="btn sm ghost" data-glk="${i}" data-op="ext">延期 30 天</button> <button class="btn sm ghost" data-glk="${i}" data-op="rvk">撤销</button>`
      : '<span class="hint">已失效，不能延期</span>'}</td></tr>`).join('');
  const m = modal(`<div class="mtitle">访客链接 · ${esc(t.no)}</div>
    <div class="mbody">
      <table class="mini-tbl">${rows || '<tr><td class="hint">还没有访客链接</td></tr>'}</table>
      <div class="sec-t">创建新链接</div>
      <div class="inrow"><input id="gl-note" placeholder="备注（必填），如：检验科-张三"></div>
      <div class="inrow"><select id="gl-days" class="sel"><option value="7">7 天</option><option value="30" selected>30 天（默认）</option><option value="90">90 天</option></select>
      <label class="ckrow" style="margin:0"><input type="checkbox" id="gl-edit"> 可填写说明</label></div>
      <div class="hint">没有永久有效的选项；链接的创建、撤销和延期都会记入时间线。</div>
      <div id="gl-out"></div>
    </div>
    <div class="mrow"><button class="btn ghost" data-x="no">关闭</button><span class="sp"></span><button class="btn" data-x="yes">创建</button></div>`, { wide: true });
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelectorAll('[data-glk]').forEach(b => b.onclick = () => {
    const g = t.guestLinks[+b.dataset.glk], op = b.dataset.op;
    if (op === 'rvk') askConfirm('撤销访客链接', `撤销「${esc(g.note)}」？撤销立即生效，对方再打开会看到链接失效页。`, '撤销', () => {
      g.state = '已撤销'; pushEvent(t, `${ME.name} 撤销了访客链接「${g.note}」`); refresh(); openLinksDialog(t);
    }, true);
    else { g.expires = '（+30 天）'; pushEvent(t, `${ME.name} 延期了访客链接「${g.note}」（+30 天）`); refresh(); toast('✓ 已延期 30 天'); openLinksDialog(t); }
  });
  m.querySelector('[data-x=yes]').onclick = () => {
    const note = m.querySelector('#gl-note').value.trim();
    if (!note) { m.querySelector('#gl-out').innerHTML = '<div class="errbox">422 note_required：备注必填，写上给谁用的，如「检验科-张三」</div>'; return; }
    const days = m.querySelector('#gl-days').value, canEdit = m.querySelector('#gl-edit').checked;
    const key = Math.random().toString(36).slice(2, 8);
    t.guestLinks.push({ note, expires: `+${days} 天`, canEdit, state: '有效', key });
    pushEvent(t, `${ME.name} 创建了访客链接「${note}」（${days} 天${canEdit ? '，可填写说明' : ''}）`);
    refresh();
    m.querySelector('#gl-out').innerHTML = `<div class="okbox">✓ 已创建。<div class="linkbox">${BASE}/g/${key}<br><button class="btn sm" onclick="copyText('${BASE}/g/${key}')">复制链接</button></div></div>`;
  };
}
function openCloseDialog(t) {
  const m = modal(`<div class="mtitle">关闭工单 · ${esc(t.no)}</div>
    <div class="mbody">
      <label class="ckrow"><input type="radio" name="cr" value="done" checked> 完成 —— 验收通过，工作闭环</label>
      <label class="ckrow"><input type="radio" name="cr" value="wontfix"> 不处理 —— 说明理由后结束</label>
      <label class="ckrow"><input type="radio" name="cr" value="duplicate"> 重复 —— 注明重复于哪一张</label>
      <div class="inrow" id="dup-row" style="display:none"><input id="dup-of" placeholder="重复于哪张工单，如 20261005-002"></div>
      <div class="hint">关闭后访客链接变为只读；重新开启后恢复。工单永远不能删除。</div>
    </div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span><button class="btn" data-x="yes">关闭工单</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelectorAll('[name=cr]').forEach(r => r.onchange = () => { m.querySelector('#dup-row').style.display = m.querySelector('[name=cr]:checked').value === 'duplicate' ? '' : 'none'; });
  m.querySelector('[data-x=yes]').onclick = () => {
    const reason = m.querySelector('[name=cr]:checked').value;
    if (reason === 'duplicate' && !m.querySelector('#dup-of').value.trim()) { toast('422 duplicate_of_required：重复关闭必须注明重复于哪一张'); return; }
    closeModal(); closeTicket(t, reason, m.querySelector('#dup-of') ? m.querySelector('#dup-of').value.trim() : '');
  };
}

/* ---------------- 建单流程 ---------------- */
function newTicketStep1HTML() {
  const cards = S.templates.filter(x => x.active).map(tp => `
    <div class="tplpick" data-act="new-tpl" data-k="${tp.key}">
      <div class="tp-name">${esc(tp.name)} <span class="badge kind">${esc(tp.kind)}</span></div>
      <div class="hint">v${tp.ver} · 已用 ${tp.used} 次${tp.preset.length ? ' · 预设 ' + tp.preset.map(esc).join('、') : ''}</div>
      <div class="hint">${tp.fields.map(f => esc(f.q) + (f.req ? '*' : '')).join(' · ')}</div>
    </div>`).join('');
  return `<div class="page new">
    <div class="sec-t">用模板创建（类型由模板决定，字段逐项填写）</div>
    ${cards}
    <div class="sec-t">不用模板（直接写标题和一整块 Markdown 说明）</div>
    <div class="tplpick" data-act="new-free"><div class="tp-name">自由格式</div>
      <div class="hint">适合简单的事：标题 + Markdown 说明，类型自己选（任务 / 反馈）</div></div>
  </div>`;
}
function fieldInputHTML(f) {
  const id = 'nf-' + f.key;
  let ctl;
  if (f.type === 'dim') {
    const opts = (S.dict[f.dim] || []).map(it => `<option${it.v === f.val ? ' selected' : ''}>${esc(it.v)}</option>`).join('');
    ctl = `<select class="sel" id="${id}"><option value="">请选择</option>${opts}<option>不确定</option></select>`;
  } else if (f.type === 'radio') {
    ctl = `<div class="radios">${f.options.map(o => `<label><input type="radio" name="${id}" value="${esc(o)}"${o === f.val ? ' checked' : ''}> ${esc(o)}</label>`).join('')}</div>`;
  } else {
    ctl = `<textarea id="${id}" placeholder="示例：${esc(f.ex || '')}"></textarea>
      <div class="hint tools">🖼 文本字段可以粘贴或上传图片（演示略）</div>`;
  }
  return `<div class="fld"><label>${esc(f.q)}${f.req ? '<i class="req">必填</i>' : ''}</label>
    ${f.guide ? `<div class="guide">${esc(f.guide)}</div>` : ''}${ctl}</div>`;
}
function newTicketFormHTML() {
  const tpl = UI.new.tpl ? tplByKey(UI.new.tpl) : null;
  const memberOpts = memberActive().map(mm => `<option value="${esc(mm.name)}">${esc(mm.name)}</option>`).join('');
  return `<div class="page new">
    <div class="crumb"><a href="#/new">← 换一种方式</a> ${tpl ? '用模板「' + esc(tpl.name) + '」创建' : '自由格式创建'}</div>
    <div class="card form">
      <div class="fld"><label>标题<i class="req">必填</i></label>
        <div class="guide">一句话讲清这件事。</div><input id="nt-title" placeholder="例如：${tpl ? esc(tpl.kind === '反馈' ? 'UD-2300 质控时报警 E-203' : '整理 SL-1000 遗留问题清单') : '……'}"></div>
      ${tpl ? tpl.fields.map(fieldInputHTML).join('') : `
        <div class="fld"><label>类型</label><div class="radios">
          <label><input type="radio" name="nt-kind" value="任务" checked> 任务（派出去请人完成）</label>
          <label><input type="radio" name="nt-kind" value="反馈"> 反馈（外部提进来请部门处理）</label></div></div>
        <div class="fld"><label>说明（一整块 Markdown）</label>
          <div class="guide">背景、要求、验收标准都可以写在这里。</div>
          <textarea id="nt-brief" style="min-height:120px"></textarea></div>`}
      <div class="fld"><label>维度标签<i class="req">至少一个维度</i></label>
        <div class="guide">至少打一个「维度:值」标签才能提交；同一维度可以打多个值；试试写别名（2300 → 机型:UD-2300）。</div>
        <div class="lblrow" id="nt-labels"></div>
        <div class="inrow"><input id="nt-lbl-in" placeholder="机型:UD-2300、系统:中间件、待初筛 …"><button class="btn ghost" id="nt-lbl-add" type="button">添加</button></div>
        <div id="nt-lbl-err"></div></div>
      <div class="fld"><label>指派给</label>
        <div class="guide">可以现在指派，也可以建好之后再说；只能指派成员。</div>
        <select class="sel" id="nt-assignee"><option value="">暂不指派</option>${memberOpts}</select></div>
    </div>
    <div id="nt-submit-err"></div>
    <div class="submitrow"><button class="btn big" data-act="new-submit">创建工单</button></div>
  </div>`;
}
function wireNewForm() {
  const add = document.getElementById('nt-lbl-add'); if (!add) return;
  const inp = document.getElementById('nt-lbl-in'), err = document.getElementById('nt-lbl-err'), row = document.getElementById('nt-labels');
  const paint = () => { row.innerHTML = UI.new.labels.map(l => `<span class="${chipClass(l)}" data-l="${esc(l)}">${esc(l)}<i data-rm="1" title="移除">×</i></span>`).join('') || '<span class="hint">还没打标签</span>';
    row.querySelectorAll('[data-rm]').forEach(x => x.onclick = () => { UI.new.labels = UI.new.labels.filter(y => y !== x.parentElement.dataset.l); paint(); }); };
  paint();
  const doAdd = () => {
    err.innerHTML = '';
    const fake = { labels: UI.new.labels, timeline: [] };
    const r = tryAddLabel(fake, inp.value, false);
    if (!r.ok) {
      let sugg = '';
      if (r.suggestions && r.suggestions.length) sugg = `<div class="sugg">建议：${r.suggestions.map(v => `<button class="fchip" data-s="${esc(r.dim + ':' + v)}">${esc(r.dim + ':' + v)}</button>`).join('')}</div>`;
      err.innerHTML = `<div class="errbox">${esc(r.msg)}${r.hint ? `<div class="hint">${esc(r.hint)}</div>` : ''}${sugg}</div>`;
      err.querySelectorAll('[data-s]').forEach(b => b.onclick = () => { inp.value = b.dataset.s; doAdd(); });
      return;
    }
    UI.new.labels = fake.labels; inp.value = ''; paint();
    if (r.note) toast(r.note);
  };
  add.onclick = doAdd;
  inp.addEventListener('keydown', e => { if (e.key === 'Enter') { e.preventDefault(); doAdd(); } });
}
function submitNewTicket() {
  const title = (document.getElementById('nt-title') || {}).value || '';
  const errBox = document.getElementById('nt-submit-err');
  if (!title.trim()) { errBox.innerHTML = '<div class="errbox">422 title_required：标题必填</div>'; window.scrollTo(0, 0); return; }
  const tpl = UI.new.tpl ? tplByKey(UI.new.tpl) : null;
  const fields = {};
  let missing = [];
  if (tpl) for (const f of tpl.fields) {
    const el = document.getElementById('nf-' + f.key);
    if (f.type === 'radio') { const c = document.querySelector(`[name=nf-${f.key}]:checked`); fields[f.key] = c ? c.value : ''; }
    else fields[f.key] = el ? el.value : '';
    if (f.req && !fields[f.key].trim()) missing.push(f.q);
  }
  const brief = (document.getElementById('nt-brief') || {}).value || '';
  const kindEl = document.querySelector('[name=nt-kind]:checked');
  const assignee = (document.getElementById('nt-assignee') || {}).value || '';
  const hasDim = UI.new.labels.some(l => l.includes(':'));
  const doCreate = () => {
    const r = createTicket({ template: UI.new.tpl, title: title.trim(), fields, briefMd: brief, kind: kindEl ? kindEl.value : '任务', labels: UI.new.labels, assignee });
    UI.new = { step: 3, tpl: null, created: r.ticket, warnings: r.warnings, share: r.share, labels: [] };
    refresh(); window.scrollTo(0, 0);
  };
  if (!hasDim) {
    errBox.innerHTML = '<div class="errbox">422 dimension_label_required：至少要打一个维度标签，否则不能提交。<div class="hint">例如 机型:UD-2300 或 立项:PRJ-2411。</div></div>';
    document.getElementById('nt-labels').scrollIntoView({ behavior: 'smooth', block: 'center' });
    return;
  }
  if (missing.length) {
    askConfirm('还有必填项没填', `<div class="warnbox">按约定，成员建单缺必填字段<b>只提醒、不拦截</b>——可以先建单，再交给访客或对方补全：</div>
      <ul class="misslist">${missing.map(m => `<li>${esc(m)}</li>`).join('')}</ul>`, '仍然创建', doCreate);
    return;
  }
  doCreate();
}
function newTicketDoneHTML() {
  const t = UI.new.created;
  if (!t) return '';
  return `<div class="page new done">
    <div class="bigok">✓</div>
    <div class="donet">已创建 <b>${esc(t.no)}</b>（${t.seq}）</div>
    <div class="hint" style="text-align:center">${esc(t.title)}</div>
    ${UI.new.warnings.length ? `<div class="warnbox"><b>${UI.new.warnings.length} 项必填未填（已按约定放行）：</b>${UI.new.warnings.map(esc).join('、')}。可以发一条「可填写说明」的访客链接请对方补全。</div>` : ''}
    ${UI.new.share ? `<div class="sec-t">share_text（可直接转发）</div>
      <div class="linkbox">${esc(UI.new.share)}</div>
      <div class="btnrow"><button class="btn" onclick="copyText(${JSON.stringify(UI.new.share).replace(/"/g, '&quot;')})">复制 share_text</button>
      <button class="btn ghost" onclick="nav('#/t/${t.no}')">打开工单</button>
      <button class="btn ghost" onclick="nav('#/inbox')">返回收件箱</button></div>`
      : `<div class="btnrow"><button class="btn" onclick="nav('#/t/${t.no}')">打开工单</button>
      <button class="btn ghost" onclick="nav('#/tickets')">去工单列表</button></div>`}
    <div class="hint" style="text-align:center">未指派的工单不会出现在任何人的收件箱里，要用查询接口找到它。</div>
  </div>`;
}

/* ---------------- 模板维护 ---------------- */
function validateTemplateSource(src) {
  const lines = src.split('\n');
  if ((lines[0] || '').trim() !== '---') return { line: 1, msg: '缺少 YAML 头：第一行应该是 ---' };
  const end = lines.indexOf('---', 1);
  if (end < 0) return { line: 1, msg: 'YAML 头没有结束：缺少第二个 ---' };
  let hasName = false, hasKind = false;
  for (let i = 1; i < end; i++) {
    if (lines[i].startsWith('名称:')) hasName = true;
    if (lines[i].startsWith('类型:')) {
      hasKind = true;
      const v = lines[i].slice(3).trim();
      if (v !== '任务' && v !== '反馈') return { line: i + 1, msg: `类型只能是 任务 或 反馈，当前是「${v}」` };
    }
  }
  if (!hasName) return { line: 1, msg: 'YAML 头缺少「名称:」' };
  if (!hasKind) return { line: 1, msg: 'YAML 头缺少「类型:」' };
  for (let i = end + 1; i < lines.length; i++) {
    const L = lines[i];
    if (L.trim() === '##') return { line: i + 1, msg: '字段标题（## 小节）不能为空' };
    const opens = (L.match(/\{/g) || []).length, closes = (L.match(/\}/g) || []).length;
    if (opens !== closes) return { line: i + 1, msg: '字段标记的花括号不成对' };
    const m = L.match(/\{([^{}]*)\}/);
    if (m) for (const tok of m[1].split(/\s+/).filter(Boolean)) {
      if (!/^(必填|选填|维度:\S+|单选:.+)$/.test(tok)) return { line: i + 1, msg: `无法识别的字段标记「${tok}」（可用：必填 / 选填 / 维度:X / 单选: A | B）` };
      if (tok.startsWith('维度:') && !S.dict[tok.slice(3)]) return { line: i + 1, msg: `维度「${tok.slice(3)}」不存在（新维度只能由管理员添加）` };
    }
  }
  return null;
}
function tplListHTML() {
  const rows = S.templates.map(tp => `<tr>
    <td data-l="模板"><a href="#/templates/${tp.key}"><b>${esc(tp.name)}</b></a><div class="hint">${tp.key}</div></td>
    <td data-l="类型"><span class="badge kind">${esc(tp.kind)}</span></td>
    <td data-l="版本">v${tp.ver}</td>
    <td data-l="最近修改">${esc(tp.updated)} · ${esc(tp.by)}</td>
    <td data-l="状态">${tp.active ? '<span class="badge open">使用中</span>' : '<span class="badge closed">已停用</span>'}</td>
    <td data-l="操作"><button class="btn sm ghost" onclick="nav('#/templates/${tp.key}')">编辑</button>
      ${tp.active ? `<button class="btn sm ghost" onclick="askConfirm('停用模板','停用后不能用「${esc(tp.name)}」新建工单；已有工单不受影响，仍按创建时的版本显示。','停用',()=>{tplByKey('${tp.key}').active=false;refresh();toast('已停用')})">停用</button>`
        : `<button class="btn sm ghost" onclick="tplByKey('${tp.key}').active=true;refresh();toast('已启用')">启用</button>`}</td>
  </tr>`).join('');
  return `<div class="page">
    <div class="pagehead"><div><div class="paget">模板维护</div><div class="hint">Markdown + 少量约定：YAML 头写名称、类型、预设标签；每个 ## 小节是一个字段。每次修改产生新版本，只能停用、不能删除。</div></div>
    <button class="btn" onclick="newTemplate()">＋ 新建模板</button></div>
    <table class="tbl"><thead><tr><th>模板</th><th>类型</th><th>版本</th><th>最近修改</th><th>状态</th><th>操作</th></tr></thead><tbody>${rows}</tbody></table>
  </div>`;
}
function tplEditHTML(key) {
  const tp = tplByKey(key); if (!tp) return '<div class="page">模板不存在</div>';
  const fieldRows = tp.fields.map(f => `<li>${esc(f.q)} ${f.req ? '<i class="req">必填</i>' : ''}${f.type === 'dim' ? ` · <span class="hint">维度 ${esc(f.dim)}</span>` : f.type === 'radio' ? ' · 单选' : ''}</li>`).join('');
  return `<div class="page">
    <div class="crumb"><a href="#/templates">← 模板列表</a></div>
    <div class="pagehead"><div><div class="paget">${esc(tp.name)} <span class="badge kind">${esc(tp.kind)}</span> v${tp.ver}
      ${tp.active ? '' : '<span class="badge closed">已停用</span>'}</div>
      <div class="hint">最近修改 ${esc(tp.updated)} · ${esc(tp.by)} · 已用 ${tp.used} 次 · 预设标签 ${tp.preset.length ? esc(tp.preset.join('、')) : '（无）'}</div></div></div>
    <div class="card">
      <div class="sec-t">模板源码（Markdown）</div>
      <textarea id="tpl-src" class="src" spellcheck="false">${esc(tp.source)}</textarea>
      <div class="btnrow">
        <button class="btn ghost" onclick="tplDemoError()">演示一处格式错误</button>
        <button class="btn ghost" onclick="tplValidate('${key}')">校验</button>
        <span class="sp"></span>
        <button class="btn" onclick="tplSave('${key}')">保存（产生新版本）</button>
      </div>
      <div id="tpl-out"></div>
    </div>
    <div class="card">
      <div class="sec-t">解析出的字段</div>
      <ul class="fldlist">${fieldRows || '<li class="hint">（源码解析后显示在这里）</li>'}</ul>
      ${tp.agentNote ? `<div class="sec-t">给 Agent 的说明（出现在填写指南和工单的「下一步」里）</div><div class="notebox">${esc(tp.agentNote)}</div>` : ''}
    </div>
    <div class="card">
      <div class="sec-t">历史版本</div>
      <table class="mini-tbl">${tp.hist.slice().reverse().map(h => `<tr><td>v${h.ver}</td><td>${esc(h.at)} · ${esc(h.by)}</td><td class="hint">${esc(h.note)}</td></tr>`).join('')}</table>
      <div class="hint">已有工单保留创建时模板版本的字段定义，模板以后修改不影响它们。</div>
    </div>
  </div>`;
}
function tplValidate(key) {
  const src = document.getElementById('tpl-src').value;
  const err = validateTemplateSource(src);
  const out = document.getElementById('tpl-out');
  out.innerHTML = err ? `<div class="errbox">保存失败 · 第 <b>${err.line}</b> 行：${esc(err.msg)}</div>` : '<div class="okbox">✓ 校验通过（预览会按这些字段生成填写表单）</div>';
}
function tplSave(key) {
  const tp = tplByKey(key);
  const src = document.getElementById('tpl-src').value;
  const err = validateTemplateSource(src);
  if (err) { document.getElementById('tpl-out').innerHTML = `<div class="errbox">保存失败 · 第 <b>${err.line}</b> 行：${esc(err.msg)}<div class="hint">格式错误时保存会被拒绝，并报出行号。</div></div>`; return; }
  tp.source = src; tp.ver += 1; tp.updated = '刚刚'; tp.by = ME.name;
  tp.hist.push({ ver: tp.ver, at: '刚刚', by: ME.name, note: '在网页上修改' });
  refresh(); toast(`✓ 已保存为 v${tp.ver}`);
}
function tplDemoError() {
  const ta = document.getElementById('tpl-src');
  const lines = ta.value.split('\n');
  lines.splice(3, 0, '## 优先级 {必填');
  ta.value = lines.join('\n');
  toast('已注入一处错误（第 4 行花括号不成对），点「校验」或「保存」看看');
}
function newTemplate() {
  const key = 'tpl-' + (S.templates.length + 1);
  S.templates.push({
    key, name: '新模板', kind: '任务', ver: 0, active: true, preset: [], updated: '—', by: ME.name, used: 0,
    agentNote: '', hist: [], source: '---\n名称: 新模板\n类型: 任务\n预设标签: []\n---\n## 第一项 {必填}\n引导说明。\n> 示例：……\n', fields: [],
  });
  nav('#/templates/' + key);
}

/* ---------------- 标签字典与别名 ---------------- */
function resolveSearchHTML(q) {
  if (!q) return '';
  const nq = norm(q); const out = [];
  for (const dim of Object.keys(S.dict)) for (const it of S.dict[dim]) {
    if (it.v === q) out.push({ l: `${dim}:${it.v}`, via: 'exact' });
    else if (it.a.some(a => norm(a) === nq)) out.push({ l: `${dim}:${it.v}`, via: 'alias', a: it.a.find(a => norm(a) === nq) });
    else { const nv = norm(it.v); if (nq.length >= 2 && nv.length >= 2 && (nv.includes(nq) || nq.includes(nv))) out.push({ l: `${dim}:${it.v}`, via: 'fuzzy' }); }
  }
  for (const p of S.plain) if (p === q) out.push({ l: p, via: 'exact' });
  if (!out.length) return `<div class="hint" style="padding:8px 0">没有匹配。可以换一种写法，或建单时勾选「允许新建字典值」；经常被叫错的话，在下面加一个别名。</div>`;
  return `<table class="mini-tbl">${out.map(r => `<tr><td><span class="${chipClass(r.l)}">${esc(r.l)}</span></td>
    <td class="hint">${r.via === 'exact' ? 'exact 精确匹配' : r.via === 'alias' ? `alias 别名「${esc(r.a)}」→ 标准值` : 'fuzzy 模糊匹配'}</td></tr>`).join('')}</table>`;
}
function labelsPageHTML() {
  const dims = Object.keys(S.dict).map(dim => `
    <div class="card">
      <div class="sec-t">${esc(dim)} <span class="hint">${S.dict[dim].length} 个值${dim === '算法' ? '（维度初始为空，此值由成员 allow_new 新建）' : ''}</span></div>
      <table class="mini-tbl">${S.dict[dim].map(it => `<tr>
        <td><span class="${chipClass(dim + ':' + it.v)}">${esc(dim)}:${esc(it.v)}</span></td>
        <td class="hint">${it.a.length ? '别名：' + it.a.map(a => `<span class="aliasbox">${esc(a)}</span>`).join(' ') : '<i>（无别名）</i>'}</td>
        </tr>`).join('')}</table>
    </div>`).join('');
  const dimOpts = Object.keys(S.dict).map(d => `<option>${esc(d)}</option>`).join('');
  return `<div class="page">
    <div class="pagehead"><div><div class="paget">标签字典与别名</div>
      <div class="hint">值来自字典，别名把俗称和常见错写对齐到标准值；打标签、筛选和下拉里写别名也能匹配。初始值从日报系统导入。</div></div></div>
    <div class="card">
      <div class="sec-t">解析（resolve）</div>
      <div class="inrow"><input id="rs-q" placeholder="输入俗称或错写试试：2300 / BM10 / 生化免疫 / 软件组"><button class="btn" onclick="doResolve()">解析</button></div>
      <div id="rs-out"></div>
    </div>
    <div class="card">
      <div class="sec-t">添加别名</div>
      <div class="hint">别名只能挂在「维度:值」的标准值上；一个别名只能指向一个标准值。试试给 机型:UD-2300 加「1680」。</div>
      <div class="inrow">
        <select id="al-dim" class="sel">${dimOpts}</select>
        <select id="al-val" class="sel"></select>
        <input id="al-alias" placeholder="别名，如 BM10">
        <button class="btn" onclick="doAddAlias()">添加</button>
      </div>
      <div id="al-out"></div>
    </div>
    ${dims}
    <div class="card">
      <div class="sec-t">不带维度的标签</div>
      <div>${S.plain.map(p => `<span class="chip plain">${esc(p)}</span> `).join('')}</div>
      <div class="hint">成员可随手新建；别名只能挂在带维度的值上。治理（停用、改名、合并、加维度）在管理页 → 标签治理。</div>
    </div>
  </div>`;
}
function wireLabelsPage() {
  const d = document.getElementById('al-dim'); if (!d) return;
  const fill = () => {
    const dim = d.value || Object.keys(S.dict)[0];
    document.getElementById('al-val').innerHTML = S.dict[dim].map(it => `<option value="${esc(it.v)}">${esc(dim)}:${esc(it.v)}</option>`).join('');
  };
  d.onchange = fill; fill();
  const rq = document.getElementById('rs-q');
  rq.addEventListener('keydown', e => { if (e.key === 'Enter') doResolve(); });
}
function doResolve() {
  const q = document.getElementById('rs-q').value.trim();
  document.getElementById('rs-out').innerHTML = resolveSearchHTML(q);
}
function doAddAlias() {
  const dim = document.getElementById('al-dim').value, val = document.getElementById('al-val').value, alias = document.getElementById('al-alias').value.trim();
  const out = document.getElementById('al-out');
  if (!alias) { out.innerHTML = '<div class="errbox">422 alias_required：别名不能为空</div>'; return; }
  const nq = norm(alias);
  for (const it of S.dict[dim]) {
    if (it.v === val) {
      if (it.a.includes(alias)) { out.innerHTML = `<div class="errbox">409 alias_exists：「${esc(alias)}」已经是 ${esc(dim)}:${esc(val)} 的别名</div>`; return; }
      continue;
    }
    if (it.a.some(a => norm(a) === nq)) { out.innerHTML = `<div class="errbox">409 alias_taken：别名「${esc(alias)}」已指向 ${esc(dim)}:${esc(it.v)}<div class="hint">改指向要管理员在管理页操作。</div></div>`; return; }
  }
  S.dict[dim].find(it => it.v === val).a.push(alias);
  refresh(); toast(`✓ 已添加：${dim}:${val} 的别名「${alias}」`);
}

/* ---------------- 个人设置 ---------------- */
function settingsPageHTML() {
  const rows = S.tokens.map((tk, i) => `<tr>
    <td data-l="Agent"><b>${esc(tk.name)}</b></td>
    <td data-l="创建于">${esc(tk.created)}</td>
    <td data-l="最近使用">${esc(tk.last)}</td>
    <td data-l="状态">${tk.revoked ? '<span class="badge closed">已停用</span>' : '<span class="badge open">使用中</span>'}</td>
    <td data-l="操作">${tk.revoked ? '<span class="hint">历史署名保留并标注「已停用」</span>'
      : `<button class="btn sm ghost" onclick="revokeToken(${i})">撤销</button>`}</td></tr>`).join('');
  return `<div class="page">
    <div class="card">
      <div class="kv"><span>账号</span><b>${esc(ME.name)} · ${ME.admin ? '管理员' : '成员'}</b></div>
      <div class="kv"><span>登录方式</span>网页会话（HttpOnly cookie，算本人操作）</div>
      <div class="btnrow"><button class="btn ghost" onclick="toast('已登出（演示）')">登出</button></div>
    </div>
    <div class="card">
      <div class="sec-t">我的成员 Agent（API token）</div>
      <div class="hint">为名下的 Agent 签发具名 token：可以有几个、分别署名「${esc(ME.name)} 的 Agent · 名字」、分别撤销。Agent 不能冒充本人，也不能签发 token；成员没有「本人 API token」——自己用命令行时，也建一个具名 Agent。</div>
      <table class="tbl"><thead><tr><th>Agent</th><th>创建于</th><th>最近使用</th><th>状态</th><th>操作</th></tr></thead><tbody>${rows}</tbody></table>
      <div class="btnrow"><button class="btn" onclick="openTokenDialog()">＋ 生成新 token</button></div>
    </div>
  </div>`;
}
function openTokenDialog() {
  const m = modal(`<div class="mtitle">生成 Agent token</div>
    <div class="mbody">
      <label class="fldlab">Agent 名字（会出现在署名里，如「${esc(ME.name)} 的 Agent · 名字」）</label>
      <input id="tk-name" placeholder="例如：笔记本、工位、巡检">
      <div class="hint">token 只在生成时显示一次，请立刻交给这个 Agent 的配置。</div>
    </div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span><button class="btn" data-x="yes">生成</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelector('[data-x=yes]').onclick = () => {
    const name = m.querySelector('#tk-name').value.trim();
    if (!name) { toast('名字不能为空'); return; }
    const tok = 'agt_' + ME.id + '_' + Math.random().toString(36).slice(2, 10) + Math.random().toString(36).slice(2, 14);
    S.tokens.unshift({ name, created: '刚刚', last: '—', revoked: false });
    refresh();
    modal(`<div class="mtitle">✓ token 已生成（只显示这一次）</div>
      <div class="mbody"><div class="fldlab">${esc(ME.name)} 的 Agent · ${esc(name)}</div>
      <div class="linkbox mono">${esc(tok)}</div>
      <div class="hint">用它做 Bearer token 调用 API；撤销立即失效，历史署名保留。</div></div>
      <div class="mrow"><button class="btn ghost" data-x="no">关闭</button><span class="sp"></span><button class="btn" data-x="cp">复制</button></div>`);
    const mm = document.querySelector('.mask:last-child');
    mm.querySelector('[data-x=no]').onclick = closeModal;
    mm.querySelector('[data-x=cp]').onclick = () => copyText(tok);
  };
}
function revokeToken(i) {
  const tk = S.tokens[i];
  askConfirm(`撤销「${tk.name}」的 token`, '撤销后立即失效（401）；它写过的回复和事件保留原署名，并标注「已停用」。', '撤销', () => {
    tk.revoked = true; tk.revokedAt = '刚刚'; refresh(); toast('✓ 已撤销');
  }, true);
}

/* ---------------- 管理页 ---------------- */
function adminPageHTML() {
  const tabs = [['members', '成员账号'], ['gov', '标签治理'], ['backup', '备份'], ['update', '更新']];
  let body = '';
  if (UI.adminTab === 'members') {
    const rows = S.members.map((mm, i) => `<tr>
      <td data-l="成员"><b>${esc(mm.name)}</b>${mm.name === ME.name ? ' <span class="hint">（我）</span>' : ''}</td>
      <td data-l="角色">${esc(mm.role)}</td>
      <td data-l="状态">${mm.active ? '<span class="badge open">使用中</span>' : '<span class="badge closed">已停用</span>'}</td>
      <td data-l="操作">${mm.name === ME.name ? '<span class="hint">—</span>'
        : `<button class="btn sm ghost" onclick="toggleMember(${i})">${mm.active ? '停用' : '启用'}</button>`}</td></tr>`).join('');
    body = `<div class="card">
      <div class="hint">创建和停用成员账号只能在网页上操作（管理员）；账号只能停用，不能删除。</div>
      <table class="tbl"><thead><tr><th>成员</th><th>角色</th><th>状态</th><th>操作</th></tr></thead><tbody>${rows}</tbody></table>
      <div class="btnrow"><button class="btn" onclick="openMemberDialog()">＋ 新建成员账号</button></div></div>`;
  } else if (UI.adminTab === 'gov') {
    const dimOpts = Object.keys(S.dict).map(d => `<option>${esc(d)}</option>`).join('');
    body = `<div class="card">
      <div class="hint">停用、改名、合并标签值，合并时历史工单上的标签一并改写；添加新维度、修改别名的指向也只有管理员能做。</div>
      <div class="inrow"><span class="fldlab" style="margin:0">维度</span><select id="gov-dim" class="sel">${dimOpts}</select></div>
      <div id="gov-list"></div>
      <div class="btnrow">
        <button class="btn ghost" onclick="govAddDim()">＋ 新建维度</button>
        <button class="btn ghost" onclick="govMoveAlias()">改别名指向</button>
      </div></div>`;
  } else if (UI.adminTab === 'backup') {
    const b = S.backup;
    const late = b.state === 'late', noT = b.state === 'no-target';
    body = `<div class="card">
      <div class="bkcard${late ? ' late' : ''}">
        <div class="kv"><span>最近一次成功备份</span><b class="${late ? 'red' : ''}">${noT ? '—' : late ? '10-14 03:00（超过 36 小时！）' : '今天 03:00'}</b></div>
        <div class="kv"><span>大小 / 完整性</span>${noT ? '—' : '12.6 MB · PRAGMA integrity_check 通过'}</div>
        <div class="kv"><span>备份目标</span>${noT ? '<b>未配置 —— 备份未离开本机</b>' : `<span class="mono">\\\\nas01\\共享\\taskboard\\backup</span>`}</div>
        <div class="kv"><span>轮换</span>每日 27/30 · 每月 4/12 · 更新前 2/5</div>
        ${late ? '<div class="errbox">备份已超过 36 小时没有成功，请检查 NAS 连接或通知管理员。</div>' : ''}
        ${noT ? '<div class="warnbox">没有配置备份目标，备份保存在本机 data_dir\\backups\\，VM 损坏时会一起丢失。</div>' : ''}
      </div>
      <div class="inrow"><span class="fldlab" style="margin:0">演示状态</span>
        <select class="sel" onchange="S.backup.state=this.value;refresh()">
          <option value="ok"${b.state === 'ok' ? ' selected' : ''}>正常（今天 03:00）</option>
          <option value="late"${late ? ' selected' : ''}>超过 36 小时</option>
          <option value="no-target"${noT ? ' selected' : ''}>未配置备份目标</option>
        </select></div>
      <div class="btnrow"><button class="btn ghost" onclick="toast('正在备份（VACUUM INTO → 镜像 → 校验）……（演示）');setTimeout(()=>toast('✓ 备份完成：taskboard-20261016-172500-daily.db'),1800)">手动备份</button></div>
      <div class="hint">恢复用命令行：taskboard restore &lt;备份文件&gt;；每天 03:00 自动备份，目标连不上时每小时重试。</div>
    </div>`;
  } else {
    const u = S.update;
    body = `<div class="card">
      <div class="kv"><span>后端 taskboard</span><b>${u.server}</b> <span class="hint">（无新版本）</span></div>
      <div class="kv"><span>前端 web</span><b>${u.web}</b> ${u.webAvail ? `<span class="badge myturn">有新版本 ${u.webAvail}</span>` : ''}</div>
      ${u.webAvail ? `<div class="notebox">${esc(u.webNote)}</div>` : ''}
      <div class="btnrow"><button class="btn" onclick="runUpdate()">检查并更新</button>
      <button class="btn ghost" onclick="askConfirm('回滚','换回上一版（.prev）和更新前的备份。用于新版本起不来时快速恢复。','回滚',()=>toast('✓ 已回滚（演示）'),true)">回滚上一版</button></div>
      <div class="hint">更新也可以在服务器上执行 taskboard update；更新前会自动备份，备份失败就中止更新。回滚用 taskboard rollback。</div>
    </div>`;
  }
  return `<div class="page">
    <div class="pagehead"><div><div class="paget">管理</div><div class="hint">仅管理员可见（吴工、李工）。</div></div></div>
    <div class="tabbar">${tabs.map(([k, n]) => `<button class="fchip${UI.adminTab === k ? ' on' : ''}" data-act="admin-tab" data-v="${k}">${n}</button>`).join('')}</div>
    ${body}
  </div>`;
}
function wireAdminPage() {
  const d = document.getElementById('gov-dim'); if (!d) return;
  const paint = () => {
    const dim = d.value || Object.keys(S.dict)[0];
    if (!S.dict[dim]) return;
    document.getElementById('gov-list').innerHTML = `<table class="tbl"><thead><tr><th>值</th><th>别名</th><th>操作</th></tr></thead><tbody>` +
      S.dict[dim].map((it, i) => `<tr><td><span class="${chipClass(dim + ':' + it.v)}">${esc(dim)}:${esc(it.v)}</span></td>
        <td class="hint">${it.a.length ? it.a.map(esc).join('、') : '—'}</td>
        <td><button class="btn sm ghost" onclick="govRename('${esc(dim)}',${i})">改名</button>
        <button class="btn sm ghost" onclick="govMerge('${esc(dim)}',${i})">合并到…</button>
        <button class="btn sm ghost" onclick="govDisable('${esc(dim)}',${i})">停用</button></td></tr>`).join('') +
      `</tbody></table>`;
  };
  d.onchange = paint; paint();
}
function toggleMember(i) {
  const mm = S.members[i];
  askConfirm(`${mm.active ? '停用' : '启用'} ${mm.name}`, mm.active ? '停用后不能登录；已有的回复和事件保留。' : '启用后恢复登录。', mm.active ? '停用' : '启用', () => {
    mm.active = !mm.active; refresh(); toast('✓ 已' + (mm.active ? '启用' : '停用'));
  }, mm.active);
}
function openMemberDialog() {
  const m = modal(`<div class="mtitle">新建成员账号</div>
    <div class="mbody"><label class="fldlab">姓名</label><input id="mm-name" placeholder="例如：孙工">
    <div class="hint">账号由管理员创建，初始密码会显示一次；成员用它在网页上登录。MVP 不接 LDAP/SSO。</div></div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span><button class="btn" data-x="yes">创建</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelector('[data-x=yes]').onclick = () => {
    const n = m.querySelector('#mm-name').value.trim();
    if (!n) { toast('姓名不能为空'); return; }
    S.members.push({ id: 'm' + Date.now(), name: n, role: '成员', active: true, since: '2026-10' });
    closeModal(); refresh(); toast(`✓ 已创建 ${n}，初始密码已显示（演示略）`);
  };
}
function govRename(dim, i) {
  const it = S.dict[dim][i];
  const m = modal(`<div class="mtitle">改名 · ${esc(dim)}:${esc(it.v)}</div>
    <div class="mbody"><input id="rn-new" value="${esc(it.v)}">
    <div class="hint">改名后，历史工单上的这个标签一并改写。</div></div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span><button class="btn" data-x="yes">改名</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelector('[data-x=yes]').onclick = () => {
    const nv = m.querySelector('#rn-new').value.trim(); if (!nv) return;
    const from = `${dim}:${it.v}`, to = `${dim}:${nv}`;
    let n = 0;
    for (const t of S.tickets) { const idx = t.labels.indexOf(from); if (idx >= 0) { t.labels[idx] = to; n++; } }
    it.v = nv; closeModal(); refresh(); toast(`✓ 已改名，${n} 张工单上的标签已改写`);
  };
}
function govMerge(dim, i) {
  const it = S.dict[dim][i];
  const others = S.dict[dim].filter(x => x !== it).map(x => `<option>${esc(x.v)}</option>`).join('');
  const m = modal(`<div class="mtitle">合并 · ${esc(dim)}:${esc(it.v)}</div>
    <div class="mbody"><div class="fldlab">合并到</div><select id="mg-to" class="sel">${others}</select>
    <div class="warnbox">合并时，历史工单上的标签「${esc(dim)}:${esc(it.v)}」会全部改写成目标值；别名一并转移。</div></div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span><button class="btn danger" data-x="yes">合并</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelector('[data-x=yes]').onclick = () => {
    const to = m.querySelector('#mg-to').value;
    const from = `${dim}:${it.v}`, full = `${dim}:${to}`;
    let n = 0;
    for (const t of S.tickets) { const idx = t.labels.indexOf(from); if (idx >= 0) { t.labels[idx] = full; n++; } }
    const target = S.dict[dim].find(x => x.v === to);
    for (const a of it.a) if (!target.a.includes(a)) target.a.push(a);
    S.dict[dim] = S.dict[dim].filter(x => x !== it);
    closeModal(); refresh(); toast(`✓ 已合并到 ${full}，${n} 张工单的标签已改写`);
  };
}
function govDisable(dim, i) {
  const it = S.dict[dim][i];
  askConfirm(`停用 ${dim}:${it.v}`, '停用后不能再打这个值；已有工单上的标签保留。', '停用', () => {
    S.dict[dim].splice(i, 1); refresh(); toast('✓ 已停用');
  }, true);
}
function govAddDim() {
  const m = modal(`<div class="mtitle">新建维度</div>
    <div class="mbody"><input id="nd-name" placeholder="维度名，如：客户">
    <div class="hint">新维度只能由管理员添加；建好后成员就能往里加值。</div></div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span><button class="btn" data-x="yes">创建</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelector('[data-x=yes]').onclick = () => {
    const n = m.querySelector('#nd-name').value.trim(); if (!n) return;
    if (S.dict[n]) { toast('维度已存在'); return; }
    S.dict[n] = []; closeModal(); refresh(); toast(`✓ 已新建维度「${n}」`);
  };
}
function govMoveAlias() {
  const dims = Object.keys(S.dict);
  const allAliases = [];
  for (const d of dims) for (const it of S.dict[d]) for (const a of it.a) allAliases.push({ d, v: it.v, a });
  if (!allAliases.length) { toast('字典里还没有别名'); return; }
  const opts = allAliases.map((x, i) => `<option value="${i}">${esc(x.d)} · 「${esc(x.a)}」（当前指向 ${esc(x.d)}:${esc(x.v)}）</option>`).join('');
  const dim = document.getElementById('gov-dim').value;
  const tgt = S.dict[dim].map(it => `<option>${esc(it.v)}</option>`).join('');
  const m = modal(`<div class="mtitle">改别名指向</div>
    <div class="mbody"><div class="fldlab">别名</div><select id="ma-a" class="sel">${opts}</select>
    <div class="fldlab">改指向（当前维度 ${esc(dim)}）</div><select id="ma-t" class="sel">${tgt}</select></div>
    <div class="mrow"><button class="btn ghost" data-x="no">取消</button><span class="sp"></span><button class="btn" data-x="yes">修改</button></div>`);
  m.querySelector('[data-x=no]').onclick = closeModal;
  m.querySelector('[data-x=yes]').onclick = () => {
    const x = allAliases[+m.querySelector('#ma-a').value];
    S.dict[x.d].find(it => it.v === x.v).a = S.dict[x.d].find(it => it.v === x.v).a.filter(a => a !== x.a);
    S.dict[dim].find(it => it.v === m.querySelector('#ma-t').value).a.push(x.a);
    closeModal(); refresh(); toast('✓ 别名已改指向');
  };
}
function runUpdate() {
  const steps = ['正在更新前备份（失败会中止更新）……', '备份完成：taskboard-…-preupdate.db', '正在下载 web v1.4.1（按更新源顺序回退）……', '校验 SHA256 通过', '解压到 web-1.4.1/，原子切换 web/ ……', '✓ 更新完成，前端已切换（刷新页面生效）'];
  const m = modal(`<div class="mtitle">更新</div><div class="mbody"><div id="up-steps">${steps.map(() => '<div class="upstep">—</div>').join('')}</div></div>
    <div class="mrow"><span class="sp"></span><button class="btn" data-x="ok" style="visibility:hidden">完成</button></div>`);
  const els = m.querySelectorAll('.upstep');
  steps.forEach((s, i) => setTimeout(() => {
    els[i].innerHTML = s; els[i].classList.add('on');
    if (i === steps.length - 1) { m.querySelector('[data-x=ok]').style.visibility = ''; m.querySelector('[data-x=ok]').onclick = () => { closeModal(); S.update.web = S.update.webAvail; S.update.webAvail = null; refresh(); }; }
  }, 700 * (i + 1)));
}

/* ---------------- 列表筛选（共享状态） ---------------- */
function filteredTickets() {
  const L = UI.list;
  return S.tickets.filter(t =>
    (L.status === 'all' || (L.status === 'open' ? t.status === 'open' : t.status === 'closed')) &&
    (!L.kind || t.kind === L.kind) &&
    (!L.assignee || (L.assignee === 'me' ? t.assignee === ME.name : L.assignee === 'none' ? !t.assignee : L.assignee === 'others' ? (t.assignee && t.assignee !== ME.name) : true)) &&
    (!L.waiting || t.waiting.party === L.waiting) &&
    (L.who === 'all' || t.lastKind === L.who) &&
    (!L.q || norm(t.title).includes(norm(L.q)))
  );
}
function filterBarHTML() {
  const L = UI.list;
  const grp = (label, k, opts) => `<span class="fgroup"><span class="flbl">${label}</span>` +
    opts.map(([v, n]) => `<button class="fchip${String(L[k]) === String(v) ? ' on' : ''}" data-act="lf" data-k="${k}" data-v="${v}">${n}</button>`).join('') + '</span>';
  return `<div class="filterbar">
    <div class="inrow searchrow"><input id="lst-q" placeholder="搜索标题、说明、回复（试试 E203）" value="${esc(L.q)}"></div>
    <div class="frow">${grp('状态', 'status', [['open', '开启'], ['closed', '已关闭'], ['all', '全部']])}</div>
    <div class="frow">${grp('类型', 'kind', [['', '全部'], ['任务', '任务'], ['反馈', '反馈']])}</div>
    <div class="frow">${grp('指派', 'assignee', [['', '全部'], ['me', '指派给我'], ['none', '未指派'], ['others', '指派给他人']])}</div>
    <div class="frow">${grp('等待方', 'waiting', [['', '全部'], ['assignee', '指派人'], ['creator', '发起人'], ['guest', '访客'], ['department', '本部门']])}</div>
    <div class="frow">${grp('操作方', 'who', [['all', '全部'], ['member', '本人'], ['agent', 'Agent'], ['guest', '访客']])}<span class="hint">按最近一次操作是谁（成员本人 / 成员 Agent / 访客）筛选</span></div>
  </div>`;
}
function wireListPage() {
  const q = document.getElementById('lst-q'); if (!q) return;
  q.addEventListener('keydown', e => { if (e.key === 'Enter') { UI.list.q = q.value; refresh(); } });
  q.addEventListener('blur', () => { if (q.value !== UI.list.q) { UI.list.q = q.value; refresh(); } });
}

/* ---------------- 全局事件代理 ---------------- */
document.body.addEventListener('click', e => {
  const el = e.target.closest('[data-act]'); if (!el) return;
  const t = UI.cur;
  switch (el.dataset.act) {
    case 'open-label': openLabelDialog(t); break;
    case 'open-assign': openAssignDialog(t); break;
    case 'open-links': openLinksDialog(t); break;
    case 'open-close': openCloseDialog(t); break;
    case 'reopen': askConfirm('重新开启', '重新开启后访客链接恢复可回复；请回复说明原因，让对方知道下一步做什么。', '重新开启', () => reopenTicket(t)); break;
    case 'reply-send': sendReply(); break;
    case 'accept': acceptAndClose(); break;
    case 'deliver-toggle':
      if (el.disabled) return;
      UI.deliver = !UI.deliver;
      el.classList.toggle('on', UI.deliver);
      el.textContent = UI.deliver ? '✓ 作为交付发送' : '标记为交付';
      break;
    case 'att-demo': toast('📎 附件上传（两步：先上传拿 att: 引用，再在正文或 attachment_ids 里引用）——演示略'); break;
    case 'tl-who': UI.tlWho = el.dataset.v; refresh(); break;
    case 'lf': UI.list[el.dataset.k] = el.dataset.v === '' ? '' : el.dataset.v; if (el.dataset.v === 'open' || el.dataset.v === 'all' || el.dataset.v === 'closed') UI.list.status = el.dataset.v; refresh(); break;
    case 'admin-tab': UI.adminTab = el.dataset.v; refresh(); break;
    case 'new-tpl': UI.new = { step: 2, tpl: el.dataset.k, labels: [] }; refresh(); break;
    case 'new-free': UI.new = { step: 2, tpl: null, labels: [] }; refresh(); break;
    case 'new-submit': submitNewTicket(); break;
  }
});
