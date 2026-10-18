// 一次性原型：工单动态标签筛选，不合并。数据与规则仅用于本地对比。
const VER='v0.3.0'
const DIMS={
  '机型':['UD-2300','UD-1600','UD-3100','BH-7600CS','BH-5800','UD-900'],
  '系统':['液路','光路','软件','打印','质控','条码'],
  '科室':['检验科','急诊','门诊化验','体检中心','病理科'],
  '产品线':['尿液','血液'],
  '其他标签':['待初筛','售后','固件','紧急','已复现']
}
const ALIASES=[['2300','机型:UD-2300'],['7600','机型:BH-7600CS'],['乱码','系统:软件'],['检验','科室:检验科']]
const T=[
 {no:'T-0152',kind:'fb',title:'UD-2300 开机后报警 E21，无法进样',wait:'你（指派人）',me:1,assignee:'陈工',creator:'陈工',guest:'检验科-张三',labels:['机型:UD-2300','系统:液路','科室:检验科','待初筛'],upd:'10 分钟前',unread:1,tpl:'Bug 反馈'},
 {no:'T-0151',kind:'fb',title:'UD-2300 清洗阶段压力波动',wait:'指派人',assignee:'陈工',creator:'王梦龙 的 Agent',guest:'急诊-周护士',labels:['机型:UD-2300','系统:液路','科室:急诊','紧急'],upd:'25 分钟前',unread:1},
 {no:'T-0150',kind:'fb',title:'UD-2300 探针吸样量偏低',wait:'发起人验收',assignee:'王梦龙',creator:'陈工',guest:'检验科-李技师',labels:['机型:UD-2300','系统:液路','科室:检验科','产品线:血液','售后'],upd:'1 小时前',unread:1},
 {no:'T-0149',kind:'fb',title:'UD-2300 管路气泡反复出现',wait:'访客',assignee:'陈工',creator:'陈工',guest:'检验科-张三',labels:['机型:UD-2300','系统:液路','科室:检验科','待初筛'],upd:'2 小时前'},
 {no:'T-0148',kind:'fb',title:'UD-2300 试剂余量显示与实际不符',wait:'访客',assignee:'陈工',creator:'李娜',guest:'检验科-张三',labels:['机型:UD-2300','系统:软件','科室:检验科','固件'],upd:'3 小时前'},
 {no:'T-0147',kind:'fb',title:'UD-2300 尿有形成分图像偶发花屏',wait:'部门（未指派）',assignee:null,creator:'王梦龙 的 Agent',guest:'检验科-张三',labels:['机型:UD-2300','系统:软件','产品线:尿液','已复现'],upd:'5 小时前'},
 {no:'T-0146',kind:'fb',title:'UD-2300 打印报告边缘缺失',wait:'指派人',assignee:'李娜',creator:'李娜',guest:'门诊化验-李医生',labels:['机型:UD-2300','科室:门诊化验'],upd:'昨天 16:20'},
 {no:'T-0145',kind:'fb',title:'UD-2300 质控曲线漂移后报警',wait:'发起人验收',assignee:'王梦龙',creator:'陈工',guest:'检验科-张三',labels:['机型:UD-2300','系统:质控','科室:检验科','产品线:血液','售后'],upd:'昨天 11:02'},
 {no:'T-0144',kind:'fb',title:'UD-2300 光源强度自检超时',wait:'你（指派人）',me:1,assignee:'陈工',creator:'陈工',guest:'病理科-赵医生',labels:['机型:UD-2300','系统:光路','科室:病理科','已复现'],upd:'昨天 09:12'},
 {no:'T-0143',kind:'fb',title:'UD-2300 样本架条码识读失败',wait:'访客',assignee:'陈工',creator:'陈工',guest:'急诊-周护士',labels:['机型:UD-2300','系统:条码','科室:急诊'],upd:'10-16'},
 {no:'T-0142',kind:'fb',title:'UD-1600 清洗槽排空不畅',wait:'你（指派人）',me:1,assignee:'陈工',creator:'陈工',guest:'检验科-王主管',labels:['机型:UD-1600','系统:液路','科室:检验科'],upd:'10-16'},
 {no:'T-0141',kind:'fb',title:'UD-1600 液路压力传感器漂移',wait:'指派人',assignee:'陈工',creator:'王梦龙 的 Agent',guest:'门诊化验-李医生',labels:['机型:UD-1600','系统:液路','科室:门诊化验','产品线:尿液','待初筛'],upd:'10-15'},
 {no:'T-0140',kind:'fb',title:'UD-1600 软件升级后报告模板丢失',wait:'访客',assignee:'李娜',creator:'陈工',guest:'检验科-张三',labels:['机型:UD-1600','系统:软件','科室:检验科','售后'],upd:'10-15'},
 {no:'T-0139',kind:'fb',title:'UD-1600 图表导出乱码',wait:'你（指派人）',me:1,assignee:'陈工',creator:'陈工',guest:'急诊-周护士',labels:['机型:UD-1600','系统:软件','科室:急诊','紧急'],upd:'10-14'},
 {no:'T-0138',kind:'fb',title:'UD-1600 打印组件卡纸',wait:'部门（未指派）',assignee:null,creator:'王梦龙 的 Agent',guest:'门诊化验-李医生',labels:['机型:UD-1600','系统:打印','科室:门诊化验','固件'],upd:'10-14'},
 {no:'T-0137',kind:'fb',title:'UD-1600 质控品批次切换失败',wait:'指派人',assignee:'王梦龙',creator:'陈工',guest:'检验科-张三',labels:['机型:UD-1600','系统:质控','科室:检验科'],upd:'10-13'},
 {no:'T-0136',kind:'fb',title:'UD-1600 光路自检中断',wait:'发起人验收',assignee:'陈工',creator:'李娜',guest:'病理科-赵医生',labels:['机型:UD-1600','系统:光路','科室:病理科'],upd:'10-12'},
 {no:'T-0135',kind:'fb',title:'UD-3100 废液阀关闭延迟',wait:'指派人',assignee:'陈工',creator:'陈工',guest:'急诊-周护士',labels:['机型:UD-3100','系统:液路','科室:急诊','产品线:血液','紧急'],upd:'10-11'},
 {no:'T-0134',kind:'fb',title:'UD-3100 数据接口偶发超时',wait:'访客',assignee:'李娜',creator:'王梦龙 的 Agent',guest:'检验科-张三',labels:['机型:UD-3100','系统:软件','科室:检验科','产品线:尿液','售后'],upd:'10-10'},
 {no:'T-0133',kind:'fb',title:'UD-3100 打印驱动兼容问题',wait:'你（指派人）',me:1,assignee:'陈工',creator:'陈工',guest:'检验科-张三',labels:['机型:UD-3100','系统:打印','科室:检验科'],upd:'10-09'},
 {no:'T-0132',kind:'fb',title:'BH-7600CS 进样针复位异响',wait:'访客',assignee:'陈工',creator:'陈工',guest:'检验科-张三',labels:['机型:BH-7600CS','系统:液路','科室:检验科','产品线:尿液'],upd:'10-08'},
 {no:'T-0131',kind:'fb',title:'BH-7600CS 图像算法版本异常',wait:'指派人',assignee:'李娜',creator:'陈工',guest:'急诊-周护士',labels:['机型:BH-7600CS','系统:软件','科室:急诊','紧急'],upd:'10-07'},
 {no:'T-0130',kind:'fb',title:'BH-7600CS 报告打印偏移',wait:'部门（未指派）',assignee:null,creator:'王梦龙 的 Agent',guest:'门诊化验-李医生',labels:['机型:BH-7600CS','系统:打印','科室:门诊化验'],upd:'10-06'},
 {no:'T-0129',kind:'fb',title:'BH-7600CS 质控记录无法保存',wait:'发起人验收',assignee:'王梦龙',creator:'李娜',guest:'检验科-张三',labels:['机型:BH-7600CS','系统:质控','科室:检验科','产品线:尿液','售后'],upd:'10-05'},
 {no:'T-0128',kind:'fb',title:'UD-900 打印模块字体缺失',wait:'你（指派人）',me:1,assignee:'陈工',creator:'陈工',guest:'体检中心-刘医生',labels:['机型:UD-900','系统:打印','科室:体检中心','产品线:血液','固件'],upd:'10-04'},
 {no:'T-0127',kind:'fb',title:'BH-5800 尿沉渣图像色彩异常',wait:'访客',assignee:'李娜',creator:'王梦龙 的 Agent',guest:'体检中心-孙医生',labels:['机型:BH-5800','系统:软件','科室:体检中心','产品线:尿液','已复现'],upd:'10-03'},
 {no:'T-0126',kind:'fb',title:'BH-5800 样本仓温度提示不稳定',wait:'你（指派人）',me:1,assignee:'陈工',creator:'陈工',guest:'急诊-周护士',labels:['机型:BH-5800','科室:急诊','产品线:尿液'],upd:'10-02'},
 {no:'T-0125',kind:'task',title:'整理季度标签治理清单',wait:'指派人',assignee:'李娜',creator:'陈工',labels:['产品线:尿液'],upd:'10-02'},
 {no:'T-0124',kind:'task',title:'整理 10 月售后故障统计，按机型出表',wait:'发起人验收',assignee:'王梦龙',creator:'陈工',labels:['售后'],closed:'已解决',upd:'10-01'},
 {no:'T-0123',kind:'task',title:'归档上周紧急工单处理记录',wait:'指派人',assignee:'李娜',creator:'陈工',labels:['紧急','已复现'],closed:'已解决',upd:'09-30'}
]
const NAV=[['inbox','📥','收件箱',3],['list','🗂','工单'],['new','📝','建单（高级）','','建单'],['tools','🧰','工具'],['me','👤','我的'],['admin','🛡','管理']]
let S={view:'list',nav:'list',w:1366,sel:0,status:'进行中',filter:{inc:[],exc:[]},text:'',expanded:false,panel:false,q:'',browse:null,drop:null,pinned:['机型','系统'],saved:[{name:'UD-2300 液路问题',filter:{inc:['机型:UD-2300','系统:液路'],exc:[]},text:''}],mobileDetail:false}
function esc(s){return String(s).replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]))}
function set(o){Object.assign(S,o);draw()}
function dimOf(label){const i=label.indexOf(':');return i<0?'其他标签':label.slice(0,i)}
function dimKey(label){const d=dimOf(label);return d==='其他标签'?'@'+label:d}
function has(t,label){return t.labels.includes(label)}
function statusOK(t){return S.status==='全部'?true:Boolean(t.closed)===(S.status==='已关闭')}
function matches(t,{ignoreDim=null,ignoreExc=null}={}){
  if(!statusOK(t))return false
  if(S.text&&!(`${t.title} ${t.no} ${t.labels.join(' ')}`).toLowerCase().includes(S.text.toLowerCase()))return false
  const groups={}
  for(const l of S.filter.inc){const k=dimKey(l);if(k===ignoreDim)continue;(groups[k]??=[]).push(l)}
  for(const values of Object.values(groups))if(!values.some(l=>has(t,l)))return false
  for(const l of S.filter.exc){if(l===ignoreExc)continue;if(has(t,l))return false}
  return true
}
function filteredIds(){return T.map((_,i)=>i).filter(i=>matches(T[i]))}
function facetCount(label){return T.filter(t=>t.labels.includes(label)&&matches(t,{ignoreDim:dimKey(label),ignoreExc:label})).length}
function toggleInc(label){
  const a=S.filter.inc.slice(),i=a.indexOf(label),e=S.filter.exc.filter(x=>x!==label)
  if(i>=0)a.splice(i,1);else a.push(label)
  S.filter={inc:a,exc:e};draw()
}
function toggleExc(label){
  const e=S.filter.exc.slice(),i=e.indexOf(label),a=S.filter.inc.filter(x=>x!==label)
  if(i>=0)e.splice(i,1);else e.push(label)
  S.filter={inc:a,exc:e};draw()
}
function removeGroup(key){set({filter:{...S.filter,inc:S.filter.inc.filter(l=>dimKey(l)!==key)}})}
function removeExc(label){set({filter:{...S.filter,exc:S.filter.exc.filter(x=>x!==label)}})}
function clearFilter(){set({filter:{inc:[],exc:[]},text:'',q:'',expanded:false})}
function groupPhrase(ls){const d=dimOf(ls[0]);return d==='其他标签'?ls.join(' 且 '):`${d}是 ${ls.map(x=>x.split(':').slice(1).join(':')).join(' 或 ')}`}
function summary(ids){
  const g=Object.values(S.filter.inc.reduce((m,l)=>{(m[dimKey(l)]??=[]).push(l);return m},{})).map(groupPhrase)
  const parts=[...g,...S.filter.exc.map(l=>`不含 ${l}`)]
  if(S.text)parts.unshift(`标题/编号含 “${S.text}”`)
  return `${parts.length?parts.join('，且 ')+'，':''}共 ${ids.length} 张`
}
function selectedChips(){
  const groups=Object.entries(S.filter.inc.reduce((m,l)=>{(m[dimKey(l)]??=[]).push(l);return m},{}))
  const all=[...groups.map(([key,ls])=>`<span class="schip"><span class="tx">${esc(groupPhrase(ls))}</span><button title="移除" onclick="event.stopPropagation();removeGroup('${esc(key)}')">×</button></span>`),
    ...S.filter.exc.map(l=>`<span class="schip exc"><span class="tx">不含 ${esc(l)}</span><button title="移除" onclick="event.stopPropagation();removeExc('${esc(l)}')">×</button></span>`)]
  let shown=all
  if(!S.expanded&&all.length>2)shown=[...all.slice(0,2),`<button class="morechips" onclick="set({expanded:true})">+${all.length-2}</button>`]
  else if(all.length>2)shown=[...all,`<button class="morechips" onclick="set({expanded:false})">收起</button>`]
  return `<div class="schips ${S.expanded?'expanded':''}">${shown.join('')||'<span class="tiny muted">未选标签条件</span>'}</div><div class="summary">${esc(summary(filteredIds()))}</div>${all.length||S.text?`<button class="tiny" style="border:0;background:none;color:#2a6fdb;padding:2px 0;cursor:pointer" onclick="clearFilter()">清空</button>`:''}`
}
function chips(ls){return ls.map(l=>`<button class="chip ${dimOf(l)==='其他标签'?'':'dim'} ${S.filter.inc.includes(l)?'selected':''}" title="点击按此筛选" onclick="event.stopPropagation();toggleInc('${esc(l)}')">${esc(l)}</button>`).join('')}
function kind(t){return t.closed?`<span class="badge closed">已关闭 · ${t.closed}</span>`:t.kind==='task'?'<span class="badge task">任务</span>':'<span class="badge fb">反馈</span>'}
function waitTx(t){return t.closed?'':`<span class="wait ${t.me?'me':''}">等待 ${t.wait}</span>`}
function lrows(ids){return ids.map(i=>{const t=T[i];return `<div class="row ${t.unread?'unread':''} ${S.sel===i?'on':''}" onclick="set({sel:${i},mobileDetail:S.w===390})">
 <div class="top">${kind(t)}<span class="tiny muted">${t.no}</span><span class="tiny muted time">${t.upd}</span></div>
 <div class="t">${esc(t.title)}</div><div class="m">${waitTx(t)||'已完成'} · 指派 ${t.assignee||'未指派'}</div><div class="tags">${chips(t.labels)||'<span class="tiny muted">无标签</span>'}</div></div>`}).join('')}
