'use strict';

let peerData = null, peerSynchronizing = false, peerRequestError = '';
const peerStates = {local:['本机节点',''],direct:['直连','positive'],relay:['经中转','positive'],missing:['本次未发现','warning'],unknown:['路由待核实','warning']};

function peerSyncButton() {
  return `<button class="btn secondary-label ${peerSynchronizing?'spin':''}" data-action="sync-peers" title="同步节点" aria-label="同步节点" ${peerSynchronizing?'disabled':''}>${icon('refresh')}${peerSynchronizing?'同步中…':'同步节点'}</button>`;
}

function renderPeers() {
  $('#view-content').innerHTML=`<div class="monitor-refresh"><span id="peer-sync-status" role="status"></span><button class="btn small" data-action="auto-refresh" aria-pressed="${autoRefresh}"></button></div>
    <div id="peer-error" role="alert"></div><div id="peer-metrics"></div>
    <section class="panel"><div class="panel-header"><h2 class="panel-heading">Peer 节点<span class="total-count" id="peer-total">0</span></h2><span class="muted mono">${e(data.config.network_name)}</span></div>
      <div class="filterbar"><div class="search">${icon('search')}<input id="search" aria-label="搜索节点名称、IP 或 Peer ID" placeholder="搜索节点名称、IP 或 Peer ID" value="${e(search)}" autocomplete="off"></div>
      <div class="filter-controls"><select id="status-filter" aria-label="筛选节点状态">${[['all','全部节点'],['discovered','当前发现（含本机）'],['direct','直连'],['relay','经中转'],['missing','本次未发现'],['local','本机节点'],['unknown','路由待核实']].map(([value,label])=>`<option value="${value}" ${filter===value?'selected':''}>${label}</option>`).join('')}</select></div></div>
      <div id="peer-table"></div></section>
    <div class="helper-line">${icon('info')}<span>数据来自当前 core 的 peer 列表；IP 为 EasyTier 虚拟 IP。收发流量为连接累计值，非实时速率或人员用量；经中转节点可能没有链路统计。本次未发现表示最近成功同步中没有该 Peer ID，可能是离线、不可达或重启后 ID 变化。尚未连接过的设备不会出现；当前接口无法将 Peer ID 与人员凭据可靠关联。</span></div>`;
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
  $('#peer-error').innerHTML=error?`<div class="banner error">${icon('info')}<p>${e(error)}${conn?.last_sync?' 保留最近成功快照，节点状态待核实。':' 可点击“同步节点”重试。'}</p></div>`:'';
  const sync=$('[data-action="sync-peers"]');if(sync) {sync.disabled=peerSynchronizing;sync.classList.toggle('spin',peerSynchronizing);sync.innerHTML=`${icon('refresh')}${peerSynchronizing?'同步中…':'同步节点'}`;}
}

function updatePeers() {
  if(view!=='peers'||!data) return;
  const items=peerData?.items||[];
  const count=status=>items.filter(p=>p.status===status).length;
  $('#peer-metrics').innerHTML=`<section class="stats" aria-label="Peer 节点概况">${[
    ['当前远端节点',items.filter(p=>p.status!=='missing'&&p.status!=='local').length,'users','最近成功同步时发现，不含本机'],
    ['直连节点',count('direct'),'monitor','与管理节点直接连接'],
    ['经中转节点',count('relay'),'refresh','通过中转路径发现'],
    ['本次未发现',count('missing'),'history','保留历史 IP 和最后发现时间']
  ].map(([label,value,i,note])=>`<div class="stat"><div class="stat-top">${label}${icon(i)}</div><div class="stat-value">${value}</div><div class="stat-note">${note}</div></div>`).join('')}</section>`;
  $('#peer-total').textContent=items.length;
  updatePeerTable();updatePeerStatus();
}

function updatePeerTable() {
  const query=search.trim().toLowerCase(),items=peerData?.items||[];
  const rows=items.filter(p=>(filter==='all'||p.status===filter||(filter==='discovered'&&p.status!=='missing'))&&[p.hostname,p.ipv4,p.cidr,p.id,p.nat_type,p.tunnel_proto,p.version].join(' ').toLowerCase().includes(query));
  const display=value=>e(value||'—');
  $('#peer-table').innerHTML=rows.length?`<div class="table-wrap"><table class="peer-table"><thead><tr><th>节点 / Peer ID</th><th>虚拟 IP</th><th>状态 / 路径</th><th>延迟 / 丢包</th><th>收发流量</th><th>最后发现</th></tr></thead><tbody>${rows.map(p=>{
    const [label,tone]=peerStates[p.status]||peerStates.unknown,visible=p.status!=='missing',remote=visible&&p.status!=='local';
    return `<tr><td data-col="node"><strong>${display(p.hostname||'未命名节点')}</strong><span class="subtext mono">ID ${e(p.id)}</span><span class="subtext">版本 ${display(p.version)}</span></td>
      <td data-col="ip"><span class="mono">${display(p.ipv4)}</span><span class="subtext mono">${display(p.cidr)}</span></td>
      <td data-col="status"><span class="badge ${tone}">${e(label)}</span><span class="subtext">${visible?`${display(p.cost)} · ${display(p.tunnel_proto)}`:'历史记录'}</span><span class="subtext">NAT ${display(p.nat_type)}</span></td>
      <td data-col="latency">${remote&&p.lat_ms&&p.lat_ms!=='-'?`${display(p.lat_ms)} ms`:'—'}<span class="subtext">${remote?`丢包 ${display(p.loss_rate)}`:''}</span></td>
      <td data-col="traffic">${remote?`收 ${display(p.rx_bytes)}<span class="subtext">发 ${display(p.tx_bytes)}</span>`:'—'}</td>
      <td data-col="seen">${date(p.last_seen,true)}</td></tr>`;
  }).join('')}</tbody></table></div><div class="table-footer"><span>共 ${rows.length} 个节点</span><span>${peerData?.connection?.last_sync?`最近成功 ${date(peerData.connection.last_sync,true)}`:'尚未同步'}${peerData&&!peerData.connection.connected?' · 快照待核实':''}</span></div>`:emptyState('monitor',query||filter!=='all'?'没有匹配的节点':peerSynchronizing?'正在获取节点列表':'暂无已发现节点',query||filter!=='all'?'试试其他关键词或筛选条件。':'点击同步节点读取当前 core 的 peer 列表。');
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
