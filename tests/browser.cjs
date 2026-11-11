const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const net = require('node:net');
const { spawn } = require('node:child_process');
const crypto = require('node:crypto');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');

let base = process.env.TEST_URL;
const qa = path.resolve(__dirname, '../.qa');
fs.mkdirSync(qa, { recursive: true });

(async () => {
  let server, browser;
  try {
    if (!base) {
      const port = await new Promise((resolve,reject) => {
        const socket = net.createServer();
        socket.on('error',reject);
        socket.listen(0,'127.0.0.1',() => { const port=socket.address().port;socket.close(()=>resolve(port)); });
      });
      const runDir=fs.mkdtempSync(path.join(qa,'browser-'));
      const config=path.join(runDir,'config.json');
      fs.copyFileSync(path.resolve(__dirname,'../config.example.json'),config);
      const binary=process.env.MANAGER_BINARY || path.join(qa,'easytier-access');
      const log=fs.openSync(path.join(runDir,'server.log'),'wx',0o600);
      server=spawn(binary,['--demo','--config',config,'--port',String(port)],{stdio:['ignore',log,log]});
      fs.closeSync(log);
      server.on('error',error=>{console.error(error);});
      base=`http://127.0.0.1:${port}`;
      for(let i=0;i<100;i++) {
        try { if((await fetch(`${base}/api/session`)).ok) break; } catch {}
        if(i===99) throw new Error('Isolated demo server did not start');
        await new Promise(resolve=>setTimeout(resolve,50));
      }
    }
    const session = await fetch(`${base}/api/session`).then(r => r.json());
    assert.equal(session.demo, true, 'Browser workflow may run only against an explicit demo server');
    browser = await chromium.launch({ channel: 'chrome', headless: true });
    const page = await browser.newPage({ viewport: { width: 1440, height: 1100 } });
    const errors = [];
    let syncRequests = 0, peerRequests = 0;
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => { if (request.url().endsWith('/api/sync')) syncRequests++; });
    page.on('request', request => { if (request.url().endsWith('/api/peers/sync')) peerRequests++; });
    await page.goto(base);
    await page.getByRole('button', { name: '进入演示', exact: true }).click();
    await page.getByRole('heading', { name: '监控看板', exact: true }).waitFor();
    await page.getByRole('button', { name: '同步节点', exact: true }).waitFor();
    await page.locator('#toast').waitFor({ state: 'hidden' });
    assert.match(await page.locator('#monitor-metrics').innerText(), /登记人员/);
    assert.match(await page.locator('.helper-line').innerText(), /设备在线状态尚未采集/);
    assert.equal(await page.locator('.monitor-table tbody tr').count(), 6);
    await page.locator('#search').fill('MacBook Pro');
    assert.equal(await page.locator('.monitor-table tbody tr').count(), 1, 'Monitor search must include device names');
    assert.equal(await page.locator('#search').evaluate(el=>document.activeElement===el), true);
    await page.clock.install();
    const firstSync = syncRequests;
    await Promise.all([
      page.waitForResponse(r=>r.url().endsWith('/api/state') && r.ok()),
      page.clock.runFor(30010)
    ]);
    assert.equal(syncRequests, firstSync+1, 'Automatic refresh must query the actual node');
    assert.equal(await page.locator('#search').inputValue(), 'MacBook Pro', 'Automatic refresh must preserve the search');
    assert.equal(await page.locator('#search').evaluate(el=>document.activeElement===el), true, 'Automatic refresh must preserve focus');
    await page.getByRole('button', { name: '自动同步：30 秒 · 暂停', exact: true }).click();
    const pausedSync = syncRequests;
    await page.clock.runFor(60010);
    assert.equal(syncRequests, pausedSync, 'Paused monitoring must stop querying the node');
    await page.getByRole('button', { name: '自动同步已暂停 · 开启', exact: true }).click();
    await page.route('**/api/sync', route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'QA 节点暂时不可用'})}), {times:1});
    await Promise.all([
      page.waitForResponse(r=>r.url().endsWith('/api/sync') && r.status()===503),
      page.clock.runFor(30010)
    ]);
    await page.locator('#monitor-error .banner').waitFor();
    assert.match(await page.locator('#monitor-error').innerText(), /保留现有快照/);
    assert.equal(await page.locator('.monitor-table tbody tr').count(), 1, 'Failed refresh must retain personnel snapshot');
    await page.getByRole('button', { name: '同步节点', exact: true }).click();
    await page.locator('#monitor-error .banner').waitFor({state:'detached'});
    await page.getByRole('button', { name: '同步节点', exact: true }).waitFor();
    await page.locator('#search').fill('');
    await page.locator('#status-filter').selectOption('expiring');
    assert.ok(await page.locator('.monitor-table tbody tr').count()>0);
    await page.locator('#status-filter').selectOption('all');
    await page.clock.resume();
    await page.clock.setFixedTime(new Date());
    await page.getByRole('button',{name:'同步节点',exact:true}).click();
    await page.getByRole('button',{name:'同步节点',exact:true}).waitFor();
    for (const [width,height] of [[375,812],[768,1024],[1024,768],[1440,1100]]) {
      await page.setViewportSize({width,height});
      await page.emulateMedia({reducedMotion:'reduce'});
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`Monitor overflows at ${width}px`);
      if(width===375 || width===1440) {
        await page.locator('#toast').waitFor({state:'hidden'});
        await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
        await page.screenshot({path:path.join(qa,width===375?'monitor-mobile.png':'monitor-desktop.png'),fullPage:true});
      }
    }
    await page.getByRole('button',{name:'节点状态',exact:true}).click();
    await page.locator('.peer-table').waitFor();
    assert.equal(await page.locator('.peer-table tbody tr').count(),4);
    assert.match(await page.locator('.peer-table').innerText(),/10.10.10.20/);
    assert.doesNotMatch(await page.locator('.peer-table').innerText(),/本机节点|本次未发现/);
    assert.match(await page.locator('.peer-table').innerText(),/林予安/);
    assert.equal(await page.locator('#status-filter option[value=missing]').count(),0);
    const peerState=await page.evaluate(()=>fetch('/api/peers').then(r=>r.json()));
    const unassignedPeer=peerState.items.find(p=>p.id==='10005');
    assert.equal(unassignedPeer.identity_status,'verified');
    await page.locator('.peer-table [data-action=peer-person]').first().click();
    await page.locator('#drawer').waitFor({state:'visible'});
    assert.match(await page.locator('#drawer').innerText(),/林予安/);
    await page.getByRole('button',{name:'关闭详情',exact:true}).click();
    await page.locator('#search').fill('林予安');
    assert.equal(await page.locator('.peer-table tbody tr').count(),1);
    await page.locator('#search').fill('');
    await page.locator('.peer-table [data-action=bind-peer]').first().click();
    await page.locator('#peer-credential').waitFor();
    await page.getByRole('button',{name:'取消',exact:true}).click();
    assert.match(await page.locator('.peer-table').innerText(),/经中转/);
    await page.locator('#search').fill('10.10.10.20');
    assert.equal(await page.locator('.peer-table tbody tr').count(),1);
    const initialPeerRequests=peerRequests;
    await Promise.all([page.waitForResponse(r=>r.url().endsWith('/api/peers/sync')&&r.ok()),page.clock.runFor(30010)]);
    assert.equal(peerRequests,initialPeerRequests+1,'Peers must synchronize every 30 seconds');
    assert.equal(await page.locator('#search').inputValue(),'10.10.10.20');
    assert.equal(await page.locator('#search').evaluate(el=>document.activeElement===el),true,'Peer refresh must preserve search focus');
    await page.getByRole('button',{name:'自动同步：30 秒 · 暂停',exact:true}).click();
    const pausedPeerRequests=peerRequests;
    await page.clock.runFor(60010);
    assert.equal(peerRequests,pausedPeerRequests,'Paused peers must not query the core');
    await page.getByRole('button',{name:'自动同步已暂停 · 开启',exact:true}).click();
    await page.route('**/api/peers/sync',route=>route.fulfill({status:503,contentType:'application/json',body:JSON.stringify({error:'QA peer 服务暂时不可用'})}),{times:1});
    await Promise.all([page.waitForResponse(r=>r.url().endsWith('/api/peers/sync')&&r.status()===503),page.clock.runFor(30010)]);
    await page.locator('#peer-error .banner').waitFor();
    assert.match(await page.locator('#peer-error').innerText(),/暂不展示上次节点列表/);
    assert.equal(await page.locator('.peer-table tbody tr').count(),0,'Failed peer sync must hide unconfirmed connections');
    await page.getByRole('button',{name:'同步节点',exact:true}).click();
    await page.locator('#peer-error .banner').waitFor({state:'detached'});
    await page.locator('#search').fill('');
    await page.locator('#status-filter').selectOption('relay');
    assert.equal(await page.locator('.peer-table tbody tr').count(),1);
    await page.locator('#status-filter').selectOption('all');
    await page.route('**/api/peers/sync',async route=>{
      const response=await route.fetch(),snapshot=await response.json();
      snapshot.items.push({id:'10006',hostname:'<img src=x onerror=alert(1)> very-long-untrusted-peer-name-for-layout',ipv4:'10.10.10.23',cidr:'10.10.10.23/24',cost:'p2p',status:'direct',identity_status:'unverified',last_seen:snapshot.now,version:'2.6.4',nat_type:'Unknown',lat_ms:'999',rx_bytes:'999 TB'});
      await route.fulfill({response,json:snapshot});
    },{times:1});
    await page.getByRole('button',{name:'同步节点',exact:true}).click();
    await page.locator('.peer-table tbody tr').filter({hasText:'10006'}).waitFor();
    const currentRow=page.locator('.peer-table tbody tr').filter({hasText:'10006'});
    assert.match(await currentRow.innerText(),/连接身份未验证/);
    assert.equal(await page.locator('.peer-table img').count(),0,'Peer hostnames must be escaped');
    for(const [width,height] of [[375,812],[768,1024],[1024,768],[1440,1100]]) {
      await page.setViewportSize({width,height});
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,`Peer panel overflows at ${width}px`);
      if(width===375||width===1440) {
        await page.locator('#toast').waitFor({state:'hidden'});
        await page.evaluate(()=>{window.scrollTo(0,0);return new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));});
        await page.screenshot({path:path.join(qa,width===375?'peers-mobile.png':'peers-desktop.png'),fullPage:true});
      }
    }
    await page.getByRole('button',{name:'同步节点',exact:true}).click();
    await page.locator('.peer-table tbody tr').filter({hasText:'10006'}).waitFor({state:'detached'});
    assert.equal(await page.locator('.peer-table tbody tr').count(),4,'A disappeared node must not remain in history');
    await page.locator('.nav-item[data-view="people"]').click();
    const leftPeerRequests=peerRequests;
    await page.clock.runFor(30010);
    assert.equal(peerRequests,leftPeerRequests,'Leaving peer panel must stop collection');
    await page.screenshot({ path: path.join(qa, 'desktop.png'), fullPage: true });

    const person = `QA 同事 ${Date.now()}`;
    await page.getByRole('button', { name: '新增人员', exact: true }).click();
    await page.locator('#person-name').fill(person);
    await page.locator('#person-team').fill('研发与基础设施');
    await page.locator('#person-contact').fill('qa.long-address-for-layout@example.com');
    await page.getByRole('button', { name: '创建人员', exact: true }).click();
    await page.locator('#modal').waitFor({ state: 'hidden' });

    await page.getByRole('button', {name:'监控看板',exact:true}).click();
    await page.locator('#search').fill(person);
    await page.locator('#status-filter').selectOption('no_access');
    assert.equal(await page.locator('.monitor-table tbody tr').count(),1,'New personnel without credentials must appear in no-access filter');
    await page.getByRole('button',{name:'查看详情',exact:true}).click();
    assert.match(await page.locator('#drawer').innerText(),new RegExp(person));
    await page.getByRole('button',{name:'关闭详情',exact:true}).click();
    await page.locator('.nav-item[data-view="people"]').click();

    for (const device of ['QA MacBook Pro', 'QA iPhone']) {
      await page.locator('.people-table tr').filter({ hasText: person }).getByRole('button', { name: '签发凭据', exact: true }).click();
      await page.locator('#issue-device').fill(device);
      await page.locator('#issue-ttl').selectOption('7');
      await page.locator('#issue-groups').fill('staff,developer');
      assert.equal(await page.locator('#launch-vpn, #launch-exit').count(),0,'VPN controls must be removed');
      if(device==='QA MacBook Pro') {
        await page.locator('#launch-ip-mode').selectOption('static');
        await page.locator('#launch-ipv4').fill('10.10.10.20/24');
        await page.screenshot({path:path.join(qa,'launch-options-desktop.png'),fullPage:true});
        await page.setViewportSize({width:375,height:812});
        assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'Launch form overflows mobile');
        await page.locator('#launch-ipv4').scrollIntoViewIfNeeded();
        await page.locator('#modal').screenshot({path:path.join(qa,'launch-options-mobile.png')});
        await page.setViewportSize({width:1440,height:1100});
      }
      await page.locator('#modal button[type=submit]').click();
      await page.getByRole('heading', { name: '凭据已签发', exact: true }).waitFor();
      assert.match(await page.locator('#join-command').innerText(), /DEMO_ONLY_/);
      assert.match(await page.locator('#join-command').innerText(), /--credential/);
      const command=await page.locator('#join-command').innerText();
      assert.doesNotMatch(command,/--exit-nodes|--manual-routes|--bind-device/);
      if(device==='QA MacBook Pro') {
        assert.match(command,/--ipv4 '10.10.10.20\/24' --dhcp false/);
      } else {
        assert.match(command,/--dhcp true/);
        assert.doesNotMatch(command,/--exit-nodes/);
      }
      await page.getByRole('button',{name:'TOML 配置',exact:true}).click();
      assert.equal(await page.locator('#launch-command-panel').isVisible(),false);
      const toml=await page.locator('#join-toml').innerText();
      assert.match(toml,/\[network_identity\]/);
      assert.match(toml,/REPLACE_WITH_CREDENTIAL_PRIVATE_KEY/,'Demo TOML must be clearly marked as a template');
      assert.equal(toml.includes('dhcp = false'),device==='QA MacBook Pro');
      assert.doesNotMatch(toml,/exit_nodes|routes =|bind_device/);
      if(device==='QA MacBook Pro') {
        const downloadPromise=page.waitForEvent('download');
        await page.getByRole('button',{name:'下载 TOML',exact:true}).click();
        const download=await downloadPromise;
        assert.equal(download.suggestedFilename(),'client.template.toml');
        assert.equal(fs.readFileSync(await download.path(),'utf8').trimEnd(),toml.trimEnd());
      }
      await page.getByRole('button',{name:'纯命令行',exact:true}).click();
      assert.equal(await page.locator('#launch-toml-panel').isVisible(),false);
      await page.locator('#modal .dialog-footer').getByRole('button', { name: '关闭', exact: true }).click();
    }

    await page.getByRole('button',{name:'凭据管理',exact:true}).click();
    const existingRow=page.locator('.credentials-table tbody tr').filter({hasText:'QA MacBook Pro'});
    await existingRow.getByRole('button',{name:'启动命令',exact:true}).click();
    const selectedCredential=await page.locator('#launch-credential').inputValue();
    assert.equal(await page.locator('#launch-ipv4').inputValue(),'10.10.10.20/24','Existing credential must remember launch choices');
    assert.equal(await page.locator('#launch-vpn, #launch-exit').count(),0);
    await page.getByRole('button',{name:'保存设置并生成命令',exact:true}).click();
    await page.getByRole('heading',{name:'启动命令已生成',exact:true}).waitFor();
    assert.match(await page.locator('#join-command').innerText(),/\$\{EASYTIER_CREDENTIAL:\?/,'Existing credential must offer a secret-free template');
    await page.locator('#modal .dialog-footer').getByRole('button',{name:'关闭',exact:true}).click();
    await existingRow.getByRole('button',{name:'启动命令',exact:true}).click();
    const otherCredential=await page.locator('#launch-credential option').filter({hasText:'QA iPhone'}).getAttribute('value');
    await page.locator('#launch-secret').fill('DEMO_ONLY_CLEARED_ON_CHANGE');
    await page.locator('#launch-credential').selectOption(otherCredential);
    assert.equal(await page.locator('#launch-secret').inputValue(),'','Changing credentials must clear the pasted secret');
    assert.equal(await page.locator('#launch-ip-mode').inputValue(),'dhcp');
    await page.locator('#launch-credential').selectOption(selectedCredential);
    const testKey=crypto.generateKeyPairSync('x25519');
    const browserSecret=testKey.privateKey.export({format:'der',type:'pkcs8'}).subarray(-32).toString('base64');
    const expectedPublic=testKey.publicKey.export({format:'der',type:'spki'}).subarray(-32).toString('base64');
    await page.locator('#launch-secret').fill(browserSecret);
    const saveRequest=page.waitForRequest(r=>r.url().endsWith('/launch')&&r.method()==='POST');
    await page.getByRole('button',{name:'保存设置并生成命令',exact:true}).click();
    assert.equal((await saveRequest).postData().includes(browserSecret),false,'Existing credential secret must stay in the browser');
    await page.getByRole('heading',{name:'启动命令已生成',exact:true}).waitFor();
    assert.ok((await page.locator('#join-command').innerText()).includes(browserSecret));
    assert.doesNotMatch(await page.locator('#join-command').innerText(),/EASYTIER_CREDENTIAL/);
    await page.getByRole('button',{name:'TOML 配置',exact:true}).click();
    const realTOML=await page.locator('#join-toml').innerText();
    assert.ok(realTOML.includes(`local_private_key = "${browserSecret}"`));
    assert.ok(realTOML.includes(`local_public_key = "${expectedPublic}"`),'Browser-derived X25519 key must match Node crypto');
    assert.doesNotMatch(realTOML,/REPLACE_WITH_/);
    const tomlDownload=page.waitForEvent('download');
    await page.getByRole('button',{name:'下载 TOML',exact:true}).click();
    const file=await tomlDownload;
    assert.equal(file.suggestedFilename(),'client.toml');
    const saved=fs.readFileSync(await file.path(),'utf8');
    assert.ok(saved.includes(browserSecret));
    const parsed=spawn(process.env.TEST_PYTHON || 'python3',['-c','import sys,tomllib; c=tomllib.loads(sys.stdin.read()); assert c["dhcp"] is False; assert c["ipv4"] == "10.10.10.20/24"; assert "exit_nodes" not in c and "routes" not in c and "bind_device" not in c["flags"]'],{stdio:['pipe','ignore','pipe']});
    parsed.stdin.end(saved);
    assert.equal(await new Promise(resolve=>parsed.on('close',resolve)),0,'Downloaded config must parse as TOML');
    assert.ok((await page.locator('#toml-command').innerText()).includes(`easytier-core -c client.toml --credential '${browserSecret}'`));
    for(const width of [375,768,1440]) {
      await page.setViewportSize({width,height:width===1440?1100:812});
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'TOML result must fit narrow screens');
      if(width===375 || width===1440) {
        // Screenshots must not retain real credential material.
        await page.locator('#join-toml').evaluate(el=>{ el.textContent=el.textContent.replace(/(local_(?:private|public)_key = ).*/g,'$1"[测试密钥已隐藏]"'); });
        await page.locator('#toml-command').evaluate(el=>{el.textContent="easytier-core -c client.toml --credential '[测试密钥已隐藏]'";});
        await page.locator('#modal').screenshot({path:path.join(qa,width===375?'toml-result-mobile.png':'toml-result-desktop.png')});
      }
    }
    await page.locator('#modal .dialog-footer').getByRole('button',{name:'关闭',exact:true}).click();
    await page.locator('.nav-item[data-view="people"]').click();

    await page.locator('#search').fill(person);
    assert.equal(await page.locator('.people-table tbody tr').count(), 1);
    assert.match(await page.locator('.people-table td[data-col=count]').innerText(), /2/);
    await page.locator('.people-table tr').filter({ hasText: person }).getByRole('button', { name: '查看', exact: true }).click();
    await page.getByRole('button', { name: '办理离职并撤权', exact: true }).click();
    await page.locator('#confirm-name').fill('错误的姓名');
    await page.getByRole('button', { name: '停用并撤销凭据', exact: true }).click();
    assert.match(await page.locator('#modal .form-error').innerText(), /不一致/);
    await page.locator('#confirm-name').fill(person);
    await page.getByRole('button', { name: '停用并撤销凭据', exact: true }).click();
    await page.locator('#modal').waitFor({ state: 'hidden' });
    await page.locator('#drawer .badge').filter({ hasText: '已离职' }).waitFor();
    assert.equal(await page.locator('#drawer .device-item .badge').filter({ hasText: '已撤销' }).count(), 2);
    await page.getByRole('button', { name: '恢复为在职人员', exact: true }).click();
    await page.locator('#drawer .detail-person .badge').filter({ hasText: '在职' }).waitFor();
    assert.equal(await page.locator('#drawer .device-item .badge').filter({ hasText: '已撤销' }).count(), 2);
    await page.getByRole('button', { name: '关闭详情', exact: true }).click();

    await page.getByRole('button', { name: '凭据管理', exact: true }).click();
    await page.getByRole('button', { name: '待关联人员', exact: true }).click();
    await page.locator('.credentials-table tbody tr').first().getByRole('button', { name: '关联人员', exact: true }).click();
    await page.locator('#assign-person').selectOption({ label: `${person} · 研发与基础设施` });
    await page.locator('#assign-device').fill('归属已补充的 Windows 工作站');
    await page.locator('#modal summary').click();
    const bindingSecret=Buffer.alloc(32,73).toString('base64');
    await page.locator('#assign-key-secret').fill(bindingSecret);
    await page.getByRole('button',{name:'提取公钥',exact:true}).click();
    assert.equal(await page.locator('#assign-key-secret').inputValue(),'');
    assert.notEqual(await page.locator('#assign-public-key').inputValue(),'');
    await page.locator('#assign-public-key').fill(unassignedPeer.public_key);
    const bindingRequest=page.waitForRequest(r=>r.url().endsWith('/assign')&&r.method()==='POST');
    await page.getByRole('button', { name: '保存归属', exact: true }).click();
    const bindingBody=(await bindingRequest).postData();
    assert.equal(bindingBody.includes(bindingSecret),false,'Private material must never leave the browser during public key backfill');
    assert.equal(JSON.parse(bindingBody).public_key,unassignedPeer.public_key);
    await page.locator('#modal').waitFor({ state: 'hidden' });
    await page.locator('#status-filter').selectOption('all');
    await page.locator('#search').fill('归属已补充');
    assert.equal(await page.locator('.credentials-table tbody tr').count(), 1);
    assert.match(await page.locator('.credentials-table tbody').innerText(), new RegExp(person));
    await page.getByRole('button',{name:'节点状态',exact:true}).click();
    const boundPeer=page.locator('.peer-table tbody tr').filter({hasText:'10005'});
    await boundPeer.waitFor();
    await boundPeer.getByRole('button',{name:person,exact:true}).waitFor();
    assert.match(await boundPeer.innerText(),new RegExp(person));
    assert.match(await boundPeer.innerText(),/管理员补录/);
    await page.locator('#status-filter').selectOption('assigned');
    assert.equal(await page.locator('.peer-table tbody tr').count(),3);
    await page.locator('#search').fill(person);
    assert.equal(await page.locator('.peer-table tbody tr').count(),1);
    await boundPeer.getByRole('button',{name:person,exact:true}).click();
    await page.locator('#drawer').getByText('归属已补充的 Windows 工作站',{exact:true}).waitFor();
    assert.match(await page.locator('#drawer').innerText(),/归属已补充的 Windows 工作站/);
    await page.getByRole('button',{name:'关闭详情',exact:true}).click();

    await page.locator('.nav-item[data-view="people"]').click();
    for (const [width,height] of [[375,812],[768,1024],[1024,768],[1440,1100]]) {
      await page.setViewportSize({ width,height });
      await page.emulateMedia({ reducedMotion: 'reduce' });
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth > innerWidth);
      assert.equal(overflow, false, `Page overflow at ${width}px`);
      if (width === 375) {
        await page.locator('#toast').waitFor({ state: 'hidden' });
        await page.screenshot({ path: path.join(qa, 'mobile.png'), fullPage: true });
      }
    }
    await page.setViewportSize({ width: 375, height: 812 });
    await page.getByRole('button', { name: '新增人员', exact: true }).click();
    await page.locator('#person-name').fill('<img src=x onerror=alert(1)>');
    await page.locator('#person-contact').fill('very-long-contact-address-to-check-wrapping@example.com');
    await page.getByRole('button', { name: '创建人员', exact: true }).click();
    await page.locator('#modal').waitFor({ state: 'hidden' });
    assert.equal(await page.locator('.people-table img').count(), 0, 'Personnel text must not become markup');
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'Long content overflows mobile');

    await page.getByRole('button', {name:'监控看板',exact:true}).click();
    await page.locator('#search').fill('<img src=x onerror=alert(1)>');
    assert.equal(await page.locator('.monitor-table img').count(),0,'Monitor must escape personnel names');
    assert.equal(await page.locator('.monitor-table tbody tr').count(),1);
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,'Long personnel name overflows mobile monitor');

    await page.getByRole('button', { name: '操作记录', exact: true }).click();
    assert.match(await page.locator('.audit-table').innerText(), /完成离职撤权/);
    await page.getByRole('button', { name: '连接设置', exact: true }).click();
    assert.match(await page.locator('.settings-grid').innerText(), /127.0.0.1:15888/);
    assert.deepEqual(errors, [], 'Unexpected browser errors');
    await page.getByRole('button', {name:'监控看板',exact:true}).click();
    await page.getByRole('button', { name: '退出登录', exact: true }).click();
    await page.getByRole('button', { name: '进入演示', exact: true }).waitFor();
    const loggedOutSync=syncRequests,loggedOutPeers=peerRequests;
    await page.clock.runFor(60010);
    assert.equal(syncRequests,loggedOutSync,'Logout must stop monitoring requests');
    assert.equal(peerRequests,loggedOutPeers,'Logout must stop peer requests');
    console.log('PASS: monitoring, current peers, authenticated personnel matching and manual public key backfill, refresh/pause/retry, focus preservation, search/filter/drilldown, static IP/DHCP, VPN option removal, existing credential selection/template, TOML switching/download/parsing, X25519 derivation, browser-only secrets, lifecycle, auth, XSS, and 375/768/1024/1440 layouts.');
  } finally {
    if(browser) await browser.close();
    if(server) server.kill('SIGTERM');
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
