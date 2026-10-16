/* ============ 原型 C · app：三栏邮件客户端（导航 | 列表 | 阅读） ============ */
UI.cView = 'inbox';   // 中栏当前列表
UI.cSel = null;       // 右栏当前工单

function setView(v, preset) {
  UI.cView = v;
  UI.list = Object.assign({ status: 'open', kind: '', assignee: '', waiting: '', who: 'all', q: '' }, preset || {});
  UI.cSel = null;
  nav('#/tickets');
}

function shell(detailHTML) {
  const inboxN = inboxItems().length;
  const v = UI.cView;
  const viewRow = (key, name, preset, n) =>
    `<a class="vrow${v === key ? ' on' : ''}" href="javascript:void 0" onclick='setView("${key}", ${JSON.stringify(preset).replace(/"/g, "&quot;")})'>${name}${n !== undefined ? ` <b class="vc">${n}</b>` : ''}</a>`;
  return `
  <div class="c-shell${location.hash.startsWith('#/t/') || isUtilRoute() ? ' show-detail' : ''}">
    <aside class="c-nav" id="cnav">
      <div class="c-brand">📮 部门工单平台</div>
      <button class="c-new" onclick="UI.new={step:1,tpl:null,labels:[]};nav('#/new')">＋ 新建工单</button>
      <div class="navg">收件</div>
      <a class="vrow${location.hash.startsWith('#/inbox') || (v === 'inbox' && !location.hash.startsWith('#/t/')) ? ' on' : ''}" href="#/inbox">📥 收件箱 ${inboxN ? `<b class="vc">${inboxN}</b>` : ''}</a>
      <div class="navg">工单视图</div>
      ${viewRow('all', '全部开启', { status: 'open', kind: '', assignee: '', waiting: '', who: 'all', q: '' })}
      ${viewRow('mine', '指派给我', { status: 'open', assignee: 'me' })}
      ${viewRow('none', '未指派', { status: 'open', assignee: 'none' })}
      ${viewRow('guest', '等待访客', { status: 'open', waiting: 'guest' })}
      ${viewRow('closed', '已关闭', { status: 'closed', kind: '', assignee: '', waiting: '', who: 'all', q: '' })}
      <div class="navg">维护</div>
      <a class="vrow${location.hash.startsWith('#/templates') ? ' on' : ''}" href="#/templates">📄 模板维护</a>
      <a class="vrow${location.hash.startsWith('#/labels') ? ' on' : ''}" href="#/labels">🏷 标签字典</a>
      <a class="vrow${location.hash.startsWith('#/settings') ? ' on' : ''}" href="#/settings">⚙️ 设置</a>
      <a class="vrow${location.hash.startsWith('#/admin') ? ' on' : ''}" href="#/admin">🛡 管理页</a>
      <div class="c-foot">${esc(ME.name)} · ${ME.admin ? '管理员' : '成员'}<br><span class="hint">原型 C · 演示数据</span></div>
    </aside>
    <div class="c-scrim" onclick="document.body.classList.remove('nav-open')"></div>
    <section class="c-list">
      <div class="c-lhead">
        <button class="burger" onclick="document.body.classList.add('nav-open')">☰</button>
        <span>${UI.cView === 'inbox' ? '收件箱' : '工单'}</span>
        <span class="hint" style="margin-left:auto">${listTitle()}</span>
      </div>
      <div class="c-listbody">${listHTML()}</div>
    </section>
    <section class="c-detail">${detailHTML || '<div class="c-empty"><div style="font-size:40px">🗂</div><p>从左边选择一张工单<br>阅读和回复都在这一栏</p></div>'}</section>
  </div>`;
}
function isUtilRoute() {
  const h = location.hash || '';
  return h.startsWith('#/new') || h.startsWith('#/templates') || h.startsWith('#/labels') || h.startsWith('#/settings') || h.startsWith('#/admin');
}
function listTitle() {
  if (UI.cView === 'inbox') return `${inboxItems().length} 项`;
  return `${filteredTickets().length} 张`;
}

