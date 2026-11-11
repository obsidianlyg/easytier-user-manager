'use strict';

let peerData = null, peerSynchronizing = false, peerRequestError = '';
const peerStates = {direct:['直连','positive'],relay:['经中转','positive']};
const visiblePeers=()=>peerRequestError||!peerData?.connection?.connected?[]:(peerData.items||[]).filter(p=>['direct','relay'].includes(p.status));

function peerSyncButton() {
  return `<button class="btn secondary-label ${peerSynchronizing?'spin':''}" data-action="sync-peers" title="同步节点" aria-label="同步节点" ${peerSynchronizing?'disabled':''}>${icon('refresh')}${peerSynchronizing?'同步中…':'同步节点'}</button>`;
}

function renderPeers() {
  $('#view-content').innerHTML=`<div class="monitor-refresh"><span id="peer-sync-status" role="status"></span><button class="btn small" data-action="auto-refresh" aria-pressed="${autoRefresh}"></button></div>
    <div id="peer-error" role="alert"></div><div id="peer-metrics"></div>
    <section class="panel"><div class="panel-header"><h2 class="panel-heading">Peer 节点<span class="total-count" id="peer-total">0</span></h2><span class="muted mono">${e(data.config.network_name)}</span></div>
      <div class="filterbar"><div class="search">${icon('search')}<input id="search" aria-label="搜索节点、人员、IP 或 Peer ID" placeholder="搜索节点、人员、IP 或 Peer ID" value="${e(search)}" autocomplete="off"></div>
      <div class="filter-controls"><select id="status-filter" aria-label="筛选节点状态">${[['all','当前连接'],['direct','直连'],['relay','经中转'],['assigned','已关联人员'],['unassigned','未关联人员']].map(([value,label])=>`<option value="${value}" ${filter===value?'selected':''}>${label}</option>`).join('')}</select></div></div>
      <div id="peer-table"></div></section>
    <div class="helper-line">${icon('info')}<span>每 30 秒查询当前连接和中转路由；不保留已消失节点，IP 为虚拟 IP。直连凭据设备按已认证公钥匹配人员；旧凭据可由管理员核对后补录，标为“管理员补录”。经中转节点暂无本地握手身份，不自动关联。收发流量为连接累计值，不是人员用量。</span></div>`;
  updatePeers();
}

function updatePeerStatus() {
  const node=$('#peer-sync-status');if(!node) return;
  const conn=peerData?.connection,stale=conn?.last_sync && Date.now()/1000-conn.last_sync>90;
  const fresh=conn?.connected&&!peerRequestError&&!stale;
  node.className=fresh?'muted':'expiry-soon';
  node.textContent=`${peerSynchronizing?'正在同步 peer…':peerRequestError?'本次同步失败':!conn?.last_sync?'尚未同步 peer':!conn.connected?'快照待核实':stale?'快照超过 90 秒，请同步核实':'Peer 节点已同步'} · ${conn?.last_sync?`最近成功 ${date(conn.last_sync,true)}`:'等待获取节点列表'}`;
  const button=$('[data-action="auto-refresh"]');
  button.textContent=autoRefresh?'自动同步：30 秒 · 暂停':'自动同步已暂停 · 开启';button.setAttribute('aria-pressed',String(autoRefresh));
  const top=$('#node-connection');top.className=`badge ${fresh?'positive':'warning'}`;top.innerHTML=`<span class="dot"></span>${fresh?'Peer 已同步':'Peer 待核实'}`;
  const error=peerRequestError||(!conn?.connected&&conn?.error);
  $('#peer-error').innerHTML=error?`<div class="banner error">${icon('info')}<p>${e(error)}${conn?.last_sync?' 当前连接无法核实，暂不展示上次节点列表。':' 可点击“同步节点”重试。'}</p></div>`:'';
  const sync=$('[data-action="sync-peers"]');if(sync) {sync.disabled=peerSynchronizing;sync.classList.toggle('spin',peerSynchronizing);sync.innerHTML=`${icon('refresh')}${peerSynchronizing?'同步中…':'同步节点'}`;}
}

