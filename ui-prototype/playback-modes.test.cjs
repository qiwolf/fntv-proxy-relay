const fs=require('node:fs'),vm=require('node:vm'),assert=require('node:assert/strict');
const c=vm.createContext({structuredClone,URL,console});const run=s=>vm.runInContext(s,c);
run(fs.readFileSync(__dirname+'/index.html','utf8').match(/<script>([\s\S]*?)<\/script>/)[1]);
for(const f of ['reviewed-ui.js','setup-wizard.js','playback-modes.js'])run(fs.readFileSync(__dirname+'/'+f,'utf8').replace(/render\(\);\s*$/,''));
run('render=()=>{};updateBar=()=>{};notice=()=>{}');
assert.doesNotMatch(run('modeChoice()'),/aria-pressed="true"/);
assert.equal((run('modeChoice()').match(/role="tab"/g)||[]).length,4);
assert.equal((run('modeChoice()').match(/role="tabpanel"/g)||[]).length,1);
for(const id of ['redirect','relay','single','split']){
 run(`selectMode('${id}')`);
 assert.equal(run('playbackMode'),null,'browsing must not activate mode');
 assert.match(run('modeChoice()'),new RegExp('aria-labelledby="mode-tab-'+id+'"'));
 assert.equal((run('modeChoice()').match(/aria-selected="true"/g)||[]).length,1);
}
for(const mode of ['redirect','relay','single','split']){
 run(`pendingMode='${mode}';confirmMode()`);
 for(const p of ['overview','wizard','services','delivery','security','diagnostics','config']){
  const html=run(p+'()');assert.ok(html.length>20);
  if(mode!=='split')assert.doesNotMatch(html,/导出媒体服务配置包|导入主代理配置包|选择部署角色/);
 }
 if(mode==='redirect')assert.doesNotMatch(run('security()+delivery()'),/生成密钥|视频监听端口|上传视频证书/);
 if(mode==='relay'){
  run('draft.services.emby.enabled=true');assert.ok(run('capabilityErrors().length')>0);
  run('draft.services.emby.enabled=false');
 }
 if(mode==='single')assert.match(run('security()'),/本容器视频密钥/);
 run(`draft.services.fntv.target='http://${mode}.example';draft.services.fntv.enabled=true`);
}
for(const mode of ['redirect','relay','single','split']){
 run(`pendingMode='${mode}';confirmMode()`);
 assert.equal(run('draft.services.fntv.target'),'http://'+mode+'.example');
}
run("pendingMode='redirect';confirmMode();modeStep=1;draft.services.fntv.port='70000'");
assert.match(run('standaloneStepErrors().join()'),/端口/);
run("pendingMode='single';confirmMode();modeStep=2;draft.videoPort=draft.services.fntv.port");
assert.match(run('standaloneStepErrors().join()'),/不能与登录端口/);
console.log('PASS: four modes × seven modules, conditional controls, unsupported combinations, isolated drafts, listener validation');
