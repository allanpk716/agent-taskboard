/* ============ 原型 B · app：移动优先，卡片 + 底部标签栏 ============ */

function shell(main) {
  const h = location.hash || '#/inbox';
  const inboxN = inboxItems().length;
  const tab = (k, icon, name, badge) =>
    `<a class="tabbtn${h.startsWith('#/' + k) ? ' on' : ''}" href="#/${k}">${icon}${badge ? `<b class="dot">${badge}</b>` : ''}<span>${name}</span></a>`;
  return `
  <div class="b-app">
    <header class="b-top">
      <div class="b-title">${pageTitle() || '部门工单平台'}</div>
      <div class="b-me">${esc(ME.name)}<i>${ME.admin ? '管理员' : '成员'}</i></div>
    </header>
    <div class="b-body">${main}</div>
    <nav class="b-tabs">
      ${tab('inbox', '📥', '收件箱', inboxN)}
      ${tab('tickets', '🗂', '工单')}
      <a class="tabbtn plus${h.startsWith('#/new') ? ' on' : ''}" href="#/new"><b>＋</b><span>建单</span></a>
      ${tab('labels', '🧰', '工具')}
      ${tab('settings', '👤', '我的')}
    </nav>
  </div>`;
}
function pageTitle() {
  const h = location.hash || '#/inbox';
  if (h.startsWith('#/t/')) return '工单';
  return { '#/inbox': '收件箱', '#/tickets': '工单', '#/new': '建单', '#/labels': '工具', '#/settings': '我的 · 设置', '#/admin': '管理', '#/templates': '模板维护' }[h.split('/').slice(0, 2).join('/')] || '';
}

/* ---------- 收件箱：卡片 ---------- */
function pageInbox() {
  const items = inboxItems();
  const cards = items.map(t => `
    <div class="tcard" onclick="nav('#/t/${t.no}')">
      <div class="tc-top"><span class="badge reason">${REASONS[t.waiting.reason] || ''}</span><span class="hint">${esc(t.updated)}</span></div>
      <div class="tc-title">${esc(t.title)}</div>
      <div class="tc-meta">${esc(t.no)} · ${esc(t.kind)} · 发起人 ${esc(creatorName(t))}</div>
      <div class="tc-foot">${labelChips(t)}<span class="hint">› 打开</span></div>
    </div>`).join('');
  return `<div class="bpage">
    <div class="b-note">「现在轮到我」的开启工单，注明原因；名下所有成员 Agent 共用这一份。</div>
    ${cards || '<div class="ecard">收件箱是空的。</div>'}
  </div>`;
}

/* ---------- 工单列表：筛选 chips + 卡片 ---------- */
function pageList() {
  const list = filteredTickets();
  const cards = list.map(t => `
    <div class="tcard" onclick="nav('#/t/${t.no}')">
      <div class="tc-top">
        <span class="badge kind">${esc(t.kind)}</span>
        ${t.status === 'closed' ? `<span class="badge closed">已关闭 · ${CLOSE_TEXT[t.closeReason]}</span>` : `<span class="hint">等待 ${esc(waitText(t))}</span>`}
        <span class="hint" style="margin-left:auto">${esc(t.updated)}</span></div>
      <div class="tc-title">${esc(t.title)}</div>
      <div class="tc-meta">${esc(t.no)} · 发起人 ${esc(creatorName(t))} · 指派 ${esc(t.assignee || '未指派')}</div>
      <div class="tc-foot">${labelChips(t)}<span class="hint">${t.lastKind === 'agent' ? '最近：Agent' : t.lastKind === 'guest' ? '最近：访客' : '最近：本人'}</span></div>
    </div>`).join('');
  return `<div class="bpage">
    <div class="sticky-fb">${filterBarHTML()}</div>
    <div class="b-note">共 <b>${list.length}</b> 张（当前条件）</div>
    ${cards || '<div class="ecard">没有符合条件的工单</div>'}
  </div>`;
}

