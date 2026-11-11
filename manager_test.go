package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type fakeBackend struct {
	credentials       []RemoteCredential
	failList          bool
	failRevoke        map[string]bool
	ambiguousGenerate bool
}

func (b *fakeBackend) List(ctx context.Context) ([]RemoteCredential, error) {
	if b.failList {
		return nil, problem(503, "unavailable", "test connection unavailable")
	}
	return append([]RemoteCredential{}, b.credentials...), nil
}
func (b *fakeBackend) Generate(ctx context.Context, id string, o IssueOptions) (string, error) {
	b.credentials = append(b.credentials, RemoteCredential{ID: id, Groups: o.Groups, ExpiryUnix: now() + int64(o.TTLDays)*86400, Reusable: boolPointer(o.Reusable), AllowRelay: o.AllowRelay, AllowedProxyCIDRs: o.AllowedProxyCIDRs})
	if b.ambiguousGenerate {
		return "", problem(503, "cli_timeout", "test timeout after remote generation")
	}
	return fakeCredentialSecret(id), nil
}

func fakeCredentialSecret(id string) string {
	raw := sha256.Sum256([]byte(id))
	return base64.StdEncoding.EncodeToString(raw[:])
}
func (b *fakeBackend) Revoke(ctx context.Context, id string) error {
	if b.failRevoke[id] {
		return problem(503, "cli_failed", "test remote revoke failed")
	}
	for i, c := range b.credentials {
		if c.ID == id {
			b.credentials = append(b.credentials[:i], b.credentials[i+1:]...)
			break
		}
	}
	return nil
}
func testManager(t *testing.T) (*Manager, *fakeBackend) {
	t.Helper()
	c := Config{DataDir: t.TempDir(), NetworkName: "office", PeerURL: "tcp://example.com:11010", Listen: "127.0.0.1", Port: 8989, PublicURL: "http://127.0.0.1:8989"}
	b := &fakeBackend{failRevoke: map[string]bool{}}
	m, err := newManager(c, b)
	if err != nil {
		t.Fatal(err)
	}
	return m, b
}
func addPerson(t *testing.T, m *Manager) Person {
	t.Helper()
	p, err := m.SavePerson("", PersonInput{Name: "测试员工", Team: "研发"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func issue(t *testing.T, m *Manager, p Person) string {
	t.Helper()
	result, err := m.Issue(context.Background(), IssueOptions{PersonID: p.ID, DeviceName: "工作电脑", TTLDays: 7, Groups: []string{"staff"}})
	if err != nil {
		t.Fatal(err)
	}
	return result["credential_id"].(string)
}

func TestOffboardingRetainsPartialFailureAndBlocksIssuance(t *testing.T) {
	m, b := testManager(t)
	p := addPerson(t, m)
	first, second := issue(t, m, p), issue(t, m, p)
	b.failRevoke[second] = true
	r, err := m.Offboard(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.Complete || r.Status != "revoking" || len(r.Revoked) != 1 || r.Revoked[0] != first || len(r.Failures) != 1 {
		t.Fatalf("unexpected partial outcome: %#v", r)
	}
	if _, err = m.Issue(context.Background(), IssueOptions{PersonID: p.ID, DeviceName: "旁路设备", TTLDays: 7}); err == nil {
		t.Fatal("issuing to offboarding person must fail")
	}
	delete(b.failRevoke, second)
	r, err = m.Offboard(context.Background(), p.ID)
	if err != nil || !r.Complete || r.Status != "offboarded" {
		t.Fatalf("retry failed: %#v %v", r, err)
	}
	if err = m.Restore(p.ID); err != nil {
		t.Fatal(err)
	}
	for _, c := range m.Store.Snapshot().Credentials {
		if c.Status != "revoked" {
			t.Fatalf("restoring person resurrected credential: %#v", c)
		}
	}
}

func TestUnknownIssuanceIsTrackedAndSecretNeverPersisted(t *testing.T) {
	m, b := testManager(t)
	p := addPerson(t, m)
	b.ambiguousGenerate = true
	_, err := m.Issue(context.Background(), IssueOptions{PersonID: p.ID, DeviceName: "中断签发", TTLDays: 7})
	if err == nil {
		t.Fatal("expected timeout")
	}
	s := m.Store.Snapshot()
	if len(s.Credentials) != 1 || s.Credentials[0].Status != "issue_unconfirmed" {
		t.Fatal("uncertain issuance was not retained")
	}
	if err = m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	s = m.Store.Snapshot()
	if s.Credentials[0].Status != "active" {
		t.Fatal("remote issuance was not reconciled")
	}
	b.ambiguousGenerate = false
	issue(t, m, p)
	bytes, err := os.ReadFile(m.Store.path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(bytes), "credential_secret") {
		t.Fatal("secret persisted")
	}
	for _, c := range m.Store.Snapshot().Credentials {
		if strings.Contains(string(bytes), fakeCredentialSecret(c.ID)) {
			t.Fatal("private credential persisted")
		}
	}
}

func TestMissingCredentialIsNotMisreportedAsRevoked(t *testing.T) {
	m, b := testManager(t)
	p := addPerson(t, m)
	cid := issue(t, m, p)
	b.credentials = nil
	if err := m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, err := findCredential(m.Store.Snapshot(), cid)
	if err != nil || c.Status != "missing" {
		t.Fatalf("unexpected state %#v %v", c, err)
	}
}

func TestFailedSyncDoesNotDiscardOrCompleteOffboarding(t *testing.T) {
	m, b := testManager(t)
	p := addPerson(t, m)
	issue(t, m, p)
	b.failList = true
	if _, err := m.Offboard(context.Background(), p.ID); err == nil {
		t.Fatal("expected failure")
	}
	s := m.Store.Snapshot()
	person, _ := findPerson(s, p.ID)
	if person.Status != "revoking" || s.Credentials[0].Status != "active" {
		t.Fatal("failure altered permission facts")
	}
	if m.Snapshot()["connection"].(Connection).Connected {
		t.Fatal("disconnected state hidden")
	}
}

func TestAtomicStoreRollbackConcurrencyAndReload(t *testing.T) {
	s, err := openStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	err = s.Update(func(v *State) error { v.Admin.Username = "should-not-commit"; return errors.New("cancel") })
	if err == nil || s.Snapshot().Admin.Username != "" {
		t.Fatal("failed transaction committed")
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Update(func(v *State) error { audit(v, "test", "person", ""); return nil }); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	reloaded, err := openStore(filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Snapshot().Audit) != 20 {
		t.Fatal("lost updates")
	}
	info, err := os.Stat(s.path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("data file permissions are not private")
	}
}

func TestHTTPAuthenticationCSRFOriginAndAssets(t *testing.T) {
	m, backend := testManager(t)
	m.Backend = &fakePeerBackend{fakeBackend: backend, peers: []Peer{{ID: "1", Hostname: "server", Cost: "Local"}, {ID: "2", Hostname: "client", Cost: "p2p", IPv4: "10.10.10.2"}}}
	hash, err := hashPassword("test-admin-pass-2026")
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Store.Update(func(s *State) error { s.Admin = Admin{Username: "admin", PasswordHash: hash}; return nil }); err != nil {
		t.Fatal(err)
	}
	assets, _ := fs.Sub(static, "static")
	web := newWeb(m, assets)
	server := httptest.NewServer(web)
	defer server.Close()
	m.Config.PublicURL = server.URL
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	request := func(method, path, body, csrf, origin string) (int, map[string]any) {
		t.Helper()
		r, _ := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if csrf != "" {
			r.Header.Set("X-CSRF-Token", csrf)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		result := map[string]any{}
		_ = json.NewDecoder(resp.Body).Decode(&result)
		return resp.StatusCode, result
	}
	status, _ := request("GET", "/api/state", "", "", "")
	if status != 401 {
		t.Fatal("state was public")
	}
	status, _ = request("GET", "/api/peers", "", "", "")
	if status != 401 {
		t.Fatal("peer state was public")
	}
	status, login := request("POST", "/api/login", `{"username":"admin","password":"test-admin-pass-2026"}`, "", server.URL)
	if status != 200 {
		t.Fatalf("login failed: %d %#v", status, login)
	}
	csrf := login["csrf"].(string)
	status, _ = request("POST", "/api/peers/sync", `{}`, "", server.URL)
	if status != 403 {
		t.Fatal("peer sync accepted missing CSRF")
	}
	status, peerState := request("POST", "/api/peers/sync", `{}`, csrf, server.URL)
	if status != 200 || len(peerState["items"].([]any)) != 1 {
		t.Fatal("authorized peer sync failed")
	}
	status, _ = request("POST", "/api/people", `{"name":"staff"}`, "", server.URL)
	if status != 403 {
		t.Fatal("missing CSRF accepted")
	}
	status, _ = request("POST", "/api/people", `{"name":"staff"}`, csrf, "https://untrusted.example")
	if status != 403 {
		t.Fatal("cross-site request accepted")
	}
	status, _ = request("POST", "/api/people", `{"name":"staff"}`, csrf, server.URL)
	if status != 201 {
		t.Fatal("authorized mutation failed")
	}
	status, state := request("GET", "/api/state", "", "", "")
	monitor, ok := state["monitor"].(map[string]any)
	if status != 200 || !ok || monitor["people_total"] != float64(1) || monitor["no_access_people"] != float64(1) || monitor["online_available"] != false {
		t.Fatalf("authenticated monitor snapshot is incorrect: %d %#v", status, monitor)
	}
	id := issue(t, m, m.Store.Snapshot().People[0])
	launchPath := "/api/credentials/" + id + "/launch"
	status, _ = request("POST", launchPath, `{"ip_mode":"dhcp"}`, "", server.URL)
	if status != 403 {
		t.Fatal("launch settings accepted missing CSRF")
	}
	status, _ = request("POST", launchPath, `{"ip_mode":"static","ipv4":"not-an-ip"}`, csrf, server.URL)
	if status != 400 {
		t.Fatal("launch settings accepted invalid IP")
	}
	status, launch := request("POST", launchPath, `{"ip_mode":"dhcp","vpn":true,"exit_node":"10.10.10.1"}`, csrf, server.URL)
	if status != 200 || !strings.Contains(launch["join_command"].(string), "${EASYTIER_CREDENTIAL:?") || launch["credential_secret"] != nil {
		t.Fatalf("launch template response incorrect: %d", status)
	}
	for _, field := range []string{"--exit-nodes", "--manual-routes", "--bind-device", "exit_nodes", "routes =", "bind_device"} {
		if strings.Contains(launch["join_command"].(string), field) || strings.Contains(launch["toml_config"].(string), field) {
			t.Fatalf("legacy HTTP launch request retained VPN field: %s", field)
		}
	}
	if options := launch["launch"].(map[string]any); options["vpn"] != nil || options["exit_node"] != nil {
		t.Fatal("legacy HTTP launch fields must be discarded")
	}
	resp, err := client.Get(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "/app.js") || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("embedded asset or CSP missing")
	}
	request("POST", "/api/logout", `{}`, csrf, server.URL)
	status, _ = request("GET", "/api/state", "", "", "")
	if status != 401 {
		t.Fatal("logout did not revoke session")
	}
	status, _ = request("GET", "/api/peers", "", "", "")
	if status != 401 {
		t.Fatal("logout did not revoke peer access")
	}
}

func TestCLIRejectsMultipleInstancesBeforeMutation(t *testing.T) {
	if os.PathSeparator != '/' {
		t.Skip("Unix test helper")
	}
	file := filepath.Join(t.TempDir(), "cli")
	if err := os.WriteFile(file, []byte("#!/bin/sh\nprintf '%s' '[{\"instance_name\":\"a\",\"result\":{\"credentials\":[]}}]'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cli := &CLI{Config: Config{CLIPath: file, RPCPortal: "127.0.0.1:1", CLITimeout: 2}}
	_, err := cli.List(context.Background())
	var api *APIError
	if !errors.As(err, &api) || api.Code != "ambiguous_instance" {
		t.Fatalf("unexpected result: %v", err)
	}
}

func availablePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}
