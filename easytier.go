package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type IssueOptions struct {
	PersonID          string        `json:"person_id"`
	DeviceName        string        `json:"device_name"`
	TTLDays           int           `json:"ttl_days"`
	Groups            []string      `json:"groups"`
	AllowRelay        bool          `json:"allow_relay"`
	Reusable          bool          `json:"reusable"`
	AllowedProxyCIDRs []string      `json:"allowed_proxy_cidrs"`
	Launch            LaunchOptions `json:"launch"`
}

type Backend interface {
	List(context.Context) ([]RemoteCredential, error)
	Generate(context.Context, string, IssueOptions) (string, error)
	Revoke(context.Context, string) error
}

type CLI struct{ Config Config }

func (c *CLI) runJSON(ctx context.Context, args ...string) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(c.Config.CLITimeout)*time.Second)
	defer cancel()
	full := []string{"-p", c.Config.RPCPortal, "-o", "json"}
	if c.Config.InstanceName != "" {
		full = append(full, "-n", c.Config.InstanceName)
	}
	full = append(full, args...)
	cmd := exec.CommandContext(ctx, c.Config.CLIPath, full...)
	cmd.Env = append(os.Environ(), "NO_COLOR=1")
	var out, diagnostics bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	err := cmd.Run()
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
			return nil, problem(503, "cli_missing", "找不到 easytier-cli，请检查服务器上的 cli_path。")
		}
		if ctx.Err() != nil {
			return nil, problem(503, "cli_timeout", "EasyTier 请求超时，请核实 core 和 RPC 地址；可同步确认操作结果。")
		}
		// Diagnostics and output may contain credential material: never return them to HTTP or logs.
		return nil, problem(502, "cli_failed", "EasyTier 操作失败，请核实配套 CLI、安全模式、RPC 地址和实例名称。")
	}
	raw := bytes.TrimSpace(out.Bytes())
	if !json.Valid(raw) {
		return nil, problem(502, "invalid_cli_json", "CLI 没有返回有效 JSON，请确认支持 -o json 和 credential。")
	}
	return json.RawMessage(raw), nil
}

func (c *CLI) command(ctx context.Context, args ...string) (json.RawMessage, error) {
	raw, err := c.runJSON(ctx, args...)
	if err != nil {
		return nil, err
	}
	if len(raw) > 0 && raw[0] == '[' {
		return nil, problem(409, "ambiguous_instance", "RPC 对应多个实例，请在配置文件设置 instance_name 后再操作。")
	}
	if len(raw) == 0 || raw[0] != '{' {
		return nil, problem(502, "invalid_cli_json", "CLI 返回的 JSON 结构不正确。")
	}
	return json.RawMessage(raw), nil
}

func (c *CLI) List(ctx context.Context) ([]RemoteCredential, error) {
	b, err := c.command(ctx, "credential", "list")
	if err != nil {
		return nil, err
	}
	var result struct {
		Credentials []RemoteCredential `json:"credentials"`
	}
	if err = json.Unmarshal(b, &result); err != nil {
		return nil, problem(502, "invalid_cli_json", "凭据列表格式不正确，本次同步未写入。")
	}
	seen := map[string]bool{}
	for _, v := range result.Credentials {
		if !validID(v.ID) || v.ExpiryUnix <= 0 || seen[v.ID] {
			return nil, problem(502, "invalid_cli_json", "凭据记录格式不正确，本次同步未写入。")
		}
		seen[v.ID] = true
	}
	return result.Credentials, nil
}

func (c *CLI) Generate(ctx context.Context, id string, o IssueOptions) (string, error) {
	args := []string{"credential", "generate", "--credential-id", id, "--ttl", strconv.Itoa(o.TTLDays * 86400), "--reusable", strconv.FormatBool(o.Reusable)}
	if len(o.Groups) > 0 {
		args = append(args, "--groups", strings.Join(o.Groups, ","))
	}
	if o.AllowRelay {
		args = append(args, "--allow-relay")
	}
	if len(o.AllowedProxyCIDRs) > 0 {
		args = append(args, "--allowed-proxy-cidrs", strings.Join(o.AllowedProxyCIDRs, ","))
	}
	b, err := c.command(ctx, args...)
	if err != nil {
		return "", err
	}
	var result struct {
		ID     string `json:"credential_id"`
		Secret string `json:"credential_secret"`
	}
	if json.Unmarshal(b, &result) != nil || result.ID != id || result.Secret == "" {
		return "", problem(502, "issue_unconfirmed", "签发结果未确认，请同步核实；已保留记录。")
	}
	return result.Secret, nil
}

func (c *CLI) Revoke(ctx context.Context, id string) error {
	b, err := c.command(ctx, "credential", "revoke", id)
	if err != nil {
		return err
	}
	var result struct {
		Success bool `json:"success"`
	}
	if json.Unmarshal(b, &result) != nil || !result.Success {
		return problem(409, "revoke_unconfirmed", "节点未确认撤销成功，请同步检查目标实例和凭据状态。")
	}
	return nil
}

type DemoBackend struct{ Store *Store }

func (d *DemoBackend) List(ctx context.Context) ([]RemoteCredential, error) {
	return d.Store.Snapshot().DemoCredentials, nil
}
func (d *DemoBackend) Generate(ctx context.Context, id string, o IssueOptions) (string, error) {
	err := d.Store.Update(func(s *State) error {
		s.DemoCredentials = append(s.DemoCredentials, RemoteCredential{ID: id, Groups: o.Groups, AllowRelay: o.AllowRelay, Reusable: boolPointer(o.Reusable), ExpiryUnix: now() + int64(o.TTLDays)*86400, AllowedProxyCIDRs: o.AllowedProxyCIDRs})
		return nil
	})
	return "DEMO_ONLY_" + strings.ReplaceAll(newID(), "-", ""), err
}
func (d *DemoBackend) Revoke(ctx context.Context, id string) error {
	return d.Store.Update(func(s *State) error {
		for i, v := range s.DemoCredentials {
			if v.ID == id {
				s.DemoCredentials = append(s.DemoCredentials[:i], s.DemoCredentials[i+1:]...)
				return nil
			}
		}
		return nil
	})
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
