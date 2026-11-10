package main

import (
	"context"
	"encoding/json"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type PeerBackend interface {
	ListPeers(context.Context) ([]Peer, error)
}

// These are the safe, formatted fields returned by non-verbose `-o json peer`.
// They contain no credential keys; peer IDs cannot be mapped to credential IDs.
type Peer struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	IPv4     string `json:"ipv4"`
	CIDR     string `json:"cidr"`
	Cost     string `json:"cost"`
	Latency  string `json:"lat_ms"`
	Loss     string `json:"loss_rate"`
	RX       string `json:"rx_bytes"`
	TX       string `json:"tx_bytes"`
	Protocol string `json:"tunnel_proto"`
	NAT      string `json:"nat_type"`
	Version  string `json:"version"`
	Status   string `json:"status"`
	LastSeen int64  `json:"last_seen"`
}

type PeerHistory struct {
	Scope    string `json:"scope"`
	LastSync int64  `json:"last_sync"`
	Items    []Peer `json:"items"`
}

type PeerSnapshot struct {
	Connection Connection `json:"connection"`
	Items      []Peer     `json:"items"`
	Now        int64      `json:"now"`
}

func peerStatus(cost string) string {
	switch cost {
	case "Local":
		return "local"
	case "p2p":
		return "direct"
	}
	if strings.HasPrefix(cost, "relay(") && strings.HasSuffix(cost, ")") {
		if hops, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(cost, "relay("), ")")); err == nil && hops > 1 {
			return "relay"
		}
	}
	return "unknown"
}

func (c *CLI) ListPeers(ctx context.Context) ([]Peer, error) {
	raw, err := c.runJSON(ctx, "peer")
	if err != nil {
		return nil, err
	}
	return parsePeers(raw)
}

func parsePeers(raw json.RawMessage) ([]Peer, error) {
	invalid := func() error {
		return problem(502, "invalid_peer_json", "节点列表格式不正确，保留上次快照；请核对配套 CLI 版本。")
	}
	var records []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &records) != nil {
		return nil, invalid()
	}
	peers := make([]Peer, 0, len(records))
	seen := map[string]bool{}
	locals := 0
	for _, record := range records {
		var fields map[string]json.RawMessage
		if json.Unmarshal(record, &fields) != nil || fields == nil {
			return nil, invalid()
		}
		if fields["result"] != nil || fields["instance_id"] != nil || fields["instance_name"] != nil {
			return nil, problem(409, "ambiguous_instance", "RPC 对应多个实例，请在配置文件设置 instance_name 后同步节点。")
		}
		var p Peer
		if json.Unmarshal(record, &p) != nil || fields["id"] == nil || fields["cost"] == nil || seen[p.ID] || p.Cost == "" {
			return nil, invalid()
		}
		if _, err := strconv.ParseUint(p.ID, 10, 32); err != nil {
			return nil, invalid()
		}
		if p.IPv4 != "" {
			if addr, err := netip.ParseAddr(p.IPv4); err != nil || !addr.Is4() {
				return nil, invalid()
			}
		}
		if p.CIDR != "" {
			if _, err := netip.ParsePrefix(p.CIDR); err != nil {
				return nil, invalid()
			}
		}
		if len(p.Hostname) > 4096 || len(p.Cost) > 64 || len(p.Protocol) > 256 || len(p.Latency) > 64 || len(p.Loss) > 64 || len(p.RX) > 64 || len(p.TX) > 64 || len(p.NAT) > 256 || len(p.Version) > 256 {
			return nil, invalid()
		}
		seen[p.ID] = true
		p.Status = peerStatus(p.Cost)
		p.LastSeen = 0
		if p.Status == "local" {
			locals++
		}
		peers = append(peers, p)
	}
	if locals > 1 {
		return nil, invalid()
	}
	return peers, nil
}

func (m *Manager) peerScope() string {
	return m.Config.RPCPortal + "\n" + m.Config.InstanceName + "\n" + m.Config.NetworkName
}

