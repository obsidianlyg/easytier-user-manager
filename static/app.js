'use strict';

const $ = (selector, root = document) => root.querySelector(selector);
const e = value => String(value ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const icon = name => `<svg class="icon" aria-hidden="true"><use href="#i-${name}"></use></svg>`;
const app = $('#app'), modal = $('#modal'), drawer = $('#drawer');
let session = {}, data = null, view = 'monitor', search = '', filter = 'all', selectedPerson = null;
let modalSubmit = null, issued = null, toastTimer = null, synchronizing = false;
let monitorTimer = null, monitorStatusTimer = null, autoRefresh = true, refreshError = '';
const titles = {peers:'节点状态',monitor:'监控看板',people:'人员管理',credentials:'凭据管理',audit:'操作记录',settings:'连接设置'};
const personStates = {active:['在职','positive'],revoking:['撤权中','warning'],offboarded:['已离职','']};
const credentialStates = {active:['有效','positive'],expired:['已到期',''],revoked:['已撤销',''],missing:['节点未发现','warning'],issuing:['签发中','warning'],issue_unconfirmed:['签发待核实','warning'],revoking:['撤销中','warning'],revoke_failed:['撤销失败','negative']};
const auditNames = {credential_key_registered:'补录凭据公钥',demo_seed:'初始化演示',launch_updated:'更新启动方式',admin_initialized:'设置管理员',admin_login:'管理员登录',person_saved:'新增人员',person_updated:'编辑人员',issue_requested:'请求签发',issue_failed:'签发待核实',credential_issued:'签发凭据',revoke_requested:'请求撤销',revoke_failed:'撤销失败',credential_revoked:'撤销凭据',credential_assigned:'关联人员',offboard_requested:'开始离职撤权',offboard_partial:'离职撤权未完成',person_offboarded:'完成离职撤权',person_restored:'恢复人员'};

function badge(status, kind = 'credential') {
  const [label, tone] = (kind === 'person' ? personStates : credentialStates)[status] || [status,''];
  return `<span class="badge ${tone}">${tone ? '<span class="dot"></span>' : ''}${e(label)}</span>`;
}
function date(value, withTime = false) {
  if (!value) return '—';
  return new Intl.DateTimeFormat('zh-CN', {year:'numeric',month:'2-digit',day:'2-digit', ...(withTime ? {hour:'2-digit',minute:'2-digit',hour12:false} : {})}).format(new Date(value * 1000));
}
function relativeExpiry(c) {
  if (!c.expires_at) return '有效期待核实';
  if (c.status === 'revoked') return '已撤销';
  const delta = c.expires_at - Math.floor(Date.now()/1000);
  if (delta <= 0) return '已到期';
  if (delta < 86400) return '不足 1 天到期';
  return `${Math.ceil(delta / 86400)} 天后到期`;
}
const liveCredentials = pid => data.credentials.filter(c => (!pid || c.person_id === pid) && c.status === 'active');
const personByID = id => data.people.find(p => p.id === id);
const credentialByID = id => data.credentials.find(c => c.id === id);
const avatar = (p, index = 0) => `<span class="avatar tone-${index%4}">${e(Array.from(p.name).slice(-2).join(''))}</span>`;

async function api(path, body) {
  const options = {credentials:'same-origin', headers:{}};
  if (body !== undefined) {
    options.method = 'POST';
    options.headers = {'Content-Type':'application/json','X-CSRF-Token':session.csrf || ''};
    options.body = JSON.stringify(body);
  }
  const response = await fetch(path, options);
  let result;
  try { result = await response.json(); } catch { throw new Error('服务器返回异常，请检查连接。'); }
  if (!response.ok) {
    if (response.status === 401 && path !== '/api/login') {
      session = {...session,authenticated:false}; data = null;
      modal.close(); drawer.close(); issued = null; renderLogin();
    }
    throw new Error(result.error || `请求失败（${response.status}）`);
  }
  return result;
}

function toast(message, error = false) {
  const node = $('#toast');
  node.textContent = message; node.classList.toggle('error',error); node.hidden = false;
  clearTimeout(toastTimer); toastTimer = setTimeout(() => { node.hidden = true; }, error ? 7500 : 4000);
}

function renderLogin() {
  clearTimeout(monitorTimer);
  clearInterval(monitorStatusTimer);
  peerData=null;peerRequestError='';
  app.innerHTML = `<main id="main-content" class="login-layout"><section class="login-card" aria-labelledby="login-title">
    <div class="brand"><span class="brand-mark">E</span><div><div class="brand-name">EasyTier</div><div class="brand-sub">ACCESS MANAGER</div></div></div>
    <h1 id="login-title">管理每一次连接</h1><p class="muted">登录管理员账号，管理人员和设备入网权限。</p>
    ${session.demo ? '<div class="login-demo-note">本地演示环境 · 所有数据均为示例，签发和撤销不会影响真实 EasyTier 节点。</div>' : ''}
    <form data-form="login"><div class="form-error" role="alert"></div>
      <div class="field"><label for="username">管理员账号</label><input id="username" name="username" required maxlength="100" autocomplete="username" ${session.demo ? 'value="admin"' : ''}></div>
      <div class="field"><label for="password">密码</label><input id="password" name="password" type="password" required autocomplete="current-password" ${session.demo ? 'value="demo-access-2026"' : ''}></div>
      <button class="btn primary" type="submit">${session.demo ? '进入演示' : '登录管理台'}${icon('arrow')}</button>
    </form><p class="login-foot">EasyTier Access · 单管理员控制台</p>
  </section></main>`;
}

function shell() {
  const c = data.config, connected = data.connection.connected;
  app.innerHTML = `<div class="layout">
    <aside class="sidebar"><div class="brand"><span class="brand-mark">E</span><div><div class="brand-name">EasyTier</div><div class="brand-sub">ACCESS MANAGER</div></div></div>
      <div class="nav-label">访问控制</div><nav class="nav" aria-label="主导航">
        ${[['monitor','monitor'],['peers','monitor'],['people','users'],['credentials','key'],['audit','history'],['settings','settings']].map(([name,i]) => `<button class="nav-item ${view===name?'active':''}" data-action="nav" data-view="${name}" ${view===name?'aria-current="page"':''}>${icon(i)}<span>${titles[name]}</span>${name==='people'?`<span class="nav-count">${data.people.filter(p=>p.status==='active').length}</span>`:''}</button>`).join('')}
      </nav><div class="sidebar-bottom"><div class="node-label"><span class="dot"></span>EasyTier 管理节点</div><div class="mono muted">${e(c.rpc_portal)}</div><div class="sidebar-foot">独立凭据 · 按设备授权<br>Go 服务 / EasyTier CLI</div></div>
    </aside>
    <section class="workspace"><header class="topbar"><div class="breadcrumb"><span>工作空间</span><span>/</span><span>${titles[view]}</span></div><div class="top-actions">
      ${c.demo ? '<span class="badge warning">演示环境</span>' : ''}<span id="node-connection" class="badge ${connected?'positive':'warning'}"><span class="dot"></span>${connected?'节点已连接':'待连接'}</span>
      <div class="admin-label"><span class="admin-avatar">AD</span><span class="admin-name">${e(session.username)}</span><button class="btn ghost small" data-action="logout" aria-label="退出登录" title="退出登录">${icon('logout')}</button></div>
    </div></header><main id="main-content" class="content"></main></section>
  </div>`;
  renderView();
  scheduleMonitor();
}

function stats() {
  const active = liveCredentials(), expiring = active.filter(c => c.expires_at - data.now < 7*86400);
  const values = [
    ['在职人员',data.people.filter(p=>p.status==='active').length,'users','已停用人员不能签发新凭据'],
    ['有效凭据',active.length,'key','来自最近一次节点同步'],
    ['即将到期',expiring.length,'history','未来 7 天内到期'],
    ['待关联凭据',active.filter(c=>!c.person_id).length,'monitor','补充归属，便于后续撤权']
  ];
  return `<section class="stats" aria-label="访问权限概况">${values.map(([label,value,i,note],index)=>`<div class="stat"><div class="stat-top">${label}${icon(i)}</div><div class="stat-value ${index===2&&value?'amber':''}">${value}<span class="sr-only"></span></div><div class="stat-note">${note}</div></div>`).join('')}</section>`;
}

function banners() {
  let result = data.config.demo ? `<div class="banner demo">${icon('info')}<p>当前为本地演示。人员、凭据与操作均为模拟数据，不连接日本节点。</p></div>` : '';
  if (view!=='peers'&&!data.connection.connected) result += `<div class="banner error" role="alert">${icon('info')}<p>${e(data.connection.error)} ${data.connection.last_sync ? `当前显示 ${date(data.connection.last_sync,true)} 的历史快照。` : '当前尚未获得节点凭据列表。'}</p></div>`;
  return result;
}

function syncButton() { if(view==='peers') return peerSyncButton(); return `<button class="btn secondary-label ${synchronizing?'spin':''}" data-action="sync" ${synchronizing?'disabled':''}>${icon('refresh')}${synchronizing?'同步中…':'同步节点'}</button>`; }
function renderView() {
  const main = $('#main-content');
  const descriptions = {peers:'查看当前连接的节点、所属人员、虚拟 IP 与链路状态。',monitor:'查看已登记人员、设备授权与需要跟进的变更。',people:'将设备凭据关联到具体人员，入职授权、离职撤权都在这里完成。',credentials:'查看节点入网凭据，管理有效期、设备归属与授权状态。',audit:'签发、撤销和人员变更均有记录，凭据私钥不会写入日志。',settings:'确认管理节点与业务网络，检查面板和 EasyTier 的连接。'};
  main.innerHTML = `<div class="page-header"><div><h1>${titles[view]}</h1><p>${descriptions[view]}</p></div><div class="actions">${syncButton()}${view==='people'?`<button class="btn primary" data-action="add-person">${icon('plus')}新增人员</button>`:view==='credentials'?`<button class="btn primary" data-action="issue">${icon('plus')}签发凭据</button>`:''}</div></div>
    <div id="view-banners">${banners()}</div>${view==='people'||view==='credentials'?stats():''}<div id="view-content"></div>`;
  if (view==='peers') return renderPeers();
  if (view==='monitor') return renderMonitor();
  if (view==='settings') return renderSettings();
  const config = {
    people:{heading:'全部人员',count:data.people.length,placeholder:'搜索姓名、部门或联系方式',options:[['all','全部状态'],['active','在职'],['revoking','撤权中'],['offboarded','已离职']]},
    credentials:{heading:'节点凭据',count:data.credentials.length,placeholder:'搜索设备、人员或凭据 ID',options:[['all','全部状态'],['active','有效'],['expiring','即将到期'],['unassigned','待关联人员'],['revoked','已撤销'],['expired','已到期'],['abnormal','需要处理']]},
    audit:{heading:'最近操作',count:data.audit.length,placeholder:'搜索人员、设备或操作',options:[]}
  }[view];
  $('#view-content').innerHTML = `<section class="panel"><div class="panel-header"><div class="panel-heading">${config.heading}<span class="total-count">${config.count}</span></div><span class="muted mono">${view==='audit'?'最近 200 条':e(data.config.network_name)}</span></div>
    <div class="filterbar"><div class="search">${icon('search')}<input id="search" aria-label="${config.placeholder}" placeholder="${config.placeholder}" value="${e(search)}" autocomplete="off"></div>
    ${config.options.length?`<div class="filter-controls"><select id="status-filter" aria-label="筛选状态">${config.options.map(([value,label])=>`<option value="${value}" ${filter===value?'selected':''}>${label}</option>`).join('')}</select>${view==='credentials'?'<button class="btn ghost small" data-action="unassigned">待关联人员</button>':''}</div>`:''}</div><div id="data-content"></div></section>
    <div class="helper-line">${icon('shield')}<span>${view==='people'?'离职撤权只影响归属此人的凭据。持有共享网络主密钥的设备，还需单独轮换密钥。':view==='credentials'?'ACL 组是访问策略标签；签发分组后，仍需在目标设备配置对应的 ACL 放行规则。':'操作记录只保存管理事件，不保存凭据私钥或设备接入命令。'}</span></div>`;
  updateTable();
}

function footer(count) {
  return `<div class="table-footer"><span>共 ${count} 条记录</span><span>${icon('refresh')}${data.connection.last_sync ? `最近同步 ${date(data.connection.last_sync,true)}` : '尚未同步节点'}${!data.connection.connected?' · 数据待核实':''}</span></div>`;
}
function emptyState(i,title,message,action='',actionText='') {
  return `<div class="empty">${icon(i)}<h3>${title}</h3><p>${message}</p>${action?`<button class="btn primary" data-action="${action}">${actionText}</button>`:''}</div>`;
}

function updateTable() {
  if (view==='peers') return updatePeerTable();
  if (view==='monitor') return updateMonitorTable();
  const query = search.trim().toLowerCase();
  if (view==='people') {
    const people = data.people.filter(p=>(filter==='all'||p.status===filter)&&[p.name,p.team,p.contact].join(' ').toLowerCase().includes(query));
    $('#data-content').innerHTML = people.length?`<div class="table-wrap"><table class="people-table"><thead><tr><th>人员</th><th>部门</th><th>有效凭据</th><th>最近到期</th><th>状态</th><th>操作</th></tr></thead><tbody>${people.map((p,index)=>{
      const creds=liveCredentials(p.id).sort((a,b)=>a.expires_at-b.expires_at),soon=creds[0];
      return `<tr><td data-col="person"><div class="person-cell">${avatar(p,index)}<div><button class="person-name" data-action="person" data-id="${p.id}">${e(p.name)}</button><span class="subtext">${e(p.contact||'未填写联系方式')}</span></div></div></td><td data-col="team">${e(p.team||'—')}</td><td data-col="count"><span class="count-number">${creds.length}</span><span class="muted"> 个</span></td><td data-col="expiry">${soon?`<span class="${soon.expires_at-data.now<7*86400?'expiry-soon':''}" title="${date(soon.expires_at,true)}">${relativeExpiry(soon)}</span>`:'—'}</td><td data-col="status">${badge(p.status,'person')}</td><td data-col="actions"><div class="row-actions"><button class="btn text" data-action="person" data-id="${p.id}">查看</button><button class="btn small" data-action="issue" data-id="${p.id}" ${p.status!=='active'?'disabled':''}>签发凭据</button></div></td></tr>`;
    }).join('')}</tbody></table></div>${footer(people.length)}`:emptyState('users',query||filter!=='all'?'没有匹配的人员':'还没有人员记录',query||filter!=='all'?'试试其他关键词或筛选条件。':'先添加一位人员，再为其设备签发独立凭据。',query||filter!=='all'?'':'add-person','新增人员');
  } else if (view==='credentials') {
    const credentials = data.credentials.filter(c=>{
      const p=personByID(c.person_id),matches=[c.device_name,c.id,p?.name,...(c.groups||[])].join(' ').toLowerCase().includes(query);
      const state=filter==='all'||c.status===filter||(filter==='expiring'&&c.status==='active'&&c.expires_at-data.now<7*86400)||(filter==='unassigned'&&!p)||(filter==='abnormal'&&!['active','expired','revoked'].includes(c.status));
      return matches&&state;
    });
    $('#data-content').innerHTML=credentials.length?`<div class="table-wrap"><table class="credentials-table"><thead><tr><th>设备与凭据</th><th>归属人员</th><th>ACL 组</th><th>有效期</th><th>状态</th><th>操作</th></tr></thead><tbody>${credentials.map(c=>{
      const p=personByID(c.person_id);
      return `<tr><td data-col="device"><strong>${e(c.device_name)}</strong><span class="subtext mono" title="${e(c.id)}">${e(c.id.slice(0,13))}…</span></td><td data-col="owner">${p?`<button class="person-name" data-action="person" data-id="${p.id}">${e(p.name)}</button>`:'<span class="expiry-soon">待关联人员</span>'}</td><td data-col="groups">${(c.groups||[]).map(g=>`<span class="chip">${e(g)}</span>`).join('')||'<span class="muted">无分组</span>'}</td><td data-col="expiry"><span class="${c.status==='active'&&c.expires_at-data.now<7*86400?'expiry-soon':''}">${date(c.expires_at)}</span><span class="subtext">${relativeExpiry(c)}</span></td><td data-col="status">${badge(c.status)}</td><td data-col="actions"><div class="row-actions">${canLaunch(c)?`<button class="btn text" data-action="launch" data-id="${c.id}">启动命令</button>`:''}<button class="btn text" data-action="assign" data-id="${c.id}">${p?'编辑归属':'关联人员'}</button>${!['revoked','expired'].includes(c.status)?`<button class="btn text danger-text" data-action="revoke" data-id="${c.id}">撤销</button>`:''}</div></td></tr>`;
    }).join('')}</tbody></table></div>${footer(credentials.length)}`:emptyState('key',query||filter!=='all'?'没有匹配的凭据':'暂无凭据',query||filter!=='all'?'试试其他关键词或筛选条件。':'同步节点可导入已有凭据，也可以为人员签发新凭据。');
  } else if (view==='audit') {
    const events=data.audit.filter(a=>[auditNames[a.action]||a.action,a.subject,a.detail].join(' ').toLowerCase().includes(query));
    $('#data-content').innerHTML=events.length?`<div class="table-wrap"><table class="audit-table"><thead><tr><th>时间</th><th>操作</th><th>对象</th><th>详情</th></tr></thead><tbody>${events.map(a=>`<tr><td class="mono">${date(a.created_at,true)}</td><td class="activity-action">${e(auditNames[a.action]||a.action)}</td><td>${e(a.subject)}</td><td class="audit-detail">${e(a.detail||'—')}</td></tr>`).join('')}</tbody></table></div>${footer(events.length)}`:emptyState('history','暂无匹配的记录','人员变更、签发和撤销操作会记录在这里。');
  }
}

function renderSettings() {
  const c=data.config;
  $('#view-content').innerHTML=`<div class="settings-grid"><section class="panel"><div class="panel-header"><div class="panel-heading">节点连接</div>${data.connection.connected?'<span class="badge positive">连接正常</span>':'<span class="badge warning">待连接</span>'}</div><div class="settings-body">${[
    ['业务网络',c.network_name],['对等节点入口',c.peer_url],['RPC 管理地址',c.rpc_portal],['指定实例',c.instance_name||'默认单实例'],['CLI 路径',c.cli_path],['运行模式',c.demo?'本地演示（模拟操作）':'真实节点'],['最近成功同步',date(data.connection.last_sync,true)]
  ].map(([label,value])=>`<div class="settings-row"><span>${label}</span><code>${e(value)}</code></div>`).join('')}</div><div class="panel-header"><span class="muted">连接参数在服务器配置文件中修改。</span><button class="btn small" data-action="sync">${icon('refresh')}测试并同步</button></div></section><aside class="note-panel">${icon('shield')}<h3>管理的是你自己的业务网络</h3><p>此面板通过同机 CLI 管理入网凭据，不需要员工注册官方 Web 账号。</p><p>签发节点必须是持有该业务网络主密钥的正式节点，并启用安全模式和凭据持久化。单纯公共中转没有其他网络的签发权限。</p><p>每台设备使用独立凭据，离职时撤销对应凭据。ACL 放行规则仍由目标节点执行。</p></aside></div>`;
}

function renderMonitor() {
  $('#view-content').innerHTML=`<div class="monitor-refresh">
    <span id="monitor-sync-status" role="status"></span>
    <button class="btn small" data-action="auto-refresh" aria-pressed="${autoRefresh}"></button>
  </div><div id="monitor-error" role="alert"></div>
  <div id="monitor-metrics"></div><div id="monitor-attention" class="monitor-attention" aria-label="待跟进事项"></div>
  <section class="panel monitor-panel"><div class="panel-header"><h2 class="panel-heading">人员授权状态<span class="total-count" id="monitor-total"></span></h2><span class="muted">按凭据 ID 关联</span></div>
    <div class="filterbar"><div class="search">${icon('search')}<input id="search" aria-label="搜索人员、部门、设备或凭据" placeholder="搜索人员、部门、设备或凭据" value="${e(search)}" autocomplete="off"></div>
      <div class="filter-controls"><select id="status-filter" aria-label="筛选监控状态">${[
        ['all','全部人员'],['attention','需要处理'],['expiring','7 天内到期'],['no_access','在职 · 无有效凭据'],['active','在职'],['revoking','撤权中'],['offboarded','已离职']
      ].map(([value,label])=>`<option value="${value}" ${filter===value?'selected':''}>${label}</option>`).join('')}</select></div>
    </div><div id="monitor-table"></div>
  </section><div class="helper-line">${icon('info')}<span>统计范围为本面板登记的人员及节点已同步的凭据。凭据有效表示拥有入网授权；EasyTier 2.6.4 当前接口无法按凭据 ID 关联在线设备，设备在线状态尚未采集。</span></div>`;
  updateMonitor();
}

function updateMonitorStatus() {
  const node=$('#monitor-sync-status');
  if (!node || !data) return;
  const conn=data.connection, stale=conn.last_sync && Date.now()/1000-conn.last_sync>90;
  node.textContent=`${synchronizing?'正在同步节点…':refreshError||!conn.connected?'同步未成功':stale?'快照超过 90 秒，请同步核实':'节点凭据已同步'} · ${conn.last_sync?`最近成功 ${date(conn.last_sync,true)}`:'尚无成功同步记录'}`;
  node.className=refreshError||!conn.connected||stale?'expiry-soon':'muted';
  const button=$('[data-action="auto-refresh"]');
  button.textContent=autoRefresh?'自动同步：30 秒 · 暂停':'自动同步已暂停 · 开启';
  button.setAttribute('aria-pressed',String(autoRefresh));
  const connectionNode=$('#node-connection'), fresh=conn.connected&&!refreshError&&!stale&&conn.last_sync;
  connectionNode.className=`badge ${fresh?'positive':'warning'}`;
  connectionNode.innerHTML=`<span class="dot"></span>${fresh?'节点已连接':'待同步核实'}`;
  const error=$('#monitor-error');
  error.innerHTML=refreshError?`<div class="banner error">${icon('info')}<p>本次同步失败：${e(refreshError)}。保留现有快照，可点击“同步节点”重试。</p></div>`:'';
}

function updateMonitor() {
  if (!data || view!=='monitor') return;
  const m=data.monitor;
  $('#view-banners').innerHTML=banners();
  $('#monitor-metrics').innerHTML=`<section class="stats" aria-label="人员监控概况">${[
    ['登记人员',m.people_total,'users',`在职 ${m.active_people} · 撤权中 ${m.revoking_people} · 离职 ${m.offboarded_people}`],
    ['有效凭据',m.active_credentials,'key','包含待关联人员的有效凭据'],
    ['即将到期',m.expiring_credentials,'history','未来 7 天内到期（含第 7 天）'],
    ['需处理人员',m.attention_people,'shield','撤权未完成、凭据异常或离职后仍有效']
  ].map(([label,value,i,note],index)=>`<div class="stat"><div class="stat-top">${label}${icon(i)}</div><div class="stat-value ${index>1&&value?'amber':''}">${value}</div><div class="stat-note">${note}</div></div>`).join('')}</section>`;
  $('#monitor-attention').innerHTML=[
    ['no_access','users','在职但无有效凭据',m.no_access_people,'人'],
    ['unassigned','key','待关联的可用或异常凭据',m.unassigned_credentials,'条'],
    ['abnormal','info','需要核实的异常凭据',m.abnormal_credentials,'条']
  ].map(([target,i,label,value,unit])=>`<button class="monitor-task" data-action="monitor-filter" data-target="${target}">${icon(i)}<span>${label}</span><strong>${value}<small>${unit}</small></strong>${icon('arrow')}</button>`).join('');
  $('#monitor-total').textContent=m.people_total;
  updateMonitorTable();
  updateMonitorStatus();
}

function updateMonitorTable() {
  const query=search.trim().toLowerCase();
  const credentials=new Map();
  for (const c of data.credentials) {
    const list=credentials.get(c.person_id)||[]; list.push(c); credentials.set(c.person_id,list);
  }
  const people=new Map(data.people.map(p=>[p.id,p]));
  const rows=data.monitor.people.filter(row=>{
    const p=people.get(row.person_id),creds=credentials.get(p.id)||[];
    const matches=[p.name,p.team,p.contact,...creds.flatMap(c=>[c.device_name,c.id])].join(' ').toLowerCase().includes(query);
    const state=filter==='all'||p.status===filter||(filter==='attention'&&row.needs_attention)||
      (filter==='expiring'&&row.expiring>0)||(filter==='no_access'&&p.status==='active'&&row.active===0);
    return matches&&state;
  });
  $('#monitor-table').innerHTML=rows.length?`<div class="table-wrap"><table class="monitor-table"><thead><tr><th>人员 / 部门</th><th>人员状态</th><th>凭据授权</th><th>最近到期</th><th>需跟进</th><th>操作</th></tr></thead><tbody>${rows.map((row,index)=>{
    const p=people.get(row.person_id),notes=[];
    if(p.status==='revoking') notes.push('离职撤权未完成');
    if(p.status!=='active'&&row.active>0) notes.push(`仍有 ${row.active} 条有效凭据`);
    if(row.abnormal) notes.push(`${row.abnormal} 条凭据异常`);
    if(row.expiring) notes.push(`${row.expiring} 条即将到期`);
    if(p.status==='active'&&!row.active) notes.push('暂无有效凭据');
    return `<tr><td data-col="person"><div class="person-cell">${avatar(p,index)}<div><button class="person-name" data-action="person" data-id="${p.id}">${e(p.name)}</button><span class="subtext">${e(p.team||'未填写部门')}</span></div></div></td>
      <td data-col="status">${badge(p.status,'person')}</td>
      <td data-col="access"><span class="badge ${row.active?'positive':''}">${row.active?'授权有效':'无有效凭据'}</span><span class="subtext">有效 ${row.active} / 共 ${row.total}${row.expired?` · 到期 ${row.expired}`:''}</span></td>
      <td data-col="expiry"><span class="${row.expiring?'expiry-soon':''}">${date(row.next_expiry)}</span>${row.next_expiry?`<span class="subtext">${relativeExpiry({expires_at:row.next_expiry,status:'active'})}</span>`:''}</td>
      <td data-col="attention" class="${row.needs_attention?'danger-text':notes.length?'expiry-soon':'muted'}">${notes.map(e).join('<br>')||'—'}</td>
      <td data-col="actions"><button class="btn small" data-action="person" data-id="${p.id}">查看详情</button></td></tr>`;
  }).join('')}</tbody></table></div>${footer(rows.length)}`:emptyState('users',data.people.length?'没有匹配的人员':'还没有人员记录',data.people.length?'试试其他关键词或筛选条件。':'先在人员管理中新增人员，再关联或签发凭据。',data.people.length?'':'add-person','新增人员');
}

function scheduleMonitor() {
  clearTimeout(monitorTimer);
  clearInterval(monitorStatusTimer);
  if (!session.authenticated || !data || !['monitor','peers'].includes(view) || document.hidden) return;
  const updateStatus=view==='peers'?updatePeerStatus:updateMonitorStatus;
  updateStatus();
  monitorStatusTimer=setInterval(updateStatus,15000);
  if (!autoRefresh) return;
  monitorTimer=setTimeout(async()=>{
    // Avoid replacing a focused table action or interrupting a personnel workflow.
    const interacting=modal.open||drawer.open||document.activeElement?.closest('#monitor-table, #monitor-attention');
    if (interacting || synchronizing || peerSynchronizing) { scheduleMonitor(); return; }
    if(view==='peers') await synchronizePeers(true);else await synchronize(true);
  },30000);
}

async function reload(background = false) {
  const snapshot=await api('/api/state');
  if (!session.authenticated) return;
  data=snapshot;
  data.people ||= []; data.credentials ||= []; data.audit ||= [];
  if (background) {
    if (view==='monitor') updateMonitor();
  } else {
    shell();
    if (drawer.open && selectedPerson) renderDrawer(selectedPerson);
  }
}
async function synchronize(background = false) {
  if (synchronizing) return;
  synchronizing=true; refreshError=''; if(data && !background) shell();
  if (background && view==='monitor') updateMonitorStatus();
  try { await api('/api/sync',{}); await reload(background); if(!background && data) toast('节点凭据已同步'); }
  catch(error) {
    refreshError=error.message;
    if (session.authenticated) { try { await reload(background); } catch {} }
    if (!background) toast(error.message,true);
  }
  finally {
    synchronizing=false;
    if(data) { if(background) { if(view==='monitor') updateMonitor(); } else shell(); }
    scheduleMonitor();
  }
}

function openModal(title,content,submitLabel=null,onSubmit=null,danger=false) {
  if (modal.open) modal.close();
  modalSubmit=onSubmit;
  modal.innerHTML=`<div class="dialog-header"><h2 id="modal-title">${title}</h2><button class="icon-btn" data-action="close-modal" aria-label="关闭弹窗" title="关闭">${icon('close')}</button></div>
    ${submitLabel?'<form class="dialog-form" data-form="modal">':''}<div class="dialog-body"><div class="form-error" role="alert"></div>${content}</div>
    <div class="dialog-footer"><button class="btn" type="button" data-action="close-modal">${submitLabel?'取消':'关闭'}</button>${submitLabel?`<button class="btn ${danger?'danger':'primary'}" type="submit">${submitLabel}</button>`:''}</div>${submitLabel?'</form>':''}`;
  modal.showModal();
}

function openPersonForm(id) {
  const p=id?personByID(id):{};
  openModal(id?'编辑人员':'新增人员',`
    <div class="field-row"><div class="field"><label for="person-name">姓名 <span class="muted">*</span></label><input id="person-name" name="name" value="${e(p.name)}" maxlength="80" required autocomplete="off"></div><div class="field"><label for="person-team">部门 / 团队</label><input id="person-team" name="team" value="${e(p.team)}" maxlength="80" placeholder="例如：研发"></div></div>
    <div class="field"><label for="person-contact">联系方式</label><input id="person-contact" name="contact" value="${e(p.contact)}" maxlength="150" placeholder="邮箱、工号或其他联系方式"></div>
    <div class="field"><label for="person-notes">备注</label><textarea id="person-notes" name="notes" maxlength="1000" placeholder="可填写设备用途或其他管理说明">${e(p.notes)}</textarea></div>
    <p class="form-note">人员档案用于管理设备归属。新建人员不会自动获得入网权限。</p>`,id?'保存修改':'创建人员',async form=>{
      await api(id?`/api/people/${id}`:'/api/people',Object.fromEntries(new FormData(form)));
      modal.close();await reload();toast(id?'人员信息已更新':'人员已添加，可为其设备签发凭据');
    });
}

function peopleOptions(selected='') {
  return data.people.filter(p=>p.status==='active').map(p=>`<option value="${p.id}" ${selected===p.id?'selected':''}>${e(p.name)}${p.team?' · '+e(p.team):''}</option>`).join('');
}

function launchFields(options={}) {
  const fixed=options.ip_mode==='static',vpn=!!options.vpn;
  return `<div class="launch-options"><h3>设备启动方式</h3>
    <div class="field"><label for="launch-ip-mode">虚拟 IP 分配</label><select id="launch-ip-mode" name="ip_mode"><option value="dhcp" ${!fixed?'selected':''}>自动分配（DHCP）</option><option value="static" ${fixed?'selected':''}>固定虚拟 IP</option></select></div>
    <div class="field" id="launch-ip-field" ${!fixed?'hidden':''}><label for="launch-ipv4">设备虚拟 IPv4</label><input id="launch-ipv4" name="ipv4" value="${e(options.ipv4||'')}" placeholder="例如：10.10.10.20/24" maxlength="32" ${fixed?'required':'disabled'}><small>固定地址应与业务网络一致，避免和现有设备冲突。</small></div>
    <label class="checkbox-field"><input id="launch-vpn" type="checkbox" name="vpn" ${vpn?'checked':''}><span>通过日本节点上网（VPN）<small>开启后接管 IPv4 上网流量；IPv6 需在设备上另行处理。</small></span></label>
    <div class="field" id="launch-exit-field" ${!vpn?'hidden':''}><label for="launch-exit">日本出口节点的虚拟 IPv4</label><input id="launch-exit" name="exit_node" value="${e(options.exit_node||'')}" placeholder="例如：10.10.10.1" maxlength="32" ${vpn?'required':'disabled'}><small>填写日本节点在 EasyTier 网络中的 IP；公网 IP 23.27.52.254 不是这里的出口地址。</small></div>
    <p class="form-note">这些选项生成设备启动参数，不会锁定凭据允许使用的 IP，也不会远程更改已运行设备。</p></div>`;
}

function launchPayload(form) {
  const values=new FormData(form);
  return {ip_mode:values.get('ip_mode'),ipv4:String(values.get('ipv4')||'').trim(),vpn:values.has('vpn'),exit_node:String(values.get('exit_node')||'').trim()};
}

function canLaunch(c) {
  const p=personByID(c.person_id);
  return c.status==='active'&&(!c.person_id||p?.status==='active');
}

function openLaunch(id) {
  const c=credentialByID(id);
  const choices=data.credentials.filter(canLaunch);
  openModal('生成设备启动命令',`<div class="field"><label for="launch-credential">选择已有凭据</label><select id="launch-credential" name="credential_id" required>${choices.map(v=>`<option value="${v.id}" ${v.id===id?'selected':''}>${e(v.device_name)} · ${e(personByID(v.person_id)?.name||'待关联人员')} · ${e(v.id.slice(0,8))}</option>`).join('')}</select></div>
    <div id="launch-fields">${launchFields(c.launch)}</div>
    <div class="field"><label for="launch-secret">原凭据私钥（可选）</label><input id="launch-secret" type="password" autocomplete="off" spellcheck="false" maxlength="300" placeholder="留空生成不含私钥的命令模板"><small>此输入仅在当前浏览器内生成命令，不上传、不保存。请确保私钥属于选中凭据。</small></div>
    <p class="form-note">面板没有保存之前签发的私钥。找不到原私钥时，需要重新签发凭据。</p>`, '保存设置并生成命令', async form=>{
      const credentialID=$('#launch-credential',form).value;
      const secret=$('#launch-secret',form).value.trim();
      if (secret && !(data.config.demo&&secret.startsWith('DEMO_ONLY_'))) {
        let length=0;try {length=atob(secret).length;} catch {}
        if(length!==32) throw new Error('原凭据应是 Base64 编码的 32 字节私钥，请核对复制内容。');
      }
      const result=await api(`/api/credentials/${credentialID}/launch`,launchPayload(form));
      if(secret) {
        result.join_command=result.join_command.replace('"${EASYTIER_CREDENTIAL:?请先设置原凭据私钥}"',`'${secret.replace(/'/g,`'"'"'`)}'`);
        result.toml_command=result.toml_command.replace('"${EASYTIER_CREDENTIAL:?请先设置原凭据私钥}"',`'${secret.replace(/'/g,`'"'"'`)}'`);
        if (!secret.startsWith('DEMO_ONLY_')) {
          const publicKey=window.easytierCredentialPublicKey(secret);
          result.toml_config=result.toml_config.replace('"REPLACE_WITH_CREDENTIAL_PRIVATE_KEY"',JSON.stringify(secret)).replace('"REPLACE_WITH_MATCHING_PUBLIC_KEY"',JSON.stringify(publicKey));
          result.toml_config=result.toml_config.replace('# 模板：必须替换下面的私钥和匹配公钥；演示凭据不能连接真实网络。\n','');
        }
        result.credential_secret=secret;
      }
      result.existing=true;result.template=!secret;
      modal.close();await reload();issued=result;showIssued();
    });
}

function openIssue(pid='') {
  if (!data.people.some(p=>p.status==='active')) { toast('请先添加在职人员，再签发凭据',true);return; }
  openModal('签发设备凭据',`
    <div class="field"><label for="issue-person">归属人员 <span class="muted">*</span></label><select id="issue-person" name="person_id" required><option value="">选择一位人员</option>${peopleOptions(pid)}</select></div>
    <div class="field"><label for="issue-device">设备名称 <span class="muted">*</span></label><input id="issue-device" name="device_name" required maxlength="100" placeholder="例如：张三的 MacBook Pro" autocomplete="off"></div>
    <div class="field-row"><div class="field"><label for="issue-ttl">有效期</label><select id="issue-ttl" name="ttl_days"><option value="1">1 天</option><option value="7">7 天</option><option value="30" selected>30 天</option><option value="90">90 天</option><option value="365">365 天</option></select></div><div class="field"><label for="issue-groups">ACL 组</label><input id="issue-groups" name="groups" value="staff" placeholder="staff, developer" maxlength="500"><small>多个组以英文逗号分隔。</small></div></div>
    ${launchFields()}
    <details class="advanced"><summary>高级权限</summary><label class="checkbox-field"><input type="checkbox" name="allow_relay"><span>允许此设备参与中转<small>默认关闭，只影响这台设备转发其他节点流量。</small></span></label><label class="checkbox-field"><input type="checkbox" name="reusable"><span>允许多设备同时使用此凭据<small>建议保持关闭，为每台设备独立签发。</small></span></label><div class="field"><label for="issue-cidrs">允许宣告的子网代理网段</label><input id="issue-cidrs" name="cidrs" placeholder="例如：192.168.10.0/24"><small>留空则不允许宣告子网代理；多个网段以英文逗号分隔。</small></div></details>
    <p class="form-note">签发后会显示设备接入命令。组名只附加身份标签，目标节点仍需配置相应 ACL。</p>`, '签发凭据', async form=>{
      const values=new FormData(form);
      const payload={person_id:values.get('person_id'),device_name:values.get('device_name'),ttl_days:Number(values.get('ttl_days')),groups:String(values.get('groups')).split(',').map(s=>s.trim()).filter(Boolean),allow_relay:values.has('allow_relay'),reusable:values.has('reusable'),allowed_proxy_cidrs:String(values.get('cidrs')||'').split(',').map(s=>s.trim()).filter(Boolean)};
      payload.launch=launchPayload(form);
      const result=await api('/api/credentials',payload);
      modal.close();await reload();issued=result;showIssued();
    });
}

function showIssued() {
  if(!issued) return;
  const c=issued;
  openModal(c.existing?'启动命令已生成':'凭据已签发',`${c.demo?'<div class="banner demo">这是演示凭据，无法连接真实网络。</div>':''}
    <div class="result-heading">${icon('check')}<strong>${c.person_name?`${e(c.person_name)} · `:''}${e(c.device_name)}</strong></div><p class="form-note">有效至 ${date(c.expires_at,true)}。${c.template?'当前为不含私钥的模板；两种方式均需填入原凭据才能连接。':'请现在复制或下载接入配置，通过可信渠道交给设备使用者。本面板不会保存或再次显示私钥。'}</p>
    <div class="launch-formats" role="group" aria-label="选择启动方式"><button class="btn small" data-action="launch-format" data-format="command" aria-pressed="true" aria-controls="launch-command-panel">纯命令行</button><button class="btn small" data-action="launch-format" data-format="toml" aria-pressed="false" aria-controls="launch-toml-panel">TOML 配置</button></div>
    <section id="launch-command-panel" aria-label="纯命令行启动"><span class="field-label">设备接入命令（Bash / Zsh）</span><pre class="secret-box" id="join-command">${e(c.join_command)}</pre><div class="secret-actions"><button class="btn small" data-action="copy-command">${icon('copy')}复制接入命令</button>${c.credential_secret?`<button class="btn small" data-action="copy-secret">${icon('key')}仅复制凭据</button>`:''}</div>${c.template?'<p class="form-note launch-help">运行前先设置终端变量 EASYTIER_CREDENTIAL 为原凭据私钥。</p>':''}</section>
    <section id="launch-toml-panel" aria-label="TOML 配置启动" hidden><span class="field-label">保存为 client.toml</span><pre class="secret-box toml-box" id="join-toml">${e(c.toml_config)}</pre><div class="secret-actions"><button class="btn small" data-action="copy-toml">${icon('copy')}复制 TOML</button><button class="btn small" data-action="download-toml">下载 TOML</button></div>
      ${c.toml_config.includes('REPLACE_WITH_CREDENTIAL_PRIVATE_KEY')?'<p class="form-note launch-help">这是配置模板，需填入原凭据私钥和匹配的公钥。可在上一页粘贴真实凭据后自动生成两者；演示凭据无法使用。</p>':''}
      <span class="field-label launch-help">配套启动命令（Bash / Zsh）</span><pre class="secret-box" id="toml-command">${e(c.toml_command)}</pre><button class="btn small" data-action="copy-toml-command">${icon('copy')}复制 TOML 启动命令</button>
      <p class="form-note launch-help">EasyTier 2.6.4 必须通过 --credential 指定凭据身份，仅运行 -c client.toml 无法完成凭据认证。请使用配套命令，不额外传入 --secure-mode true。含私钥的文件应仅允许自己读取（Linux/macOS 可执行 chmod 600 client.toml）。</p></section>
    ${c.launch?.vpn?`<div class="launch-vpn-note"><h3>VPN 出口准备</h3><p>日本节点必须有虚拟 IP <code>${e(c.launch.exit_node)}</code>，并在 core 的 <code>[flags]</code> 中设置 <code>enable_exit_node = true</code>。</p><p>命令会接管 IPv4 路由，需要管理员权限和可用 TUN。若要确保全部上网流量经日本节点，需在客户端停用未经过 VPN 的 IPv6，并核实 DNS 与公网出口。命令生成后，设备仍需实际运行才能生效。</p></div>`:''}
    <div class="helper-line">${icon('info')}<span>在设备原有 EasyTier 配置中使用此凭据，接入时无需分发网络主密钥。</span></div>`);
}

function renderDrawer(id) {
  const p=personByID(id);if(!p) return;
  selectedPerson=id;
  const creds=data.credentials.filter(c=>c.person_id===id);
  drawer.innerHTML=`<div class="dialog-header"><div><span class="eyebrow">PERSON DETAILS</span><h2 id="drawer-title">人员与设备</h2></div><button class="icon-btn" data-action="close-drawer" title="关闭详情" aria-label="关闭详情">${icon('close')}</button></div><div class="dialog-body">
    <div class="detail-person">${avatar(p)}<div><h2>${e(p.name)}</h2><span class="subtext">${e(p.team||'未填写部门')}</span></div><div class="actions">${badge(p.status,'person')}</div></div>
    <dl class="detail-grid"><dt>联系方式</dt><dd>${e(p.contact||'—')}</dd><dt>加入时间</dt><dd>${date(p.created_at)}</dd><dt>备注</dt><dd>${e(p.notes||'—')}</dd></dl>
    <button class="btn small" data-action="edit-person" data-id="${id}">编辑人员信息</button>
    <div class="section-line"><h3>设备凭据 <span class="total-count">${creds.length}</span></h3><button class="btn small" data-action="issue" data-id="${id}" ${p.status!=='active'?'disabled':''}>${icon('plus')}签发凭据</button></div>
    ${creds.length?creds.map(c=>`<div class="device-item"><div class="device-item-head"><span class="device-item-title">${icon('monitor')}${e(c.device_name)}</span>${badge(c.status)}</div><div class="mono">${e(c.id)}</div><div class="device-item-meta"><span>${(c.groups||[]).map(g=>`<span class="chip">${e(g)}</span>`).join('')} ${relativeExpiry(c)}</span><div class="actions">${canLaunch(c)?`<button class="btn text" data-action="launch" data-id="${c.id}">启动命令</button>`:''}${!['revoked','expired'].includes(c.status)?`<button class="btn text danger-text" data-action="revoke" data-id="${c.id}">撤销</button>`:''}</div></div></div>`).join(''):'<p class="form-note">暂未关联设备凭据。</p>'}
    <section class="danger-zone"><h3>${p.status==='offboarded'?'恢复人员':'离职与撤权'}</h3><p>${p.status==='offboarded'?'恢复后可重新签发凭据，之前撤销的凭据不会恢复。':'停止为此人签发新凭据，并逐个撤销其名下仍有效的设备凭据。持有共享主密钥的设备还需另行处理。'}</p><button class="btn ${p.status==='offboarded'?'':'small'}" data-action="${p.status==='offboarded'?'restore':'offboard'}" data-id="${id}">${p.status==='offboarded'?'恢复为在职人员':p.status==='revoking'?'重试未完成的撤权':'办理离职并撤权'}</button></section>
  </div>`;
}
function openPerson(id) { renderDrawer(id);if(!drawer.open) drawer.showModal(); }

function openRevoke(id) {
  const c=credentialByID(id);if(!c) return;
  openModal('撤销设备凭据',`<div class="danger-copy">撤销 <strong>${e(c.device_name)}</strong> 的入网凭据。<br>节点确认撤销后，此凭据将失效，后续需要重新签发才能接入。</div><p class="mono muted">${e(c.id)}</p>`, '确认撤销', async()=>{
    await api(`/api/credentials/${id}/revoke`,{});modal.close();await reload();toast('节点已确认凭据撤销');
  },true);
}

function openOffboard(id) {
  const p=personByID(id),count=liveCredentials(id).length;
  openModal('办理离职并撤权',`<div class="danger-copy">将停用 <strong>${e(p.name)}</strong>，并核实、撤销其名下的有效凭据（当前快照 ${count} 条）。部分失败时会保留“撤权中”状态，可重试。</div><p class="form-note">仅处理已关联到此人的凭据。共享网络主密钥、其他账号或未关联设备需单独处理。</p><div class="field"><label for="confirm-name">输入「${e(p.name)}」确认</label><input id="confirm-name" name="confirm_name" required autocomplete="off" placeholder="${e(p.name)}"></div>`, '停用并撤销凭据', async form=>{
    if(new FormData(form).get('confirm_name')!==p.name) throw new Error('输入的姓名不一致，请核对。');
    const result=await api(`/api/people/${id}/offboard`,{});
    modal.close();await reload();
    toast(result.complete?`已停用 ${p.name}，确认撤销 ${result.revoked.length} 条凭据`:`${result.failures.length} 条凭据撤销失败，人员保持撤权中，请重试。`,!result.complete);
  },true);
}

const credentialScope=()=>[data.config.rpc_portal,data.config.instance_name||'',data.config.network_name].join('\n');

function openAssign(id) {
  const c=credentialByID(id);
  if(!data.people.some(p=>p.status==='active')) { toast('请先添加在职人员',true);return; }
  openModal('编辑凭据归属',`<div class="field"><label for="assign-person">归属人员</label><select id="assign-person" name="person_id" required><option value="">选择一位人员</option>${peopleOptions(c.person_id)}</select></div><div class="field"><label for="assign-device">设备名称</label><input id="assign-device" name="device_name" value="${e(c.device_name)}" required maxlength="100"></div><details class="advanced"><summary>节点身份公钥</summary><div class="field"><label for="assign-public-key">凭据公钥（可选）</label><input id="assign-public-key" name="public_key" class="mono" maxlength="100" value="${e(c.public_key_scope===credentialScope()?c.public_key||'':'')}" ${c.public_key_scope===credentialScope()&&c.public_key?'readonly':''} placeholder="client.toml 中的 local_public_key"><small>新签发凭据自动登记。旧凭据需核对后补录，已登记公钥不能覆盖。</small></div>${c.public_key_scope!==credentialScope()||!c.public_key?'<div class="field"><label for="assign-key-secret">用原凭据在本机提取（可选）</label><input id="assign-key-secret" type="password" autocomplete="off" maxlength="100"><small>私钥不上传，只发送提取后的公钥。</small></div><button class="btn small" type="button" data-action="derive-binding-key">提取公钥</button>':''}</details><p class="form-note">修改归属只更新管理记录，不改变节点上的凭据权限和有效期。补录公钥由管理员确认对应关系。</p>`, '保存归属', async form=>{
    await api(`/api/credentials/${id}/assign`,Object.fromEntries(new FormData(form)));modal.close();if(view==='peers') peerData=await api('/api/peers');await reload();toast('凭据归属已更新');
  });
}

async function copy(value) {
  if(navigator.clipboard && window.isSecureContext) await navigator.clipboard.writeText(value);
  else {
    const input=document.createElement('textarea');input.value=value;input.setAttribute('readonly','');modal.append(input);input.select();
    const success=document.execCommand('copy');input.remove();if(!success) throw new Error('浏览器未允许复制，请手动选中接入命令复制。');
  }
  toast('已复制');
}

document.addEventListener('click',async event=>{
  const button=event.target.closest('[data-action]');if(!button||button.disabled) return;
  const {action,id}=button.dataset;
  try {
    if(action==='nav') { view=button.dataset.view;search='';filter='all';shell();if(view==='peers') await synchronizePeers(); }
    else if(action==='bind-peer') openPeerBind(button.dataset.id);
    else if(action==='peer-person') {await reload();openPerson(button.dataset.id);}
    else if(action==='derive-binding-key') {const key=window.easytierCredentialPublicKey($('#assign-key-secret',modal).value.trim());$('#assign-public-key',modal).value=key;$('#assign-key-secret',modal).value='';toast('已提取公钥，保存时仅发送公钥');}
    else if(action==='sync-peers') await synchronizePeers();
    else if(action==='sync') await synchronize();
    else if(action==='auto-refresh') { autoRefresh=!autoRefresh;if(view==='peers') updatePeerStatus();else updateMonitorStatus();scheduleMonitor(); }
    else if(action==='monitor-filter') {
      const target=button.dataset.target;
      search='';filter=target;
      if(target==='unassigned'||target==='abnormal') { view='credentials';shell(); }
      else { renderView();$('#status-filter').focus(); }
    }
    else if(action==='add-person') openPersonForm();
    else if(action==='edit-person') openPersonForm(id);
    else if(action==='person') openPerson(id);
    else if(action==='issue') openIssue(id||'');
    else if(action==='launch') openLaunch(id);
    else if(action==='assign') openAssign(id);
    else if(action==='revoke') openRevoke(id);
    else if(action==='offboard') openOffboard(id);
    else if(action==='close-modal') modal.close();
    else if(action==='close-drawer') { drawer.close();selectedPerson=null; }
    else if(action==='unassigned') { filter='unassigned';renderView(); }
    else if(action==='copy-command') await copy(issued.join_command);
    else if(action==='copy-secret') await copy(issued.credential_secret);
    else if(action==='launch-format') {
      const toml=button.dataset.format==='toml';
      $('#launch-command-panel').hidden=toml;$('#launch-toml-panel').hidden=!toml;
      document.querySelectorAll('[data-action="launch-format"]').forEach(node=>node.setAttribute('aria-pressed',String(node===button)));
    }
    else if(action==='copy-toml') await copy(issued.toml_config);
    else if(action==='copy-toml-command') await copy(issued.toml_command);
    else if(action==='download-toml') {
      const url=URL.createObjectURL(new Blob([issued.toml_config],{type:'application/toml;charset=utf-8'}));
      const link=document.createElement('a');link.href=url;link.download=issued.toml_config.includes('REPLACE_WITH_CREDENTIAL_PRIVATE_KEY')?'client.template.toml':'client.toml';document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);
    }
    else if(action==='restore') { button.disabled=true;await api(`/api/people/${id}/restore`,{});await reload();toast('人员已恢复，历史凭据不会恢复'); }
    else if(action==='logout') { await api('/api/logout',{});session={demo:session.demo};data=null;issued=null;drawer.close();modal.close();renderLogin(); }
  } catch(error) { toast(error.message,true);button.disabled=false; }
});

document.addEventListener('submit',async event=>{
  const form=event.target;if(!form.matches('[data-form]')) return;
  event.preventDefault();
  const errorBox=$('.form-error',form),button=$('button[type="submit"]',form),oldLabel=button.textContent;
  errorBox.textContent='';button.disabled=true;button.textContent='处理中…';
  try {
    if(form.dataset.form==='login') {
      session={...session,...await api('/api/login',Object.fromEntries(new FormData(form))),authenticated:true};
      await reload();synchronize();
    } else if(modalSubmit) await modalSubmit(form);
  } catch(error) { errorBox.textContent=error.message; }
  finally { button.disabled=false;button.textContent=oldLabel; }
});
document.addEventListener('input',event=>{ if(event.target.id==='search') { search=event.target.value;updateTable(); } });
document.addEventListener('change',event=>{
  if(event.target.id==='status-filter') { filter=event.target.value;updateTable(); }
  if(event.target.id==='launch-ip-mode') {
    const fixed=event.target.value==='static';$('#launch-ip-field').hidden=!fixed;$('#launch-ipv4').disabled=!fixed;$('#launch-ipv4').required=fixed;
  }
  if(event.target.id==='launch-vpn') {
    const vpn=event.target.checked;$('#launch-exit-field').hidden=!vpn;$('#launch-exit').disabled=!vpn;$('#launch-exit').required=vpn;
  }
  if(event.target.id==='launch-credential') {
    $('#launch-fields').innerHTML=launchFields(credentialByID(event.target.value).launch);$('#launch-secret').value='';
  }
});
modal.addEventListener('close',()=>{ if(modal.open) return;modalSubmit=null;issued=null;modal.innerHTML=''; });
drawer.addEventListener('close',()=>{ selectedPerson=null; });
document.addEventListener('visibilitychange',scheduleMonitor);

(async()=>{
  try { session=await api('/api/session');if(session.authenticated) { await reload();if(!data.connection.last_sync) synchronize(); } else renderLogin(); }
  catch(error) { app.innerHTML=`<main id="main-content" class="login-layout"><div class="login-card"><h1>连接暂时不可用</h1><p class="muted">${e(error.message)}</p><button class="btn" data-action="reload-page">刷新页面</button></div></main>`;const button=$('[data-action="reload-page"]');button.addEventListener('click',()=>location.reload()); }
})();
