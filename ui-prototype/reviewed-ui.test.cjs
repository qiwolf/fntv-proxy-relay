const fs = require('node:fs');
const vm = require('node:vm');
const assert = require('node:assert/strict');
const context = vm.createContext({structuredClone, URL, console});
vm.runInContext(fs.readFileSync(__dirname+'/index.html','utf8').match(/<script>([\s\S]*?)<\/script>/)[1], context);
vm.runInContext(fs.readFileSync(__dirname+'/reviewed-ui.js','utf8').replace(/render\(\);\s*$/, ''), context);
const run = code => vm.runInContext(code, context);
for (const role of ['proxy','media']) {
 run(`editingRole='${role}';draft.role='${role}'`);
 for (const page of ['overview','wizard','services','delivery','security','diagnostics','config']) {
  const html=run(`${page}()`);
  assert.doesNotMatch(html, /原型|演示|配置草案|原服务器地址/);
 }
 if(role==='media') {
  assert.doesNotMatch(run('delivery()'), /目录对应|STRM 目录/);
  assert.doesNotMatch(run('services()'), /id="target"|id="host"|id="port"/);
  assert.doesNotMatch(run('diagnostics()'), /STRM 目录与读取权限/);
  assert.match(run('config()'), /导入主代理配置包/);
 } else {
  assert.doesNotMatch(run('services()'), /媒体公网|视频访问地址/);
  assert.match(run('security()'), /主代理 HTTPS 连接证书/);
  assert.doesNotMatch(run('security()'), /<h2>视频 HTTPS 连接证书/);
 }
}
run(`render=()=>{};notice=()=>{};tab='fntv';draft.services.fntv.enabled=true;draft.services.emby.enabled=true;cancelChanges(true)`);
assert.equal(run('draft.services.fntv.enabled'), false);
assert.equal(run('draft.services.emby.enabled'), true);
run(`draft.transport='http'`);
assert.doesNotMatch(run('security()'), /上传证书/);
console.log('PASS: 14 role/page renders, module boundaries, scoped cancel, certificate visibility');
vm.runInContext(fs.readFileSync(__dirname+'/setup-wizard.js','utf8').replace(/render\(\);\s*$/, ''), context);
assert.match(run('wizard()'),/1. 选择部署角色/);
assert.doesNotMatch(run('wizard()'),/aria-pressed="true"/);
run(`setupRoleSelected=true`);
run(`editingRole='proxy';draft=structuredClone(initial);step=0`);
assert.match(run('currentStepErrors().join()'),/至少选择/);
run(`draft.services.fntv.enabled=true`);
assert.equal(run('currentStepErrors().length'),0);
run(`step=1;draft.services.fntv.host=''`);
assert.equal(run('currentStepErrors().length'),0,'IP-only entry accepted');
run(`draft.services.fntv.target='invalid'`);
assert.match(run('currentStepErrors().join()'),/服务器地址/);
run(`draft.services.fntv.target=initial.services.fntv.target;step=3;draft.transport='http'`);
assert.doesNotMatch(run('wizard()'),/上传证书|去配置/);
assert.equal(run('wizardSteps().includes("连接证书")'),false);
assert.match(run('wizard()'),/生成密钥/);
assert.match(run('currentStepErrors().join()'),/管理服务未连接/);
run(`editingRole='media';step=0`);
assert.match(run('wizard()'),/导入主代理配置包/);
assert.match(run('currentStepErrors().join()'),/尚不能导入/);
for(const role of ['proxy','media']) {
 run(`editingRole='${role}'`);
 for(let i=0;i<run('wizardSteps().length');i++) {
  run(`step=${i}`);
  assert.doesNotMatch(run('wizard()'),/去配置|原型|演示/);
 }
}
console.log('PASS: wizard stages, step validation, IP-only entry, backend gates and key controls');
run(`editingRole='proxy';draft=structuredClone(initial);draft.services.fntv.enabled=true;step=2;tab='jellyfin'`);
assert.match(run('setupNetwork()'), /飞牛影视：两边的 STRM 目录/);
assert.doesNotMatch(run('setupNetwork()'), /Emby|Jellyfin|class="tabs"/);
run(`addSetupDirectory('fntv');draft.services.fntv.pathRules[0]={from:'/movies',to:'/copy'};draft.services.emby.enabled=true;addSetupDirectory('emby')`);
assert.match(run('setupNetwork()'), /Emby：两边的 STRM 目录/);
assert.match(run('setupNetwork()'), /value="\/copy"/);
run(`removeSetupDirectory('emby',0)`);
assert.equal(run('draft.services.fntv.pathRules.length'),1);
run(`draft.services.emby.pathRules=[{from:'invalid',to:'invalid'}];draft.services.emby.enabled=false`);
assert.equal(run('currentStepErrors(2).length'),0);
assert.doesNotMatch(run('setupNetwork()'), /Emby|Jellyfin/);
run(`editingRole='media'`);
assert.doesNotMatch(run('setupNetwork()'), /两边的 STRM 目录/);
console.log('PASS: independent wizard directory forms, selected-service filtering and edit isolation');
run(`updateBar=()=>{};editingRole='proxy';draft=structuredClone(initial)`);
assert.equal(run('listenParts().port'),'28008');
run(`editListener('port','29000')`);
assert.equal(run('draft.listen'),'0.0.0.0:29000');
run(`editListener('address','::')`);
assert.equal(run('draft.listen'),'[::]:29000');
assert.equal(run('listenParts().address'),'::');
run(`draft.transport='http'`);
assert.match(run('currentStepErrors(3).join()'),/密钥/);
run(`editListener('port','70000')`);
assert.match(run('currentStepErrors(1).join()'),/监听端口/);
run(`step=1`);
assert.match(run('wizard()'),/域名入口的转发端口（主代理共享）/);
run(`step=3`);
assert.doesNotMatch(run('wizard()'),/域名入口的转发端口|高级：绑定地址/);
run(`draft.services.fntv.enabled=true;step=2`);
assert.match(run('wizard()'),/飞牛影视的 STRM 视频来源/);
run(`editingRole='media';draft=structuredClone(initial)`);
assert.equal(run('listenParts().port'),'49967');
assert.match(run('setupNetwork()'),/接收视频请求的端口（媒体服务）/);
console.log('PASS: listener defaults, port editing, IPv6 formatting and validation');