func (m *Manager) SyncPeers(ctx context.Context) error {
	m.op.Lock()
	defer m.op.Unlock()
	provider, ok := m.Backend.(PeerBackend)
	var peers []Peer
	var err error
	if !ok {
		err = problem(503, "peers_unavailable", "当前后端不支持 peer 同步。")
	} else {
		peers, err = provider.ListPeers(ctx)
	}
	if err == nil {
		timestamp := now()
		err = m.Store.Update(func(s *State) error {
			history := s.PeerHistory
			if history == nil || history.Scope != m.peerScope() {
				history = &PeerHistory{Scope: m.peerScope()}
			}
			current := make(map[string]Peer, len(peers)+len(history.Items))
			for _, p := range history.Items {
				p.Status = "missing"
				p.Latency, p.Loss, p.RX, p.TX, p.Protocol = "", "", "", "", ""
				current[p.ID] = p
			}
			for _, p := range peers {
				p.Status, p.LastSeen = peerStatus(p.Cost), timestamp
				current[p.ID] = p
			}
			history.Items = make([]Peer, 0, len(current))
			for _, p := range current {
				history.Items = append(history.Items, p)
			}
			sort.Slice(history.Items, func(i, j int) bool {
				a, b := history.Items[i], history.Items[j]
				if a.LastSeen != b.LastSeen {
					return a.LastSeen > b.LastSeen
				}
				return a.ID < b.ID
			})
			// Keep recent observations bounded; this is a node list, not a traffic log.
			if len(history.Items) > 5000 {
				history.Items = history.Items[:5000]
			}
			history.LastSync = timestamp
			s.PeerHistory = history
			return nil
		})
	}
	m.peerMu.Lock()
	defer m.peerMu.Unlock()
	if err != nil {
		m.peerConnection.Connected, m.peerConnection.Error = false, err.Error()
		return err
	}
	m.peerConnection = Connection{Connected: true, LastSync: now()}
	return nil
}

func (m *Manager) PeerSnapshot() PeerSnapshot {
	snapshot := PeerSnapshot{Items: []Peer{}, Now: now()}
	history := m.Store.Snapshot().PeerHistory
	m.peerMu.RLock()
	snapshot.Connection = m.peerConnection
	m.peerMu.RUnlock()
	if history != nil && history.Scope == m.peerScope() {
		snapshot.Items = history.Items
		snapshot.Connection.LastSync = history.LastSync
	}
	if snapshot.Items == nil {
		snapshot.Items = []Peer{}
	}
	if !snapshot.Connection.Connected && snapshot.Connection.Error == "" {
		snapshot.Connection.Error = "尚未同步 peer 节点，请同步核实。"
	}
	rank := map[string]int{"local": 0, "direct": 1, "relay": 2, "unknown": 3, "missing": 4}
	sort.SliceStable(snapshot.Items, func(i, j int) bool {
		a, b := snapshot.Items[i], snapshot.Items[j]
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] < rank[b.Status]
		}
		return a.Hostname < b.Hostname
	})
	return snapshot
}

func (d *DemoBackend) ListPeers(ctx context.Context) ([]Peer, error) {
	return []Peer{
		{ID: "10001", Hostname: "Japan-Relay", IPv4: "10.10.10.1", CIDR: "10.10.10.1/24", Cost: "Local", NAT: "Public", Version: "2.6.4"},
		{ID: "10002", Hostname: "J4105", IPv4: "10.10.10.20", CIDR: "10.10.10.20/24", Cost: "p2p", Latency: "38.25", Loss: "0.0%", RX: "156.3 MB", TX: "82.5 MB", Protocol: "tcp", NAT: "PortRestricted", Version: "2.6.4"},
		{ID: "10003", Hostname: "MacBook-Pro", IPv4: "10.10.10.21", CIDR: "10.10.10.21/24", Cost: "p2p", Latency: "42.18", Loss: "0.2%", RX: "24.8 MB", TX: "12.4 MB", Protocol: "udp", NAT: "Symmetric", Version: "2.6.4"},
		{ID: "10004", Hostname: "Office-Windows", IPv4: "10.10.10.22", CIDR: "10.10.10.22/24", Cost: "relay(2)", Latency: "75.10", Loss: "0.0%", RX: "0 B", TX: "0 B", NAT: "Unknown", Version: "2.6.4"},
	}, nil
}