/* ---------- 中栏：收件箱 / 列表 ---------- */
function listHTML() {
  if (UI.cView === 'inbox') {
    const items = inboxItems();
    return items.map(t => `<div class="lrow${UI.cSel === t.no ? ' sel' : ''}" onclick="UI.cSel='${t.no}';nav('#/t/${t.no}')">
      <div class="lr-top"><span class="badge reason">${REASONS[t.waiting.reason] || ''}</span><span class="hint">${esc(t.updated)}</span></div>
      <div class="lr-t">${esc(t.title)}</div>
      <div class="lr-m">${esc(t.no)} · ${esc(t.kind)} · ${esc(creatorName(t))}</div>
    </div>`).join('') || '<div class="c-empty small">收件箱是空的</div>';
  }
  const list = filteredTickets();
  return filterBarHTML() + `<div class="lcnt">${list.length} 张</div>` + list.map(t => `
    <div class="lrow${UI.cSel === t.no ? ' sel' : ''}" onclick="UI.cSel='${t.no}';nav('#/t/${t.no}')">
      <div class="lr-top"><span class="badge kind">${esc(t.kind)}</span>
        ${t.status === 'closed' ? '<span class="badge closed">已关闭</span>' : `<span class="hint">等 ${esc(waitText(t))}</span>`}
        <span class="hint" style="margin-left:auto">${esc(t.updated)}</span></div>
      <div class="lr-t">${esc(t.title)}</div>
      <div class="lr-m">${esc(t.no)} · ${esc(t.assignee || '未指派')}${t.lastKind === 'agent' ? ' · 最近 Agent' : t.lastKind === 'guest' ? ' · 最近访客' : ''}</div>
    </div>`).join('') || '<div class="c-empty small">没有符合条件的工单</div>';
}

/* ---------- 右栏：详情（对话流 + 元信息条） ---------- */
function pageDetail(no) {
  const t = findTicket(no);
  if (!t) return '<div class="c-empty">工单不存在</div>';
  UI.cur = t; UI.tlWho = 'all'; UI.deliver = false;
  return `
  <div class="dwrap">
    <div class="dhead">
      <button class="back" onclick="UI.cSel=null;nav(UI.cView==='inbox'?'#/inbox':'#/tickets')">‹ 返回列表</button>
      <div class="dmeta">${esc(t.no)} · ${t.seq} · ${esc(t.kind)}${t.template ? ' · 模板 ' + esc(tplByKey(t.template).name) : ''}</div>
      <h1>${esc(t.title)}</h1>
      <div class="dbadges">${statusBadge(t)}${t.waiting.reason && t.status === 'open' ? `<span class="hint">（${REASONS[t.waiting.reason]}）</span>` : ''}</div>
      <div class="dstrip">
        <span>发起人 <b>${esc(creatorName(t))}</b></span>
        <span>指派 <b>${esc(t.assignee || '未指派')}</b> <button class="btn sm ghost" data-act="open-assign">${t.assignee ? '改派' : '指派'}</button></span>
        <span>${labelChips(t)}<button class="btn sm ghost" data-act="open-label">🏷</button></span>
        <span><button class="btn sm ghost" data-act="open-links">🔗 访客链接（${t.guestLinks.length}）</button></span>
        ${t.status === 'open' ? '<span><button class="btn sm ghost" data-act="open-close">✔ 关闭</button></span>' : '<span><button class="btn sm ghost" data-act="reopen">↺ 重新开启</button></span>'}
      </div>
    </div>
    <div class="dbody">
      ${acceptPanelHTML(t)}
      <div class="conv">${conversationHTML(t)}</div>
    </div>
    ${t.status === 'open' ? composerHTML(t)
      : `<div class="composer ro">工单已关闭（${CLOSE_TEXT[t.closeReason] || ''}）<button class="btn sm ghost" data-act="reopen">重新开启</button></div>`}
  </div>`;
}

/* ---------- 工具 / 建单页占右栏 ---------- */
function pageNew() {
  if (UI.new.step === 1) return newTicketStep1HTML();
  if (UI.new.step === 3) return newTicketDoneHTML();
  return newTicketFormHTML();
}
function utilBack() {
  return `<div class="utilhead"><button class="back" onclick="nav(UI.cView==='inbox'?'#/inbox':'#/tickets')">‹ 返回${UI.cView === 'inbox' ? '收件箱' : '列表'}</button></div>`;
}

/* ---------- 路由 ---------- */
function renderApp() {
  const h = (location.hash || '#/inbox').replace(/^#\//, '');
  let detail = null;
  if (h === 'inbox') UI.cView = 'inbox';
  if (h === 'tickets') { if (UI.cView === 'inbox') UI.cView = 'all'; }
  if (h.startsWith('t/')) detail = pageDetail(decodeURIComponent(h.slice(2)));
  else if (h.startsWith('templates/')) detail = utilBack() + tplEditHTML(h.slice(10));
  else if (h === 'templates') detail = utilBack() + tplListHTML();
  else if (h === 'new') detail = utilBack() + pageNew();
  else if (h === 'labels') detail = utilBack() + labelsPageHTML();
  else if (h === 'settings') detail = utilBack() + settingsPageHTML();
  else if (h === 'admin') detail = utilBack() + adminPageHTML();
  document.getElementById('app').innerHTML = shell(detail);
  document.body.classList.remove('nav-open');
  if (!h.startsWith('t/') && !isUtilRoute()) { /* 列表页 */ }
  if (UI.cView !== 'inbox' || h === 'tickets') wireListPage();
  if (h === 'labels') wireLabelsPage();
  if (h === 'admin') wireAdminPage();
  if (h === 'new' && UI.new.step === 2) wireNewForm();
}
window.refresh = renderApp;
renderApp();
