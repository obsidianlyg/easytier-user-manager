package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"
)

type LaunchOptions struct {
	IPMode string `json:"ip_mode"`
	IPv4   string `json:"ipv4"`
}

func launchIPv4(value string, allowPrefix bool) (netip.Addr, bool) {
	if allowPrefix && strings.Contains(value, "/") {
		prefix, err := netip.ParsePrefix(value)
		if err != nil || prefix.Bits() == 0 || !prefix.Addr().Is4() || !prefix.Addr().IsGlobalUnicast() {
			return netip.Addr{}, false
		}
		return prefix.Addr(), true
	}
	addr, err := netip.ParseAddr(value)
	return addr, err == nil && addr.Is4() && addr.IsGlobalUnicast()
}

func validateLaunch(o *LaunchOptions) error {
	if o.IPMode == "" {
		o.IPMode = "dhcp"
	}
	if o.IPMode != "dhcp" && o.IPMode != "static" {
		return problem(400, "invalid_launch", "请选择自动分配或固定虚拟 IP。")
	}
	o.IPv4 = strings.TrimSpace(o.IPv4)
	if o.IPMode == "static" {
		if _, ok := launchIPv4(o.IPv4, true); !ok {
			return problem(400, "invalid_launch", "请填写有效的固定虚拟 IPv4，例如 10.10.10.20 或 10.10.10.20/24。")
		}
	} else {
		o.IPv4 = ""
	}
	return nil
}

func checkLaunchConflict(s State, id string, o LaunchOptions, reusable bool) error {
	if o.IPMode != "static" {
		return nil
	}
	if reusable {
		return problem(400, "static_reusable", "固定虚拟 IP 请使用独立设备凭据，关闭多设备同时复用。")
	}
	want, _ := launchIPv4(o.IPv4, true)
	for _, c := range s.Credentials {
		if c.ID == id || c.Status == "revoked" || (c.ExpiresAt > 0 && c.ExpiresAt <= now()) || c.Launch.IPMode != "static" {
			continue
		}
		addr, ok := launchIPv4(c.Launch.IPv4, true)
		if ok && addr == want {
			return problem(409, "ip_conflict", "此虚拟 IP 已被其他未失效凭据的启动设置使用，请选择其他 IP。")
		}
	}
	return nil
}

func launchCommand(c Config, secret string, o LaunchOptions) string {
	credential := shellQuote(secret)
	if secret == "" {
		credential = `"${EASYTIER_CREDENTIAL:?请先设置原凭据私钥}"`
	}
	command := fmt.Sprintf("easytier-core --network-name %s --secure-mode true --credential %s -p %s", shellQuote(c.NetworkName), credential, shellQuote(c.PeerURL))
	if o.IPMode == "static" {
		command += " --ipv4 " + shellQuote(o.IPv4) + " --dhcp false"
	} else {
		command += " --dhcp true"
	}
	return command
}

func tomlString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func launchTOMLCommand(secret string) string {
	credential := shellQuote(secret)
	if secret == "" {
		credential = `"${EASYTIER_CREDENTIAL:?请先设置原凭据私钥}"`
	}
	// 2.6.4 normalizes a missing network secret to an empty secret when loading
	// TOML; --credential is required to restore the credential-node identity.
	return "easytier-core -c client.toml --credential " + credential
}

func launchTOML(c Config, secret string, o LaunchOptions) string {
	private, public := "REPLACE_WITH_CREDENTIAL_PRIVATE_KEY", "REPLACE_WITH_MATCHING_PUBLIC_KEY"
	if key := credentialPublicKey(secret); key != "" {
		private, public = secret, key
	}
	var b strings.Builder
	b.WriteString("# 保存为 client.toml，使用面板提供的 TOML 配套启动命令。\n")
	b.WriteString("# EasyTier 2.6.4 必须同时传入 --credential 才能以凭据身份连接。\n")
	b.WriteString("# 不要附加 --secure-mode true，它可能覆盖文件中的凭据密钥。\n")
	if private == "REPLACE_WITH_CREDENTIAL_PRIVATE_KEY" {
		b.WriteString("# 模板：必须替换下面的私钥和匹配公钥；演示凭据不能连接真实网络。\n")
	}
	fmt.Fprintf(&b, "dhcp = %t\n", o.IPMode != "static")
	if o.IPMode == "static" {
		fmt.Fprintf(&b, "ipv4 = %s\n", tomlString(o.IPv4))
	}
	fmt.Fprintf(&b, "\n[network_identity]\nnetwork_name = %s\n", tomlString(c.NetworkName))
	fmt.Fprintf(&b, "\n[[peer]]\nuri = %s\n", tomlString(c.PeerURL))
	fmt.Fprintf(&b, "\n[secure_mode]\nenabled = true\nlocal_private_key = %s\nlocal_public_key = %s\n", tomlString(private), tomlString(public))
	b.WriteString("\n[flags]\nno_tun = false\n")
	return b.String()
}

func (m *Manager) SaveLaunch(ctx context.Context, id string, options LaunchOptions) (map[string]any, error) {
	if err := validateLaunch(&options); err != nil {
		return nil, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	if err := m.sync(ctx); err != nil {
		return nil, err
	}
	s := m.Store.Snapshot()
	c, err := findCredential(s, id)
	if err != nil {
		return nil, err
	}
	if c.Status != "active" || c.ExpiresAt <= now() {
		return nil, problem(409, "credential_inactive", "此凭据尚未确认有效、已撤销或已到期，不能生成接入命令。")
	}
	if c.PersonID != "" {
		p, err := findPerson(s, c.PersonID)
		if err != nil || p.Status != "active" {
			return nil, problem(409, "person_disabled", "归属人员已停用或正在撤权，不能生成接入命令。")
		}
	}
	if err := checkLaunchConflict(s, id, options, c.Reusable); err != nil {
		return nil, err
	}
	err = m.Store.Update(func(state *State) error {
		for i := range state.Credentials {
			if state.Credentials[i].ID == id {
				state.Credentials[i].Launch = options
				state.Credentials[i].UpdatedAt = now()
			}
		}
		audit(state, "launch_updated", c.DeviceName, c.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"join_command": launchCommand(m.Config, "", options), "toml_config": launchTOML(m.Config, "", options), "toml_command": launchTOMLCommand(""), "launch": options, "device_name": c.DeviceName, "credential_id": c.ID, "expires_at": c.ExpiresAt, "demo": m.Config.Demo}, nil
}
