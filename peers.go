package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"sort"
	"strconv"
	"strings"
)

type PeerBackend interface {
	ListPeers(context.Context) ([]Peer, error)
}

// Only public connection identity and formatted link statistics leave the CLI boundary.
type Peer struct {
	PublicKey        string `json:"public_key,omitempty"`
	IdentityStatus   string `json:"identity_status,omitempty"`
	CredentialID     string `json:"credential_id,omitempty"`
	CredentialStatus string `json:"credential_status,omitempty"`
	PersonID         string `json:"person_id,omitempty"`
	PersonName       string `json:"person_name,omitempty"`
	PersonStatus     string `json:"person_status,omitempty"`
	DeviceName       string `json:"device_name,omitempty"`
	BindingSource    string `json:"binding_source,omitempty"`
	ID               string `json:"id"`
	Hostname         string `json:"hostname"`
	IPv4             string `json:"ipv4"`
	CIDR             string `json:"cidr"`
	Cost             string `json:"cost"`
	Latency          string `json:"lat_ms"`
	Loss             string `json:"loss_rate"`
	RX               string `json:"rx_bytes"`
	TX               string `json:"tx_bytes"`
	Protocol         string `json:"tunnel_proto"`
	NAT              string `json:"nat_type"`
	Version          string `json:"version"`
	Status           string `json:"status"`
	LastSeen         int64  `json:"last_seen"`
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
	peers, err := parsePeers(raw)
	if err != nil {
		return nil, err
	}
	details, err := c.runJSON(ctx, "-v", "peer")
	if err != nil {
		return nil, err
	}
	identities, err := parsePeerIdentities(details, c.Config.NetworkName)
	if err != nil {
		return nil, err
	}
	connected := []Peer{}
	for _, p := range peers {
		identity, found := identities[p.ID]
		if !found || (p.Status != "direct" && p.Status != "relay") || (p.Status == "direct" && !identity.Connected) {
			continue
		}
		p.PublicKey, p.IdentityStatus = identity.PublicKey, identity.Status
		if p.Status == "relay" {
			// A relay's own key never identifies the node behind it.
			p.PublicKey, p.IdentityStatus = "", "relay"
		}
		connected = append(connected, p)
	}
	return connected, nil
}

type peerIdentity struct {
	Connected bool
	PublicKey string
	Status    string
}

func parsePeerIdentities(raw json.RawMessage, networkName string) (map[string]peerIdentity, error) {
	var records []struct {
		Result json.RawMessage `json:"result"`
		Route  *struct {
			PeerID uint32 `json:"peer_id"`
		} `json:"route"`
		Peer *struct {
			PeerID uint32 `json:"peer_id"`
			Conns  []struct {
				PeerID    uint32 `json:"peer_id"`
				Closed    *bool  `json:"is_closed"`
				Network   string `json:"network_name"`
				PublicKey []byte `json:"noise_remote_static_pubkey"`
				Auth      *int   `json:"secure_auth_level"`
				Identity  *int   `json:"peer_identity_type"`
			} `json:"conns"`
		} `json:"peer"`
	}
	invalid := func() error {
		return problem(502, "invalid_peer_identity", "无法解析节点连接身份，请核对配套 CLI 版本后同步。")
	}
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &records) != nil {
		return nil, invalid()
	}
	result := map[string]peerIdentity{}
	for _, record := range records {
		if record.Result != nil {
			return nil, problem(409, "ambiguous_instance", "请设置 instance_name 后同步节点。")
		}
		if record.Route == nil {
			return nil, invalid()
		}
		id := strconv.FormatUint(uint64(record.Route.PeerID), 10)
		if _, duplicate := result[id]; duplicate {
			return nil, invalid()
		}
		identity := peerIdentity{Status: "unverified"}
		keys := map[string]bool{}
		otherIdentity := false
		if record.Peer != nil {
			if record.Peer.PeerID != record.Route.PeerID {
				return nil, invalid()
			}
			for _, conn := range record.Peer.Conns {
				if conn.Closed == nil || *conn.Closed || conn.PeerID != record.Route.PeerID {
					continue
				}
				identity.Connected = true
				// Credential peers must be authenticated as PeerVerified, not merely encrypted.
				if conn.Auth == nil || conn.Identity == nil || *conn.Auth != 2 || *conn.Identity != 1 || conn.Network != networkName {
					otherIdentity = true
					if conn.Auth != nil && *conn.Auth == 3 && conn.Identity != nil && *conn.Identity == 0 {
						identity.Status = "admin"
					}
					continue
				}
				key, err := canonicalPublicKey(base64.StdEncoding.EncodeToString(conn.PublicKey))
				if err != nil {
					otherIdentity = true
					continue
				}
				keys[key] = true
			}
		}
		if len(keys) > 1 || (len(keys) > 0 && otherIdentity) {
			identity.Status = "conflict"
		} else if len(keys) == 1 {
			identity.Status = "verified"
			for key := range keys {
				identity.PublicKey = key
			}
		}
		result[id] = identity
	}
	return result, nil
}

