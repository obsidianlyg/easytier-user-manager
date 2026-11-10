package main

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestMonitorAuthorizationBoundariesAndOwnership(t *testing.T) {
	const timestamp int64 = 1800000000
	people := []Person{
		{ID: "a", Name: "同名员工", Status: "active"},
		{ID: "b", Name: "同名员工", Status: "active"},
		{ID: "c", Status: "offboarded"},
		{ID: "d", Status: "revoking"},
	}
	credentials := []Credential{
		{PersonID: "a", Status: "active", ExpiresAt: timestamp + 7*86400},
		{PersonID: "a", Status: "active", ExpiresAt: timestamp + 7*86400 + 1},
		{PersonID: "b", Status: "active", ExpiresAt: timestamp},
		{PersonID: "b", Status: "revoked", ExpiresAt: timestamp + 100},
		{PersonID: "c", Status: "active", ExpiresAt: timestamp + 1},
		{PersonID: "d", Status: "revoke_failed"},
		{PersonID: "d", Status: "missing"},
		{Status: "active", ExpiresAt: timestamp + 8*86400},
		{PersonID: "unknown", Status: "issue_unconfirmed"},
		{Status: "expired", ExpiresAt: timestamp - 1},
		{Status: "revoked", ExpiresAt: timestamp + 1},
	}
	m := buildMonitor(people, credentials, timestamp)
	if m.PeopleTotal != 4 || m.ActivePeople != 2 || m.RevokingPeople != 1 || m.OffboardedPeople != 1 ||
		m.ActiveCredentials != 4 || m.ExpiringCredentials != 2 || m.AttentionPeople != 2 ||
		m.NoAccessPeople != 1 || m.UnassignedCredentials != 2 || m.AbnormalCredentials != 3 || m.OnlineAvailable {
		t.Fatalf("unexpected counts: %+v", m)
	}
	rows := make(map[string]PersonMonitor)
	for _, row := range m.People {
		rows[row.PersonID] = row
	}
	if rows["a"].Active != 2 || rows["a"].Expiring != 1 || rows["a"].NextExpiry != timestamp+7*86400 ||
		rows["b"].Active != 0 || rows["b"].Expired != 1 || rows["b"].Total != 2 {
		t.Fatalf("duplicate names or expiration boundaries mixed: %+v", rows)
	}
	if !m.People[0].NeedsAttention || !m.People[1].NeedsAttention || m.People[2].NeedsAttention {
		t.Fatal("attention rows must appear first")
	}
	if credentials[2].Status != "active" {
		t.Fatal("monitor must not mutate stored credential statuses")
	}
}

func TestMonitorFailedSyncRetainsSnapshotAndReportsDisconnection(t *testing.T) {
	m, b := testManager(t)
	p := addPerson(t, m)
	issue(t, m, p)
	if err := m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := m.Snapshot()
	before := first["monitor"].(Monitor)
	if before.ActiveCredentials != 1 || before.ActivePeople != 1 {
		t.Fatalf("bad initial snapshot: %+v", before)
	}
	b.failList = true
	if err := m.Sync(context.Background()); err == nil {
		t.Fatal("expected failed sync")
	}
	last := m.Snapshot()
	conn := last["connection"].(Connection)
	if conn.Connected || conn.Error == "" || conn.LastSync != first["connection"].(Connection).LastSync {
		t.Fatalf("failed synchronization lost freshness information: %+v", conn)
	}
	if !reflect.DeepEqual(before, last["monitor"]) {
		t.Fatal("failed sync must preserve personnel authorization snapshot")
	}
	encoded, err := json.Marshal(last)
	if err != nil || !json.Valid(encoded) {
		t.Fatal("monitor must remain valid JSON")
	}
}

func TestMonitorEmptyAndPendingPersonnel(t *testing.T) {
	m := buildMonitor(nil, nil, 1800000000)
	if m.People == nil || len(m.People) != 0 || m.PeopleTotal != 0 {
		t.Fatal("empty monitor must serialize an empty personnel array")
	}
	m = buildMonitor([]Person{{ID: "pending", Status: "revoking"}, {ID: "new", Status: "active"}}, nil, 1800000000)
	if m.AttentionPeople != 1 || m.NoAccessPeople != 1 || !m.People[0].NeedsAttention {
		t.Fatalf("pending offboarding without credentials must still need attention: %+v", m)
	}
}
