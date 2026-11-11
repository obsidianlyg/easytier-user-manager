package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type Config struct {
	Listen       string `json:"listen"`
	Port         int    `json:"port"`
	PublicURL    string `json:"public_url"`
	DataDir      string `json:"data_dir"`
	CLIPath      string `json:"cli_path"`
	RPCPortal    string `json:"rpc_portal"`
	InstanceName string `json:"instance_name"`
	NetworkName  string `json:"network_name"`
	PeerURL      string `json:"peer_url"`
	CLITimeout   int    `json:"cli_timeout"`
	Demo         bool   `json:"demo"`
}

func loadConfig(path string, demo bool, port int) (Config, error) {
	c := Config{Listen: "127.0.0.1", Port: 8989, PublicURL: "http://127.0.0.1:8989",
		DataDir: "./data", CLIPath: "/usr/local/bin/easytier-cli", RPCPortal: "127.0.0.1:15888",
		NetworkName: "office", PeerURL: "tcp://vps.obsidianlyg.top:11010", CLITimeout: 15}
	base, err := os.Getwd()
	if err != nil {
		return c, err
	}
	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		if err = json.Unmarshal(content, &c); err != nil {
			return c, err
		}
		abs, err := filepath.Abs(path)
		if err != nil {
			return c, err
		}
		base = filepath.Dir(abs)
	}
	if port != 0 {
		c.Port = port
		c.PublicURL = fmt.Sprintf("http://127.0.0.1:%d", port)
	}
	c.Demo = demo
	if demo {
		c.Listen = "127.0.0.1"
		c.DataDir = filepath.Join(base, "data-demo")
		c.PublicURL = fmt.Sprintf("http://127.0.0.1:%d", c.Port)
	}
	if !filepath.IsAbs(c.DataDir) {
		c.DataDir = filepath.Join(base, c.DataDir)
	}
	c.PublicURL = strings.TrimRight(c.PublicURL, "/")
	u, err := url.Parse(c.PublicURL)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Path != "" || u.RawQuery != "" || u.User != nil {
		return c, errors.New("public_url 必须是没有路径的 http(s) 地址")
	}
	if c.Port < 1 || c.Port > 65535 || c.CLITimeout < 1 || c.CLITimeout > 120 {
		return c, errors.New("端口或 cli_timeout 不正确")
	}
	return c, nil
}

type Person struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Team      string `json:"team"`
	Contact   string `json:"contact"`
	Notes     string `json:"notes"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

type Credential struct {
	PublicKey         string        `json:"public_key,omitempty"`
	PublicKeyScope    string        `json:"public_key_scope,omitempty"`
	PublicKeySource   string        `json:"public_key_source,omitempty"`
	ID                string        `json:"id"`
	PersonID          string        `json:"person_id"`
	DeviceName        string        `json:"device_name"`
	Groups            []string      `json:"groups"`
	AllowedProxyCIDRs []string      `json:"allowed_proxy_cidrs"`
	AllowRelay        bool          `json:"allow_relay"`
	Reusable          bool          `json:"reusable"`
	ExpiresAt         int64         `json:"expires_at"`
	Status            string        `json:"status"`
	Source            string        `json:"source"`
	CreatedAt         int64         `json:"created_at"`
	UpdatedAt         int64         `json:"updated_at"`
	Launch            LaunchOptions `json:"launch"`
}

type RemoteCredential struct {
	ID                string   `json:"credential_id"`
	Groups            []string `json:"groups"`
	AllowRelay        bool     `json:"allow_relay"`
	Reusable          *bool    `json:"reusable"`
	ExpiryUnix        int64    `json:"expiry_unix"`
	AllowedProxyCIDRs []string `json:"allowed_proxy_cidrs"`
}

type Audit struct {
	ID        int64  `json:"id"`
	CreatedAt int64  `json:"created_at"`
	Action    string `json:"action"`
	Subject   string `json:"subject"`
	Detail    string `json:"detail"`
}

type Admin struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}
type State struct {
	PeerHistory     *PeerHistory       `json:"peer_history,omitempty"`
	Version         int                `json:"version"`
	Admin           Admin              `json:"admin"`
	People          []Person           `json:"people"`
	Credentials     []Credential       `json:"credentials"`
	Audit           []Audit            `json:"audit"`
	DemoCredentials []RemoteCredential `json:"demo_credentials,omitempty"`
	AuditSequence   int64              `json:"audit_sequence"`
}

type Connection struct {
	Connected bool   `json:"connected"`
	LastSync  int64  `json:"last_sync"`
	Error     string `json:"error"`
}
type APIError struct {
	Message string `json:"error"`
	Code    string `json:"code"`
	Status  int    `json:"-"`
}

func (e *APIError) Error() string { return e.Message }
func problem(status int, code, message string) *APIError {
	return &APIError{Message: message, Code: code, Status: status}
}

var idPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var groupPattern = regexp.MustCompile(`^[\p{L}\p{N}_.:-]{1,64}$`)

func validID(id string) bool { return idPattern.MatchString(id) }
func newID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	x := hex.EncodeToString(b)
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:]
}
func now() int64               { return time.Now().Unix() }
func boolPointer(v bool) *bool { return &v }
func boolDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}
func cleanText(value, label string, max int, required bool) (string, error) {
	value = strings.TrimSpace(value)
	if (required && value == "") || len([]rune(value)) > max || strings.ContainsRune(value, 0) {
		return "", problem(400, "invalid_request", label+"不能为空或过长")
	}
	return value, nil
}

func audit(s *State, action, subject, detail string) {
	s.AuditSequence++
	s.Audit = append(s.Audit, Audit{ID: s.AuditSequence, CreatedAt: now(), Action: action, Subject: subject, Detail: detail})
	if len(s.Audit) > 5000 {
		s.Audit = s.Audit[len(s.Audit)-5000:]
	}
}

func findPerson(s State, id string) (Person, error) {
	for _, p := range s.People {
		if p.ID == id {
			return p, nil
		}
	}
	return Person{}, problem(404, "not_found", "人员不存在")
}
func findCredential(s State, id string) (Credential, error) {
	for _, c := range s.Credentials {
		if c.ID == id {
			return c, nil
		}
	}
	return Credential{}, problem(404, "not_found", "凭据不存在")
}