function go(k){set({view:k==='inbox'||k==='list'?k:'other',nav:k,panel:false,drop:null,mobileDetail:false})}
function side(extra=''){return `<aside class="side"><div class="brand">工单板</div><button class="btn block" onclick="set({view:'other',nav:'wizard'})">＋ 请人反馈</button>
 <div class="nav">${NAV.map(([k,ic,n,b])=>`<div class="it ${S.nav===k?'on':''}" onclick="go('${k}')">${ic} ${n}${b?`<i class="dot">${b}</i>`:''}</div>${k==='list'&&extra?extra:''}`).join('')}</div>
 <div class="foot"><b>陈工</b> <span class="muted tiny">管理员</span><div class="ver">工单板 ${VER}</div></div></aside>`}
function rail(){return `<aside class="rail"><div class="plus" onclick="set({view:'other',nav:'wizard'})">＋</div>${NAV.map(([k,ic,n,b,sn])=>`<div class="it ${S.nav===k?'on':''}" onclick="go('${k}')">${ic}${b?`<i class="dot">${b}</i>`:''}<span>${sn||n}</span></div>`).join('')}<div class="ver">${VER}</div></aside>`}
const MSGS=[
 {k:'sys',tx:'陈工 通过「请人反馈」创建工单，访客链接已发出 · 10-15 09:12'},
 {k:'brief',who:'检验科-张三',role:'访客',time:'10-15 10:03',qa:[['出问题的仪器','UD-2300（3 号机）'],['实际看到了什么','开机自检到一半弹出 E21，进样针不动'],['之前做过什么','昨天下班前换过清洗液']],files:['📷 报警画面.jpg','📄 system_log.txt']},
 {k:'msg',who:'陈工',role:'成员',mine:1,time:'10-15 10:20',tx:'收到。麻烦再看下清洗液瓶管路有没有气泡？能拍一张最好。'},
 {k:'msg',who:'检验科-张三',role:'访客',time:'10-15 10:41',tx:'有一点气泡，照片发上来了。',files:['📷 管路.jpg']},
 {k:'msg',who:'陈工 的 Agent',role:'Agent',agent:1,time:'10-15 10:45',tx:'已比对日志：E21 与液路压力异常吻合。建议先按说明书 6.3 排气，再开机自检。'}
]
function convo(){return MSGS.map(m=>{
 if(m.k==='sys')return `<div class="sys">${m.tx}</div>`
 const fs=m.files?`<div class="files">${m.files.map(f=>`<span class="file">${f}</span>`).join('')}</div>`:''
 if(m.k==='brief')return `<div class="msg ${m.mine?'mine':''}"><div class="who"><b>${m.who}</b> · ${m.role} · ${m.time}</div><div class="small" style="font-weight:650">访客填写的说明</div><div style="margin-top:5px">${m.qa.map(([q,a])=>`<div class="tiny muted">${q}</div><div style="line-height:1.5;margin-bottom:4px">${a}</div>`).join('')}${fs}</div></div>`
 return `<div class="msg ${m.mine?'mine':''} ${m.agent?'agent':''}"><div class="who"><b>${m.who}</b> · ${m.role} · ${m.time}</div><div class="tx">${m.tx}</div>${fs}</div>`}).join('')}
