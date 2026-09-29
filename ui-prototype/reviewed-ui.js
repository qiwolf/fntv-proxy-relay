// Role-aware page definitions. No network writes or persistence in this UI build.
const isProxy = () => editingRole === 'proxy';
const roleName = () => isProxy() ? '主代理' : '媒体服务';
const ipEntryLabel='通过 IP 连接本服务的端口（可选）';
const ipEntryHint='想在播放器里用“主代理 IP:端口”添加本服务时填写；只通过域名连接时可留空。这是代理新开的入口，不是上方影视服务器原有的端口。';
function ipEntryHelp(){return '<details><summary>播放器里怎么填？为什么各服务要用不同端口？</summary><p>例如主代理所在服务器的 IP 是 192.168.1.10，这里填 28015，就在播放器的“添加服务器”中填写 http://192.168.1.10:28015；如果播放器把地址和端口分开填写，就分别填 192.168.1.10 和 28015。</p><p>连接这个入口后，主代理会把登录和影片列表请求转给上方配置的影视服务器。飞牛、Emby、Jellyfin 各用不同端口，程序才能知道你要连接哪一种服务。</p><p>上面的“共享监听端口”供域名入口转发使用；这里是给当前服务单独开的 IP 入口。两种方式至少配置一种，也可以同时使用。</p><p>容器或路由器有端口映射时，播放器应填写它实际能访问的 IP 和映射后的端口。上面的 HTTP 例子适用于可信内网；公网登录请使用 HTTPS 反代，不要直接暴露未加密的登录入口。</p></details>';}
const disabledAction = (label) => `<span tabindex="0" title="管理服务未连接，暂不可用"><button disabled>${label}</button></span>`;
const serviceTabs = () => `<div class="tabs">${Object.keys(types).map(k => `<button class="${tab===k?'selected':''}" onclick="tab='${k}';render()">${types[k]}</button>`).join('')}</div>`;
const serviceDirty = () => JSON.stringify(draft.services[tab]) !== JSON.stringify(applied.services[tab]);
function listenParts(){
 const raw=draft.listen||(isProxy()?'0.0.0.0:28008':'0.0.0.0:49967');
 const m=raw.match(/^\[([^\]]+)\]:(.*)$/)||raw.match(/^([^:]*):(.*)$/);
 return m?{address:m[1]||'0.0.0.0',port:m[2]}:{address:raw,port:''};
}
function editListener(part,value){
 const parts=listenParts();parts[part]=value.trim();
 const address=parts.address.replace(/^\[|\]$/g,'');
 draft.listen=(address.includes(':')?'['+address+']':address)+':'+parts.port;updateBar();
}
function listenerFields(prefix='listener'){
 const p=listenParts();
 return `<div class="field"><label for="${prefix}-port">${isProxy()?'域名入口的转发端口（主代理共享）':'接收视频请求的端口（媒体服务）'}<small>${isProxy()?'通过域名连接时，让反向代理把请求转发到这里。飞牛、Emby、Jellyfin 可以共用这个端口，程序根据域名区分服务；不使用域名时，使用下方各服务的独立 IP 入口。':'播放器的视频请求最终转发到这里；它不是登录影视服务器的端口。“视频访问地址”填写播放器实际访问的公网地址和端口。'}</small></label><input id="${prefix}-port" type="number" min="1" max="65535" value="${esc(p.port)}" onchange="editListener('port',this.value)"></div><details style="margin-bottom:24px"><summary>容器和路由器端口怎么对应？</summary><p>这里设置的是本程序内部接收请求的端口，不会自动开放网络端口。Docker 使用 bridge 网络时，需要把这个端口映射到宿主机；使用 host 网络时，程序直接使用宿主机端口。</p><p>如果映射前后的端口不同，反代或客户端应连接它实际能访问的映射后端口。路由器端口映射也遵循这一规则。</p></details><details style="margin-bottom:24px"><summary>高级：绑定地址</summary><label for="${prefix}-address">绑定地址<small>默认 0.0.0.0，监听所有 IPv4 网卡；通常无需填写具体机器 IP。</small></label><input id="${prefix}-address" value="${esc(p.address)}" onchange="editListener('address',this.value)"><p>指定地址时，必须是程序运行环境中可用的地址。IPv6 可使用 ::，是否同时接受 IPv4 取决于系统设置，请分别验证。绑定地址不是客户端访问地址。</p></details>`;
}
function actionBar(serviceOnly=false) {
 const changed=serviceOnly?serviceDirty():dirty();
 return `<div class="actions page-actions">${serviceOnly?`<button aria-pressed="${draft.services[tab].enabled}" title="切换后需保存并应用才会生效" onclick="draft.services[tab].enabled=!draft.services[tab].enabled;render()">服务开关：${draft.services[tab].enabled?'已启用':'已关闭'}</button>`:''}<button data-cancel ${!changed?'disabled':''} title="${serviceOnly?'撤销当前服务的未保存修改':'撤销本端所有未保存修改'}" onclick="cancelChanges(${serviceOnly})">取消修改</button><span tabindex="0" title="管理服务未连接，暂时无法保存或应用"><button class="primary" disabled>保存并应用</button></span><small data-change-status>${changed?'有未保存修改':'没有未保存修改'}</small></div><p class="muted">${serviceOnly?'操作仅针对当前服务。修改在保存并应用后生效。':'保存范围为当前配置对象，不会修改另一端。'}</p>`;
}
function cancelChanges(serviceOnly) {
 if(serviceOnly)draft.services[tab]=structuredClone(applied.services[tab]);
 else draft=structuredClone(applied);
 render();notice(serviceOnly?'已撤销当前服务的未保存修改':'已撤销本端未保存修改');
}
edit=function(k,v,s){if(s)draft.services[s][k]=v;else draft[k]=v;updateBar();};
updateBar=function(){
 document.getElementById('draftLabel').textContent=dirty()?'本端有未保存修改':'本端没有未保存修改';
 const changed=active===2?serviceDirty():dirty();
 document.querySelectorAll('[data-cancel]').forEach(b=>b.disabled=!changed);
 document.querySelectorAll('[data-change-status]').forEach(b=>b.textContent=changed?'有未保存修改':'没有未保存修改');
};
services=function(){
 const s=draft.services[tab];
 return header(isProxy()?'服务接入':'服务授权',isProxy()?'分别设置主代理连接的服务器，以及客户端登录入口。':'按服务接收主代理的授权配置，不在这里设置登录入口。')+serviceTabs()+`<section class="card"><h2>${types[tab]}</h2>${actionBar(true)}${isProxy()?`<div class="two">${field(types[tab]+' 服务器地址','target',s.target,'填写主代理能连接的原有服务地址，包含 http:// 或 https://。',tab)}${field('客户端登录域名（可选）','host',s.host,'只填域名，不含协议或路径。使用域名接入时，将反代转发到本端共享监听端口。',tab)}${field(ipEntryLabel,'port',s.port,ipEntryHint,tab)}</div>${ipEntryHelp()}<details><summary>域名接入共用哪个端口？</summary><p>当前监听端口：<code>${esc(listenParts().port)}</code>。在部署向导的“连接与登录”中设置。</p><p>不同服务使用不同域名，反代需保留客户端请求的域名。</p></details>`:`<p>服务标识：<code>${tab}</code></p><p>共享密钥和服务标识来自主代理配置包。启用本服务前，请先导入对应配置。</p><button onclick="go(6)">前往导入配置包</button>`}</section>`;
};
overview=function(){return header('配置概览','查看当前编辑的配置；运行状态需连接管理服务后获取。')+`<div class="grid"><section class="card"><small>配置对象</small><div class="metric">${roleName()}</div><p>${isProxy()?'连接影视服务器，生成播放地址。':'验证播放地址，读取并传输视频。'}</p></section><section class="card"><small>编辑中设置为启用</small><div class="metric">${Object.values(draft.services).filter(s=>s.enabled).length} 个</div><p>不代表正在运行的服务数量。</p></section><section class="card"><small>运行状态</small><div class="metric">未知</div><p>管理服务未连接。</p></section></div><section class="card" style="margin-top:24px"><h2>两个角色分别做什么？</h2><p>登录与影片信息：客户端 → 主代理 → 飞牛影视 / Emby / Jellyfin。</p><p>直出视频数据：视频来源服务器 → 媒体服务 → 客户端。</p><p class="muted">这是工作方式说明，不是实时流量监测。普通文件和转码不保证走直出链路。</p><button onclick="go(1)">查看配置步骤</button></section>`;};
wizard=function(){
 const tasks=isProxy()?[
 ['接入影视服务器','填写服务器地址和客户端登录入口。',2],
 ['设置视频传输与 STRM 目录','填写视频访问地址；需要读取 STRM 时配置对应目录。',3],
 ['准备共享密钥','主代理生成，各服务独立保存。',4],
 ['交给媒体服务','导出配置包，在媒体服务中导入。',6],
 ['验证播放','检查连接，并在客户端验证起播和拖动。',5]
 ]:[
 ['导入主代理配置包','取得服务标识和共享密钥。',6],
 ['确认视频传输设置','核对监听地址、视频访问地址和允许读取的服务器。',3],
 ['准备连接证书','由本服务直接提供 HTTPS 时，上传证书及私钥。',4],
 ['验证播放','检查视频来源连接，并由外网客户端验证播放。',5]
 ];
 return header('部署向导',`配置${roleName()}的步骤；每一步都跳转到负责该功能的模块。`)+`<section class="card">${tasks.map(([name,desc,n],i)=>`<div class="row"><div><h3>${i+1}. ${name}</h3><p class="muted">${desc}</p></div><button onclick="go(${n})">去配置</button></div>`).join('')}<details><summary>两个角色能放在同一台机器吗？</summary><p>可以。同一镜像分别启动主代理和媒体服务两个容器，各自保存配置，也可以部署到不同机器。</p></details></section>`;
};
const reviewedPathFields=pathFields;
pathFields=function(){return reviewedPathFields().replace(/<p class="muted" style="margin-top:24px">[\s\S]*?<\/p>/,'');};
delivery=function(){return header('视频传输',isProxy()?'设置客户端取视频的地址，以及主代理读取 STRM 时使用的目录。':'设置本服务如何接收视频请求，以及可以从哪些服务器读取视频。')+`<section class="card">${actionBar()}${field('视频访问地址','media',draft.media,'客户端取视频时使用的完整 HTTPS 地址，包含实际公网端口。例如 https://media.example.com:49967。')}${!isProxy()?listenerFields():''}<p class="muted">两端配置的视频访问地址应一致。主代理用它生成播放地址，媒体服务用它校验请求；这里只填写地址，不创建域名解析或端口映射。</p></section><section class="card"><h2>STRM 视频来源</h2>${serviceTabs()}<label for="upstreams">允许访问的视频来源地址（每行一个）<small>填写当前服务的 STRM 视频链接中的主机和端口，不是影视服务器的登录地址。</small></label><textarea id="upstreams" onchange="edit(\'upstreams\',this.value,tab)">${esc(draft.services[tab].upstreams??draft.upstream)}</textarea><details><summary>应该填哪个地址？</summary><p>如果 STRM 内容为 http://192.0.2.20:19798/dav/movie.mkv，这里填写 192.0.2.20:19798。不填写协议、影片路径或账号密码。</p><p>此设置限制程序从哪里读取视频，不是限制哪些用户可以播放。若链接跳转到另一台服务器，也需按实际情况允许该服务器。</p></details></section>${isProxy()?pathFields():''}`;};
const reviewedSecurity=security;
security=function(){
 let html=reviewedSecurity();
 // Remove unrelated/unfinished status copy by constructing only the relevant sections.
 const cert=html.match(/<section class="card"><h2>视频 HTTPS 连接证书<\/h2>[\s\S]*?<\/section>/)?.[0]||'';
 const certSection=cert.replace('视频 HTTPS 连接证书',isProxy()?'主代理 HTTPS 连接证书':'视频 HTTPS 连接证书').replace('让客户端安全连接视频服务。不是播放票据的密钥。',isProxy()?'只有主代理直接提供 HTTPS 登录入口时才需要上传。若 HTTPS 由反代提供，证书由反代管理。':'只有媒体服务直接提供 HTTPS 时才需要上传。若 HTTPS 由反代提供，证书由反代管理。').replace('正式版会自动安全保存；更新失败保留原证书。证书不会自动签发或续期。','证书由程序保存；不自动签发、续期或同步。').replace(certificateHelp,isProxy()?'<details><summary>反向代理与直连有什么区别？</summary><p>HTTPS 在反代终止时，证书放在反代上；路由器端口映射不提供 HTTPS。客户端直接连接本程序时，需在这里配置匹配访问域名或 IP 的受信任证书。</p></details>':certificateHelp.replace('并把所在目录挂载到负责 HTTPS 的容器中。','通过上传或粘贴方式添加。'));
 return header('安全与证书',`管理${roleName()}的连接加密与共享密钥。`)+`<section class="card">${actionBar()}<h2>本端连接方式</h2>${isProxy()?listenerFields():''}<label for="transport">监听方式</label><select id="transport" onchange="edit('transport',this.value);render()"><option value="https" ${draft.transport!=='http'?'selected':''}>本程序提供 HTTPS</option><option value="http" ${draft.transport==='http'?'selected':''}>可信内网 HTTP，由反代提供公网 HTTPS</option></select><p class="muted">HTTP 只用于可信内网反代连接，不要把该监听端口直接暴露到公网。</p></section>${draft.transport==='http'?'':certSection}<section class="card"><h2>${isProxy()?'主代理生成共享密钥':'从主代理取得共享密钥'}</h2><p>${isProxy()?'首次配置时为每个服务生成独立密钥，已有密钥继续使用。导出配置包交给媒体服务，播放器无需配置密钥。':'导入主代理配置包，取得同一服务的共享密钥。媒体服务不单独生成新的密钥。'}</p><p class="muted">密钥状态：尚未读取。</p><button onclick="go(6)">${isProxy()?'前往导出配置包':'前往导入配置包'}</button></section>`;
};
diagnostics=function(){const items=isProxy()?['配置格式','飞牛影视 / Emby / Jellyfin 连接','STRM 目录与读取权限','视频访问地址连通性']:['配置格式','共享密钥与服务标识','视频来源服务器连接','本端监听与证书'];return header('诊断与日志','检查本端配置与连接。客户端是否能播放，需要实际验证。')+`<section class="card"><h2>本端检查</h2>${disabledAction('开始检查')}${items.map(n=>`<div class="row"><strong>${n}</strong><span>未检查</span></div>`).join('')}</section><section class="card"><h2>客户端播放验证</h2><p>用外网客户端播放 STRM 影片，确认能够开始播放并拖动进度；再检查媒体服务流量，确认视频经过预期出口。</p><p class="muted">连接成功不等于播放成功。转码失败还需查看飞牛影视、Emby 或 Jellyfin 自身的日志。</p></section><section class="card"><h2>本端日志</h2><p>暂无日志，管理服务未连接。</p>${disabledAction('下载脱敏诊断包')}</section>`;};
config=function(){return header('配置管理',`管理${roleName()}的配置包、备份与恢复。`)+`<section class="card"><h2>${isProxy()?'导出给媒体服务':'导入主代理配置包'}</h2><p>${isProxy()?'主代理导出配置包，再到媒体服务网页导入。':'选择主代理导出的配置包，核对服务标识及密钥冲突后导入。'}</p>${disabledAction(isProxy()?'导出媒体服务配置包':'导入主代理配置包')}<details><summary>配置包包含什么？</summary><p>服务标识、共享密钥及必要的视频访问设置。不含管理员密码、证书私钥或 STRM 文件。配置包包含敏感信息，需加密保存，不可公开分享。</p><p>媒体服务导入后，还需核对本机监听设置，并按连接方式准备证书。</p></details></section><section class="card"><h2>本端配置备份与恢复</h2><p>恢复先进入待保存配置，不立即覆盖运行配置。</p><div class="row"><div><strong>导出配置文件</strong><br><small>不含密钥、证书和私钥，不能代替完整备份。</small></div>${disabledAction('导出配置')}</div><div class="row"><strong>从配置文件恢复</strong><div class="actions">${disabledAction('上传配置文件')}${disabledAction('粘贴配置')}</div></div><div class="row"><strong>历史版本</strong><span>尚未读取</span></div><details><summary>查看当前未保存修改</summary><p>对比修改前后的参数，不保存或应用。</p><button onclick="review()">打开变更明细</button></details></section>`;};
review=function(){const before=Object.fromEntries(flatten(applied)),after=Object.fromEntries(flatten(draft));const keys=[...new Set([...Object.keys(before),...Object.keys(after)])].filter(k=>before[k]!==after[k]);const d=document.getElementById('dialog');d.innerHTML=`<h2>本端未保存修改</h2><p>仅对比参数，不保存或应用。管理服务未连接。</p><details open><summary>变更明细（${keys.length} 项）</summary><div class="tablewrap"><table><tr><th>参数</th><th>修改前</th><th>修改后</th></tr>${keys.map(k=>`<tr><td>${esc(k)}</td><td>${esc(before[k]??'未设置')}</td><td>${esc(after[k]??'已移除')}</td></tr>`).join('')}</table></div></details><button onclick="document.getElementById('dialog').close()">返回编辑</button>`;d.showModal();};
render=function(){
 pages[2]=isProxy()?'服务接入':'服务授权';pages[3]='视频传输';
 document.title='Relay · 配置工作台';
 document.getElementById('nav').innerHTML=pages.map((p,i)=>`<button class="${active===i?'active':''}" ${active===i?'aria-current="page"':''} onclick="go(${i})"><span class="num">0${i+1}</span>${p}</button>`).join('');
 document.getElementById('roleLabel').innerHTML=`<span class="actions" aria-label="配置对象"><button aria-pressed="${isProxy()}" onclick="switchRole('proxy')">主代理配置</button><button aria-pressed="${!isProxy()}" onclick="switchRole('media')">媒体服务配置</button></span>`;
 document.getElementById('reviewTop').hidden=true;
 document.querySelector('.sidefoot').textContent='Relay · 配置工作台';
 document.querySelector('.topbar .pill').textContent='开发环境';
 document.getElementById('main').innerHTML='<div class="banner">管理服务未连接：当前内容仅供编辑，上传、保存和应用暂不可用。刷新页面会重置修改。</div>'+[overview,wizard,services,delivery,security,diagnostics,config][active]();
 document.querySelectorAll('button[onclick^="managedDialog"]').forEach(b=>{b.disabled=true;b.title='管理服务未连接';});
 updateBar();
};
render();
