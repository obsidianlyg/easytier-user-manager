package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNativeEasyTierCredentialLifecycle(t *testing.T) {
	corePath, cliPath := os.Getenv("ET_TEST_CORE"), os.Getenv("ET_TEST_CLI")
	if corePath == "" || cliPath == "" {
		t.Skip("Set ET_TEST_CORE and ET_TEST_CLI for isolated native integration")
	}
	directory := t.TempDir()
	rpc, listener := availablePort(t), availablePort(t)
	credentialPath := filepath.Join(directory, "core-credentials.json")
	config := fmt.Sprintf("hostname = \"access-integration\"\nrpc_portal = %q\nlisteners = [%q]\ncredential_file = %q\n[network_identity]\nnetwork_name = \"access-integration\"\nnetwork_secret = \"isolated-test-only-0ad89b\"\n[secure_mode]\nenabled = true\n[flags]\nno_tun = true\n", rpc, "tcp://"+listener, credentialPath)
	configPath := filepath.Join(directory, "core.toml")
	if err := os.WriteFile(configPath, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(directory, "core.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd := exec.Command(corePath, "-c", configPath, "--secure-mode", "true", "-r", rpc)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	deadline := time.Now().Add(15 * time.Second)
	for {
		conn, err := net.DialTimeout("tcp", rpc, 100*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("isolated core RPC did not start")
		}
		time.Sleep(50 * time.Millisecond)
	}
	cli := &CLI{Config: Config{CLIPath: cliPath, RPCPortal: rpc, NetworkName: "access-integration", CLITimeout: 5}}
	ctx := context.Background()
	for {
		_, err = cli.List(ctx)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("credential service did not start:", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	c := Config{DataDir: filepath.Join(directory, "panel-data"), NetworkName: "access-integration", PeerURL: "tcp://" + listener}
	m, err := newManager(c, cli)
	if err != nil {
		t.Fatal(err)
	}
	p := addPerson(t, m)
	result, err := m.Issue(ctx, IssueOptions{PersonID: p.ID, DeviceName: "真实 CLI 测试设备", TTLDays: 1, Groups: []string{"staff"}})
	if err != nil {
		t.Fatal("real issuance:", err)
	}
	cid := result["credential_id"].(string)
	clientConfig := launchTOML(c, result["credential_secret"].(string), LaunchOptions{IPMode: "dhcp"})
	clientConfig = "hostname = \"toml-credential-client\"\nlisteners = []\n" + strings.Replace(clientConfig, "no_tun = false", "no_tun = true", 1)
	clientPath := filepath.Join(directory, "client.toml")
	if err := os.WriteFile(clientPath, []byte(clientConfig), 0600); err != nil {
		t.Fatal(err)
	}
	client := exec.Command(corePath, "-c", clientPath, "--credential", result["credential_secret"].(string), "-r", availablePort(t))
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Process.Kill(); _ = client.Wait() }()
	connected := false
	deadline = time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		output, err := exec.CommandContext(ctx, cliPath, "-p", rpc, "-o", "json", "peer").Output()
		cancel()
		var peers []struct {
			Hostname string `json:"hostname"`
			Cost     string `json:"cost"`
		}
		if err == nil && json.Unmarshal(output, &peers) == nil {
			for _, peer := range peers {
				if peer.Hostname == "toml-credential-client" && peer.Cost != "Local" {
					connected = true
				}
			}
		}
		if connected {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !connected {
		t.Fatal("generated TOML client did not complete real credential handshake")
	}
	if err := m.SyncPeers(ctx); err != nil {
		t.Fatal("native peer synchronization:", err)
	}
	peerSnapshot := m.PeerSnapshot()
	direct := false
	for _, peer := range peerSnapshot.Items {
		if peer.Hostname == "toml-credential-client" && peer.Status == "direct" {
			direct = true
			if peer.PersonID != p.ID || peer.CredentialID != cid || peer.IdentityStatus != "verified" || peer.BindingSource != "issued" {
				t.Fatalf("native authenticated identity did not match issued credential: %+v", peer)
			}
		}
	}
	if !peerSnapshot.Connection.Connected || len(peerSnapshot.Items) != 1 || !direct {
		t.Fatal("native peer list did not identify the connected remote node")
	}
	if err = m.Sync(ctx); err != nil {
		t.Fatal("real synchronization:", err)
	}
	monitor := m.Snapshot()["monitor"].(Monitor)
	if monitor.ActiveCredentials != 1 || monitor.ExpiringCredentials != 1 || monitor.People[0].PersonID != p.ID {
		t.Fatalf("real credential missing from monitor: %+v", monitor)
	}
	credential, err := findCredential(m.Store.Snapshot(), cid)
	if err != nil || credential.Status != "active" || credential.Reusable || len(credential.Groups) != 1 || credential.Groups[0] != "staff" {
		b, _ := json.Marshal(credential)
		t.Fatalf("remote fields mismatch: %s %v", b, err)
	}
	if err = m.Revoke(ctx, cid); err != nil {
		t.Fatal("real revocation:", err)
	}
	monitor = m.Snapshot()["monitor"].(Monitor)
	if monitor.ActiveCredentials != 0 || monitor.NoAccessPeople != 1 {
		t.Fatalf("monitor retained revoked access: %+v", monitor)
	}
	remote, err := cli.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range remote {
		if v.ID == cid {
			t.Fatal("revoked credential still active on core")
		}
	}
	if _, err = os.Stat(credentialPath); err != nil {
		t.Fatal("core credential persistence file missing")
	}
	_ = client.Process.Kill()
	_ = client.Wait()
	deadline = time.Now().Add(6 * time.Second)
	for {
		if err := m.SyncPeers(ctx); err != nil {
			t.Fatal(err)
		}
		if len(m.PeerSnapshot().Items) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected peer retained in current node list")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestNativeEasyTierLaunchOptions(t *testing.T) {
	corePath := os.Getenv("ET_TEST_CORE")
	if corePath == "" {
		t.Skip("Set ET_TEST_CORE for native launch validation")
	}
	configPath := filepath.Join(t.TempDir(), "core.toml")
	if err := os.WriteFile(configPath, []byte("hostname = \"isolated-launch-check\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"--dhcp", "true"},
		{"--ipv4", "10.10.10.20/24", "--dhcp", "false"},
	} {
		full := append([]string{"--check-config", "--config-file", configPath, "--network-name", "isolated-launch-check", "--secure-mode", "true"}, args...)
		if err := exec.Command(corePath, full...).Run(); err != nil {
			t.Fatalf("official core rejected launch flags: %v", err)
		}
	}
	secret := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	for _, fixed := range []bool{false, true} {
		options := LaunchOptions{IPMode: "dhcp"}
		if fixed {
			options.IPMode, options.IPv4 = "static", "10.10.10.20/24"
		}
		text := launchTOML(Config{NetworkName: "isolated-launch-check", PeerURL: "tcp://127.0.0.1:11010"}, secret, options)
		if err := os.WriteFile(configPath, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		if err := exec.Command(corePath, "--check-config", "-c", configPath).Run(); err != nil {
			t.Fatal("official core rejected generated TOML")
		}
	}
}
