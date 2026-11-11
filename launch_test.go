package main

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLaunchValidation(t *testing.T) {
	cases := []struct {
		name string
		opts LaunchOptions
		fail bool
	}{
		{"default_dhcp", LaunchOptions{}, false},
		{"static", LaunchOptions{IPMode: "static", IPv4: "10.10.10.20/24"}, false},
		{"bad_mode", LaunchOptions{IPMode: "typo"}, true},
		{"missing_ip", LaunchOptions{IPMode: "static"}, true},
		{"ipv6", LaunchOptions{IPMode: "static", IPv4: "::1"}, true},
		{"loopback", LaunchOptions{IPMode: "static", IPv4: "127.0.0.1/24"}, true},
		{"unspecified", LaunchOptions{IPMode: "static", IPv4: "0.0.0.0"}, true},
		{"shell_injection", LaunchOptions{IPMode: "static", IPv4: "10.0.0.1;id"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLaunch(&tc.opts)
			if (err != nil) != tc.fail {
				t.Fatalf("validation: %v", err)
			}
		})
	}
	opts := LaunchOptions{IPv4: "old value"}
	if err := validateLaunch(&opts); err != nil || opts.IPMode != "dhcp" || opts.IPv4 != "" {
		t.Fatal("disabled fields were not normalized")
	}
}

func TestLaunchTOMLKeysAndOptions(t *testing.T) {
	secret := base64.StdEncoding.EncodeToString([]byte("12345678901234567890123456789012"))
	raw, _ := base64.StdEncoding.DecodeString(secret)
	key, _ := ecdh.X25519().NewPrivateKey(raw)
	public := base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
	config := Config{NetworkName: "office\"\n\\network", PeerURL: "tcp://127.0.0.1:11010"}
	for _, fixed := range []bool{false, true} {
		opts := LaunchOptions{IPMode: "dhcp"}
		if fixed {
			opts.IPMode = "static"
			opts.IPv4 = "10.10.10.20/24"
		}
		text := launchTOML(config, secret, opts)
		for _, want := range []string{"local_private_key = " + tomlString(secret), "local_public_key = " + tomlString(public), "network_name = " + tomlString(config.NetworkName), "[[peer]]\nuri = " + tomlString(config.PeerURL)} {
			if !strings.Contains(text, want) {
				t.Fatalf("TOML is missing expected field")
			}
		}
		if strings.Contains(text, "network_secret") || strings.Contains(text, "REPLACE_WITH_") {
			t.Fatal("credential config contains master secret or placeholder")
		}
		if strings.Contains(text, "dhcp = false") != fixed || strings.Contains(text, "ipv4 = ") != fixed {
			t.Fatal("TOML IP mode differs from selected options")
		}
		if strings.Contains(text, "exit_nodes") || strings.Contains(text, "routes = ") || strings.Contains(text, "bind_device") {
			t.Fatal("TOML must not include VPN settings")
		}
	}
	for _, secret := range []string{"", "DEMO_ONLY_TEST"} {
		text := launchTOML(config, secret, LaunchOptions{IPMode: "dhcp"})
		if !strings.Contains(text, "REPLACE_WITH_CREDENTIAL_PRIVATE_KEY") || !strings.Contains(text, "REPLACE_WITH_MATCHING_PUBLIC_KEY") {
			t.Fatal("template must clearly mark both missing keys")
		}
	}
}

