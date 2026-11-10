package main

import "sort"

// Monitor is derived from the same credential snapshot as the management views.
// Credential validity describes authorization, not device connectivity.
type Monitor struct {
	PeopleTotal           int             `json:"people_total"`
	ActivePeople          int             `json:"active_people"`
	RevokingPeople        int             `json:"revoking_people"`
	OffboardedPeople      int             `json:"offboarded_people"`
	ActiveCredentials     int             `json:"active_credentials"`
	ExpiringCredentials   int             `json:"expiring_credentials"`
	AttentionPeople       int             `json:"attention_people"`
	NoAccessPeople        int             `json:"no_access_people"`
	UnassignedCredentials int             `json:"unassigned_credentials"`
	AbnormalCredentials   int             `json:"abnormal_credentials"`
	OnlineAvailable       bool            `json:"online_available"`
	People                []PersonMonitor `json:"people"`
}

type PersonMonitor struct {
	PersonID       string `json:"person_id"`
	Total          int    `json:"total"`
	Active         int    `json:"active"`
	Expiring       int    `json:"expiring"`
	Expired        int    `json:"expired"`
	Abnormal       int    `json:"abnormal"`
	NextExpiry     int64  `json:"next_expiry"`
	NeedsAttention bool   `json:"needs_attention"`
}

func credentialAbnormal(status string) bool {
	return status != "active" && status != "expired" && status != "revoked"
}

func buildMonitor(people []Person, credentials []Credential, timestamp int64) Monitor {
	m := Monitor{PeopleTotal: len(people), People: make([]PersonMonitor, len(people))}
	index := make(map[string]int, len(people))
	for i, p := range people {
		index[p.ID] = i
		m.People[i].PersonID = p.ID
		switch p.Status {
		case "active":
			m.ActivePeople++
		case "revoking":
			m.RevokingPeople++
		case "offboarded":
			m.OffboardedPeople++
		}
	}
	for _, c := range credentials {
		status := c.Status
		if status == "active" && c.ExpiresAt <= timestamp {
			status = "expired"
		}
		active := status == "active"
		expiring := active && c.ExpiresAt <= timestamp+7*86400
		abnormal := credentialAbnormal(status)
		if active {
			m.ActiveCredentials++
		}
		if expiring {
			m.ExpiringCredentials++
		}
		if abnormal {
			m.AbnormalCredentials++
		}
		i, found := index[c.PersonID]
		if !found {
			// Historical expired/revoked records do not require ownership repair.
			if active || abnormal {
				m.UnassignedCredentials++
			}
			continue
		}
		p := &m.People[i]
		p.Total++
		if active {
			p.Active++
			if p.NextExpiry == 0 || c.ExpiresAt < p.NextExpiry {
				p.NextExpiry = c.ExpiresAt
			}
		}
		if expiring {
			p.Expiring++
		}
		if status == "expired" {
			p.Expired++
		}
		if abnormal {
			p.Abnormal++
		}
	}
	for i, p := range people {
		row := &m.People[i]
		if p.Status == "active" && row.Active == 0 {
			m.NoAccessPeople++
		}
		row.NeedsAttention = p.Status == "revoking" || row.Abnormal > 0 ||
			(p.Status != "active" && row.Active > 0)
		if row.NeedsAttention {
			m.AttentionPeople++
		}
	}
	// Put incomplete revocations first, then approaching expirations. Preserve
	// the original personnel order for rows with equal priority.
	sort.SliceStable(m.People, func(i, j int) bool {
		a, b := m.People[i], m.People[j]
		if a.NeedsAttention != b.NeedsAttention {
			return a.NeedsAttention
		}
		if (a.NextExpiry > 0) != (b.NextExpiry > 0) {
			return a.NextExpiry > 0
		}
		return a.NextExpiry < b.NextExpiry
	})
	return m
}
