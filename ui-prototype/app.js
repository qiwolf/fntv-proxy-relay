// Live management bridge. Secrets and sessions are never written to browser storage.
let sessionToken='',savedVersion=0,liveStatus={},keyStatus={},certStatus={},backupVersions=[];
let saving=false;
async function api(path,body){
 const r=await fetch('/api/'+path,{method:body===undefined?'GET':'POST',headers:{'Content-Type':'application/json',Authorization:'Bearer '+sessionToken},body:body===undefined?undefined:JSON.stringify(body)});
 const value=await r.json();if(!r.ok){if(r.status===401&&!path.startsWith('auth/')){sessionToken='';await showLogin();}const e=Error(value.error||'操作失败');e.status=r.status;throw e;}return value;
}
async function perform(fn){try{await fn();}catch(e){notice(e.message);}}
function checkNewPassword(password){if([...password].length<12||new TextEncoder().encode(password).length>72)throw Error('密码至少 12 个字符，UTF-8 编码后不超过 72 字节（纯中文最多 24 字）。');}
function loginMarkup(first){return `<section class="card" style="max-width:512px;margin:64px auto"><h1>${first?'设置管理员':'管理员登录'}</h1><p>${first?'仅有一个管理员。设置后请妥善保管账号和密码。':'使用已设置的管理员账号。'}</p><form id="loginForm"><label for="admin-name">账号名</label><input id="admin-name" name="username" autocomplete="username" required maxlength="64"><label for="admin-password">密码</label><input id="admin-password" name="password" type="password" autocomplete="${first?'new-password':'current-password'}" minlength="12" maxlength="72" required>${first?'<p>密码至少 12 个字符。</p><label for="admin-confirm">确认密码</label><input id="admin-confirm" name="confirm" type="password" autocomplete="new-password" required>':''}<p id="login-error" role="alert"></p><button class="primary" type="submit">${first?'设置并登录':'登录'}</button></form></section>`;}
async function showLogin(){
 sessionToken='';document.getElementById('account-actions')?.remove();const dialog=document.getElementById('dialog');dialog.close();dialog.innerHTML='';document.getElementById('reviewTop').hidden=true;document.getElementById('draftLabel').textContent='请登录管理';document.querySelector('.topbar .pill').textContent='尚未登录';
 const status=await api('auth/status');document.getElementById('main').innerHTML=loginMarkup(!status.initialized);document.getElementById('nav').innerHTML='';document.getElementById('roleLabel').innerHTML='';
 document.getElementById('loginForm').onsubmit=async e=>{e.preventDefault();const f=e.target;const button=f.querySelector('button');button.disabled=true;try{
  if(!status.initialized&&f.elements.password.value!==f.elements.confirm.value)throw Error('两次密码不一致');
  if(!status.initialized)checkNewPassword(f.elements.password.value);
  const value=await api('auth/'+(status.initialized?'login':'setup'),{username:f.elements.username.value,password:f.elements.password.value});sessionToken=value.token;f.reset();await loadLive();
 }catch(err){const output=document.getElementById('login-error');if(output)output.textContent=err.message;else notice(err.message);}finally{button.disabled=false;}};
}
function toConfig(){
 if(!playbackMode)throw Error('请先选择播放模式');
 const split=playbackMode==='split',media=split&&!isProxy(),p=listenParts(),chosen=enabledServices();
 const first=chosen[0]?.[0];
 const c={version:1,mode:playbackMode,listen:{address:split?p.address:(draft.mainBind||'0.0.0.0'),port:split?+p.port:+(draft.services[first]?.port||28005)},services:{},transport:(playbackMode==='single'||media)?(draft.transport||'https'):'http'};
 if(split)c.role=editingRole;
 if(playbackMode!=='redirect')c.public_base_url=playbackMode==='relay'?(draft.relayBase||''):draft.media;
 if(playbackMode==='single')c.media_listen={address:draft.videoBind||'0.0.0.0',port:+(draft.videoPort||49963)};
 for(const [id,s]of Object.entries(draft.services)){
  const out={enabled:!!s.enabled};c.services[id]=out;
  out.allowed_upstreams=(s.upstreams??draft.upstream??'').split('\n').map(x=>x.trim()).filter(Boolean);
  if(!media){if(s.target)out.target=s.target;out.strm_directories=(s.pathRules||[]).map(r=>({source:r.from,local:r.to}));if(split&&s.host)out.hosts=s.host.split(/\s+/).filter(Boolean);if(s.port&&(split||id!==first))out.listen={address:s.bind||'0.0.0.0',port:+s.port};}
 }
 return c;
}
function fromConfig(c){
 for(const id of Object.keys(roleStates))delete roleStates[id];for(const mode of Object.keys(modeStates))delete modeStates[mode];step=0;modeStep=0;
 playbackMode=c.mode;pendingMode=c.mode;choosingMode=false;editingRole=c.role||'proxy';setupRoleSelected=true;pendingSetupRole=editingRole;
 draft=structuredClone(initial);const address=c.listen.address||'0.0.0.0';draft.mainBind=address;draft.listen=(address.includes(':')?'['+address+']':address)+':'+c.listen.port;draft.media=c.public_base_url||'';draft.relayBase=c.public_base_url||'';draft.transport=c.transport||'http';draft.videoPort=String(c.media_listen?.port||49963);draft.videoBind=c.media_listen?.address||'0.0.0.0';
 let first=true;for(const id of Object.keys(types)){const s=c.services[id]||{enabled:false};Object.assign(draft.services[id],{enabled:s.enabled,target:s.target||'',host:(s.hosts||[]).join(' '),bind:s.listen?.address||'0.0.0.0',port:s.listen?String(s.listen.port):(c.mode!=='split'&&s.enabled&&first?String(c.listen.port):''),upstreams:(s.allowed_upstreams||[]).join('\n'),pathRules:(s.strm_directories||[]).map(r=>({from:r.source,to:r.local}))});if(s.enabled)first=false;}
}
async function refreshStatus(){[liveStatus,keyStatus,certStatus,backupVersions]=await Promise.all([api('status'),api('keys'),api('certificate'),api('backups')]);}
async function loadLive(){await refreshStatus();try{const d=await api('config');savedVersion=d.version;fromConfig(d.data);applied=structuredClone(draft);active=0;}catch(e){if(e.status!==404)throw e;savedVersion=0;}render();}
async function saveLive(applyNow){
 if(saving)return false;saving=true;let saved=false;const snapshot=structuredClone(draft);
 try{const c=toConfig();const doc=await api('config',{version:savedVersion,data:c});savedVersion=doc.version;applied=snapshot;saved=true;if(applyNow)await api('apply',{version:savedVersion});await refreshStatus();render();notice(applyNow?'配置已应用，请验证客户端播放':'配置已保存，运行服务未变更');return true;
 }catch(e){if(saved&&sessionToken){try{await refreshStatus();}catch(_){}render();}notice((saved?'配置已保存，但'+(applyNow?'应用未完成：':'状态刷新失败：'):'保存失败：')+e.message);return false;}finally{saving=false;}
}
actionBar=function(serviceOnly=false){return `<div class="actions page-actions">${serviceOnly?`<button aria-pressed="${draft.services[tab].enabled}" title="保存并应用后生效" onclick="draft.services[tab].enabled=!draft.services[tab].enabled;render()">服务开关：${draft.services[tab].enabled?'已启用':'已关闭'}</button>`:''}<button onclick="cancelChanges(${serviceOnly})" title="撤销尚未保存的修改">取消修改</button><button onclick="saveLive(false)" title="只保存配置，不改变正在运行的服务">保存配置</button><button class="primary" onclick="saveLive(true)" title="保存并重启本程序管理的播放进程；失败尝试恢复旧配置">保存并应用</button></div><p class="muted">保存范围：本端完整配置。应用会短暂中断本端播放连接。</p>`;};
keyPanel=function(){return `<section class="card"><h2>共享密钥</h2>${enabledServices().map(([id])=>`<p>${types[id]}：${keyStatus[id]?'已保存':'未配置'}</p>`).join('')}<p>播放器无需配置密钥。生成时复用已有密钥，不覆盖。</p>${editingRole!=='media'?'<p>点击“生成密钥”会先保存本端整份配置，包括其他页面尚未保存的修改，再为已启用且没有密钥的服务生成密钥。不会应用配置，也不会重启服务；保存失败时不会生成密钥。</p><button title="先保存本端整份配置，再生成缺失密钥；不应用、不重启" onclick="generateLiveKeys()">生成密钥</button>':'<button onclick="bundleDialog(true)">导入主代理配置包</button>'}${playbackMode==='split'&&isProxy()?'<button onclick="bundleDialog(false)">导出媒体服务配置包</button>':''}</section>`;};
localKeyPanel=keyPanel;
async function generateLiveKeys(){if(!await saveLive(false))return false;return perform(async()=>{keyStatus=await api('keys/generate',{version:savedVersion});render();notice('密钥已安全保存');});}
function certificatePanel(){return `<section class="card"><h2>视频 HTTPS 证书</h2><p>本程序直接提供 HTTPS 时需要。反代提供 HTTPS 时，证书留在反代。</p><label for="live-transport">视频监听连接方式</label><select id="live-transport" onchange="edit('transport',this.value);render()"><option value="https" ${draft.transport!=='http'?'selected':''}>本程序提供 HTTPS</option><option value="http" ${draft.transport==='http'?'selected':''}>内网 HTTP，由前置反代提供 HTTPS</option></select>${draft.transport==='http'?'<p>不要将未加密的视频监听直接暴露到公网。</p>':`<p>${certStatus.configured?'已保存证书 · 到期 '+esc(certStatus.expires_at):'尚未配置证书'}</p><button onclick="certificateDialog()">上传或粘贴证书</button>`}<details><summary>域名、IP 和路由器端口映射</summary><p>证书必须覆盖客户端实际访问的域名或 IP。端口映射只转发连接，不提供证书；IPv6 直连也一样。证书由用户提供，不自动签发、续期或同步。自签证书可能不被播放器信任。</p></details></section>`;}
localCertificate=certificatePanel;
security=function(){return header('安全与证书','本端连接加密和播放授权。')+actionBar()+((playbackMode==='single'||(playbackMode==='split'&&!isProxy()))?certificatePanel():'<section class="card"><h2>登录入口 HTTPS</h2><p>主代理登录入口由前置反代提供 HTTPS。这里不上传反代证书。</p></section>')+(['single','split'].includes(playbackMode)?keyPanel():'');};
setupCertificate=()=> (playbackMode==='split'&&isProxy())?'<p>主代理登录入口由前置反代提供 HTTPS，无需在主代理上传证书。</p>':certificatePanel();
function dialogHTML(html,actions=''){const d=document.getElementById('dialog');d.innerHTML=html+'<div class="dialog-actions"><button onclick="document.getElementById(\'dialog\').close()">关闭</button>'+actions+'</div>';if(!d.open)d.showModal();}
function certificateDialog(){dialogHTML(`<h2>上传或粘贴证书</h2><label for="cert-file">证书链 PEM 文件</label><input id="cert-file" type="file" onchange="readTextFile(this,'cert-text')"><textarea id="cert-text" aria-label="证书链" placeholder="-----BEGIN CERTIFICATE-----"></textarea><label for="key-file">私钥 PEM 文件</label><input id="key-file" type="file" onchange="readTextFile(this,'key-text')"><textarea id="key-text" aria-label="证书私钥" placeholder="-----BEGIN PRIVATE KEY-----"></textarea><p>验证成功才替换旧证书；应用配置后由视频监听使用。</p><button class="primary" onclick="uploadCertificate()">验证并保存证书</button>`);}
async function readTextFile(input,id){await perform(async()=>{const f=input.files[0];if(!f)return;if(f.size>1048576)throw Error('文件超过 1 MiB');document.getElementById(id).value=await f.text();});}
async function uploadCertificate(){await perform(async()=>{certStatus=await api('certificate',{version:certStatus.version||0,certificate_pem:document.getElementById('cert-text').value,private_key_pem:document.getElementById('key-text').value,public_url:draft.media});document.getElementById('dialog').innerHTML='';document.getElementById('dialog').close();render();notice('证书已验证保存；请应用配置');});}
function download(name,text){const u=URL.createObjectURL(new Blob([text],{type:'application/json'}));const a=document.createElement('a');a.href=u;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(u),1000);}
config=function(){return header('配置管理','配置文件不含密钥和证书；导入后先核对，再保存应用。')+(playbackMode==='split'?`<section class="card"><h2>${isProxy()?'交给媒体服务':'接收主代理配置'}</h2><button onclick="bundleDialog(${!isProxy()})">${isProxy()?'导出加密配置包':'导入加密配置包'}</button><p>包含服务密钥与必要的视频设置，不包含管理员密码、证书私钥或 STRM 文件。</p></section>`:'')+`<section class="card"><h2>本端配置备份与恢复</h2><button onclick="download('relay-config.json',JSON.stringify(toConfig(),null,2))">导出配置</button><button onclick="configDialog()">上传或粘贴配置</button><button onclick="review()">查看未保存修改</button><h3>历史版本</h3>${backupVersions?.length?backupVersions.map(v=>`<p>版本 ${v} <button onclick="restoreVersion(${v})">恢复到编辑区</button></p>`).join(''):'<p>暂无历史版本</p>'}</section>`;};
function configDialog(){dialogHTML('<h2>导入管理配置</h2><p>接受从本界面导出的 JSON / YAML 管理配置，未知字段会拒绝，不会静默丢弃。</p><input aria-label="配置文件" type="file" onchange="readTextFile(this,\'config-text\')"><textarea id="config-text" aria-label="配置内容"></textarea><button onclick="importConfig()">校验并放入编辑区</button>');}
const configBody=config;config=function(){return actionBar()+configBody();};
async function importConfig(){await perform(async()=>{const c=await api('config/validate',{text:document.getElementById('config-text').value});fromConfig(c);document.getElementById('dialog').close();render();notice('已放入编辑区，尚未保存或应用');});}
async function restoreVersion(v){await perform(async()=>{const d=await api('backup/'+v);fromConfig(d.data);render();notice('已恢复到编辑区，尚未保存或应用');});}
function bundleDialog(importing){dialogHTML(`<h2 id="bundle-title">${importing?'导入':'导出'}加密媒体配置包</h2><p class="muted">${importing?'输入导出这个配置包时设置的密码。如果不知道，请向导出方索取；这里不是设置新密码，也不是管理员登录密码。':'为这个配置包设置至少 12 位的密码，通过安全方式告知媒体服务管理员。'}</p><div class="dialog-field"><label for="bundle-password">${importing?'导出时设置的配置包密码':'新配置包密码'}</label><input id="bundle-password" type="password" autocomplete="off" placeholder="${importing?'请输入导出时设置的密码':'至少 12 位'}"></div>${importing?'<div class="dialog-field"><label for="bundle-file">配置包文件</label><input id="bundle-file" type="file" onchange="readTextFile(this,\'bundle-text\')"></div><div class="dialog-field"><label for="bundle-text">加密包内容</label><textarea id="bundle-text" placeholder="选择文件后自动填入，也可直接粘贴"></textarea></div>':'<p class="dialog-note">导出会先保存本端整份配置，不会应用配置或重启服务。</p>'}`,`<button class="primary" onclick="transferBundle(${importing})">${importing?'解密并导入':'加密并下载'}</button>`);}
async function transferBundle(importing){await perform(async()=>{if(!importing&&!await saveLive(false))return;const body={password:document.getElementById('bundle-password').value,version:savedVersion};if(importing)body.bundle=document.getElementById('bundle-text').value;const result=await api('bundle/'+(importing?'import':'export'),body);if(importing){await loadLive();managed.bundle=true;active=1;step=1;render();}else download('relay-media.encrypted.json',result.bundle);document.getElementById('dialog').innerHTML='';document.getElementById('dialog').close();notice(importing?'已导入，核对监听和证书后应用':'加密包已下载');});}
function passwordDialog(){dialogHTML('<h2>修改管理员密码</h2><input id="old-password" type="password" autocomplete="current-password" aria-label="原密码" placeholder="原密码"><input id="new-password" type="password" autocomplete="new-password" aria-label="新密码" placeholder="新密码，至少 12 位"><input id="confirm-password" type="password" autocomplete="new-password" aria-label="确认新密码" placeholder="再次输入新密码"><button onclick="changePassword()">修改密码</button>');}
async function changePassword(){await perform(async()=>{const p=document.getElementById('new-password').value;if(p!==document.getElementById('confirm-password').value)throw Error('两次新密码不一致');checkNewPassword(p);await api('auth/password',{old_password:document.getElementById('old-password').value,new_password:p});document.getElementById('dialog').innerHTML='';document.getElementById('dialog').close();sessionToken='';await showLogin();notice('密码已修改，请重新登录');});}
async function logout(){await perform(async()=>{await api('auth/logout',{});sessionToken='';await showLogin();});}
overview=function(){const rt=liveStatus.runtime;return header('运行概览','配置保存和运行状态分别展示。')+`<section class="card"><p>已保存版本：${savedVersion}</p><p>运行状态：${rt?(rt.running?'正在运行':'未运行'):'尚未读取'}</p><p>运行模式：${esc(playbackModes[rt?.mode]?.name||'未应用')}</p>${rt?.mode&&rt?.applied_at?'<p>最近应用：'+esc(rt.applied_at)+'</p>':''}<p>编辑模式：${esc(playbackModes[playbackMode]?.name||'未选择')}</p><p>播放是否正常仍需通过客户端验证起播与拖动。</p>${actionBar()}<button onclick="go(1)">部署向导</button></section>`;};
diagnostics=function(){return header('诊断与操作记录','检查配置与受管理进程，不以检查通过代替实际播放。')+`<section class="card"><button onclick="runDiagnostics()">开始检查</button><button onclick="showAudit()">查看操作记录</button><button onclick="downloadDiagnostics()">下载脱敏诊断报告</button><pre id="diagnostic-result"></pre><pre id="audit-result"></pre><p>操作记录仅包含时间、操作类型和结果，不包含密码、密钥或视频链接。</p></section>`;};
async function showAudit(){await perform(async()=>{document.getElementById('audit-result').textContent=JSON.stringify(await api('logs'),null,2);});}
async function downloadDiagnostics(){await perform(async()=>{const [status,logs]=await Promise.all([api('status'),api('logs')]);download('relay-diagnostics.json',JSON.stringify({status,logs},null,2));});}
async function runDiagnostics(){await perform(async()=>{const result=await api('diagnostics');document.getElementById('diagnostic-result').textContent=JSON.stringify(result,null,2);});}
function configurationSummary(){
 const split=playbackMode==='split',media=split&&!isProxy(),direct=['single','split'].includes(playbackMode);
 const row=(name,value)=>`<tr><th scope="row">${esc(name)}</th><td style="overflow-wrap:anywhere;white-space:pre-wrap">${esc(value||'未填写')}</td></tr>`;
 let basic=row('播放模式',playbackModes[playbackMode].name)+row('视频流向',playbackModes[playbackMode].flow);
 if(split)basic+=row('本端角色',roleName())+row(media?'媒体监听':'共享登录监听',draft.listen||(media?'0.0.0.0:49967':'0.0.0.0:28008'));
 if(direct)basic+=row('客户端视频访问地址',draft.media);
 if(playbackMode==='relay')basic+=row('中继入口',draft.relayBase||'使用相对路径，与登录入口同源');
 if(playbackMode==='single')basic+=row('视频监听',(draft.videoBind||'0.0.0.0')+':'+(draft.videoPort||'49963'));
 if(playbackMode==='single'||media)basic+=row('视频连接方式',draft.transport==='http'?'内网 HTTP，公网 HTTPS 由反代提供':'本程序提供 HTTPS')+(draft.transport==='http'?'':row('证书状态',certStatus.configured?'已保存；到期 '+certStatus.expires_at:'尚未配置'));
 const services=Object.entries(draft.services).map(([id,s])=>{
  if(!s.enabled)return `<p>${esc(types[id])}：未启用</p>`;
  let rows='';if(!media){rows+=row('影视服务器地址',s.target);if(split)rows+=row('客户端登录域名',s.host||'未配置，使用 IP 端口');rows+=row('登录监听端口',s.port||(split?'未配置，使用域名入口':'未填写'));}
  if(playbackMode!=='redirect')rows+=row('允许访问的视频来源',s.upstreams??draft.upstream);
  if(direct)rows+=row('共享密钥',keyStatus[id]?'已保存（不显示密钥内容）':'尚未配置');
  let paths='';if(!media){paths=`<h4>STRM 目录对应</h4>${s.pathRules?.length?`<div class="tablewrap"><table><tr><th>影视服务器中的目录</th><th>主代理可读取的目录</th></tr>${s.pathRules.map(r=>`<tr><td style="overflow-wrap:anywhere">${esc(r.from)}</td><td style="overflow-wrap:anywhere">${esc(r.to)}</td></tr>`).join('')}</table></div>`:'<p>未配置目录对应；如需主代理读取 STRM 文件，请返回“视频与目录”补充。</p>'}`;}
  return `<section aria-label="${esc(types[id])} 配置摘要" style="margin-top:24px"><h3>${esc(types[id])} · 已启用</h3><div class="tablewrap"><table>${rows}</table></div>${paths}</section>`;
 }).join('');
 return `<p>以下是本次将保存的配置，请核对后再操作。保存不会启动服务；保存并应用会更新本端运行进程。</p><div class="tablewrap"><table>${basic}</table></div>${services}<p class="muted">${media?'媒体服务不读取 STRM 文件，无需挂载 STRM 目录。':'此处展示当前编辑值，不代表已应用到运行服务。'}密钥和私钥不会显示在摘要中。</p><details><summary>查看本次修改明细</summary><button onclick="review()">对比保存前后的参数</button></details>`;
}
modeReview=()=>configurationSummary()+actionBar();
const oldStepErrors=currentStepErrors;currentStepErrors=function(index=step){const errors=oldStepErrors(index).filter(x=>!x.includes('管理服务未连接'));if(!isProxy()&&index===0&&(!enabledServices().length||enabledServices().some(([id])=>!keyStatus[id])))errors.push('请导入包含全部已启用服务密钥的主代理配置包');if(index===3&& !isProxy()&&draft.transport!=='http'&&!certStatus.configured)errors.push('请上传证书');if(isProxy()&&index===3&&enabledServices().some(([id])=>!keyStatus[id]))errors.push('请先生成密钥');return errors;};
const oldStandaloneErrors=standaloneStepErrors;standaloneStepErrors=function(){const e=oldStandaloneErrors().filter(x=>!x.includes('管理服务未连接'));if(playbackMode==='single'&&modeStep===3&&draft.transport!=='http'&&!certStatus.configured)e.push('请先上传证书');if(playbackMode==='single'&&modeStep===4&&enabledServices().some(([id])=>!keyStatus[id]))e.push('请先生成密钥');return e;};
const standaloneWizard=wizard;
wizard=function(){
 if(playbackMode!=='split'||choosingMode)return standaloneWizard();
 if(!setupRoleSelected)return setupRolePage();
 const labels=wizardSteps();step=Math.min(step,labels.length-1);let body='';
 if(step===0)body=isProxy()?modeServiceSelection():`<p>导入主代理配置包，取得服务设置和共享密钥。</p><button onclick="bundleDialog(true)">导入主代理配置包</button><p>${enabledServices().length&&enabledServices().every(([id])=>keyStatus[id])?'已启用服务的密钥已保存，可继续核对。':'请先导入配置包，再继续。'}</p>`;
 if(step===1)body=isProxy()?listenerFields('setup-shared')+enabledServices().map(([id,s])=>`<section class="card"><h3>${types[id]}</h3>${setupField(types[id]+' 服务器地址','target',s.target,'填写主代理能连接的 HTTP / HTTPS 地址。',id)}${setupField('客户端登录域名（可选）','host',s.host,'想在播放器里用域名连接时填写；只使用下方的 IP 入口时可留空。',id)}${setupField(ipEntryLabel,'port',s.port,ipEntryHint,id)}${ipEntryHelp()}</section>`).join(''):keyPanel();
 if(step===2)body=setupNetwork();
 if(step===3)body=isProxy()?keyPanel().replace('<button onclick="bundleDialog(false)">导出媒体服务配置包</button>',''):setupCertificate();
 if(step===labels.length-1)body=configurationSummary()+actionBar()+`${isProxy()?'<button onclick="bundleDialog(false)">导出媒体服务配置包</button>':'<button onclick="go(5)">前往诊断</button>'}<p>应用后仍需在外网客户端验证起播、拖动和视频流向。</p>`;
 return header('部署向导',`逐步完成${roleName()}配置。`)+`<div class="steps"><span>1 / 播放模式</span><span>2 / 部署角色：${roleName()}</span>${labels.map((label,i)=>`<span class="${step===i?'on':''}" ${step===i?'aria-current="step"':''}>${i+3} / ${label}</span>`).join('')}</div><section class="card"><h2>${step+3}. ${labels[step]}</h2>${body}<div id="setupErrors" role="alert"></div><div class="footbar"><button onclick="previousSetupStep()">上一步</button>${step<labels.length-1?'<button class="primary" onclick="advanceSetup()">下一步</button>':''}</div></section>`;
};
review=function(){const before=Object.fromEntries(flatten(applied)),after=Object.fromEntries(flatten(draft));const keys=[...new Set([...Object.keys(before),...Object.keys(after)])].filter(k=>before[k]!==after[k]);dialogHTML(`<h2>未保存修改</h2><p>仅对比编辑内容与最近保存的配置，不改变运行服务。</p><div class="tablewrap"><table><tr><th>参数</th><th>保存值</th><th>编辑值</th></tr>${keys.map(k=>`<tr><td>${esc(k)}</td><td>${esc(before[k]??'未设置')}</td><td>${esc(after[k]??'已移除')}</td></tr>`).join('')}</table></div>`);};
render=function(){
 if(!sessionToken)return;
 document.title='Relay · 配置工作台';document.querySelector('.sidefoot').textContent='Relay · 配置工作台';document.querySelector('.topbar .pill').textContent='管理服务已连接';document.getElementById('reviewTop').hidden=true;
 const choosing=!playbackMode||choosingMode;pages[2]=editingRole==='media'?'服务授权':'服务接入';pages[3]='视频传输';
 document.getElementById('nav').innerHTML=choosing?'<button class="active" onclick="go(1)">部署向导</button>':pages.map((p,i)=>`<button class="${active===i?'active':''}" ${active===i?'aria-current="page"':''} onclick="go(${i})">${p}</button>`).join('');
 document.getElementById('roleLabel').innerHTML=choosing?'请选择播放模式':`<span>${playbackModes[playbackMode].name}${playbackMode==='split'?' · '+roleName():''}</span>`;
 document.getElementById('main').innerHTML=choosing?modeChoice():[overview,wizard,services,delivery,security,diagnostics,config][active]();
 document.getElementById('draftLabel').textContent=dirty()?'有未保存修改':`已保存版本 ${savedVersion}`;
 let account=document.getElementById('account-actions');if(!account){account=document.createElement('div');account.id='account-actions';account.className='actions';document.querySelector('.topbar').append(account);}account.innerHTML=(choosing?'':'<button onclick="openModeChoice()" title="选择播放方式；不会立即改变运行服务">切换模式</button>')+'<button onclick="passwordDialog()" title="修改当前管理员的密码">修改密码</button><button onclick="logout()" title="退出当前登录">退出</button>';
};
perform(showLogin);
