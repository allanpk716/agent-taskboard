/* ============ 原型 A · app：左侧栏 + 紧凑表格（桌面优先，手机可抽屉） ============ */

function shell(main) {
  const h = location.hash || '#/inbox';
  const act = k => h.startsWith('#/' + k) ? ' class="on"' : '';
  const inboxN = inboxItems().length;
  return `
  <aside class="side" id="side">
    <div class="brand">📋 部门工单平台<span class="hint">成员网页 · 原型 A</span></div>
    <nav>
      <a href="#/inbox"${act('inbox')}><span>📥 收件箱</span>${inboxN ? `<b class="cnt">${inboxN}</b>` : ''}</a>
      <a href="#/tickets"${act('tickets')}><span>🗂 工单列表</span></a>
      <a href="#/new"${act('new')}><span>✏️ 建单</span></a>
      <div class="navg">维护</div>
      <a href="#/templates"${act('templates')}><span>📄 模板维护</span></a>
      <a href="#/labels"${act('labels')}><span>🏷 标签字典与别名</span></a>
      <a href="#/settings"${act('settings')}><span>⚙️ 个人设置</span></a>
      <a href="#/admin"${act('admin')}><span>🛡 管理页</span></a>
    </nav>
    <div class="sidefoot"><b>${esc(ME.name)}</b> · ${ME.admin ? '管理员' : '成员'}<br><span class="hint">全部数据为演示</span></div>
  </aside>
  <div class="mainwrap">
    <header class="topbar">
      <button class="burger" onclick="document.body.classList.toggle('side-open')">☰</button>
      <div class="tb-title">${pageTitle()}</div>
      <span class="sp"></span>
      <button class="btn" onclick="nav('#/new')">＋ 建单</button>
    </header>
    <div class="scrim" onclick="document.body.classList.remove('side-open')"></div>
    <main class="main">${main}</main>
  </div>`;
}
function pageTitle() {
  const h = location.hash || '#/inbox';
  return { '#/inbox': '收件箱', '#/tickets': '工单列表', '#/new': '建单', '#/templates': '模板维护', '#/labels': '标签字典与别名', '#/settings': '个人设置', '#/admin': '管理' }[h.split('/').slice(0, 2).join('/')] || (h.startsWith('#/t/') ? '工单详情' : h.startsWith('#/templates/') ? '模板维护' : '');
}

/* ---------- 收件箱 ---------- */
function pageInbox() {
  const items = inboxItems();
  const rows = items.map(t => `<tr class="clk" onclick="nav('#/t/${t.no}')">
    <td data-l="原因"><span class="badge reason">${REASONS[t.waiting.reason] || ''}</span></td>
    <td data-l="工单"><a href="#/t/${t.no}"><b>${esc(t.no)}</b></a><div>${esc(t.title)}</div>
      <div class="hint">发起人 ${esc(creatorName(t))}</div></td>
    <td data-l="类型"><span class="badge kind">${esc(t.kind)}</span></td>
    <td data-l="等待方">${esc(waitText(t))}</td>
    <td data-l="自">${esc(t.updated)}</td>
  </tr>`).join('');
  return `<div class="page">
    <div class="pagehead"><div><div class="paget">收件箱</div>
      <div class="hint">「现在轮到我」的开启工单，注明原因；同一成员名下的所有成员 Agent 共用这一份。未指派的工单不会出现在收件箱里，要用查询接口找。</div></div></div>
    ${items.length ? `<table class="tbl"><thead><tr><th>原因</th><th>工单</th><th>类型</th><th>等待方</th><th>时间</th></tr></thead><tbody>${rows}</tbody></table>`
      : '<div class="card">收件箱是空的。</div>'}
  </div>`;
}

/* ---------- 工单列表 ---------- */
function pageList() {
  const list = filteredTickets();
  const rows = list.map(t => {
    const who = t.lastKind === 'agent' ? '<span class="badge kind">Agent</span>' : t.lastKind === 'guest' ? '<span class="badge">访客</span>' : '<span class="badge">本人</span>';
    return `<tr class="clk" onclick="nav('#/t/${t.no}')">
    <td data-l="编号"><b>${esc(t.no)}</b><div class="hint">${t.seq}</div></td>
    <td data-l="标题"><a href="#/t/${t.no}">${esc(t.title)}</a>
      ${t.status === 'closed' ? `<span class="badge closed">已关闭 · ${CLOSE_TEXT[t.closeReason]}</span>` : ''}</td>
    <td data-l="类型"><span class="badge kind">${esc(t.kind)}</span></td>
    <td data-l="创建者">${esc(creatorName(t))}</td>
    <td data-l="指派">${esc(t.assignee || '未指派')}</td>
    <td data-l="标签">${labelChips(t)}</td>
    <td data-l="等待方">${esc(waitText(t))}</td>
    <td data-l="更新">${esc(t.updated)}</td>
    <td data-l="操作方">${who}</td>
  </tr>`;
  }).join('');
  return `<div class="page">
    ${filterBarHTML()}
    <div class="cnt-line">共 <b>${list.length}</b> 张（当前条件；接口还支持模板、创建者、时间范围、关键词等条件）</div>
    <table class="tbl"><thead><tr><th>编号</th><th>标题</th><th>类型</th><th>创建者</th><th>指派</th><th>标签</th><th>等待方</th><th>更新</th><th>操作方</th></tr></thead>
    <tbody>${rows || '<tr><td colspan="9" class="hint" style="text-align:center;padding:30px">没有符合条件的工单</td></tr>'}</tbody></table>
  </div>`;
}

