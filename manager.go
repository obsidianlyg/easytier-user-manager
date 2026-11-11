package main

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"sync"
)

type Manager struct {
	Config         Config
	Store          *Store
	Backend        Backend
	op             sync.Mutex
	connectionMu   sync.RWMutex
	connection     Connection
	peerMu         sync.RWMutex
	peerConnection Connection
}

func newManager(c Config, backend Backend) (*Manager, error) {
	store, err := openStore(c.DataDir)
	if err != nil {
		return nil, err
	}
	m := &Manager{Config: c, Store: store, Backend: backend, connection: Connection{Error: "尚未连接，请同步节点凭据。"}}
	if m.Backend == nil {
		if c.Demo {
			m.Backend = &DemoBackend{Store: store}
		} else {
			m.Backend = &CLI{Config: c}
		}
	}
	if c.Demo {
		if err = m.seedDemo(); err != nil {
			return nil, err
		}
		if err = m.sync(context.Background()); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func (m *Manager) sync(ctx context.Context) error {
	credentials, err := m.Backend.List(ctx)
	if err != nil {
		m.connectionMu.Lock()
		m.connection.Connected = false
		m.connection.Error = err.Error()
		m.connectionMu.Unlock()
		return err
	}
	err = m.Store.Update(func(s *State) error {
		index := map[string]int{}
		found := map[string]bool{}
		for i, c := range s.Credentials {
			index[c.ID] = i
		}
		for _, remote := range credentials {
			found[remote.ID] = true
			status := "active"
			if remote.ExpiryUnix <= now() {
				status = "expired"
			}
			i, ok := index[remote.ID]
			if !ok {
				i = len(s.Credentials)
				s.Credentials = append(s.Credentials, Credential{ID: remote.ID, DeviceName: "未关联设备", Source: "imported", CreatedAt: now()})
			}
			c := &s.Credentials[i]
			c.Groups = remote.Groups
			c.AllowRelay = remote.AllowRelay
			c.Reusable = boolDefault(remote.Reusable, true)
			c.ExpiresAt = remote.ExpiryUnix
			c.AllowedProxyCIDRs = remote.AllowedProxyCIDRs
			c.Status = status
			c.UpdatedAt = now()
		}
		for i := range s.Credentials {
			c := &s.Credentials[i]
			if !found[c.ID] && c.Status != "revoked" {
				c.Status = "missing"
				if c.ExpiresAt > 0 && c.ExpiresAt <= now() {
					c.Status = "expired"
				}
				c.UpdatedAt = now()
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	m.connectionMu.Lock()
	m.connection = Connection{Connected: true, LastSync: now()}
	m.connectionMu.Unlock()
	return nil
}

func (m *Manager) Sync(ctx context.Context) error {
	m.op.Lock()
	defer m.op.Unlock()
	return m.sync(ctx)
}

func (m *Manager) Snapshot() map[string]any {
	s := m.Store.Snapshot()
	timestamp := now()
	for i := range s.Credentials {
		c := &s.Credentials[i]
		if c.Status == "active" && c.ExpiresAt <= timestamp {
			c.Status = "expired"
		}
		if c.Groups == nil {
			c.Groups = []string{}
		}
		if c.AllowedProxyCIDRs == nil {
			c.AllowedProxyCIDRs = []string{}
		}
	}
	sort.SliceStable(s.People, func(i, j int) bool {
		if (s.People[i].Status == "active") != (s.People[j].Status == "active") {
			return s.People[i].Status == "active"
		}
		return s.People[i].CreatedAt > s.People[j].CreatedAt
	})
	sort.SliceStable(s.Credentials, func(i, j int) bool { return s.Credentials[i].CreatedAt > s.Credentials[j].CreatedAt })
	sort.Slice(s.Audit, func(i, j int) bool { return s.Audit[i].ID > s.Audit[j].ID })
	if len(s.Audit) > 200 {
		s.Audit = s.Audit[:200]
	}
	m.connectionMu.RLock()
	conn := m.connection
	m.connectionMu.RUnlock()
	return map[string]any{"people": s.People, "credentials": s.Credentials, "audit": s.Audit, "connection": conn, "now": timestamp,
		"monitor": buildMonitor(s.People, s.Credentials, timestamp),
		"config":  map[string]any{"demo": m.Config.Demo, "rpc_portal": m.Config.RPCPortal, "instance_name": m.Config.InstanceName, "network_name": m.Config.NetworkName, "peer_url": m.Config.PeerURL, "cli_path": m.Config.CLIPath}}
}

type PersonInput struct {
	Name    string `json:"name"`
	Team    string `json:"team"`
	Contact string `json:"contact"`
	Notes   string `json:"notes"`
}

func (m *Manager) SavePerson(id string, input PersonInput) (Person, error) {
	var err error
	if input.Name, err = cleanText(input.Name, "姓名", 80, true); err != nil {
		return Person{}, err
	}
	if input.Team, err = cleanText(input.Team, "部门", 80, false); err != nil {
		return Person{}, err
	}
	if input.Contact, err = cleanText(input.Contact, "联系方式", 150, false); err != nil {
		return Person{}, err
	}
	if input.Notes, err = cleanText(input.Notes, "备注", 1000, false); err != nil {
		return Person{}, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	var p Person
	err = m.Store.Update(func(s *State) error {
		idx := -1
		if id != "" {
			for i, v := range s.People {
				if v.ID == id {
					idx = i
					break
				}
			}
			if idx < 0 {
				return problem(404, "not_found", "人员不存在")
			}
		}
		if idx < 0 {
			idx = len(s.People)
			s.People = append(s.People, Person{ID: newID(), Status: "active", CreatedAt: now()})
		}
		p = s.People[idx]
		p.Name = input.Name
		p.Team = input.Team
		p.Contact = input.Contact
		p.Notes = input.Notes
		p.UpdatedAt = now()
		s.People[idx] = p
		action := "person_saved"
		if id != "" {
			action = "person_updated"
		}
		audit(s, action, p.Name, p.Team)
		return nil
	})
	return p, err
}

func validateIssue(o *IssueOptions) error {
	if err := validateLaunch(&o.Launch); err != nil {
		return err
	}
	if !validID(o.PersonID) {
		return problem(400, "invalid_request", "人员编号不正确")
	}
	var err error
	if o.DeviceName, err = cleanText(o.DeviceName, "设备名称", 100, true); err != nil {
		return err
	}
	if o.TTLDays < 1 || o.TTLDays > 365 {
		return problem(400, "invalid_request", "有效期必须为 1 至 365 天")
	}
	if len(o.Groups) > 16 || len(o.AllowedProxyCIDRs) > 16 {
		return problem(400, "invalid_request", "组或子网数量不能超过 16")
	}
	groups := []string{}
	seen := map[string]bool{}
	for _, g := range o.Groups {
		if !groupPattern.MatchString(g) {
			return problem(400, "invalid_request", "ACL 组名只能包含文字、数字、下划线、点、冒号和连字符")
		}
		if !seen[g] {
			groups = append(groups, g)
			seen[g] = true
		}
	}
	o.Groups = groups
	for i, c := range o.AllowedProxyCIDRs {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return problem(400, "invalid_request", "请填写有效的 CIDR 网段")
		}
		o.AllowedProxyCIDRs[i] = p.Masked().String()
	}
	return nil
}

func (m *Manager) Issue(ctx context.Context, o IssueOptions) (map[string]any, error) {
	if err := validateIssue(&o); err != nil {
		return nil, err
	}
	m.op.Lock()
	defer m.op.Unlock()
	p, err := findPerson(m.Store.Snapshot(), o.PersonID)
	if err != nil {
		return nil, err
	}
	if p.Status != "active" {
		return nil, problem(409, "person_disabled", "此人员已停用或正在撤权，不能签发新凭据。")
	}
	if err = m.sync(ctx); err != nil {
		return nil, err
	}
	if err = checkLaunchConflict(m.Store.Snapshot(), "", o.Launch, o.Reusable); err != nil {
		return nil, err
	}
	c := Credential{ID: newID(), PersonID: p.ID, DeviceName: o.DeviceName, Groups: o.Groups, AllowedProxyCIDRs: o.AllowedProxyCIDRs, AllowRelay: o.AllowRelay, Reusable: o.Reusable, ExpiresAt: now() + int64(o.TTLDays)*86400, Status: "issuing", Source: "panel", CreatedAt: now(), UpdatedAt: now(), Launch: o.Launch}
	err = m.Store.Update(func(s *State) error {
		s.Credentials = append(s.Credentials, c)
		audit(s, "issue_requested", p.Name, o.DeviceName+" · "+c.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	secret, err := m.Backend.Generate(ctx, c.ID, o)
	publicKey := credentialPublicKey(secret)
	if m.Config.Demo {
		publicKey = demoPublicKey(c.ID)
	}
	if err == nil && publicKey == "" {
		err = problem(502, "invalid_credential", "签发结果无法解析，请同步核实。")
	}
	if err != nil {
		if saveErr := m.Store.Update(func(s *State) error {
			for i := range s.Credentials {
				if s.Credentials[i].ID == c.ID {
					s.Credentials[i].Status = "issue_unconfirmed"
				}
			}
			audit(s, "issue_failed", p.Name, "结果未确认 · "+c.ID)
			return nil
		}); saveErr != nil {
			return nil, saveErr
		}
		return nil, problem(502, "issue_unconfirmed", "签发结果未确认，已保留待核实记录。请先同步，避免重复签发。")
	}
	err = m.Store.Update(func(s *State) error {
		if err := m.setCredentialPublicKey(s, c.ID, publicKey, "issued"); err != nil {
			return err
		}
		for i := range s.Credentials {
			if s.Credentials[i].ID == c.ID {
				s.Credentials[i].Status = "active"
				s.Credentials[i].UpdatedAt = now()
			}
		}
		audit(s, "credential_issued", p.Name, c.DeviceName+" · "+c.ID)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"credential_id": c.ID, "credential_secret": secret, "join_command": launchCommand(m.Config, secret, o.Launch), "toml_config": launchTOML(m.Config, secret, o.Launch), "toml_command": launchTOMLCommand(secret), "launch": o.Launch, "person_name": p.Name, "device_name": c.DeviceName, "expires_at": c.ExpiresAt, "demo": m.Config.Demo}, nil
}

func (m *Manager) revoke(ctx context.Context, id string) error {
	c, err := findCredential(m.Store.Snapshot(), id)
	if err != nil {
		return err
	}
	if c.Status == "revoked" {
		return nil
	}
	err = m.Store.Update(func(s *State) error {
		for i := range s.Credentials {
			if s.Credentials[i].ID == id {
				s.Credentials[i].Status = "revoking"
			}
		}
		audit(s, "revoke_requested", c.DeviceName, id)
		return nil
	})
	if err != nil {
		return err
	}
	err = m.Backend.Revoke(ctx, id)
	if err != nil {
		if saveErr := m.Store.Update(func(s *State) error {
			for i := range s.Credentials {
				if s.Credentials[i].ID == id {
					s.Credentials[i].Status = "revoke_failed"
				}
			}
			audit(s, "revoke_failed", c.DeviceName, id)
			return nil
		}); saveErr != nil {
			return saveErr
		}
		return err
	}
	return m.Store.Update(func(s *State) error {
		for i := range s.Credentials {
			if s.Credentials[i].ID == id {
				s.Credentials[i].Status = "revoked"
				s.Credentials[i].UpdatedAt = now()
			}
		}
		audit(s, "credential_revoked", c.DeviceName, id)
		return nil
	})
}
func (m *Manager) Revoke(ctx context.Context, id string) error {
	m.op.Lock()
	defer m.op.Unlock()
	if err := m.sync(ctx); err != nil {
		return err
	}
	return m.revoke(ctx, id)
}

func (m *Manager) Assign(id, personID, deviceName, publicKey string) error {
	m.op.Lock()
	defer m.op.Unlock()
	p, err := findPerson(m.Store.Snapshot(), personID)
	if err != nil {
		return err
	}
	if p.Status != "active" {
		return problem(409, "person_disabled", "只能关联到在职人员")
	}
	c, err := findCredential(m.Store.Snapshot(), id)
	if err != nil {
		return err
	}
	if deviceName == "" {
		deviceName = c.DeviceName
	}
	deviceName, err = cleanText(deviceName, "设备名称", 100, true)
	if err != nil {
		return err
	}
	return m.Store.Update(func(s *State) error {
		if publicKey != "" {
			if err := m.setCredentialPublicKey(s, id, publicKey, "manual"); err != nil {
				return err
			}
			audit(s, "credential_key_registered", p.Name, "管理员补录公钥 · "+id)
		}
		for i := range s.Credentials {
			if s.Credentials[i].ID == id {
				s.Credentials[i].PersonID = personID
				s.Credentials[i].DeviceName = deviceName
				s.Credentials[i].UpdatedAt = now()
			}
		}
		audit(s, "credential_assigned", p.Name, deviceName+" · "+id)
		return nil
	})
}

type OffboardResult struct {
	Complete bool                `json:"complete"`
	Revoked  []string            `json:"revoked"`
	Failures []map[string]string `json:"failures"`
	Status   string              `json:"status"`
}

func (m *Manager) Offboard(ctx context.Context, id string) (OffboardResult, error) {
	m.op.Lock()
	defer m.op.Unlock()
	result := OffboardResult{Revoked: []string{}, Failures: []map[string]string{}}
	p, err := findPerson(m.Store.Snapshot(), id)
	if err != nil {
		return result, err
	}
	err = m.Store.Update(func(s *State) error {
		for i := range s.People {
			if s.People[i].ID == id {
				s.People[i].Status = "revoking"
				s.People[i].UpdatedAt = now()
			}
		}
		audit(s, "offboard_requested", p.Name, "已停止签发，开始核实关联凭据")
		return nil
	})
	if err != nil {
		return result, err
	}
	if err = m.sync(ctx); err != nil {
		return result, err
	}
	for _, c := range m.Store.Snapshot().Credentials {
		if c.PersonID != id || c.Status != "active" {
			continue
		}
		if err = m.revoke(ctx, c.ID); err != nil {
			result.Failures = append(result.Failures, map[string]string{"id": c.ID, "error": err.Error()})
		} else {
			result.Revoked = append(result.Revoked, c.ID)
		}
	}
	result.Complete = len(result.Failures) == 0
	result.Status = "offboarded"
	if !result.Complete {
		result.Status = "revoking"
	}
	err = m.Store.Update(func(s *State) error {
		for i := range s.People {
			if s.People[i].ID == id {
				s.People[i].Status = result.Status
				s.People[i].UpdatedAt = now()
			}
		}
		action := "person_offboarded"
		if !result.Complete {
			action = "offboard_partial"
		}
		audit(s, action, p.Name, fmt.Sprintf("确认撤销 %d 条，失败 %d 条", len(result.Revoked), len(result.Failures)))
		return nil
	})
	return result, err
}

func (m *Manager) Restore(id string) error {
	m.op.Lock()
	defer m.op.Unlock()
	p, err := findPerson(m.Store.Snapshot(), id)
	if err != nil {
		return err
	}
	if p.Status != "offboarded" {
		return problem(409, "invalid_state", "只有已离职人员可恢复，请先完成撤权。")
	}
	return m.Store.Update(func(s *State) error {
		for i := range s.People {
			if s.People[i].ID == id {
				s.People[i].Status = "active"
				s.People[i].UpdatedAt = now()
			}
		}
		audit(s, "person_restored", p.Name, "历史凭据不恢复，需要重新签发")
		return nil
	})
}

func (m *Manager) seedDemo() error {
	if m.Store.Snapshot().Admin.Username != "" {
		return nil
	}
	hash, err := hashPassword("demo-access-2026")
	if err != nil {
		return err
	}
	return m.Store.Update(func(s *State) error {
		s.Admin = Admin{Username: "admin", PasswordHash: hash}
		people := [][3]string{{"林予安", "基础设施", "lin@example.com"}, {"陈知远", "研发", "chen@example.com"}, {"许以宁", "产品设计", "xu@example.com"}, {"周时序", "研发", "zhou@example.com"}, {"陆景和", "运维", "lu@example.com"}, {"沈亦舟", "外部协作", "shen@example.com"}}
		for i, p := range people {
			status := "active"
			if i == 5 {
				status = "offboarded"
			}
			s.People = append(s.People, Person{ID: newID(), Name: p[0], Team: p[1], Contact: p[2], Status: status, CreatedAt: now() - int64(20-i)*86400, UpdatedAt: now()})
		}
		devices := []struct {
			owner        int
			label, group string
			days         int
		}{{0, "MacBook Pro", "admin", 28}, {0, "iPhone", "staff", 13}, {1, "ThinkPad T14", "developer", 6}, {2, "MacBook Air", "staff", 2}, {3, "Windows 工作站", "developer", 18}, {4, "运维笔记本", "admin", 1}, {5, "临时协作电脑", "guest", -2}, {-1, "待认领的设备", "guest", 4}}
		for _, d := range devices {
			id := newID()
			pid := ""
			status := "active"
			source := "panel"
			if d.owner >= 0 {
				pid = s.People[d.owner].ID
			} else {
				source = "imported"
			}
			if d.owner == 5 {
				status = "revoked"
			}
			expiry := now() + int64(d.days)*86400
			s.Credentials = append(s.Credentials, Credential{ID: id, PersonID: pid, DeviceName: d.label, Groups: []string{d.group}, AllowedProxyCIDRs: []string{}, ExpiresAt: expiry, Status: status, Source: source, CreatedAt: now() - 3*86400, UpdatedAt: now()})
			if d.owner >= 0 {
				if err := m.setCredentialPublicKey(s, id, demoPublicKey(id), "issued"); err != nil {
					return err
				}
			}
			if status != "revoked" {
				s.DemoCredentials = append(s.DemoCredentials, RemoteCredential{ID: id, Groups: []string{d.group}, ExpiryUnix: expiry, Reusable: boolPointer(false), AllowedProxyCIDRs: []string{}})
			}
		}
		audit(s, "demo_seed", "演示环境", "人员和凭据为本地模拟数据，不连接真实节点")
		return nil
	})
}