function updatePeers() {
  if(view!=='peers'||!data) return;
  const items=visiblePeers();
  const count=status=>items.filter(p=>p.status===status).length;
  $('#peer-metrics').innerHTML=`<section class="stats" aria-label="Peer 节点概况">${[
    ['当前连接节点',items.length,'monitor','最近成功同步的远端节点'],
    ['直连节点',count('direct'),'monitor','与管理节点直接连接'],
    ['经中转节点',count('relay'),'refresh','通过中转路径发现'],
    ['已关联节点',items.filter(p=>p.person_id).length,'users','已登记人员归属的节点']
  ].map(([label,value,i,note])=>`<div class="stat"><div class="stat-top">${label}${icon(i)}</div><div class="stat-value">${value}</div><div class="stat-note">${note}</div></div>`).join('')}</section>`;
  $('#peer-total').textContent=items.length;
  updatePeerTable();updatePeerStatus();
}

function updatePeerTable() {
  const query=search.trim().toLowerCase(),items=visiblePeers();
  const rows=items.filter(p=>(filter==='all'||p.status===filter||(filter==='assigned'&&p.person_id)||(filter==='unassigned'&&!p.person_id))&&[p.hostname,p.ipv4,p.cidr,p.id,p.nat_type,p.tunnel_proto,p.version,p.person_name,p.device_name,p.credential_id].join(' ').toLowerCase().includes(query));
  const display=value=>e(value||'—');
  $('#peer-table').innerHTML=rows.length?`<div class="table-wrap"><table class="peer-table"><thead><tr><th>节点 / Peer ID</th><th>虚拟 IP</th><th>所属人员 / 凭据</th><th>状态 / 路径</th><th>延迟 / 丢包</th><th>收发流量</th><th>同步时间</th></tr></thead><tbody>${rows.map(p=>{
    const [label,tone]=peerStates[p.status];
    return `<tr><td data-col="node"><strong>${display(p.hostname||'未命名节点')}</strong><span class="subtext mono">ID ${e(p.id)}</span><span class="subtext">版本 ${display(p.version)}</span></td>
      <td data-col="ip"><span class="mono">${display(p.ipv4)}</span><span class="subtext mono">${display(p.cidr)}</span></td>
      <td data-col="owner">${peerOwner(p)}</td>
      <td data-col="status"><span class="badge ${tone}">${e(label)}</span><span class="subtext">${display(p.cost)} · ${display(p.tunnel_proto)}</span><span class="subtext">NAT ${display(p.nat_type)}</span></td>
      <td data-col="latency">${p.lat_ms&&p.lat_ms!=='-'?`${display(p.lat_ms)} ms`:'—'}<span class="subtext">丢包 ${display(p.loss_rate)}</span></td>
      <td data-col="traffic">收 ${display(p.rx_bytes)}<span class="subtext">发 ${display(p.tx_bytes)}</span></td>
      <td data-col="seen">${date(p.last_seen,true)}</td></tr>`;
  }).join('')}</tbody></table></div><div class="table-footer"><span>共 ${rows.length} 个节点</span><span>${peerData?.connection?.last_sync?`最近成功 ${date(peerData.connection.last_sync,true)}`:'尚未同步'}${peerData&&!peerData.connection.connected?' · 快照待核实':''}</span></div>`:emptyState('monitor',query||filter!=='all'?'没有匹配的节点':peerSynchronizing?'正在获取节点列表':peerRequestError||!peerData?.connection?.connected?'当前连接待核实':'暂无连接节点',query||filter!=='all'?'试试其他关键词或筛选条件。':'点击同步节点查询，断开的节点不会保留在列表中。');
}

