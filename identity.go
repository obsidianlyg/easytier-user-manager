package main

import (
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

func credentialPublicKey(secret string) string {
	raw, err := base64.StdEncoding.DecodeString(secret)
	if err != nil || len(raw) != 32 {
		return ""
	}
	key, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(key.PublicKey().Bytes())
}

func canonicalPublicKey(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(raw) != 32 || string(raw) == string(make([]byte, 32)) {
		return "", problem(400, "invalid_public_key", "请填写 Base64 编码的 32 字节凭据公钥，不要填写私钥。")
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

func demoPublicKey(id string) string {
	raw := sha256.Sum256([]byte("demo-public-key:" + id))
	return credentialPublicKey(base64.StdEncoding.EncodeToString(raw[:]))
}

func (m *Manager) setCredentialPublicKey(s *State, id, publicKey, source string) error {
	key, err := canonicalPublicKey(publicKey)
	if err != nil {
		return err
	}
	for _, c := range s.Credentials {
		if c.PublicKeyScope != m.peerScope() || c.PublicKey == "" {
			continue
		}
		if c.ID == id && c.PublicKey != key {
			return problem(409, "public_key_conflict", "该凭据已登记其他公钥，不能覆盖；请核对所选凭据。")
		}
		if c.ID != id && c.PublicKey == key {
			return problem(409, "public_key_conflict", "该公钥已关联其他凭据，请核对所选凭据。")
		}
	}
	for i := range s.Credentials {
		c := &s.Credentials[i]
		if c.ID == id {
			if c.PublicKey != key || c.PublicKeyScope != m.peerScope() {
				c.PublicKey, c.PublicKeyScope, c.PublicKeySource = key, m.peerScope(), source
			}
			return nil
		}
	}
	return problem(404, "not_found", "凭据不存在")
}
