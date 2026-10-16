// PROTOTYPE — 访客网页原型的共享假数据与状态切换，用完即弃
const T = {
  no: '20261015-007', seq: '#1234', kind: '反馈',
  title: 'BH-7600CS 质控时报警 E-203，无法开始测试',
  dept: '研发部', creatorMember: '李四',
  linkNote: '检验科-张三', expires: '11月14日',
  fields: [
    { key: 'model', q: '哪台仪器？', type: 'dim', req: true, val: 'BH-7600CS',
      guide: '在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。',
      options: ['BH-7600CS', 'BH-7600', 'BH-5390', 'CA-800', '不确定'] },
    { key: 'ver', q: '序列号和软件版本是多少？', type: 'text', req: false, val: '',
      guide: '在“设置 → 关于本机”里可以看到。', ex: '序列号 7600CS-2023-0412，软件 V3.2.1' },
    { key: 'when', q: '什么时候发生的？', type: 'text', req: true, val: '10月15日上午 9:10 左右',
      guide: '大概时间即可。', ex: '今天上午 9 点左右' },
    { key: 'steps', q: '你做了哪些操作？', type: 'text', req: true,
      val: '1. 开机自检完成\n2. 进入质控界面\n3. 点击“开始”',
      guide: '按顺序写，一步一行。', ex: '1. 开机自检完成 2. 进入质控界面 3. 点击“开始”' },
    { key: 'actual', q: '实际看到了什么？', type: 'text', req: true,
      val: '屏幕弹出“E-203 试剂余量不足”，但试剂是早上刚换的新瓶。', img: 'E203报警.jpg',
      guide: '报警请把原文连同编号照抄，能拍照最好。', ex: '弹出“E-203 试剂余量不足”' },
    { key: 'expect', q: '你期望看到什么？', type: 'text', req: true, val: '正常开始质控测试。',
      guide: '本来应该发生什么。', ex: '正常开始质控测试' },
    { key: 'freq', q: '出现得频繁吗？', type: 'radio', req: true, val: '每次',
      options: ['每次', '经常', '偶尔', '仅一次'], guide: '' },
    { key: 'guess', q: '你的猜测（选填）', type: 'text', req: false, val: '',
      guide: '和上面的现象分开写，猜错也没关系。', ex: '可能是试剂瓶的液位传感器没识别到' },
  ],
  attachments: [
    { name: 'E203报警.jpg', size: '1.2 MB' },
    { name: '仪器日志.zip', size: '8.4 MB' },
  ],
  replies: [
    { who: 'guest', name: '张三', via: '检验科-张三', time: '10-15 09:32', event: '通过反馈入口提交了这张工单' },
    { who: 'agent', name: '李四 的 Agent · 初筛', time: '10-15 09:40',
      body: '感谢反馈，已转给仪器组。为了尽快定位，还需要两项信息：\n\n1. **软件版本**：在“设置 → 关于本机”里，例如 `V3.2.1`\n2. **试剂瓶照片**：报警时试剂瓶在仪器上的样子，拍清楚瓶身标签\n\n可以直接在下方回复，或者点“补全说明”填进对应的栏目。' },
  ],
};

const STATES = [
  ['read', '阅读与回复'],
  ['fill', '补全说明'],
  ['closed', '已关闭'],
  ['expired', '链接失效'],
  ['intake', '反馈入口'],
  ['intakeDone', '入口提交成功'],
];

function esc(s) { return String(s).replace(/[&<>"]/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c])); }
// 极简 Markdown：粗体、行内代码、编号列表、段落
function md(s) {
  return esc(s).split(/\n\n+/).map(p => {
    p = p.replace(/\*\*(.+?)\*\*/g, '<b>$1</b>').replace(/`(.+?)`/g, '<code>$1</code>');
    if (/^\d+\. /.test(p)) return '<ol>' + p.split('\n').map(l => '<li>' + l.replace(/^\d+\. /, '') + '</li>').join('') + '</ol>';
    return '<p>' + p.replace(/\n/g, '<br>') + '</p>';
  }).join('');
}
function img(name) { return `<div class="imgph">🖼 ${esc(name)}</div>`; }

function toast(msg, ms = 2600) {
  let t = document.getElementById('toast');
  if (!t) { t = document.createElement('div'); t.id = 'toast'; document.body.appendChild(t); }
  t.textContent = msg; t.className = 'show';
  clearTimeout(t._h); t._h = setTimeout(() => t.className = '', ms);
}
// 演示：模拟提交时服务正在更新
function fakeSend() {
  toast('服务更新中，内容已保存在本机，正在自动重试…', 1800);
  setTimeout(() => toast('✓ 已发送'), 2000);
}
function fakeBigFile(el) {
  el.innerHTML = `<div class="toobig">⚠ <b>录像.mp4（186 MB）</b>超过单个文件 50 MB 的上限，没有上传。<br>请把文件放到 NAS 共享盘，再把路径粘贴到文字里，例如 <code>\\\\nas01\\检验科\\录像.mp4</code></div>`;
}

function mountSwitcher(render) {
  const bar = document.createElement('div');
  bar.id = 'protobar';
  bar.innerHTML = '<span>原型状态：</span>' + STATES.map(([k, n]) => `<button data-k="${k}">${n}</button>`).join('');
  document.body.prepend(bar);
  const go = k => {
    bar.querySelectorAll('button').forEach(b => b.classList.toggle('on', b.dataset.k === k));
    document.getElementById('app').innerHTML = render(k);
    window.scrollTo(0, 0);
  };
  bar.addEventListener('click', e => e.target.dataset.k && go(e.target.dataset.k));
  go('read');
}

const PROTO_CSS = `
#protobar{position:sticky;top:0;z-index:99;background:#222;color:#bbb;font:12px sans-serif;padding:6px 8px;display:flex;gap:4px;overflow-x:auto;white-space:nowrap}
#protobar button{background:#444;color:#eee;border:0;border-radius:10px;padding:3px 9px;font-size:12px}
#protobar button.on{background:#f5b301;color:#000}
#toast{position:fixed;left:50%;bottom:90px;transform:translateX(-50%);background:#333;color:#fff;padding:10px 16px;border-radius:8px;font-size:14px;opacity:0;transition:.2s;pointer-events:none;z-index:100;max-width:90%}
#toast.show{opacity:1}
.imgph{background:repeating-linear-gradient(45deg,#e8e8e8,#e8e8e8 8px,#f2f2f2 8px,#f2f2f2 16px);border-radius:6px;height:110px;display:flex;align-items:center;justify-content:center;color:#777;font-size:13px;margin:6px 0}
.toobig{background:#fff4e5;border:1px solid #f0b060;border-radius:6px;padding:8px 10px;font-size:13px;line-height:1.6;margin-top:6px}
code{background:#f0f0f0;padding:1px 4px;border-radius:3px;font-size:.92em}
`;
document.head.insertAdjacentHTML('beforeend', `<style>${PROTO_CSS}</style>`);
