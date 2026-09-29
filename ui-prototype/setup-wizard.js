// The wizard edits the same role-scoped draft as the regular modules.
const wizardSteps = () => isProxy()
 ? ['选择服务','连接与登录','视频与目录','共享密钥','核对与保存']
 : ['导入配置包','核对服务','视频连接','连接证书','核对与保存'];
const enabledServices = () => Object.entries(draft.services).filter(([,s])=>s.enabled);
function keyPanel() {
 const selected=enabledServices();
 return `<section class="card"><h2>${isProxy()?'主代理生成共享密钥':'从主代理取得共享密钥'}</h2><p>${isProxy()?'为已启用且没有密钥的服务生成并保存密钥，已有密钥不覆盖。之后导出配置包，交给媒体服务。':'媒体服务使用主代理配置包中的密钥，不另行生成。播放器无需配置密钥。'}</p>${selected.length?selected.map(([k])=>`<div class="row"><strong>${types[k]}</strong><span>密钥状态未知 · 管理服务未连接</span></div>`).join(''):'<p>尚未选择服务。请先在向导中选择，或在服务接入中启用。</p>'}<div class="actions" style="margin-top:16px">${isProxy()?disabledAction('生成密钥')+disabledAction('导出媒体服务配置包'):disabledAction('导入主代理配置包')}</div>${isProxy()?'<details style="margin-top:16px"><summary>高级：更换已有密钥</summary><p>更换后需更新媒体服务，已有播放链接可能失效。仅在迁移或密钥泄露时使用，不属于首次配置的必要步骤。</p>'+disabledAction('更换密钥')+'</details>':''}</section>`;
}
const securityBeforeWizard=security;
security=function(){return securityBeforeWizard().replace(/<section class="card"><h2>(主代理生成共享密钥|从主代理取得共享密钥)<\/h2>[\s\S]*?<\/section>/,keyPanel());};
function setupField(label,key,value,hint='',scope='') {
 return field(label,key,value,hint,scope).replaceAll(`id="${key}"`,`id="setup-${scope}-${key}"`).replaceAll(`for="${key}"`,`for="setup-${scope}-${key}"`);
}
function currentStepErrors(index=step) {
 const e=[],selected=enabledServices();
 if((isProxy()&&index===1)||(!isProxy()&&index===2)){
  const p=listenParts();
  if(!/^\d+$/.test(p.port)||+p.port<1||+p.port>65535)e.push('监听端口应在 1–65535 之间。');
  let valid=false;
  if(p.address.includes(':')){try{valid=Boolean(new URL('http://['+p.address+']/').hostname);}catch{}}
  else valid=/^\d+\.\d+\.\d+\.\d+$/.test(p.address)&&p.address.split('.').every(n=>+n<=255);
  if(!valid)e.push('绑定地址须为有效 IPv4 或 IPv6 地址。');
 }
 const urlOK=v=>{try{const u=new URL(v);return ['http:','https:'].includes(u.protocol)&&!u.username&&!u.password&&!u.search&&!u.hash;}catch{return false;}};
 if(isProxy()&&index===0&&!selected.length)e.push('至少选择一个影视服务。');
 if(!isProxy()&&index===0)e.push('管理服务未连接，尚不能导入配置包。请连接后再继续。');
 if(isProxy()&&index===1){
  const ports=new Set(),hosts=new Set();
  for(const [k,s] of selected){
   if(!urlOK(s.target))e.push(types[k]+'：服务器地址须为有效 HTTP / HTTPS 地址，不包含凭据或查询参数。');
   if(!s.host.trim()&&!s.port.trim())e.push(types[k]+'：登录域名和 IP 端口至少填写一种。');
   if(s.host){if(!/^[a-z0-9.-]+$/i.test(s.host))e.push(types[k]+'：域名不含协议、端口或路径。');if(hosts.has(s.host.toLowerCase()))e.push('不同服务不能使用相同登录域名。');hosts.add(s.host.toLowerCase());}
   if(s.port){if(!/^\d+$/.test(s.port)||+s.port<1||+s.port>65535)e.push(types[k]+'：端口应在 1–65535 之间。');if(ports.has(+s.port))e.push('不同服务不能使用相同 IP 登录端口。');ports.add(+s.port);}
  }
 }
 if(index===2){
  if(!urlOK(draft.media)||!draft.media.startsWith('https://'))e.push('视频访问地址须为完整 HTTPS 地址。');
  for(const [k,s] of selected){
   const upstreams=(s.upstreams??draft.upstream).split(/\n/).map(x=>x.trim()).filter(Boolean);
   if(!upstreams.length||upstreams.some(x=>/[\s/@?#]/.test(x)))e.push(types[k]+'：至少填写一个视频服务器，只填写主机名或主机名:端口。');
   for(const r of s.pathRules||[])if([r.from,r.to].some(x=>!x.startsWith('/')||x.includes('\\')||x.split('/').some(p=>p==='.'||p==='..')))e.push(types[k]+'：目录对应须使用绝对路径，不包含 . 或 ..。');
  }
 }
 if(!isProxy()&&index===3&&draft.transport!=='http')e.push('需要先上传并验证本端证书。管理服务未连接，暂不能完成此步骤。');
 if(isProxy()&&index===3)e.push('需要确认所选服务的密钥已保存。管理服务未连接，暂不能生成或读取密钥。');
 return [...new Set(e)];
}
function advanceSetup(){const e=currentStepErrors();if(e.length){document.getElementById('setupErrors').innerHTML=e.map(x=>`<p>${esc(x)}</p>`).join('');return;}if(step<wizardSteps().length-1){step++;render();}}
function addSetupDirectory(service){
 if(!draft.services[service]?.enabled)return;
 (draft.services[service].pathRules??=[]).push({from:'',to:''});render();
}
function removeSetupDirectory(service,index){
 if(!draft.services[service]?.enabled)return;
 draft.services[service].pathRules.splice(index,1);render();
}
function setupDirectories(service,s){
 return `<section aria-label="${types[service]} STRM 目录" style="margin-top:24px"><h3>${types[service]}：两边的 STRM 目录</h3><p>需要主代理读取 STRM 文件时，在这里填写对应目录。通常只填一组总目录；有多个独立挂载位置时，再分别添加。</p>${(s.pathRules||[]).map((r,i)=>`<fieldset style="margin:16px 0;border:1px solid var(--line);padding:16px"><legend>目录对应 ${i+1}</legend><div class="two"><div class="field"><label for="setup-${service}-from-${i}">${types[service]} 中的 STRM 目录</label><input id="setup-${service}-from-${i}" value="${esc(r.from)}" onchange="draft.services['${service}'].pathRules[${i}].from=this.value;updateBar()"></div><div class="field"><label for="setup-${service}-to-${i}">主代理能读取的对应目录<small>使用 Docker 时，填写主代理容器内的目录。</small></label><input id="setup-${service}-to-${i}" value="${esc(r.to)}" onchange="draft.services['${service}'].pathRules[${i}].to=this.value;updateBar()"></div></div><button onclick="removeSetupDirectory('${service}',${i})">移除 ${types[service]} 目录对应 ${i+1}</button></fieldset>`).join('')}<button onclick="addSetupDirectory('${service}')">${s.pathRules?.length?'再添加':'添加'} ${types[service]} 目录对应</button><details style="margin-top:16px"><summary>两台机器上的目录不同怎么办？</summary><p>先把对应的 STRM 文件同步或共享给主代理，再填写两边实际可见的目录。这里不会自动同步文件或创建容器挂载；媒体服务无需这些目录。</p></details></section>`;
}
function setupNetwork(){return setupField('视频访问地址','media',draft.media,'客户端取视频时使用的完整 HTTPS 地址及公网端口。')+(!isProxy()?listenerFields('setup-listener'):'')+enabledServices().map(([k,s])=>`<section class="card"><h3>${types[k]}的 STRM 视频来源</h3><label for="setup-upstream-${k}">允许访问的视频来源地址（每行一个）</label><textarea id="setup-upstream-${k}" onchange="edit('upstreams',this.value,'${k}')">${esc(s.upstreams??draft.upstream)}</textarea><p class="muted">填写 STRM 文件里视频链接的主机和端口。例如 http://192.0.2.20:19798/dav/movie.mkv，只填 192.0.2.20:19798，不是影视服务器的登录地址。</p>${isProxy()?setupDirectories(k,s):''}</section>`).join('');}
function setupCertificate(){let html=security();html=html.slice(html.indexOf('<section class="card">'));html=html.replace(actionBar(),'');if(isProxy())html=html.replace(listenerFields(),'');return html.replace(/<section class="card"><h2>(主代理生成共享密钥|从主代理取得共享密钥)<\/h2>[\s\S]*$/,'');}
let setupRoleSelected=false;
let pendingSetupRole=null;
function selectSetupRole(role){pendingSetupRole=role;render();}
function confirmSetupRole(){
 if(!['proxy','media'].includes(pendingSetupRole))return;
 switchRole(pendingSetupRole);step=0;setupRoleSelected=true;render();
}
function previousSetupStep(){
 if(step===0){setupRoleSelected=false;pendingSetupRole=editingRole;}else step--;
 render();
}
function setupRolePage(){return header('部署向导','先选择这份配置的部署角色，再填写对应设置。')+`<section class="card"><h2>1. 选择部署角色</h2><button class="choice" aria-pressed="${pendingSetupRole==='proxy'}" onclick="selectSetupRole('proxy')"><strong>主代理</strong><small>连接飞牛影视 / Emby / Jellyfin，生成播放地址和共享密钥。</small></button><button class="choice" aria-pressed="${pendingSetupRole==='media'}" onclick="selectSetupRole('media')"><strong>媒体服务</strong><small>导入主代理配置包，读取并向客户端传输视频。</small></button><p>两端分别配置。选择角色不会部署或更改正在运行的服务。</p>${pendingSetupRole&&pendingSetupRole!==editingRole?'<div class="banner">下一步将切换配置对象。当前已填内容会保留，另一端使用自己的配置，不会混用。刷新页面仍会丢失未保存内容。</div>':''}<div class="footbar"><span>${pendingSetupRole?'已选择：'+(pendingSetupRole==='proxy'?'主代理':'媒体服务'):'请选择一个角色'}</span><button class="primary" ${pendingSetupRole?'':'disabled'} onclick="confirmSetupRole()">下一步</button></div></section>`;}
const switchRoleBeforeSetup=switchRole;
switchRole=function(role){if(role!==editingRole){setupRoleSelected=false;pendingSetupRole=null;}switchRoleBeforeSetup(role);};
wizard=function(){
 if(!setupRoleSelected)return setupRolePage();
 const labels=wizardSteps();step=Math.min(step,labels.length-1);let body='';
 if(step===0)body=isProxy()?`<p>选择要通过主代理接入的服务，可以选择多个。</p>${Object.entries(types).map(([k,name])=>`<label class="check"><input type="checkbox" ${draft.services[k].enabled?'checked':''} onchange="draft.services['${k}'].enabled=this.checked;updateBar()">${name}</label>`).join('')}`:`<p>上传主代理导出的加密配置包，取得服务标识、共享密钥及视频设置。不会自动覆盖已有密钥。</p>${disabledAction('导入主代理配置包')}<p>导入状态：尚未导入。管理服务未连接。</p>`;
 if(step===1)body=isProxy()?listenerFields('setup-shared')+enabledServices().map(([k,s])=>`<section class="card"><h3>${types[k]}</h3>${setupField(types[k]+' 服务器地址','target',s.target,'主代理能访问的 HTTP / HTTPS 地址。',k)}${setupField('客户端登录域名（可选）','host',s.host,'想在播放器里用域名连接时填写；只使用下方的 IP 入口时可留空。',k)}${setupField(ipEntryLabel,'port',s.port,ipEntryHint,k)}${ipEntryHelp()}</section>`).join(''):`<p>核对导入的服务及密钥状态；不创建另一套密钥。</p>${keyPanel()}`;
 if(step===2)body=setupNetwork();
 if(step===3)body=isProxy()?keyPanel().replace(disabledAction('导出媒体服务配置包'),''):setupCertificate();
 if(step===labels.length-1)body=`<p>保存会应用本端配置，可能短暂中断播放。两端配置需要分别保存。</p><div class="row"><strong>配置对象</strong><span>${roleName()}</span></div><div class="row"><strong>已选择服务</strong><span>${enabledServices().map(([k])=>types[k]).join('、')||'无'}</span></div><div class="row"><strong>视频访问地址</strong><span>${esc(draft.media)}</span></div><details><summary>变更明细</summary><button onclick="review()">查看修改前后对比</button></details><div class="actions" style="margin-top:16px">${disabledAction('保存并应用')}${isProxy()?disabledAction('导出媒体服务配置包'):disabledAction('检查连接')}</div><h3 style="margin-top:24px">部署后仍需验证</h3><p>${isProxy()?'确认 STRM 已挂载或同步；在媒体服务导入配置包。':'确认视频来源可以访问。'}核对端口映射，用外网客户端验证起播、拖动和视频数据流向。</p><p class="muted">当前尚未保存或部署，不能据此判断播放成功。</p>`;
 return header('部署向导',`在这里逐步完成${roleName()}配置，无需切换模块。`)+`<div class="steps" aria-label="配置进度"><span>1 / 部署角色：${roleName()}</span>${labels.map((label,i)=>`<span class="${i===step?'on':''}" ${i===step?'aria-current="step"':''}>${i+2} / ${label}</span>`).join('')}</div><section class="card"><h2>${step+2}. ${labels[step]}</h2>${body}<div id="setupErrors" class="error" role="alert" style="margin-top:16px" hidden></div><div class="footbar"><button onclick="previousSetupStep()">上一步</button>${step<labels.length-1?'<button class="primary" onclick="document.getElementById(\'setupErrors\').hidden=false;advanceSetup()">下一步</button>':''}</div></section>`;
};
render();
