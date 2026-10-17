function setMode(m){document.body.classList.toggle('pc',m==='pc');document.querySelectorAll('[data-mode]').forEach(b=>b.classList.toggle('on',b.dataset.mode===m));fit()}
function fit(){const w=document.getElementById('pcwrap');if(w)w.style.transform='scale('+Math.min(1,window.innerWidth/1280)+')'}
window.addEventListener('resize',fit)
function copy(btn){btn.textContent='✓ 已复制';setTimeout(()=>btn.textContent=btn.dataset.t,1200)}
function shareText(who,title,days){const name=(who.split('-').pop()||who).trim();return name+'你好，关于「'+title+'」，麻烦打开下面的链接，按提示把情况写详细一些；有日志、照片可以直接传上去：\nhttp://taskboard.lan/g/tbg_7Kq2…x9\n（不用登录，'+days+' 天内有效）'}
