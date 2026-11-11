package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

func TestPeerIdentityRequiresAuthenticatedCredentialAndConsistentKeys(t *testing.T) {
	key := credentialPublicKey(fakeCredentialSecret("first"))
	other := credentialPublicKey(fakeCredentialSecret("second"))
	conn := func(public string, auth, identity int, closed bool, network string) map[string]any {
		raw, _ := base64.StdEncoding.DecodeString(public)
		return map[string]any{"peer_id": 42, "noise_remote_static_pubkey": raw, "secure_auth_level": auth, "peer_identity_type": identity, "is_closed": closed, "network_name": network}
	}
	for _, test := range []struct {
		name      string
		conns     []map[string]any
		status    string
		public    string
		connected bool
	}{
		{"verified", []map[string]any{conn(key, 2, 1, false, "office")}, "verified", key, true},
		{"closed", []map[string]any{conn(key, 2, 1, true, "office")}, "unverified", "", false},
		{"encrypted-only", []map[string]any{conn(key, 1, 1, false, "office")}, "unverified", "", true},
		{"admin", []map[string]any{conn(key, 3, 0, false, "office")}, "admin", "", true},
		{"foreign-network", []map[string]any{conn(key, 2, 1, false, "another-network")}, "unverified", "", true},
		{"multiple-links", []map[string]any{conn(key, 2, 1, false, "office"), conn(key, 2, 1, false, "office")}, "verified", key, true},
		{"conflicting-keys", []map[string]any{conn(key, 2, 1, false, "office"), conn(other, 2, 1, false, "office")}, "conflict", "", true},
		{"mixed-auth", []map[string]any{conn(key, 2, 1, false, "office"), conn(key, 1, 1, false, "office")}, "conflict", "", true},
		{"invalid-key", []map[string]any{conn("", 2, 1, false, "office")}, "unverified", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw, _ := json.Marshal([]any{map[string]any{"route": map[string]any{"peer_id": 42}, "peer": map[string]any{"peer_id": 42, "conns": test.conns}}})
			parsed, err := parsePeerIdentities(raw, "office")
			if err != nil {
				t.Fatal(err)
			}
			p := parsed["42"]
			if p.Status != test.status || p.PublicKey != test.public || p.Connected != test.connected {
				t.Fatalf("identity mismatch: %+v", p)
			}
		})
	}
	for _, raw := range []string{`null`, `{}`, `[{}]`, `[{"result":[]}]`, `[{"route":{"peer_id":42},"peer":{"peer_id":43}}]`} {
		if _, err := parsePeerIdentities(json.RawMessage(raw), "office"); err == nil {
			t.Fatal("invalid identity accepted")
		}
	}
}

func TestIssuedIdentityAndManualBackfillFollowPersonnelWithoutGuessing(t *testing.T) {
	m, backend := testManager(t)
	p := addPerson(t, m)
	id := issue(t, m, p)
	c, _ := findCredential(m.Store.Snapshot(), id)
	if c.PublicKey != credentialPublicKey(fakeCredentialSecret(id)) || c.PublicKeyScope != m.peerScope() || c.PublicKeySource != "issued" {
		t.Fatal("issued public identity missing")
	}
	b := &fakePeerBackend{fakeBackend: backend, peers: []Peer{
		{ID: "1", Hostname: "workstation", IPv4: "10.0.0.2", Cost: "p2p", PublicKey: c.PublicKey, IdentityStatus: "verified"},
		{ID: "2", Hostname: "workstation", IPv4: "10.0.0.2", Cost: "p2p", PublicKey: c.PublicKey, IdentityStatus: "unverified"},
		{ID: "3", Hostname: "workstation", Cost: "relay(2)", PublicKey: c.PublicKey, IdentityStatus: "verified"},
	}}
	m.Backend = b
	if err := m.SyncPeers(context.Background()); err != nil {
		t.Fatal(err)
	}
	matched := 0
	for _, peer := range m.PeerSnapshot().Items {
		if peer.PersonID != "" {
			matched++
			if peer.ID != "1" || peer.PersonID != p.ID || peer.CredentialID != id {
				t.Fatal("matched name/IP or unverified/relayed identity")
			}
		}
	}
	if matched != 1 {
		t.Fatal("authenticated credential did not resolve")
	}
	// Simulate an older installation that did not store public keys.
	if err := m.Store.Update(func(s *State) error { s.Credentials[0].PublicKey = ""; return nil }); err != nil {
		t.Fatal(err)
	}
	if m.PeerSnapshot().Items[0].PersonID != "" {
		t.Fatal("old credential guessed from metadata")
	}
	if err := m.Assign(id, p.ID, "manual workstation", c.PublicKey); err != nil {
		t.Fatal(err)
	}
	peer := m.PeerSnapshot().Items[0]
	if peer.BindingSource != "manual" || peer.PersonID != p.ID {
		t.Fatal("manual backfill not labeled")
	}
	if _, err := m.SavePerson(p.ID, PersonInput{Name: "改名员工"}); err != nil {
		t.Fatal(err)
	}
	if m.PeerSnapshot().Items[0].PersonName != "改名员工" {
		t.Fatal("stale personnel details")
	}
	second := issue(t, m, p)
	if err := m.Assign(second, p.ID, "other", c.PublicKey); err == nil {
		t.Fatal("duplicate public key accepted")
	}
	if err := m.Assign(id, p.ID, "other", credentialPublicKey(fakeCredentialSecret("another-key"))); err == nil {
		t.Fatal("existing public key overwritten")
	}
	if err := m.Assign(id, p.ID, "other", "invalid-public-key"); err == nil {
		t.Fatal("invalid public key accepted")
	}
	if err := m.Revoke(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	peer = m.PeerSnapshot().Items[0]
	if peer.PersonID != p.ID || peer.CredentialStatus != "revoked" {
		t.Fatal("existing connection mislabeled after revocation")
	}
	if err := m.Store.Update(func(s *State) error { s.Credentials[0].PublicKeyScope = "different instance"; return nil }); err != nil {
		t.Fatal(err)
	}
	if m.PeerSnapshot().Items[0].PersonID != "" {
		t.Fatal("identity from another scope matched")
	}
	encoded, _ := json.Marshal(m.Store.Snapshot())
	if strings.Contains(string(encoded), fakeCredentialSecret(id)) {
		t.Fatal("private key stored during binding")
	}
}
