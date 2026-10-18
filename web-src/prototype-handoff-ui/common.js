const CAND = window.CAND || 'A'
const NAMES = {
  A: ['候选 A · 信息栏常驻卡片', '把交接文案固定放在右侧信息栏，改派后用弹窗完整确认'],
  B: ['候选 B · 标题栏按钮 + 预览弹窗', '详情不放常驻文案，按需从标题栏或菜单打开预览'],
  C: ['候选 C · 对话流内嵌 + 一键复制', '入口跟随指派事件和待办横幅，点击直接复制']
}
const S = { scene:'s1', who:'wang', expanded:false, info:true, mode:'both', openB:true, drawerB:true, assignedC:true, toastShown:false, openAssignC:false, viewC:false, copiedC:'' }
const T = { no:'T-0124', title:'升级 3 号机固件到 2.4.1 并验证', assignee:'王梦龙', creator:'陈工', labels:['固件','机型:UD-2300'] }
const HUMAN = `王梦龙：陈工派给你一张任务工单 T-0124「升级 3 号机固件到 2.4.1 并验证」，网页：http://taskboard.lan/t/T-0124\n要交给你的 Agent，把下面这段转给它：`
const AGENT = `请处理 agent-taskboard 上的工单 T-0124「升级 3 号机固件到 2.4.1 并验证」。\n1. 先读说明书：http://taskboard.lan/agent.md\n2. 凭证：用我在网页「我的 → Agent」签发的 token，请求头 Authorization: Bearer <token>；没有就先停下来让我去签发，不要用密码登录。\n3. 读工单：curl -H "Authorization: Bearer $TOKEN" "http://taskboard.lan/api/v1/tickets/T-0124?format=markdown"\n4. 照工单末尾「下一步」做；之后的待办看 /api/v1/inbox。`
const BOTH = HUMAN + '\n\n' + AGENT
const $ = id => document.getElementById(id)
const esc = s => String(s).replace(/[&<>]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]))
const chips = a => a.map(x => `<span class="chip ${x.includes(':') ? 'dim' : ''}">${esc(x)}</span>`).join('')
const effectiveWho = () => (S.scene === 's1' || S.scene === 's3') ? S.who : (S.scene === 's4' ? 'wang' : 'chen')
const modeText = () => effectiveWho() === 'wang' ? AGENT : (S.mode === 'both' ? BOTH : AGENT)
const set = o => { Object.assign(S,o); render() }
let toastTimer, toastEl
function toast(msg, content, actionLabel){
  clearTimeout(toastTimer); if(toastEl) toastEl.remove()
  const el = document.createElement('div')
  el.className = 'toast'
  el.innerHTML = esc(msg) + (content ? ' <button>查看内容</button>' : '')
  const btn = el.querySelector('button')
  if(actionLabel) btn.onclick = () => { el.remove(); copyText(content,true) }
  else if(content) btn.onclick = () => { set({viewC:true,copiedC:content}) }
  document.querySelector(CAND === 'C' && S.scene === 's3' ? '.phone' : '.frame-pc').appendChild(el)
  toastEl = el
  toastTimer = setTimeout(() => { el.remove(); if(toastEl === el) toastEl = null }, 2500)
}
async function copyText(text, special, toastMsg='已复制交接文案'){
  let ok = false
  try { await navigator.clipboard.writeText(text); ok = true }
  catch(e) {
    const ta = document.createElement('textarea')
    ta.value = text; ta.className = 'copy-source'; document.body.appendChild(ta); ta.focus(); ta.select()
    ok = document.getSelection().toString() === text
    setTimeout(() => ta.remove(), 100)
  }
  if(ok) toast(toastMsg, special ? text : '')
  else toast('复制失败，请手动选择文字')
}
function protoBar(){
  const scenes = [['s1','PC 工单详情'],['s2','改派完成'],['s3','手机工单详情'],['s4','我的 → Agent']]
  const people = [['wang','我是王梦龙（指派人）'],['chen','我是陈工（发起人）']]
  return `<div class="proto-top"><b class="proto-title">${esc(NAMES[CAND][0])}</b><span class="proto-desc">${esc(NAMES[CAND][1])}</span></div>
  <div class="seg">${scenes.map(([k,n]) => `<button class="${S.scene===k?'on':''}" onclick="set({scene:'${k}',mode:'both',openB:true,drawerB:true,viewC:false})">${n}</button>`).join('')}</div>
  ${S.scene==='s2' ? '<div class="note">改派由发起人陈工操作（本场景固定陈工视角）。</div>' : `<div class="seg who">${people.map(([k,n]) => `<button class="${S.who===k?'on':''}" onclick="set({who:'${k}',viewC:false})">${n}</button>`).join('')}</div>`}
  <div class="note">一次性静态原型。身份切换影响场景 1 和场景 3；场景 4 三个候选相同。</div>`
}
function side(){
  return `<aside class="side"><div class="brand">工单板</div><button class="btn block">＋ 请人反馈</button>
  <div class="nav">${[['收件箱','2'],['工单',''],['工具',''],['我的',''],['管理','']].map(([n,b],i) => `<div class="it ${i===0?'on':''}">${n}${b?`<i class="dot">${b}</i>`:''}</div>`).join('')}</div>
  <div class="foot"><b>${effectiveWho()==='wang'?'王梦龙':'陈工'}</b><div class="tiny muted">工单板 v0.2.0</div></div></aside>`
}
const ROWS = [
  {no:'T-0128', title:'UD-2300 开机后报警 E21，无法进样', who:'陈工'},
  {no:'T-0125', title:'打印报告时条码缺一截', who:'陈工'},
  {no:T.no, title:T.title, who:'王梦龙', on:true},
  {no:'T-0119', title:'质控结果导出 Excel 乱码', who:'李娜'}
]
function list(){return `<div class="lcol"><div class="lhead"><b>工单</b><span class="small muted">4 张</span><input class="search" placeholder="搜索标题、编号、标签…"><div class="fchips"><span class="fc on">进行中</span><span class="fc">指派给我</span><span class="fc">固件</span></div></div><div class="lbody">${ROWS.map(r=>`<div class="row ${r.on?'on':''}"><div class="tiny muted">${r.no}<span style="float:right">昨天</span></div><div class="t">${esc(r.title)}</div><div class="m">等待 指派人 · ${r.who}</div></div>`).join('')}</div></div>`}
function assignmentCard(compact){
  const label = effectiveWho()==='wang' ? '复制给我的 Agent' : '复制发给王梦龙'
  return `<div class="timeline-card" style="${compact?'max-width:100%;':''}"><div class="txt"><b>陈工 把工单指派给 王梦龙 · 今天 10:32</b><br><span class="tiny muted">等待 指派人</span></div><button class="btn sm" onclick="copyText(${effectiveWho()==='wang'?'AGENT':'BOTH'},true)">${label}</button></div>`
}
function conversation(withAssignment){
  return `<div class="thread"><div class="convo"><div class="sys">陈工 创建任务工单 · 10-17 09:20</div>
  <div class="msg"><div class="who"><b>陈工</b> · 成员 · 10-17 09:21</div><div class="tx">请把 3 号机固件升级到 2.4.1，升级后按说明书跑一次自检和验证。</div></div>
  <div class="msg"><div class="who"><b>王梦龙</b> · 成员 · 10-17 10:20</div><div class="tx">收到，我先确认现场备件和当前固件版本。</div></div>
  ${withAssignment ? assignmentCard() : '<div class="sys">陈工 把工单指派给 王梦龙 · 今天 10:32</div>'}
  </div><div class="composer"><textarea placeholder="回复…"></textarea><button class="btn">发送</button></div></div>`
}
function actions(){
  const b = CAND==='B' ? '<button class="btn ghost sm" onclick="set({openB:true})">🤖 交给 Agent</button>' : ''
  return `<button class="btn ghost sm">🏷 标签</button><button class="btn ghost sm">👤 改派</button>${b}<button class="btn ghost sm">🔗 访客链接</button><button class="btn ghost sm">✔ 关闭</button>`
}
function noToken(){return effectiveWho()==='wang' ? `<div class="no-token">你还没签发 Agent token → <a>去「我的 → Agent」</a></div>` : ''}
function preview(opts={}){
  return `${opts.seg ? `<div class="two-seg"><button class="${S.mode==='both'?'on':''}" onclick="set({mode:'both'})">发给王梦龙（含人话行）</button><button class="${S.mode==='agent'?'on':''}" onclick="set({mode:'agent'})">只要 Agent 段</button></div>` : ''}
  <pre class="handoff-pre ${opts.collapsed && !S.expanded ? 'collapse' : ''}">${esc(modeText())}</pre>
  ${opts.collapsed ? `<button class="btn plain tiny" onclick="set({expanded:!S.expanded})">${S.expanded?'收起':'展开全文'}</button>` : ''}
  <div style="display:flex;gap:6px;margin-top:8px"><button class="btn block" onclick="copyText(modeText())">复制交接文案</button></div>${noToken()}`
}
function meta(){
  const handoff = CAND==='A' ? `<div class="mblk"><div class="h">交给 Agent</div><div class="handoff-card">${preview({collapsed:true,seg:S.who==='chen'})}</div></div>` : ''
  return `<div class="mblk"><div class="h">状态</div><span class="badge task">任务</span> <span class="wait">等待 指派人</span></div>
  <div class="mblk"><div class="h">指派人<a>改派</a></div>${T.assignee}</div>${handoff}
  <div class="mblk"><div class="h">发起人</div>${T.creator}</div><div class="mblk"><div class="h">标签</div>${chips(T.labels)}</div><div class="mblk"><div class="h">访客链接</div><span class="muted small">还没有</span></div>`
}
function detail(){
  const banner = CAND==='C' && S.who==='wang' ? `<div class="banner"><span>这张工单等你处理</span><button class="btn sm" onclick="copyText(AGENT,true)">复制给我的 Agent</button></div>` : ''
  return `<div class="dcol"><div class="dhead"><div><span class="badge task">任务</span> <span class="small muted">${T.no}</span></div><h2>${esc(T.title)}</h2><div class="acts">${actions()}</div></div>${banner}<div class="dmain">${conversation(CAND==='C')}<div class="meta">${meta()}</div></div></div>`
}
function modal(title, body, foot){
  return `<div class="modal-mask" onclick="if(event.target===this)closeOverlay()"><div class="modal"><div class="modal-head"><span>${title}</span><button class="btn plain" onclick="closeOverlay()">✕</button></div><div class="modal-body">${body}</div><div class="modal-foot">${foot}</div></div></div>`
}
function closeOverlay(){ set({openB:false,drawerB:false,openAssignC:false}) }
function assignFirst(){
  return modal('改派 · T-0124', `<div class="assign-form"><label>指派给（只能指派成员）</label><select><option>王梦龙</option><option>李娜</option></select><div class="small muted" style="margin-top:8px">改派后，原指派人和新指派人的收件箱都会变化。</div></div>`, `<button class="btn ghost" onclick="closeOverlay()">取消</button><button class="btn" onclick="confirmAssign()">确认改派</button>`)
}
function confirmAssign(){
  if(CAND==='C'){ set({assignedC:true,openAssignC:false,toastShown:true}); toast('已指派给 王梦龙', BOTH, '复制交接文案'); return }
  set({openB:true})
}
function handoffModal(withSeg){
  return modal('交给 Agent', preview({seg:withSeg && S.who==='chen'}), `<span class="tiny muted" style="margin-right:auto;align-self:center">文案不含凭证，对方的 Agent 用它主人自己签的 token</span><button class="btn ghost" onclick="closeOverlay()">关闭</button>`)
}
function assignedModal(){return modal('已指派给 王梦龙', preview({seg:CAND==='B'}), '<button class="btn ghost" onclick="closeOverlay()">关闭</button>')}
function pc(){
  let app = side()+list()
  if(S.scene==='s4') return side()+mePage()
  if(S.scene==='s2'){
    if(CAND==='A') return app+withOverlay(detail(),assignedModal())
    if(CAND==='B') return app+withOverlay(detail(),S.openB?assignedModal():null)
    return app+detail()
  }
  if(CAND==='B' && S.openB) return app+withOverlay(detail(),handoffModal(true))
  if(CAND==='C' && S.viewC) return app+withOverlay(detail(),modal('刚复制的交接文案',`<textarea class="handoff-textarea" readonly style="height:260px">${esc(S.copiedC)}</textarea>`,'<button class="btn ghost" onclick="set({viewC:false})">关闭</button>'))
  return app+detail()
}
function withOverlay(base,overlay){return base+(overlay||'')}
function mobile(){
  const isC=CAND==='C'
  const banner=isC&&S.who==='wang' ? `<div class="banner"><span>这张工单等你处理</span><button class="btn sm" onclick="copyText(AGENT,true)">复制给我的 Agent</button></div>` : ''
  const info=`<div class="mob-info ${S.info?'open':''}"><div class="mob-info-head" onclick="set({info:!S.info})"><span class="badge task">任务</span><b style="flex:1">${T.no}</b><span class="tiny muted">${S.info?'收起':'展开信息'}</span></div><div class="mob-info-body"><div class="small">状态：等待 指派人</div><div class="small">指派人：王梦龙 · 发起人：陈工</div><div style="margin:5px 0">${chips(T.labels)}</div>${CAND==='A'?`<div class="handoff-card">${preview({collapsed:true,seg:S.who==='chen'})}</div>`:''}</div></div>`
  const menu=CAND==='B' ? '<button class="btn plain" onclick="set({drawerB:true})">⋯</button>' : ''
  const overlay=CAND==='B'&&S.drawerB ? `<div class="sheet-mask" onclick="if(event.target===this)closeOverlay()"><div class="sheet"><b>交给 Agent</b><div style="margin-top:10px">${preview({seg:S.who==='chen'})}</div></div></div>` : ''
  const cv=CAND==='C'?`<div class="mob-convo">${assignmentCard(true)}<div class="mob-msg"><div class="tiny muted">陈工 · 成员</div><div style="margin-top:4px">请把 3 号机固件升级到 2.4.1，升级后跑一次自检和验证。</div></div><div class="mob-msg"><div class="tiny muted">王梦龙 · 成员</div><div style="margin-top:4px">收到，我先确认现场备件和当前固件版本。</div></div>${S.viewC?`<textarea class="handoff-textarea" readonly style="height:240px">${esc(S.copiedC)}</textarea>`:''}</div>`:`<div class="mob-convo"><div class="mob-msg"><div class="tiny muted">陈工 · 成员</div><div style="margin-top:4px">请把 3 号机固件升级到 2.4.1，升级后跑一次自检和验证。</div></div><div class="sys">陈工 把工单指派给 王梦龙</div></div>`
  return `${banner}<div class="mob-top"><span class="mob-title">工单板</span><span class="tiny muted">${T.no}</span><span style="margin-left:auto">${menu}</span></div>${info}${cv}<div class="mob-tabbar"><div class="on">📥 收件箱</div><div>🗂 工单</div><div>👤 我的</div></div>${overlay}`
}
function mePage(){
  return `<div class="me-wrap"><div class="me-page"><div class="card"><h1 class="section-title">Agent</h1><p class="section-sub">为名下的 Agent 签发具名 token</p>
  <table class="table"><tr><th>名字</th><th>状态</th><th>最近使用</th><th></th></tr><tr><td>办公室电脑</td><td>生效中</td><td>昨天 18:20</td><td><button class="btn ghost sm">撤销</button></td></tr></table>
  <div style="margin-top:14px"><button class="btn">生成 token</button></div></div></div>
  ${modal('✓ token 已生成（只显示这一次）', `<div class="small muted">王梦龙 的 Agent · 办公室电脑</div><div class="token-box" style="margin-top:7px">tbk_live_example_only_not_a_real_token</div><div class="next-tip"><b>下一步：</b>把这个 token 配进你的 Agent。以后接到工单，在工单上复制交接文案发给它就行——文案里不带 token。</div>`, '<button class="btn ghost" onclick="closeOverlay()">关闭</button><button class="btn" onclick="copyText(\'tbk_live_example_only_not_a_real_token\',false,\'已复制 token\')">复制</button>')}`
}
function scene(){
  if(S.scene==='s3') return `<div class="scale-wrap phone-scale"><div class="phone-shell"><div class="phone">${mobile()}</div></div></div>`
  return `<div class="scale-wrap"><div class="frame-pc">${pc()}</div></div>`
}
function render(){
  $('bar').innerHTML=protoBar(); $('stage').innerHTML=scene(); fit()
  document.documentElement.style.setProperty('--proto-h', $('bar').offsetHeight+'px')
  if(CAND==='C' && S.scene==='s2' && S.assignedC && !S.toastShown){ S.toastShown=true; toast('已指派给 王梦龙', BOTH, '复制交接文案') }
}
function fit(){
  const wrap=$('stage').firstElementChild, box=wrap.firstElementChild
  const w=box.offsetWidth,h=box.offsetHeight,pw=Math.max(innerWidth-32,1),ph=Math.max(innerHeight-32,1)
  const scale=Math.min(pw/w,ph/h,1)
  box.style.transform=`scale(${scale})`; wrap.style.width=w*scale+'px'; wrap.style.height=h*scale+'px'
}
addEventListener('resize',fit)
