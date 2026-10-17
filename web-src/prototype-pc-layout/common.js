// 一次性原型：PC 响应式布局（#57），不合并。数据全部写死在内存里。
const S={view:'inbox',w:1366,sel:0,open:false,tab:'convo',nav:'inbox'}
const VER='v0.2.0'
const T=[
 {no:'T-0128',kind:'fb',title:'UD-2300 开机后报警 E21，无法进样',wait:'你（指派人）',me:1,assignee:'陈工',creator:'陈工',guest:'检验科-张三',labels:['机型:UD-2300','待初筛'],upd:'10 分钟前',unread:1,tpl:'Bug 反馈'},
 {no:'T-0127',kind:'task',title:'整理 9 月售后故障统计，按机型出表',wait:'你（指派人）',me:1,assignee:'陈工',creator:'王梦龙 的 Agent',labels:['售后'],upd:'1 小时前',unread:1},
 {no:'T-0125',kind:'fb',title:'打印报告时条码缺一截',wait:'访客',assignee:'陈工',creator:'陈工',guest:'门诊化验-李医生',labels:['机型:UD-1600'],upd:'昨天 16:20'},
 {no:'T-0124',kind:'task',title:'升级 3 号机固件到 2.4.1 并验证',wait:'发起人验收',assignee:'王梦龙',creator:'陈工',labels:['固件','机型:UD-2300'],upd:'昨天 11:02',unread:1},
 {no:'T-0121',kind:'fb',title:'试剂余量显示与实际不符',wait:'部门（未指派）',assignee:null,creator:'李娜',guest:'急诊-周护士',labels:['待初筛'],upd:'10-14'},
 {no:'T-0119',kind:'fb',title:'质控结果导出 Excel 乱码',wait:'指派人',assignee:'李娜',creator:'李娜',labels:['机型:UD-1600','软件'],upd:'10-13'},
 {no:'T-0116',kind:'task',title:'给新同事开通成员账号',wait:'指派人',assignee:'李娜',creator:'陈工',labels:[],upd:'10-12'},
 {no:'T-0110',kind:'fb',title:'样本架识别偶发失败',closed:'已解决',assignee:'陈工',creator:'陈工',guest:'检验科-张三',labels:['机型:UD-2300'],upd:'10-09'},
]
const INBOX=[0,1,3]
function esc(s){return String(s).replace(/[&<>]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;'}[c]))}
function kind(t){return t.closed?`<span class="badge closed">已关闭 · ${t.closed}</span>`:t.kind==='task'?'<span class="badge task">任务</span>':'<span class="badge fb">反馈</span>'}
function waitTx(t){return t.closed?'':`<span class="wait ${t.me?'me':''}">等待 ${t.wait}</span>`}
function chips(ls){return ls.map(l=>`<span class="chip ${l.includes(':')?'dim':''}">${esc(l)}</span>`).join('')}
function set(o){Object.assign(S,o);draw()}
const NAV=[['inbox','📥','收件箱',3],['list','🗂','工单'],['new','📝','建单（高级）','','建单'],['tools','🧰','工具'],['me','👤','我的'],['admin','🛡','管理']]
function go(k){if(k==='inbox')set({view:'inbox',nav:'inbox',open:false});else if(k==='list')set({view:'list',nav:'list',open:false});else set({view:'other',nav:k})}
function side(extra){return `<aside class="side"><div class="brand">工单板</div>
 <button class="btn block" onclick="set({view:'other',nav:'wizard'})">＋ 请人反馈</button>
 <div class="nav">${NAV.map(([k,ic,n,b])=>`<div class="it ${S.nav===k?'on':''}" onclick="go('${k}')">${ic} ${n}${b?`<i class="dot">${b}</i>`:''}</div>${k==='list'&&extra?extra:''}`).join('')}</div>
 <div class="foot"><b>陈工</b> <span class="muted tiny">管理员</span><div class="ver">工单板 ${VER}</div></div></aside>`}
function rail(){return `<aside class="rail"><div class="plus" title="请人反馈" onclick="set({view:'other',nav:'wizard'})">＋</div>
 ${NAV.map(([k,ic,n,b,sn])=>`<div class="it ${S.nav===k?'on':''}" onclick="go('${k}')">${ic}${b?`<i class="dot">${b}</i>`:''}<span>${sn||n}</span></div>`).join('')}<div class="ver">${VER}</div></aside>`}
function other(){const n={wizard:'「请人反馈」表单（#56 已定，这里不展开）',new:'建单（高级）页',tools:'工具：标签字典 / 模板维护',me:'我的：个人设置、Agent token',admin:'管理：成员账号、更新'}[S.nav];return `<div class="ph">${n}<br>（内容沿用现有页面，宽屏下居中、最大宽度 960）</div>`}
const MSGS=[
 {k:'sys',tx:'陈工 通过「请人反馈」创建了工单，访客链接发给 检验科-张三 · 10-15 09:12'},
 {k:'brief',who:'检验科-张三',role:'访客',time:'10-15 10:03',qa:[['出问题的仪器','UD-2300（3 号机）'],['什么时候开始','今天早上开机就这样'],['实际看到了什么','开机自检到一半弹出 E21，进样针不动，重启两次一样'],['之前做过什么','昨天下班前换过清洗液']],files:['📷 报警画面.jpg','📄 system_log_1015.txt']},
 {k:'msg',who:'陈工',role:'成员',mine:1,time:'10-15 10:20',tx:'收到。麻烦再看下清洗液瓶的管路有没有气泡？能拍一张最好。'},
 {k:'msg',who:'检验科-张三',role:'访客',time:'10-15 10:41',tx:'有一点气泡，照片发上来了。',files:['📷 管路.jpg']},
 {k:'msg',who:'陈工 的 Agent',role:'Agent',agent:1,time:'10-15 10:45',tx:'已比对日志：E21 是液路压力异常，和管路进气对得上。建议先按说明书 6.3 排气，再开机自检。'},
]
function convo(guest,noBrief){return MSGS.filter(m=>!(guest&&m.k==='sys')).map(m=>{
 if(m.k==='brief'&&noBrief)return `<div class="sys">${noBrief} · 10-15 10:03</div>`
 if(m.k==='sys')return `<div class="sys">${m.tx}</div>`
 const mine=guest?m.role==='访客':m.mine
 const who=`<div class="who"><b>${guest&&m.role==='访客'?'你':m.who}</b> · ${m.role} · ${m.time}</div>`
 const fs=m.files?`<div class="files">${m.files.map(f=>`<span class="file">${f}</span>`).join('')}</div>`:''
 if(m.k==='brief')return `<div class="msg ${mine?'mine':''}">${who}<div class="small" style="font-weight:600">${guest?'你填写的说明':'访客填写的说明'} · Bug 反馈</div><div class="qa">${m.qa.map(([q,a])=>`<div class="q">${q}</div><div class="a">${a}</div>`).join('')}</div>${fs}</div>`
 return `<div class="msg ${mine?'mine':''} ${m.agent?'agent':''}">${who}<div class="tx">${m.tx}</div>${fs}</div>`}).join('')}
function composer(guest){return `<div class="composer"><textarea placeholder="${guest?'回复（可传照片、日志）':'回复…'}"></textarea><div style="display:flex;flex-direction:column;gap:6px">${guest?'':'<label class="tiny muted"><input type="checkbox"> 交付</label>'}<span style="display:flex;gap:6px"><button class="btn ghost sm">📎</button><button class="btn sm">发送</button></span></div></div>`}
function metaBlocks(t){return `
 <div class="mblk"><div class="h">状态</div>${kind(t)} ${waitTx(t)}</div>
 <div class="mblk"><div class="h">指派人<a>改派</a></div>${t.assignee||'未指派'}</div>
 <div class="mblk"><div class="h">发起人</div>${t.creator}${t.tpl?`<div class="tiny muted">模板 ${t.tpl}</div>`:''}</div>
 <div class="mblk"><div class="h">标签<a>编辑</a></div>${chips(t.labels)||'<span class="muted small">无</span>'}</div>
 <div class="mblk"><div class="h">访客链接<a>管理</a></div>${t.guest?`<div class="link"><b>${t.guest}</b> · 可填写说明<div class="u">http://tb.lan/g/7Kq2…</div><div class="tiny muted">还剩 28 天 · 已用 3 次</div></div>`:'<span class="muted small">还没有</span>'}</div>`}
function actBtns(){return `<button class="btn ghost sm">🏷 标签</button><button class="btn ghost sm">👤 改派</button><button class="btn ghost sm">🔗 访客链接</button><button class="btn ghost sm">✔ 关闭</button>`}
function stripMeta(t){return `<div class="strip">${kind(t)} ${waitTx(t)}<span>指派 <b>${t.assignee||'未指派'}</b></span><span>${chips(t.labels)}</span>${t.guest?`<span>🔗 ${t.guest} · 剩 28 天</span>`:''}<span style="margin-left:auto;color:#2a6fdb">全部信息 ▾</span></div>`}
function guestHead(){const t=T[S.sel];return `<div class="gtop"><span class="logo">工单板</span><span class="muted small">${t.no} · 陈工 请你反馈</span><span style="margin-left:auto" class="small muted">你是访客，不用登录</span></div>`}
function bar(){return `<b>${NAME}</b><span>${DESC0}</span><span style="flex:1"></span>
 ${[['inbox','收件箱'],['list','工单列表'],['detail','工单详情'],['guest','访客页']].map(([k,n])=>`<button class="${S.view===k?'on':''}" onclick="${k==='detail'?"set({view:'detail',nav:'list',open:true})":k==='guest'?"set({view:'guest'})":`go('${k}')`}">${n}</button>`).join('')}
 <span>宽</span>${[1366,1024].map(w=>`<button class="${S.w===w?'on':''}" onclick="set({w:${w}})">${w}</button>`).join('')}<div class="d">${DESC}</div>`}
function fit(){const pc=document.getElementById('pc');pc.style.width=S.w+'px';const s=innerWidth/S.w;pc.style.transform=`scale(${s})`;document.getElementById('stage').style.height=800*s+'px'}
function draw(){document.getElementById('bar').innerHTML=bar();document.getElementById('pc').innerHTML=view();fit()}
addEventListener('resize',fit)