func TestLegacyVPNLaunchSettingsIgnored(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"version":1,"credentials":[{"id":"legacy","launch":{"ip_mode":"static","ipv4":"10.10.10.20/24","vpn":true,"exit_node":"10.10.10.1"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "manager.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	options := store.Snapshot().Credentials[0].Launch
	if options != (LaunchOptions{IPMode: "static", IPv4: "10.10.10.20/24"}) {
		t.Fatal("legacy data lost supported launch options")
	}
	for _, text := range []string{launchCommand(Config{}, "", options), launchTOML(Config{}, "", options)} {
		for _, field := range []string{"exit-nodes", "manual-routes", "bind-device", "exit_nodes", "routes =", "bind_device"} {
			if strings.Contains(text, field) {
				t.Fatalf("legacy data generated VPN settings: %s", field)
			}
		}
	}
	if err := store.Update(func(*State) error { return nil }); err != nil {
		t.Fatal(err)
	}
	bytes, err := os.ReadFile(store.path)
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Credentials []struct {
			Launch map[string]any `json:"launch"`
		} `json:"credentials"`
	}
	if err := json.Unmarshal(bytes, &saved); err != nil {
		t.Fatal(err)
	}
	if saved.Credentials[0].Launch["vpn"] != nil || saved.Credentials[0].Launch["exit_node"] != nil {
		t.Fatal("saving legacy data retained VPN settings")
	}
}

func TestLaunchSettingsPersistWithoutPrivateCredential(t *testing.T) {
	m, _ := testManager(t)
	p := addPerson(t, m)
	id := issue(t, m, p)
	opts := LaunchOptions{IPMode: "static", IPv4: "10.10.10.20/24"}
	result, err := m.SaveLaunch(context.Background(), id, opts)
	if err != nil {
		t.Fatal(err)
	}
	command := result["join_command"].(string)
	for _, fragment := range []string{"--ipv4 '10.10.10.20/24'", "--dhcp false", "${EASYTIER_CREDENTIAL:?"} {
		if !strings.Contains(command, fragment) {
			t.Fatalf("missing launch option: %s", fragment)
		}
	}
	if strings.Contains(command, "test-private-credential") {
		t.Fatal("existing credentials must generate a secret-free template")
	}
	if !strings.Contains(result["toml_config"].(string), "REPLACE_WITH_CREDENTIAL_PRIVATE_KEY") || !strings.Contains(result["toml_command"].(string), "--credential \"${EASYTIER_CREDENTIAL:?") {
		t.Fatal("existing credentials must offer matching secret-free TOML and startup templates")
	}
	if err := m.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	c, _ := findCredential(m.Store.Snapshot(), id)
	if c.Launch != opts {
		t.Fatal("sync discarded launch preferences")
	}
	store, err := openStore(m.Config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	c, _ = findCredential(store.Snapshot(), id)
	if c.Launch != opts {
		t.Fatal("restart discarded launch preferences")
	}
	bytes, err := os.ReadFile(m.Store.path)
	if err != nil || strings.Contains(string(bytes), "test-private-credential") {
		t.Fatal("private credential must not be persisted")
	}
	if err := m.Revoke(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveLaunch(context.Background(), id, opts); err == nil {
		t.Fatal("revoked credential must not get a launch command")
	}
}

func TestIssuanceUsesLaunchAndPreventsIPConflicts(t *testing.T) {
	m, _ := testManager(t)
	p := addPerson(t, m)
	first := IssueOptions{PersonID: p.ID, DeviceName: "固定设备", TTLDays: 30, Launch: LaunchOptions{IPMode: "static", IPv4: "10.10.10.20/24"}}
	result, err := m.Issue(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result["join_command"].(string), "--ipv4 '10.10.10.20/24'") {
		t.Fatal("issuance omitted selected fixed IP")
	}
	second := first
	second.Launch.IPv4 = "10.10.10.20"
	if _, err := m.Issue(context.Background(), second); err == nil {
		t.Fatal("same host with a different prefix must conflict")
	}
	second.Launch.IPv4 = "10.10.10.21"
	second.Reusable = true
	if _, err := m.Issue(context.Background(), second); err == nil {
		t.Fatal("fixed IP must not be assigned to a multi-device credential")
	}
	if len(m.Store.Snapshot().Credentials) != 1 {
		t.Fatal("invalid issuance created a credential")
	}
	id := result["credential_id"].(string)
	if _, err := m.Offboard(context.Background(), p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SaveLaunch(context.Background(), id, first.Launch); err == nil {
		t.Fatal("offboarded personnel must not generate a launch command")
	}
}
