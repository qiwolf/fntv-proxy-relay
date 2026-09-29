// Mode-level UI controller. Runtime configuration adapters remain backend work.
const playbackModes={
 redirect:{name:'302 直链',description:'客户端直接连接 STRM 视频来源，视频不经过本代理。',flow:'视频来源 → 客户端',steps:['选择服务','连接与登录','STRM 解析','核对与保存']},
 relay:{name:'同源中继',description:'视频经过原登录入口或 VPS，客户端不必直接访问视频来源。',flow:'视频来源 → 原入口 / VPS → 客户端',steps:['选择服务','连接与登录','中继与目录','核对与保存']},
 single:{name:'单容器直出',description:'在一个容器中同时运行主代理和视频监听，不需要两端交接配置包。',flow:'视频来源 → 本容器视频监听 → 客户端',steps:['选择服务','连接与登录','视频与目录','视频连接加密','本地密钥','核对与保存']},
 split:{name:'分机直出',description:'主代理负责登录和播放地址，独立媒体服务负责传输视频。',flow:'视频来源 → 独立媒体服务 → 客户端'}
};
let playbackMode=null,pendingMode=null,choosingMode=true,modeStep=0;
const modeHelp={
 redirect:{title:'客户端直连视频（302 直链）',purpose:'代理处理登录和播放地址，播放器直接向视频来源取文件，代理不转发视频内容。',scene:'视频链接在客户端所在网络可以直接访问，希望减少代理带宽消耗。',needs:'客户端必须能访问视频来源及跳转后的地址，并满足来源的认证要求。只有服务器能访问的内网链接通常不能这样播放。',limit:'不会把内网视频地址变成公网地址，也不会隐藏客户端收到的视频来源地址。'},
 relay:{title:'原入口转发视频（同源中继）',purpose:'播放器从原登录入口取视频，代理代为读取视频来源，再把内容转发给播放器。',scene:'视频来源在内网，客户端无法直连；希望沿用现有 HTTPS 入口，不另设视频出口。',needs:'入口代理必须能连接视频来源。“同源”指客户端使用同一访问入口，不要求程序都在一台机器上。',limit:'视频消耗原入口的带宽。入口在 VPS 上时，视频仍经过 VPS，不能解决 VPS 带宽瓶颈。当前此模式仅支持飞牛影视。'},
 single:{title:'同一容器提供视频出口（单容器直出）',purpose:'一个容器同时提供登录代理和独立视频监听。播放器取得播放地址后，从视频监听取内容，不需要在两个容器间交接配置包。',scene:'希望简化部署，并且这台机器既能读取视频来源，又能提供客户端可访问的视频出口。',needs:'登录与视频使用不同监听；视频出口需配置可达地址、端口映射或反代，以及 HTTPS。一个容器不代表只占一个端口。',limit:'只有视频入口直接到达这台机器，才能发挥它的出口带宽；若视频入口仍经过 VPS，就仍消耗 VPS 带宽。多服务组合仍待验证。'},
 split:{title:'独立服务器提供视频出口（分机直出）',purpose:'主代理负责登录和签发播放地址；媒体服务负责读取视频并传给客户端。两端分别运行，可以部署在不同地点。',scene:'影视服务器与视频来源不在同一地点，或希望登录经过 VPS、视频从带宽更大的机房直接传出。',needs:'媒体服务必须能读取视频来源，客户端必须能访问媒体出口。主代理生成共享密钥并导出配置包，媒体服务导入。',limit:'部署和维护两端配置；媒体服务无需同步或挂载 STRM 文件。要绕开 VPS，媒体出口本身也不能再由 VPS 转发。'}
};
const modeStates={};
const splitPages={overview,wizard,services,delivery,security,diagnostics,config};
function captureMode(){return {draft:structuredClone(draft),applied:structuredClone(applied),managed:structuredClone(managed),backup:backup?structuredClone(backup):null,roles:structuredClone(roleStates),editingRole,step,modeStep,setupRoleSelected,pendingSetupRole};}
function selectMode(mode){if(!playbackModes[mode])return;pendingMode=mode;render();}
function browseMode(mode){selectMode(mode);document.getElementById('mode-tab-'+mode)?.focus();}
function modeTabKey(event,mode){
 const ids=Object.keys(playbackModes),i=ids.indexOf(mode);let target;
 if(event.key==='ArrowRight')target=ids[(i+1)%ids.length];
 if(event.key==='ArrowLeft')target=ids[(i+ids.length-1)%ids.length];
 if(event.key==='Home')target=ids[0];
 if(event.key==='End')target=ids[ids.length-1];
 if(target){event.preventDefault();browseMode(target);}
}
function openModeChoice(){pendingMode=playbackMode;choosingMode=true;active=1;render();}
function confirmMode(){
 if(!playbackModes[pendingMode])return;
 if(pendingMode!==playbackMode){
  if(playbackMode)modeStates[playbackMode]=captureMode();
  const next=modeStates[pendingMode];
  for(const k of Object.keys(roleStates))delete roleStates[k];
  if(next){draft=structuredClone(next.draft);applied=structuredClone(next.applied);managed=structuredClone(next.managed);backup=next.backup;Object.assign(roleStates,structuredClone(next.roles));editingRole=next.editingRole;step=next.step;modeStep=next.modeStep;setupRoleSelected=next.setupRoleSelected;pendingSetupRole=next.pendingSetupRole;}
  else {draft=structuredClone(initial);applied=structuredClone(initial);managed={certificate:false,keys:{},config:false,bundle:false};backup=null;editingRole='proxy';step=0;modeStep=0;setupRoleSelected=false;pendingSetupRole=null;}
  playbackMode=pendingMode;
 }
 choosingMode=false;active=1;render();
}
function modeChoice(){
 const viewed=pendingMode||playbackMode||'redirect',m=playbackModes[viewed],h=modeHelp[viewed];
 return header('选择播放模式','点击上方标签查看说明，确认后再开始配置。')+`<section class="mode-window"><div class="mode-tablist" role="tablist" aria-label="播放模式">${Object.entries(playbackModes).map(([id,item])=>`<button id="mode-tab-${id}" role="tab" aria-selected="${viewed===id}" aria-controls="mode-panel" tabindex="${viewed===id?'0':'-1'}" onclick="browseMode('${id}')" onkeydown="modeTabKey(event,'${id}')">${item.name}</button>`).join('')}</div><div class="mode-panel" id="mode-panel" role="tabpanel" aria-labelledby="mode-tab-${viewed}" tabindex="0"><h2>${h.title}</h2><p>${h.purpose}</p><div class="banner"><strong>视频怎么走</strong><br>${m.flow}</div><h3>适合什么情况</h3><p>${h.scene}</p><details><summary>需要准备什么？有什么限制？</summary><p style="margin-top:16px">${h.needs}</p><p>${h.limit}</p></details><p class="muted" style="margin-top:24px">以上说明针对 STRM 视频。普通文件、字幕和转码可能继续经过影视服务器。</p>${playbackMode&&viewed!==playbackMode?'<div class="banner">开始配置将切换编辑模式，可能涉及不同的端口、配置格式和启动入口。各模式已填内容分别保留，不会自动迁移密钥或修改运行服务。</div>':''}<div class="footbar"><div><strong>当前查看：${m.name}</strong><br><small>切换标签仅查看说明，不应用配置。</small></div><div class="actions"><button class="primary" onclick="pendingMode='${viewed}';confirmMode()">使用此模式，开始配置</button>${playbackMode?'<button onclick="choosingMode=false;render()">返回当前配置</button>':''}</div></div></div></section>`;
}
function modeLimit(){
 if(playbackMode==='relay')return '当前同源中继支持飞牛影视。Emby / Jellyfin 尚无此模式，不能使用直链代替中继。';
 if(playbackMode==='single')return '传统入口共用视频监听和密钥；多服务直出组合仍需验证。当前界面可编辑，但不能据此应用或导出。';
 return '';
}
function capabilityErrors(){return playbackMode==='relay'&&enabledServices().some(([id])=>id!=='fntv')?['当前版本不支持 Emby / Jellyfin 同源中继，请取消选择或更换模式。']:[];}
function modeServiceSelection(){return `${modeLimit()?'<p class="muted">'+modeLimit()+'</p>':''}${Object.entries(types).map(([id,name])=>`<label class="check"><input type="checkbox" ${draft.services[id].enabled?'checked':''} ${playbackMode==='relay'&&id!=='fntv'?'disabled':''} onchange="draft.services['${id}'].enabled=this.checked;render()">${name}${playbackMode==='relay'&&id!=='fntv'?'（当前版本不支持）':''}</label>`).join('')}`;}
function legacyConnections(){return enabledServices().map(([id,s])=>`<section class="card"><h3>${types[id]}</h3>${setupField(types[id]+' 服务器地址','target',s.target,'填写程序能连接的 HTTP / HTTPS 地址。',id)}${setupField('登录监听端口','port',s.port,'传统入口各服务分别监听；域名反代应转发至对应端口。',id)}</section>`).join('')||'<p>请先选择服务。</p>';}
function sourceFields(){return enabledServices().map(([id,s])=>`<section class="card"><h3>${types[id]}的 STRM 视频来源</h3><label for="mode-source-${id}">允许访问的视频来源地址（每行一个）</label><textarea id="mode-source-${id}" onchange="edit('upstreams',this.value,'${id}')">${esc(s.upstreams??draft.upstream)}</textarea><p>填写 STRM 视频链接中的主机及端口，不填写协议、文件路径或登录地址。</p>${setupDirectories(id,s)}</section>`).join('');}
function mediaPortFields(){return setupField('视频监听端口','videoPort',draft.videoPort??'49963','与登录端口分开。bridge 网络需映射此端口。')+`<details><summary>高级：视频绑定地址</summary>${setupField('视频绑定地址','videoBind',draft.videoBind??'0.0.0.0','默认所有 IPv4 网卡；IPv6 使用 :: 时需实际验证。')}</details>`;}
function standaloneVideo(){
 if(playbackMode==='redirect')return '<p>客户端必须能直接访问 STRM 视频链接，包括链接跳转后的地址。此模式不设置独立视频监听或媒体服务。</p>'+enabledServices().map(([id,s])=>setupDirectories(id,s)).join('');
 if(playbackMode==='relay')return setupField('中继公网入口（可选）','relayBase',draft.relayBase??'','例如 https://tv.example.com；留空使用相对路径，与登录入口同源。')+sourceFields();
 return setupField('视频访问地址','media',draft.media,'客户端取视频时访问的完整 HTTPS 地址，包含公网端口。')+mediaPortFields()+sourceFields();
}
function localKeyPanel(){return `<section class="card"><h2>本容器视频密钥</h2><p>主代理和视频监听在本容器内使用同一份密钥。已有密钥保留，不需要导出再导入。</p><p>状态未知：管理服务未连接。</p>${disabledAction('生成密钥')}<details><summary>高级：更换密钥</summary><p>更换可能使已有播放地址失效，必须确认后执行。</p>${disabledAction('更换密钥')}</details></section>`;}
function localCertificate(){return `<section class="card"><h2>视频连接加密</h2><label for="mode-tls">HTTPS 由谁提供？</label><select id="mode-tls" onchange="edit('transport',this.value);render()"><option value="https" ${draft.transport!=='http'?'selected':''}>本容器的视频监听</option><option value="http" ${draft.transport==='http'?'selected':''}>前置反代（本容器仅可信内网 HTTP）</option></select>${draft.transport==='http'?'<p>证书配置在前置反代上；不要将内网 HTTP 监听直接暴露到公网。</p>':'<p>上传匹配视频访问域名或 IP 的证书链与私钥。路由器端口映射不提供 HTTPS。</p><div class="actions">'+disabledAction('上传视频证书')+disabledAction('粘贴证书与私钥')+'</div>'}<p>不自动签发、续期或同步证书。</p></section>`;}
function modeReview(){return `<h3>核对配置</h3><p>模式：${playbackModes[playbackMode].name}</p><p>运行入口：传统 fntv-proxy；不能导出成统一 services 格式。</p><p>所选服务：${enabledServices().map(([id])=>types[id]).join('、')||'无'}</p>${modeLimit()?'<p>'+modeLimit()+'</p>':''}<details><summary>变更明细</summary><button onclick="review()">查看修改前后对比</button></details>${disabledAction('保存并应用')}<p>保存及运行格式适配尚未接通。请勿把本页填写结果当作部署完成。</p>`;}
function standaloneStepErrors(){
 const errors=capabilityErrors();
 if(modeStep===0&&!enabledServices().length)errors.push('至少选择一个服务。');
 if(modeStep===1){const ports=new Set();for(const [id,s] of enabledServices()){
  try{const u=new URL(s.target);if(!['http:','https:'].includes(u.protocol)||u.username||u.password||u.search||u.hash)throw 0;}catch{errors.push(types[id]+'：填写有效的 HTTP / HTTPS 服务器地址。');}
  if(!/^\d+$/.test(s.port)||+s.port<1||+s.port>65535)errors.push(types[id]+'：端口应在 1–65535 之间。');
  if(ports.has(+s.port))errors.push('登录端口不能重复。');ports.add(+s.port);
 }}
 if(modeStep===2){
  if(playbackMode==='single'){
   try{const u=new URL(draft.media);if(u.protocol!=='https:'||u.username||u.password)throw 0;}catch{errors.push('视频访问地址必须为有效 HTTPS 地址。');}
   const p=draft.videoPort??'49963';if(!/^\d+$/.test(p)||+p<1||+p>65535)errors.push('视频端口应在 1–65535 之间。');
   if(enabledServices().some(([,s])=>+s.port===+p))errors.push('视频端口不能与登录端口相同。');
  }
  for(const [id,s] of enabledServices()){
   if(playbackMode!=='redirect'&&!(s.upstreams??draft.upstream).trim())errors.push(types[id]+'：请填写视频来源地址。');
   for(const r of s.pathRules||[])if([r.from,r.to].some(p=>!p.startsWith('/')||p.includes('\\')||p.split('/').some(x=>x==='.'||x==='..')))errors.push(types[id]+'：目录必须是有效绝对路径。');
  }
 }
 if(playbackMode==='single'&&modeStep===3&&draft.transport!=='http')errors.push('管理服务未连接，尚不能上传和验证视频证书。');
 if(playbackMode==='single'&&modeStep===4)errors.push('管理服务未连接，尚不能确认密钥已安全保存。');
 return [...new Set(errors)];
}
function nextModeStep(){const e=standaloneStepErrors();if(e.length){document.getElementById('modeErrors').innerHTML=e.map(x=>'<p>'+esc(x)+'</p>').join('');return;}modeStep++;render();}
function previousModeStep(){if(modeStep===0){openModeChoice();return;}modeStep--;render();}
wizard=function(){
 if(!playbackMode||choosingMode)return modeChoice();
 if(playbackMode==='split')return splitPages.wizard().replace(/(\d+) \/ /g,(_,n)=>(+n+1)+' / ').replace(/<h2>(\d+)\./g,(_,n)=>'<h2>'+(+n+1)+'.');
 const labels=playbackModes[playbackMode].steps;
 const body=modeStep===0?modeServiceSelection():modeStep===1?legacyConnections():modeStep===2?standaloneVideo():playbackMode==='single'&&modeStep===3?localCertificate():playbackMode==='single'&&modeStep===4?localKeyPanel():modeReview();
 return header('部署向导',playbackModes[playbackMode].name+'：在本向导逐步填写当前模式的配置。')+`<div class="steps"><span>1 / 播放模式</span>${labels.map((s,i)=>`<span class="${i===modeStep?'on':''}" ${i===modeStep?'aria-current="step"':''}>${i+2} / ${s}</span>`).join('')}</div><section class="card"><h2>${modeStep+2}. ${labels[modeStep]}</h2>${body}<div id="modeErrors" role="alert"></div><div class="footbar"><button onclick="previousModeStep()">上一步</button>${modeStep<labels.length-1?'<button class="primary" onclick="nextModeStep()">下一步</button>':''}</div></section>`;
};
overview=function(){if(playbackMode==='split')return splitPages.overview();return header('配置概览',playbackModes[playbackMode].name)+`<section class="card"><h2>视频数据流向</h2><p>${playbackModes[playbackMode].flow}</p><p>此处说明预期链路，不是实时监测。普通文件、字幕及转码需另外验证。</p><p>运行状态：未知，管理服务未连接。</p><button onclick="go(1)">继续部署向导</button></section>`;};
services=function(){if(playbackMode==='split')return splitPages.services();return header('服务接入','传统入口按服务分别设置登录监听。')+`<section class="card">${actionBar()}${modeServiceSelection()}</section>`+legacyConnections();};
delivery=function(){if(playbackMode==='split')return splitPages.delivery();return header('视频传输',playbackModes[playbackMode].description)+`<section class="card">${actionBar()}${standaloneVideo()}</section>`;};
security=function(){if(playbackMode==='split')return splitPages.security();return header('安全与证书','只显示当前模式需要的安全配置。')+(playbackMode==='single'?localCertificate()+localKeyPanel():'<section class="card"><h2>登录入口 HTTPS</h2><p>传统入口的公网 HTTPS 由前置反代提供，证书配置在反代上。本页不上传独立视频服务证书。</p><p>管理入口认证尚未接通；不要在此输入真实秘密。</p></section>');};
diagnostics=function(){if(playbackMode==='split')return splitPages.diagnostics();const check=playbackMode==='redirect'?'客户端能否直接访问视频来源和跳转地址':playbackMode==='relay'?'原入口是否成功读取并中继视频':'本容器视频监听、证书、密钥及实际视频出口';return header('诊断与日志',playbackModes[playbackMode].name)+`<section class="card"><h2>当前模式检查</h2><p>${check}</p>${disabledAction('开始检查')}<p>未执行检查。外网客户端需实际验证起播与拖动；不能用端口连通代替播放验收。</p></section><section class="card"><h2>日志</h2><p>管理服务未连接，暂无日志。</p></section>`;};
config=function(){if(playbackMode==='split')return splitPages.config();return header('配置管理','当前格式：传统 fntv-proxy。模式切换不自动迁移配置。')+`<section class="card"><h2>本端配置与备份</h2><p>模式：${playbackModes[playbackMode].name}。导入需识别配置格式，不能静默丢弃字段。</p><div class="actions">${disabledAction('导入已有配置')}${disabledAction('导出配置')}${disabledAction('恢复备份')}</div><details><summary>查看未保存修改</summary><button onclick="review()">打开变更明细</button></details><p>配置文件不等于含密钥的完整备份。真实导入、导出及应用尚未接通。</p></section>`;};
const renderBeforeModes=render;
const updateBarBeforeModes=updateBar;
updateBar=function(){
 updateBarBeforeModes();
 if(playbackMode&&playbackMode!=='split'){
  document.querySelectorAll('[data-cancel]').forEach(b=>b.disabled=!dirty());
  document.querySelectorAll('[data-change-status]').forEach(b=>b.textContent=dirty()?'有未保存修改':'没有未保存修改');
 }
};
render=function(){
 if(!playbackMode||choosingMode){
  active=1;document.getElementById('main').innerHTML=modeChoice();
  document.getElementById('roleLabel').innerHTML='<span>请选择播放模式</span>';
  document.getElementById('nav').innerHTML='<button class="active" onclick="go(1)">部署向导</button>';
  document.getElementById('reviewTop').hidden=true;document.getElementById('draftLabel').textContent='尚未应用配置';return;
 }
 renderBeforeModes();
 const role=document.getElementById('roleLabel');
 if(playbackMode!=='split')role.innerHTML='';
 role.insertAdjacentHTML('afterbegin',`<span class="pill">${playbackModes[playbackMode].name}</span> <button onclick="openModeChoice()">切换播放模式</button> `);
};
render();
