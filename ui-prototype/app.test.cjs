const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const nodes=new Map();
function node(id){if(!nodes.has(id))nodes.set(id,{innerHTML:'',textContent:'',hidden:false,close(){this.open=false},remove(){nodes.delete(id)},append(n){nodes.set(n.id,n)}});return nodes.get(id)}
const document={getElementById:id=>node(id),querySelector:s=>node(s),querySelectorAll:()=>[],createElement:()=>({})};
const c=vm.createContext({structuredClone,URL,console,document,setTimeout,Blob,TextEncoder});const run=s=>vm.runInContext(s,c);
run(fs.readFileSync(__dirname+'/index.html','utf8').match(/<script>([\s\S]*?)<\/script>/)[1]);
for(const f of ['reviewed-ui.js','setup-wizard.js','playback-modes.js'])run(fs.readFileSync(__dirname+'/'+f,'utf8').replace(/render\(\);\s*$/,''));
run('render=()=>{throw Error("old renderer must not run")};notice=()=>{}');
run(fs.readFileSync(__dirname+'/app.js','utf8').replace(/perform\(showLogin\);\s*$/,''));
(async()=>{
 assert.throws(()=>run(`checkNewPassword('中文密码')`),/至少 12/);
 assert.doesNotThrow(()=>run(`checkNewPassword('中文密码中文密码中文密码')`));
 assert.throws(()=>run(`checkNewPassword('中'.repeat(25))`),/72/);
 run(`editingRole='proxy'`);
 assert.match(run('listenerFields()'),/域名入口的转发端口/);
 assert.match(run('listenerFields()'),/程序根据域名区分服务/);
 assert.match(run('listenerFields()'),/<summary>容器和路由器端口怎么对应？<\/summary>/);
 assert.match(run('keyPanel()'),/包括其他页面尚未保存的修改/);
 assert.match(run('keyPanel()'),/不会应用配置，也不会重启服务/);
 run(`editingRole='media'`);
 assert.match(run('listenerFields()'),/接收视频请求的端口/);
 assert.doesNotMatch(run('keyPanel()'),/包括其他页面尚未保存的修改/);
 run(`globalThis.originalDialogHTML=dialogHTML;globalThis.bundleHTML='';dialogHTML=html=>bundleHTML=html;bundleDialog(true)`);
 assert.match(run('bundleHTML'),/输入导出这个配置包时设置的密码/);
 assert.match(run('bundleHTML'),/不是设置新密码，也不是管理员登录密码/);
 run('bundleDialog(false)');
 assert.match(run('bundleHTML'),/为这个配置包设置至少 12 位的密码/);
 assert.match(run('bundleHTML'),/导出会先保存本端整份配置/);
 run(`dialogHTML=originalDialogHTML;editingRole='proxy'`);
 run(`fromConfig({version:1,mode:'redirect',listen:{port:28005},services:{fntv:{enabled:true,target:'http://127.0.0.1:8005'}}})`);
 assert.equal(run('draft.listen'),'0.0.0.0:28005');
 run(`liveStatus={runtime:{running:true,mode:'redirect',applied_at:'2026-09-29T00:00:00Z'}}`);assert.match(run('overview()'),/正在运行/);assert.doesNotMatch(run('overview()'),/未启动/);
 assert.equal(run('toConfig().listen.port'),28005);
 run(`sessionToken='test';active=1;modeStep=1;draft.services.fntv.target='http://127.0.0.1:9';draft.services.fntv.port='29870';nextModeStep()`);
 assert.equal(run('modeStep'),2,'wizard advances after valid connections');
 assert.match(run('configurationSummary()'),/29870/);
 assert.match(run('configurationSummary()'),/http:\/\/127\.0\.0\.1:9/);
 assert.match(run('configurationSummary()'),/STRM 目录对应/);
 run(`draft.services.emby.target='http://127.0.0.1:8096';draft.services.emby.port='28006';draft.services.emby.bind='127.0.0.2'`);
 assert.equal(run('toConfig().services.emby.target'),'http://127.0.0.1:8096','disabled service settings preserved');
 assert.equal(run('toConfig().services.emby.listen.address'),'127.0.0.2');
 run(`fromConfig({version:1,mode:'split',role:'media',listen:{address:'::',port:49967},public_base_url:'https://video.example',services:{fntv:{enabled:true},emby:{enabled:true}}})`);
 assert.equal(run('draft.listen'),'[::]:49967');assert.equal(run('toConfig().listen.address'),'::');
 assert.equal(run('toConfig().services.fntv.target'),undefined,'media must not acquire proxy target');
 assert.match(run('configurationSummary()'),/媒体服务不读取 STRM/);
 assert.doesNotMatch(run('configurationSummary()'),/影视服务器地址/);
 run(`keyStatus={fntv:true};step=0`);assert.match(run('currentStepErrors().join()'),/全部已启用服务/);
 run(`keyStatus.emby=true`);assert.doesNotMatch(run('currentStepErrors().join()'),/全部已启用服务/);
 for(const role of ['proxy','media']){
  run(`editingRole='${role}';setupRoleSelected=true;playbackMode='split';choosingMode=false`);
  const steps=run('wizardSteps().length');for(let i=0;i<steps;i++){
   run(`step=${i}`);const html=run('wizard()');assert.doesNotMatch(html,/管理服务未连接|applyDemo|disabled/);
   if(role==='proxy'&&i===1){assert.match(html,/通过 IP 连接本服务的端口/);assert.match(html,/192\.168\.1\.10:28015/);assert.match(html,/不是上方影视服务器原有的端口/);}
  }
  assert.equal(run('wizardSteps().includes("连接证书")'),role==='media');
  run('step=3;keyStatus={};certStatus={};draft.transport="https"');
  assert.match(run('currentStepErrors().join()'),role==='proxy'?/请先生成密钥/:/请上传证书/);
  assert.match(run('wizard()'),role==='proxy'?/生成密钥/:/上传或粘贴证书/);
  if(role==='proxy'){
   assert.doesNotMatch(run('wizard()'),/导出媒体服务配置包/);
   run('step=wizardSteps().length-1');
   assert.equal((run('wizard()').match(/导出媒体服务配置包/g)||[]).length,1);
  }
 }
 run(`sessionToken='test';active=1;render()`);assert.doesNotMatch(node('main').innerHTML,/管理服务未连接/);
 run(`globalThis.calls=[];api=async(path)=>{calls.push(path);throw Error('save rejected')};notice=()=>{};render=()=>{};refreshStatus=async()=>{};applied=structuredClone(draft)`);
 assert.equal(await run('saveLive(false)'),false);
 await run('generateLiveKeys()');assert.deepEqual(JSON.parse(run('JSON.stringify(calls)')),['config','config'],'no generation after failed save');
 run(`calls=[];api=async(path)=>{calls.push(path);if(path==='apply')throw Error('runtime failed');return {version:4}};globalThis.message='';notice=x=>message=x`);
 assert.equal(await run('saveLive(true)'),false);assert.equal(run('savedVersion'),4);assert.match(run('message'),/配置已保存，但应用未完成/);
 run(`api=async()=>({version:5});notice=()=>{}`);assert.equal(await run('saveLive(false)'),true);
 run(`api=async()=>({initialized:true});sessionToken='secret';document.getElementById('account-actions').innerHTML='old account';`);
 await run('showLogin()');assert.equal(run('sessionToken'),'');assert.equal(nodes.has('account-actions'),false);assert.equal(node('dialog').innerHTML,'');
 console.log('app bridge tests passed');
})().catch(e=>{console.error(e);process.exitCode=1});