function composer(){return `<div class="composer"><textarea placeholder="回复…"></textarea><div style="display:flex;flex-direction:column;gap:6px"><span style="display:flex;gap:6px"><button class="btn ghost sm">📎</button><button class="btn sm">发送</button></span></div></div>`}
function metaBlocks(t){return `<div class="mblk"><div class="h">状态</div>${kind(t)} ${waitTx(t)}</div>
 <div class="mblk"><div class="h">指派人</div>${t.assignee||'未指派'}</div><div class="mblk"><div class="h">发起人</div>${t.creator}${t.tpl?`<div class="tiny muted">模板 ${t.tpl}</div>`:''}</div>
 <div class="mblk"><div class="h">标签（点击筛选）</div>${chips(t.labels)||'<span class="muted small">无</span>'}</div>
 <div class="mblk"><div class="h">访客链接</div>${t.guest?`<div class="small">${t.guest}</div>`:'<span class="muted small">还没有</span>'}</div>`}
function stripMeta(t){return `<div class="strip">${kind(t)} ${waitTx(t)}<span>指派 <b>${t.assignee||'未指派'}</b></span><span>${chips(t.labels)}</span></div>`}
function detail(wide){const t=T[S.sel];return `<div class="dcol"><div class="dhead"><div style="display:flex;gap:6px;align-items:center">${kind(t)}<span class="small muted">${t.no}</span></div><h2>${esc(t.title)}</h2><div class="acts"><button class="btn ghost sm">🏷 标签</button><button class="btn ghost sm">👤 改派</button><button class="btn ghost sm">✔ 关闭</button></div></div>${wide?'':stripMeta(t)}<div class="dmain"><div style="flex:1;display:flex;flex-direction:column;min-width:0"><div class="convo">${convo()}</div>${composer()}</div>${wide?`<div class="meta">${metaBlocks(t)}</div>`:''}</div></div>`}
function listHead(ids){return `<div class="ltitle"><b>${S.view==='inbox'?'收件箱':'工单'}</b><span class="muted small lcount">${ids.length} 张</span><span style="flex:1"></span><span class="statuschips">${['进行中','已关闭','全部'].map(x=>`<button class="sc ${S.status===x?'on':''}" onclick="set({status:'${x}'})">${x}</button>`).join('')}</span></div>${filterHead(ids)}${selectedChips()}`}
function view(){
  const ids=filteredIds();if(!ids.includes(S.sel)&&ids.length)S.sel=ids[0]
  if(S.w===390)return mobileView(ids)
  const wide=S.w>=1280,n=wide?side(typeof sideExtra==='function'?sideExtra():''):rail()
  if(S.view==='other')return n+`<div class="ph">这页沿用现有内容，原型不展开</div>`
  return n+`<div class="lcol" style="width:${wide?380:320}px"><div class="lhead">${listHead(ids)}</div><div class="lbody">${lrows(ids)}</div>${panelOverlay(ids)}</div>${detail(wide)}`
}
function mobileView(ids){
  if(S.mobileDetail){const t=T[S.sel];return `<div class="mobile"><div class="mtop"><button class="plain" style="font-size:18px" onclick="set({mobileDetail:false})">‹</button><b>${t.no}</b><span class="tiny muted" style="margin-left:auto">${S.status}</span></div><div class="mdetail"><div style="flex:1;overflow:auto;padding:14px 14px 20px">${kind(t)}<h2 style="font-size:18px;line-height:1.4;margin:6px 0">${esc(t.title)}</h2><div style="margin:8px 0">${chips(t.labels)}</div>${convo()}</div></div><div class="mtabs">${NAV.slice(0,4).map(([k,i,n])=>`<div class="it ${k==='list'?'on':''}" onclick="go('${k}')">${i}<span>${n}</span></div>`).join('')}</div></div>`}
  return `<div class="mobile"><div class="mtop"><b>工单</b><span class="tiny muted lcount">${ids.length} 张</span><span style="flex:1"></span><span class="statuschips">${['进行中','已关闭'].map(x=>`<button class="sc ${S.status===x?'on':''}" onclick="set({status:'${x}'})">${x}</button>`).join('')}</span></div><div style="padding:8px 10px;background:#fff;border-bottom:1px solid #f0f2f6">${typeof mobileExtra==='function'?`<div class="savedscroll">${mobileExtra()}</div>`:''}${filterHead(ids)}${selectedChips()}</div><div class="mlist">${lrows(ids)}</div>${mobileOverlay(ids)}<div class="mtabs">${NAV.slice(0,4).map(([k,i,n])=>`<div class="it ${k==='list'?'on':''}" onclick="go('${k}')">${i}<span>${n}</span></div>`).join('')}</div></div>`
}
function fit(){const p=document.getElementById('pc'),bar=document.getElementById('bar');p.style.width=S.w+'px';p.style.height=S.w===390?Math.max(700,innerHeight-bar.offsetHeight):800+'px';const s=Math.min(innerWidth/S.w,1);p.style.transform=`scale(${s})`;document.getElementById('stage').style.height=p.offsetHeight*s+'px'}
function draw(){document.getElementById('bar').innerHTML=bar();document.getElementById('pc').innerHTML=view();fit()}
addEventListener('resize',fit)