/* ---------- 工单详情：整页对话流（沿用访客网页的对话流观感） ---------- */
function pageDetail(no) {
  const t = findTicket(no);
  if (!t) return '<div class="bpage ecard">工单不存在</div>';
  UI.cur = t; UI.tlWho = 'all'; UI.deliver = false;
  return `<div class="bpage detail">
    <div class="dhead">
      <a class="back" href="#/tickets">‹ 列表</a>
      <div class="dmeta">${esc(t.no)} · ${t.seq} · ${esc(t.kind)}${t.template ? ' · 模板 ' + esc(tplByKey(t.template).name) : ''}</div>
      <h1>${esc(t.title)}</h1>
      <div>${statusBadge(t)}${t.waiting.reason && t.status === 'open' ? `<span class="hint">（${REASONS[t.waiting.reason]}）</span>` : ''}</div>
      <details class="mmeta">
        <summary>标签 · 指派 · 访客链接</summary>
        <div class="kv"><span>标签</span><span>${labelChips(t)}<button class="btn sm ghost" data-act="open-label">＋</button></span></div>
        <div class="kv"><span>发起人</span><b>${esc(creatorName(t))}</b></div>
        <div class="kv"><span>指派人</span><b>${esc(t.assignee || '未指派')}</b> <button class="btn sm ghost" data-act="open-assign">${t.assignee ? '改派' : '指派'}</button></div>
        <div class="kv"><span>等待方</span><b>${esc(waitText(t))}</b></div>
        <div class="kv"><span>访客链接</span><span>${t.guestLinks.length ? t.guestLinks.map(g => `<span class="mini">${esc(g.note)}</span>`).join(' ') : '<span class="hint">无</span>'}
          <button class="btn sm ghost" data-act="open-links">管理</button></span></div>
        <div class="kv"><span>更多</span><span>${t.status === 'open' ? '<button class="btn sm ghost" data-act="open-close">关闭工单</button>' : '<button class="btn sm ghost" data-act="reopen">重新开启</button>'}</span></div>
      </details>
      <div class="actrow">
        <button class="btn sm ghost" data-act="open-label">🏷 标签</button>
        <button class="btn sm ghost" data-act="open-assign">👤 ${t.assignee ? '改派' : '指派'}</button>
        <button class="btn sm ghost" data-act="open-links">🔗 访客链接</button>
        ${t.status === 'open' ? '<button class="btn sm ghost" data-act="open-close">✔ 关闭</button>' : '<button class="btn sm ghost" data-act="reopen">↺ 重开</button>'}
      </div>
    </div>
    ${acceptPanelHTML(t)}
    <div class="conv">${conversationHTML(t)}</div>
    ${t.status === 'open' ? composerHTML(t)
      : `<div class="composer ro">工单已关闭（${CLOSE_TEXT[t.closeReason] || ''}）<button class="btn sm ghost" data-act="reopen">重新开启</button></div>`}
  </div>`;
}

/* ---------- 工具页（标签字典 + 模板入口 + 管理） ---------- */
function pageTools() {
  return labelsPageHTML() + `
    <div class="card">
      <div class="sec-t" style="margin-top:0">更多工具</div>
      <a class="toolrow" href="#/templates">📄 模板维护<span class="hint">Markdown 模板 · 版本 · 停用</span></a>
      <a class="toolrow" href="#/admin">🛡 管理页<span class="hint">成员账号 · 标签治理 · 备份 · 更新</span></a>
    </div>`;
}

/* ---------- 建单 ---------- */
function pageNew() {
  if (UI.new.step === 1) return newTicketStep1HTML();
  if (UI.new.step === 3) return newTicketDoneHTML();
  return newTicketFormHTML();
}

/* ---------- 路由 ---------- */
function renderApp() {
  const h = (location.hash || '#/inbox').replace(/^#\//, '');
  let main;
  if (h.startsWith('t/')) main = pageDetail(decodeURIComponent(h.slice(2)));
  else if (h.startsWith('templates/')) main = tplEditHTML(h.slice(10));
  else if (h === 'templates') main = tplListHTML();
  else if (h === 'tickets') main = pageList();
  else if (h === 'new') main = pageNew();
  else if (h === 'labels') main = pageTools();
  else if (h === 'settings') main = settingsPageHTML();
  else if (h === 'admin') main = adminPageHTML();
  else main = pageInbox();
  document.getElementById('app').innerHTML = shell(main);
  if (h === 'tickets') wireListPage();
  if (h === 'labels') wireLabelsPage();
  if (h === 'admin') wireAdminPage();
  if (h === 'new' && UI.new.step === 2) wireNewForm();
}
window.refresh = renderApp;
renderApp();
