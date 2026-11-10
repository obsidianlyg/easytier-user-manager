package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePeerJSONAndRejectInstanceFanout(t *testing.T) {
	valid := `[{"id":"1","hostname":"Japan","cost":"Local"},{"id":"2","hostname":"J4105","ipv4":"10.10.10.20","cidr":"10.10.10.20/24","cost":"p2p","lat_ms":"38.25","loss_rate":"0.0%","rx_bytes":"12.4 MB","tx_bytes":"1.2 MB","status":"missing","last_seen":1,"unneeded_private_key":"never-forward"},{"id":"3","cost":"relay(2)"},{"id":"4","cost":"relay(0)"}]`
	peers, err := parsePeers(json.RawMessage(valid))
	if err != nil || len(peers) != 4 {
		t.Fatalf("single-instance peer array rejected: %v", err)
	}
	for i, want := range []string{"local", "direct", "relay", "unknown"} {
		if peers[i].Status != want || peers[i].LastSeen != 0 {
			t.Fatal("untrusted status or timestamps accepted")
		}
	}
	encoded, _ := json.Marshal(peers)
	if strings.Contains(string(encoded), "never-forward") {
		t.Fatal("raw CLI fields leaked")
	}
	for _, raw := range []string{
		`null`, `{}`, `[null]`, `[{}]`, `[{"id":"1","cost":null}]`, `[{"id":1,"cost":"p2p"}]`,
		`[{"id":"4294967296","cost":"p2p"}]`, `[{"id":"1","cost":"p2p","ipv4":"not-an-ip"}]`,
		`[{"id":"1","cost":"p2p","cidr":"bad"}]`,
		`[{"id":"1","cost":"p2p"},{"id":"1","cost":"relay(2)"}]`,
		`[{"id":"1","cost":"Local"},{"id":"2","cost":"Local"}]`,
	} {
		if _, err := parsePeers(json.RawMessage(raw)); err == nil {
			t.Fatalf("malformed peer response accepted: %s", raw)
		}
	}
	_, err = parsePeers(json.RawMessage(`[{"instance_name":"a","result":[{"id":"1","cost":"Local"}]},{"instance_name":"b","result":[]}]`))
	var problem *APIError
	if !errors.As(err, &problem) || problem.Code != "ambiguous_instance" {
		t.Fatal("fanout envelope was treated as peer records")
	}
	peers, err = parsePeers(json.RawMessage(`[]`))
	if err != nil || peers == nil || len(peers) != 0 {
		t.Fatal("empty success must remain an empty JSON array")
	}
}

type fakePeerBackend struct {
	*fakeBackend
	peers     []Peer
	peerError error
}

func (b *fakePeerBackend) ListPeers(context.Context) ([]Peer, error) {
	return append([]Peer{}, b.peers...), b.peerError
}

func TestPeerHistoryFailureRestartAndScope(t *testing.T) {
	m, credentials := testManager(t)
	b := &fakePeerBackend{fakeBackend: credentials, peers: []Peer{
		{ID: "1", Hostname: "server", Cost: "Local"},
		{ID: "2", Hostname: "same-name", IPv4: "10.0.0.2", Cost: "p2p", Latency: "20.1", RX: "2 MB"},
		{ID: "3", Hostname: "same-name", IPv4: "10.0.0.3", Cost: "relay(2)"},
	}}
	m.Backend = b
	if err := m.SyncPeers(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := m.PeerSnapshot()
	if !first.Connection.Connected || len(first.Items) != 3 || first.Connection.LastSync == 0 {
		t.Fatal("peer sync did not produce a fresh snapshot")
	}
	b.peerError = problem(503, "unavailable", "peer service unavailable")
	if err := m.SyncPeers(context.Background()); err == nil {
		t.Fatal("expected peer synchronization failure")
	}
	failed := m.PeerSnapshot()
	if failed.Connection.Connected || failed.Connection.Error == "" || failed.Connection.LastSync != first.Connection.LastSync || !reflect.DeepEqual(failed.Items, first.Items) {
		t.Fatal("failed sync must retain peer states and timestamps")
	}
	b.peerError = nil
	b.peers = []Peer{b.peers[0], {ID: "4", Hostname: "same-name", IPv4: "10.0.0.2", Cost: "p2p"}}
	if err := m.SyncPeers(context.Background()); err != nil {
		t.Fatal(err)
	}
	last := m.PeerSnapshot()
	items := map[string]Peer{}
	for _, p := range last.Items {
		items[p.ID] = p
	}
	if len(items) != 4 || items["2"].Status != "missing" || items["2"].IPv4 != "10.0.0.2" || items["2"].LastSeen == 0 || items["2"].RX != "" || items["2"].Latency != "" || items["4"].Status != "direct" {
		t.Fatal("history must preserve missing peer identity without stale metrics or guessing by IP/name")
	}
	restarted, err := newManager(m.Config, b)
	if err != nil {
		t.Fatal(err)
	}
	restored := restarted.PeerSnapshot()
	if len(restored.Items) != 4 || restored.Connection.Connected || restored.Connection.LastSync == 0 {
		t.Fatal("restart must preserve history but require fresh synchronization")
	}
	restarted.Config.InstanceName = "another-network"
	if len(restarted.PeerSnapshot().Items) != 0 {
		t.Fatal("history from another RPC instance leaked")
	}
	if err := restarted.SyncPeers(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(restarted.PeerSnapshot().Items) != 2 {
		t.Fatal("new instance inherited old peers")
	}
	if len(m.Store.Snapshot().Credentials) != 0 || m.connection.Connected {
		t.Fatal("peer collection must not change credential state")
	}
}

func TestCLIListPeersUsesArrayWithoutWeakeningCredentialCommands(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("Unix CLI helper")
	}
	path := filepath.Join(t.TempDir(), "cli")
	script := "#!/bin/sh\nprintf '%s' '[{\"id\":\"42\",\"hostname\":\"node\",\"cost\":\"Local\"}]'\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &CLI{Config: Config{CLIPath: path, RPCPortal: "127.0.0.1:15888", InstanceName: "office", CLITimeout: 2}}
	peers, err := cli.ListPeers(context.Background())
	if err != nil || len(peers) != 1 || peers[0].ID != "42" {
		t.Fatal("normal single-instance peer array rejected")
	}
	if _, err := cli.List(context.Background()); err == nil {
		t.Fatal("credential object requirement must not be relaxed")
	}
}