/* ---------- 工单详情：左对话流 + 右信息栏 ---------- */
function pageDetail(no) {
  const t = findTicket(no);
  if (!t) return '<div class="page card">工单不存在（演示数据里没有这一张）</div>';
  UI.cur = t; UI.tlWho = 'all'; UI.deliver = false;
  return `<div class="page detail">
    <div class="dmain">
      <div class="dhead">
        <div class="dmeta">${esc(t.no)} · ${t.seq} · ${esc(t.kind)}${t.template ? ' · 模板 ' + esc(tplByKey(t.template).name) : ''}</div>
        <h1>${esc(t.title)}</h1>
        <div class="dhead-r">${statusBadge(t)}${t.waiting.reason && t.status === 'open' ? ` <span class="hint">（${REASONS[t.waiting.reason]}）</span>` : ''}</div>
        <div class="dchips">${labelChips(t)}<button class="btn sm ghost" data-act="open-label">＋ 打标签</button></div>
      </div>
      ${acceptPanelHTML(t)}
      <div class="conv">${conversationHTML(t)}</div>
      ${t.status === 'open' ? composerHTML(t)
        : `<div class="composer ro">工单已关闭（${CLOSE_TEXT[t.closeReason] || ''}）。<button class="btn sm ghost" data-act="reopen">重新开启</button><span class="hint">关闭原因：${CLOSE_TEXT[t.closeReason] || ''}${t.closeReason === 'duplicate' ? '' : ''}；重新开启后访客链接恢复可回复。</span></div>`}
    </div>
    <aside class="dside">
      <div class="card">
        <div class="kv"><span>发起人</span><b>${esc(creatorName(t))}</b></div>
        <div class="kv"><span>指派人</span><b>${esc(t.assignee || '未指派')}</b>
          <button class="btn sm ghost" data-act="open-assign" style="margin-left:6px">${t.assignee ? '改派' : '指派'}</button></div>
        <div class="kv"><span>等待方</span><b>${esc(waitText(t))}</b></div>
        <div class="kv"><span>状态</span><b>${t.status === 'open' ? '开启' : '已关闭'}</b></div>
        <div class="kv"><span>创建 / 更新</span>${esc(t.created)} · ${esc(t.updated)}</div>
      </div>
      <div class="card">
        <div class="sec-t" style="margin-top:0">访客链接（${t.guestLinks.length}）</div>
        ${t.guestLinks.length ? `<table class="mini-tbl">${t.guestLinks.map(g => `<tr><td>${esc(g.note)}${g.canEdit ? ' <span class="mini">可填写说明</span>' : ''}<div class="hint">${esc(g.expires)} · ${esc(g.state)}</div></td></tr>`).join('')}</table>`
          : '<div class="hint">还没有访客链接</div>'}
        <div class="btnrow"><button class="btn ghost sm" data-act="open-links">管理访客链接</button></div>
      </div>
      <div class="card">
        <div class="sec-t" style="margin-top:0">操作</div>
        <div class="btnrow">
          <button class="btn ghost sm" data-act="open-assign">${t.assignee ? '改派' : '指派'}</button>
          ${t.status === 'open'
            ? '<button class="btn ghost sm" data-act="open-close">关闭工单</button>'
            : '<button class="btn ghost sm" data-act="reopen">重新开启</button>'}
        </div>
        <div class="hint">交付用回复里的「标记为交付」；验收在上方交付提示里。</div>
      </div>
    </aside>
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
  else if (h === 'labels') main = labelsPageHTML();
  else if (h === 'settings') main = settingsPageHTML();
  else if (h === 'admin') main = adminPageHTML();
  else main = pageInbox();
  document.getElementById('app').innerHTML = shell(main);
  document.body.classList.remove('side-open');
  if (h === 'tickets') wireListPage();
  if (h === 'labels') wireLabelsPage();
  if (h === 'admin') wireAdminPage();
  if (h === 'new' && UI.new.step === 2) wireNewForm();
}
window.refresh = renderApp;
renderApp();