func parsePeers(raw json.RawMessage) ([]Peer, error) {
	invalid := func() error {
		return problem(502, "invalid_peer_json", "节点列表格式不正确，本次连接无法核实；请核对配套 CLI 版本。")
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
		p.PublicKey, p.IdentityStatus = "", ""
		p.CredentialID, p.PersonID, p.PersonName, p.PersonStatus, p.CredentialStatus, p.DeviceName, p.BindingSource = "", "", "", "", "", "", ""
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
			history := &PeerHistory{Scope: m.peerScope(), Items: []Peer{}, LastSync: timestamp}
			for _, p := range peers {
				if p.Cost == "Local" || (peerStatus(p.Cost) != "direct" && peerStatus(p.Cost) != "relay") {
					continue
				}
				p.Status, p.LastSeen = peerStatus(p.Cost), timestamp
				history.Items = append(history.Items, p)
			}
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
	state := m.Store.Snapshot()
	history := state.PeerHistory
	m.peerMu.RLock()
	snapshot.Connection = m.peerConnection
	m.peerMu.RUnlock()
	if history != nil && history.Scope == m.peerScope() {
		snapshot.Connection.LastSync = history.LastSync
		if snapshot.Connection.Connected {
			byKey := map[string][]Credential{}
			for _, c := range state.Credentials {
				if c.PublicKey != "" && c.PublicKeyScope == m.peerScope() {
					byKey[c.PublicKey] = append(byKey[c.PublicKey], c)
				}
			}
			for _, p := range history.Items {
				if p.Status != "direct" && p.Status != "relay" {
					continue
				}
				p.CredentialID, p.PersonID, p.PersonName, p.PersonStatus, p.CredentialStatus, p.DeviceName, p.BindingSource = "", "", "", "", "", "", ""
				if p.IdentityStatus == "verified" && p.Status == "direct" && p.PublicKey != "" {
					matches := byKey[p.PublicKey]
					if len(matches) > 1 {
						p.IdentityStatus, p.PublicKey = "conflict", ""
					}
					if len(matches) == 1 {
						c := matches[0]
						p.CredentialID, p.CredentialStatus, p.DeviceName, p.BindingSource = c.ID, c.Status, c.DeviceName, c.PublicKeySource
						if c.Status == "active" && c.ExpiresAt <= snapshot.Now {
							p.CredentialStatus = "expired"
						}
						if person, err := findPerson(state, c.PersonID); err == nil {
							p.PersonID, p.PersonName, p.PersonStatus = person.ID, person.Name, person.Status
						}
					}
				}
				snapshot.Items = append(snapshot.Items, p)
			}
		}
	} else {
		snapshot.Connection = Connection{}
	}
	if snapshot.Items == nil {
		snapshot.Items = []Peer{}
	}
	if !snapshot.Connection.Connected && snapshot.Connection.Error == "" {
		snapshot.Connection.Error = "尚未同步 peer 节点，请同步核实。"
	}
	rank := map[string]int{"direct": 0, "relay": 1}
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
	peers := []Peer{
		{ID: "10002", Hostname: "J4105", IPv4: "10.10.10.20", CIDR: "10.10.10.20/24", Cost: "p2p", Latency: "38.25", Loss: "0.0%", RX: "156.3 MB", TX: "82.5 MB", Protocol: "tcp", NAT: "PortRestricted", Version: "2.6.4"},
		{ID: "10003", Hostname: "MacBook-Pro", IPv4: "10.10.10.21", CIDR: "10.10.10.21/24", Cost: "p2p", Latency: "42.18", Loss: "0.2%", RX: "24.8 MB", TX: "12.4 MB", Protocol: "udp", NAT: "Symmetric", Version: "2.6.4"},
		{ID: "10004", Hostname: "Office-Windows", IPv4: "10.10.10.22", CIDR: "10.10.10.22/24", Cost: "relay(2)", Latency: "75.10", Loss: "0.0%", RX: "0 B", TX: "0 B", NAT: "Unknown", Version: "2.6.4"},
		{ID: "10005", Hostname: "Unassigned-Laptop", IPv4: "10.10.10.23", CIDR: "10.10.10.23/24", Cost: "p2p", Latency: "40.1", Loss: "0.0%", RX: "1 MB", TX: "2 MB", NAT: "Unknown", Version: "2.6.4", IdentityStatus: "verified"},
	}
	state := d.Store.Snapshot()
	for i, index := range []int{0, 2} {
		if index < len(state.Credentials) {
			key := state.Credentials[index].PublicKey
			if key == "" {
				key = demoPublicKey(state.Credentials[index].ID)
			}
			peers[i].PublicKey, peers[i].IdentityStatus = key, "verified"
		}
	}
	peers[2].IdentityStatus = "relay"
	for _, c := range state.Credentials {
		if c.Source == "imported" {
			peers[3].PublicKey = demoPublicKey(c.ID)
			break
		}
	}
	return peers, nil
}