async function synchronizePeers(background=false) {
  if(peerSynchronizing) return;
  const csrf=session.csrf;
  peerSynchronizing=true;peerRequestError='';if(view==='peers') updatePeerStatus();
  try {
    const result=await api('/api/peers/sync',{});
    if(session.authenticated&&session.csrf===csrf) peerData=result;
    if(!background&&session.authenticated&&session.csrf===csrf) toast('Peer 节点已同步');
  } catch(error) {
    if(session.authenticated&&session.csrf===csrf) {
      peerRequestError=error.message;
      try {const result=await api('/api/peers');if(session.authenticated&&session.csrf===csrf) peerData=result;} catch {}
      if(!background) toast(error.message,true);
    }
  } finally {
    peerSynchronizing=false;
    if(session.authenticated&&session.csrf===csrf&&view==='peers') updatePeers();
    scheduleMonitor();
  }
}


function peerOwner(p) {
  if(p.person_id) return `<button class="person-name" data-action="peer-person" data-id="${e(p.person_id)}">${e(p.person_name)}</button> ${badge(p.person_status,'person')}<span class="subtext">${e(p.device_name)} · ${p.binding_source==='manual'?'管理员补录':'签发公钥匹配'}</span><span class="subtext">凭据 ${badge(p.credential_status)}</span><span class="subtext mono" title="${e(p.credential_id)}">${e(p.credential_id.slice(0,13))}…</span>`;
  if(p.credential_id) return `<span class="expiry-soon">凭据待关联人员</span><span class="subtext">${e(p.device_name)}</span><button class="btn text" data-action="assign" data-id="${e(p.credential_id)}">关联人员</button>`;
  const notes={relay:'经中转，身份未取得',admin:'网络主密钥节点',unverified:'连接身份未验证',conflict:'连接公钥冲突'};
  return `<span class="muted">${e(notes[p.identity_status]||'未登记凭据公钥')}</span>${p.identity_status==='verified'&&p.public_key?`<button class="btn text" data-action="bind-peer" data-id="${e(p.id)}">关联人员</button>`:''}`;
}

function openPeerBind(id) {
  const peer=visiblePeers().find(p=>p.id===id);
  if(!peer||peer.identity_status!=='verified'||!peer.public_key) return;
  const credentials=liveCredentials().filter(c=>!c.public_key||c.public_key_scope!==credentialScope()||c.public_key===peer.public_key);
  if(!credentials.length) {toast('没有可关联的有效凭据，请先同步凭据或重新签发。',true);return;}
  openModal('关联节点与人员',`
    <p class="form-note">请选择这台设备实际使用的凭据，并核对其 client.toml 中的 local_public_key，或从原凭据在本机提取公钥。此关联由管理员确认，接口不会直接返回凭据 ID。</p>
    <div class="field"><label for="peer-public-key">已认证的节点公钥</label><input id="peer-public-key" class="mono" readonly value="${e(peer.public_key)}"></div>
    <div class="field"><label for="peer-credential">设备使用的凭据</label><select id="peer-credential" required><option value="">选择凭据</option>${credentials.map(c=>`<option value="${e(c.id)}">${e(c.device_name)} · ${e(personByID(c.person_id)?.name||'未关联人员')} · ${e(c.id.slice(0,13))}</option>`).join('')}</select></div>
    <div class="field"><label for="peer-person">归属人员</label><select id="peer-person" required><option value="">选择人员</option>${peopleOptions('')}</select></div>
    <div class="field"><label for="peer-device">设备名称</label><input id="peer-device" required maxlength="100" value="${e(peer.hostname)}"></div>
    <label class="checkbox-field"><input type="checkbox" required><span>我已核对节点公钥属于所选凭据，再关联到上述人员。</span></label>`, '确认关联', async form=>{
      const credential=$('#peer-credential',form).value;
      await api(`/api/credentials/${credential}/assign`,{person_id:$('#peer-person',form).value,device_name:$('#peer-device',form).value,public_key:peer.public_key});
      modal.close();peerData=await api('/api/peers');await reload();toast('节点归属已关联');
    });
  $('#peer-credential',modal).addEventListener('change',event=>{
    const c=credentialByID(event.target.value);if(!c)return;
    $('#peer-person',modal).value=c.person_id||'';$('#peer-device',modal).value=c.device_name;
  });
}
