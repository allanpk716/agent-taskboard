// 一次性原型：访客分步补全说明（#60），不合并。假数据：张工请检验科-张三补全一张 Bug 反馈。
const BUG=[
{k:'model',t:'哪台仪器？',type:'chips',req:1,g:'在仪器背面铭牌上可以看到型号；不确定可以选“不确定”。',opts:['BH-7600CS','UD-2300','SL-1000','不确定']},
{k:'sn',t:'序列号和软件版本是多少？',type:'text',g:'在“设置 → 关于本机”里可以看到。',ex:'序列号 7600CS-2023-0412，软件 V3.2.1'},
{k:'when',t:'什么时候发生的？',type:'text',req:1,g:'大概时间即可，便于我们查仪器日志。',ex:'今天上午 9 点左右'},
{k:'steps',t:'你做了哪些操作？',type:'text',req:1,big:1,g:'按顺序写，一步一行。',ex:'1. 开机自检完成\n2. 进入质控界面\n3. 点击“开始”',more:'比如：在哪个界面、点了哪个按钮、输入了什么。'},
{k:'saw',t:'实际看到了什么？',type:'text',req:1,big:1,g:'屏幕上的报警或提示请原样照抄，包括编号；能拍照最好。',ex:'弹出“E-203 试剂余量不足”',more:'比如：报警编号、提示原文、屏幕上显示的数值。拍张照也行。'},
{k:'expect',t:'你期望看到什么？',type:'text',req:1,g:'本来应该发生什么。',ex:'正常开始质控测试'},
{k:'freq',t:'出现得频繁吗？',type:'chips',req:1,opts:['每次','经常','偶尔','仅一次']},
{k:'impact',t:'对工作有影响吗？',type:'chips',g:'帮助我们判断优先级。',opts:['无法出报告','能绕过','不影响']},
{k:'guess',t:'你的猜测或补充？',type:'text',g:'如果你觉得是什么原因，写在这里；猜错也没关系。',ex:'可能是试剂瓶的液位传感器没识别到'}]
const FREE=[{k:'desc',t:'请把情况写详细',type:'text',req:1,big:1,g:'可以按下面几点写，想到哪写到哪，不用写成一段完整的话：',tips:['哪台设备、哪个系统','什么时候发生的','做了哪些操作（按顺序）','看到了什么（报警、提示原文照抄）','本来应该怎样','出现得频繁吗'],more:'再多写几句会更好，参照上面几点补充。'}]
const S={v:'land',i:0,route:'bug',vals:{},files:[],author:'检验科-张三',open:0}
const FIELDS=()=>S.route==='bug'?BUG:FREE
const MEMBER='张工',TITLE='UD-2300 质控报 E-203',NO='20261017-004'
const esc=s=>(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/"/g,'&quot;')
const filled=f=>!!(S.vals[f.k]||'').trim()
const reqs=()=>FIELDS().filter(f=>f.req)
const reqDone=()=>reqs().filter(filled).length
function setMode(m){document.body.classList.toggle('pc',m==='pc');document.querySelectorAll('[data-mode]').forEach(b=>b.classList.toggle('on',b.dataset.mode===m));fit()}
function setRoute(r){S.route=r;S.v='land';S.i=0;S.vals={};S.files=[];S.open=0;document.querySelectorAll('[data-route]').forEach(b=>b.classList.toggle('on',b.dataset.route===r));draw()}
function fit(){const w=document.getElementById('pcwrap');if(w)w.style.transform='scale('+Math.min(1,window.innerWidth/1280)+')'}
window.addEventListener('resize',fit)
function go(v,i){S.v=v;if(i!==undefined)S.i=i;draw();window.scrollTo(0,0)}
// 文本输入不重绘（保住焦点），只刷新追问提示和按钮
function setv(k,v,redraw){S.vals[k]=v;if(redraw){draw();return}document.querySelectorAll('.nudge-'+k).forEach(e=>e.innerHTML=nudgeText(k));if(window.live)live()}
function nudgeText(k){const f=FIELDS().find(x=>x.k===k);const v=(S.vals[k]||'').trim();if(!f||!f.big||!v||v==='不清楚'||v.length>=20)return '';return '能再具体一点吗？'+f.more}
function input(f,auto){
  if(f.type==='chips')return `<div class="chips">${f.opts.map(o=>`<span class="chip ${S.vals[f.k]===o?'on':''}" onclick="setv('${f.k}','${o}',1)">${o}</span>`).join('')}</div>`
  return `${f.tips?`<ul class="tips">${f.tips.map(t=>`<li>${t}</li>`).join('')}</ul>`:''}<textarea rows="${f.big?(f.tips?8:5):2}" placeholder="${f.ex?esc('示例：'+f.ex.split('\n')[0]+(f.ex.includes('\n')?' …':'')):'在这里写…'}" oninput="setv('${f.k}',this.value)" ${auto?'autofocus':''}>${esc(S.vals[f.k])}</textarea>
  <div class="nudge nudge-${f.k}">${nudgeText(f.k)}</div>
  <div style="display:flex;gap:14px">${f.ex&&f.ex.includes('\n')?`<button class="linkish" onclick="this.nextElementSibling.style.display='block';this.remove()">看完整示例</button><div class="ex" style="display:none;flex:1"><b>示例</b>\n${esc(f.ex)}</div>`:''}
  ${f.req&&!f.tips?`<button class="linkish" onclick="setv('${f.k}','不清楚',1)">不清楚</button>`:''}${f.photo||f.tips?`<button class="linkish" onclick="addFile('photo')">📷 附张照片</button>`:''}</div>`}
const FAKE={photo:['IMG_2041.jpg','1.2 MB','🖼'],album:['截图_0930.png','640 KB','🖼'],file:['UD2300_log_20261017.zip','3.4 MB','📄']}
function addFile(kind){const [n,s,ic]=FAKE[kind];S.files.push({n,s,ic});draw()}
function fileList(){return S.files.length?`<div class="files">${S.files.map((f,i)=>`<div class="file">${f.ic} ${f.n} <span class="muted">· ${f.s}</span><span class="x" onclick="S.files.splice(${i},1);draw()">✕</span></div>`).join('')}</div>`:''}
function attach(m){return (m==='pc'?`<div class="drop" onclick="addFile('file')">把日志、照片拖到这里，或 <u>点击选择文件</u></div>`:
  `<div class="att-btns"><button onclick="addFile('photo')"><span class="ic">📷</span>拍照</button><button onclick="addFile('album')"><span class="ic">🖼</span>相册</button><button onclick="addFile('file')"><span class="ic">📄</span>文件</button></div>`)+
  fileList()+`<div class="small muted" style="margin-top:8px">单个文件不超过 5 MB；日志太大的话，把 NAS 共享盘路径写在说明里就行。</div>`}
function gtopBar(){return `<div class="gtop"><span class="logo">工单台</span><span class="no">${NO}</span><span class="ver">v0.2.0</span></div>`}
function landing(m,startLabel){const n=FIELDS().length,r=reqs().length;return `<div style="${m==='pc'?'max-width:560px;margin:30px auto':''}"><div class="card" style="margin-top:${m==='pc'?0:16}px">
<div class="muted small">${MEMBER} 请你补充一个问题的情况</div>
<div style="font-size:21px;font-weight:700;margin:6px 0 12px">${TITLE}</div>
<div style="background:#f4f8ff;border-radius:10px;padding:12px;line-height:1.75;font-size:14px">
⏱ 大约 ${S.route==='bug'?'5':'3'} 分钟${S.route==='bug'?`，${n} 个问题（${r} 个必填）`:'，写一段说明'}<br>📎 可以上传照片、截图和日志<br>💾 写到一半关掉也没关系，内容保存在这台设备上</div>
<div style="margin-top:14px"><div class="small muted" style="margin-bottom:4px">你的称呼（${MEMBER}会看到）</div><input type="text" value="${esc(S.author)}" oninput="S.author=this.value"></div>
<button class="btn big block" style="margin-top:16px" onclick="go('fill',0)">${startLabel||'开始填写'}</button>
<div class="center" style="margin-top:10px"><button class="linkish" onclick="go('convo')">只想留言，不填了 →</button></div></div></div>`}
function reviewList(onEdit){return FIELDS().map((f,i)=>{const v=(S.vals[f.k]||'').trim();return `<div class="rv"><div style="flex:1"><div class="q">${f.t}${f.req?'<i class="req">必填</i>':''}</div><div class="a ${v?'':(f.req?'miss':'none')}">${v?esc(v):(f.req?'还没写':'没填')}</div></div><span class="ed" onclick="${onEdit(i,f)}">${v?'改':'去写'}</span></div>`}).join('')}
function submitBar(){const ok=reqDone()===reqs().length;return `<div style="margin:0 12px 20px"><button class="btn big block" ${ok?'':'disabled'} onclick="go('done')">提交给${MEMBER}</button>${ok?'':`<div class="small center" style="color:#d64545;margin-top:6px">还有 ${reqs().length-reqDone()} 个必填项没写，不知道的可以写“不清楚”</div>`}</div>`}
function done(m){return `<div style="${m==='pc'?'max-width:560px;margin:30px auto':''}"><div class="bigok">✓</div><div class="center" style="font-size:18px;font-weight:600">已提交给${MEMBER}</div>
<div class="card small" style="line-height:1.8">${MEMBER}看过后会在这里回复你。<b>请收藏这个链接</b>，之后打开就能看到回复，也可以接着补充情况或修改说明。</div>
<div style="margin:0 12px"><button class="btn big block" onclick="go('convo')">查看对话</button></div></div>`}
function briefCard(){const fs=FIELDS().filter(filled);return `<div class="card"><div style="display:flex;align-items:center"><b>说明</b><span class="small muted" style="margin-left:8px">${esc(S.author)} 补全于 刚刚</span><button class="btn ghost" style="margin-left:auto;padding:5px 12px;font-size:13px" onclick="go('fill',0)">✏ 修改说明</button></div>
<div class="brief-qa">${fs.length?fs.map(f=>`<div class="q">${f.t}</div><div class="a">${esc(S.vals[f.k])}</div>`).join(''):'<div class="muted" style="margin-top:8px">（还没填写）</div>'}</div>${fileList()}</div>`}
function convo(m){return `<div style="${m==='pc'?'max-width:760px;margin:0 auto':''}"><div style="padding:14px 12px 0"><div class="small muted">${NO} · ${MEMBER} 发起</div><div style="font-size:19px;font-weight:700">${TITLE}</div></div>
${briefCard()}<div class="ev">${esc(S.author)} 补全了说明 · 刚刚</div><div class="ev">轮到 ${MEMBER} 回复</div><div style="height:${m==='pc'?40:200}px"></div>
<div class="composer"><span style="font-size:20px">📎</span><textarea rows="1" placeholder="补充情况，或回复${MEMBER}…"></textarea><button class="btn" style="padding:8px 14px">发送</button></div></div>`}
function draw(){document.getElementById('ph').innerHTML=view('ph');document.getElementById('pcm').innerHTML=view('pc');fit()}
function protoBar(label,desc){return `<div class="proto"><b>${label}</b>${desc}<span style="flex:1"></span><button data-route="bug" class="on" onclick="setRoute('bug')">Bug 模板</button><button data-route="free" onclick="setRoute('free')">自由描述</button><span style="width:6px"></span><button data-mode="phone" class="on" onclick="setMode('phone')">📱 手机</button><button data-mode="pc" onclick="setMode('pc')">🖥 电脑</button></div>
<div id="phone">${gtopBar()}<div id="ph"></div></div><div id="pcwrap">${gtopBar()}<div id="pcm"></div></div>`}
