package main

import (
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

const sessionTTL = 12 * time.Hour

type Session struct {
	CSRF, Username string
	ExpiresAt      time.Time
}
type Web struct {
	Manager  *Manager
	Assets   fs.FS
	authMu   sync.Mutex
	sessions map[string]Session
	attempts map[string][]time.Time
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 24)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(salt) + ":" + base64.StdEncoding.EncodeToString(key), nil
}
func verifyPassword(password, stored string) bool {
	parts := strings.Split(stored, ":")
	if len(parts) != 2 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	expected, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, 600000, 32)
	return err == nil && hmac.Equal(key, expected)
}
func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func tokenHash(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

func newWeb(m *Manager, assets fs.FS) *Web {
	return &Web{Manager: m, Assets: assets, sessions: map[string]Session{}, attempts: map[string][]time.Time{}}
}
func (w *Web) session(r *http.Request) (Session, string, bool) {
	cookie, err := r.Cookie("et_access_session")
	if err != nil {
		return Session{}, "", false
	}
	digest := tokenHash(cookie.Value)
	w.authMu.Lock()
	defer w.authMu.Unlock()
	s, ok := w.sessions[digest]
	if !ok || !s.ExpiresAt.After(time.Now()) {
		delete(w.sessions, digest)
		return Session{}, "", false
	}
	return s, digest, true
}
func (w *Web) cookie(token string, age int) *http.Cookie {
	return &http.Cookie{Name: "et_access_session", Value: token, Path: "/", MaxAge: age, HttpOnly: true, Secure: strings.HasPrefix(w.Manager.Config.PublicURL, "https://"), SameSite: http.SameSiteStrictMode}
}

func (w *Web) checkOrigin(r *http.Request) error {
	c := w.Manager.Config
	u, _ := url.Parse(c.PublicURL)
	allowed := map[string]bool{strings.ToLower(u.Host): true}
	origins := map[string]bool{c.PublicURL: true}
	if c.Listen == "127.0.0.1" || c.Listen == "localhost" {
		for _, h := range []string{fmt.Sprintf("127.0.0.1:%d", c.Port), fmt.Sprintf("localhost:%d", c.Port)} {
			allowed[h] = true
			origins["http://"+h] = true
		}
	}
	if !allowed[strings.ToLower(r.Host)] {
		return problem(403, "invalid_host", "请求域名与 public_url 不一致")
	}
	if origin := r.Header.Get("Origin"); origin != "" && !origins[origin] {
		return problem(403, "invalid_origin", "请求来源不允许")
	}
	return nil
}

func writeJSON(out http.ResponseWriter, status int, value any) {
	out.Header().Set("Content-Type", "application/json; charset=utf-8")
	out.WriteHeader(status)
	_ = json.NewEncoder(out).Encode(value)
}
func writeError(out http.ResponseWriter, err error) {
	var api *APIError
	if errors.As(err, &api) {
		writeJSON(out, api.Status, api)
		return
	}
	// Filesystem errors are logged without request bodies or subprocess diagnostics.
	log.Printf("request failure: %T", err)
	writeJSON(out, 500, problem(500, "internal_error", "服务暂时出错，请检查数据目录权限后重试。"))
}
func decodeBody(out http.ResponseWriter, r *http.Request, value any) error {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		return problem(415, "invalid_content_type", "请使用 JSON 请求")
	}
	if len(r.TransferEncoding) > 0 {
		return problem(400, "invalid_request", "不支持分块请求")
	}
	r.Body = http.MaxBytesReader(out, r.Body, 32768)
	decoder := json.NewDecoder(r.Body)
	if err = decoder.Decode(value); err != nil {
		return problem(400, "invalid_json", "JSON 请求格式不正确或请求过大")
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return problem(400, "invalid_json", "请求只能包含一个 JSON 对象")
	}
	return nil
}

func (w *Web) login(out http.ResponseWriter, r *http.Request) error {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeBody(out, r, &input); err != nil {
		return err
	}
	if input.Username == "" || len(input.Username) > 200 || len(input.Password) > 1000 {
		return problem(401, "invalid_login", "用户名或密码不正确")
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	nowTime := time.Now()
	w.authMu.Lock()
	for key, times := range w.attempts {
		kept := times[:0]
		for _, t := range times {
			if nowTime.Sub(t) < 15*time.Minute {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(w.attempts, key)
		} else {
			w.attempts[key] = kept
		}
	}
	if len(w.attempts[ip]) >= 8 || len(w.attempts) > 8192 {
		w.authMu.Unlock()
		return problem(429, "rate_limited", "登录尝试过多，请 15 分钟后再试")
	}
	w.attempts[ip] = append(w.attempts[ip], nowTime)
	w.authMu.Unlock()
	admin := w.Manager.Store.Snapshot().Admin
	if !verifyPassword(input.Password, admin.PasswordHash) || !hmac.Equal([]byte(input.Username), []byte(admin.Username)) {
		return problem(401, "invalid_login", "用户名或密码不正确")
	}
	token := randomToken()
	s := Session{CSRF: randomToken(), Username: admin.Username, ExpiresAt: time.Now().Add(sessionTTL)}
	w.authMu.Lock()
	delete(w.attempts, ip)
	for key, session := range w.sessions {
		if !session.ExpiresAt.After(time.Now()) {
			delete(w.sessions, key)
		}
	}
	w.sessions[tokenHash(token)] = s
	w.authMu.Unlock()
	if err := w.Manager.Store.Update(func(state *State) error { audit(state, "admin_login", admin.Username, ""); return nil }); err != nil {
		return err
	}
	http.SetCookie(out, w.cookie(token, int(sessionTTL.Seconds())))
	writeJSON(out, 200, map[string]any{"username": s.Username, "csrf": s.CSRF, "demo": w.Manager.Config.Demo})
	return nil
}

func (w *Web) ServeHTTP(out http.ResponseWriter, r *http.Request) {
	out.Header().Set("Cache-Control", "no-store")
	out.Header().Set("X-Content-Type-Options", "nosniff")
	out.Header().Set("X-Frame-Options", "DENY")
	out.Header().Set("Referrer-Policy", "no-referrer")
	out.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	defer func() {
		if recover() != nil {
			writeError(out, errors.New("internal panic"))
		}
	}()
	if err := w.route(out, r); err != nil {
		writeError(out, err)
	}
}

func (w *Web) route(out http.ResponseWriter, r *http.Request) error {
	if err := w.checkOrigin(r); err != nil {
		return err
	}
	p := r.URL.Path
	if r.Method == "GET" {
		assets := map[string]string{"/": "index.html", "/app.js": "app.js", "/peers.js": "peers.js", "/credential-key.js": "credential-key.js", "/style.css": "style.css"}
		if filename, ok := assets[p]; ok {
			content, err := fs.ReadFile(w.Assets, filename)
			if err != nil {
				return err
			}
			out.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(filename)))
			_, err = out.Write(content)
			return err
		}
		s, _, authenticated := w.session(r)
		if p == "/api/session" {
			writeJSON(out, 200, map[string]any{"authenticated": authenticated, "username": s.Username, "csrf": s.CSRF, "demo": w.Manager.Config.Demo})
			return nil
		}
		if !authenticated {
			return problem(401, "authentication_required", "请先登录")
		}
		if p == "/api/state" {
			writeJSON(out, 200, w.Manager.Snapshot())
			return nil
		}
		if p == "/api/peers" {
			writeJSON(out, 200, w.Manager.PeerSnapshot())
			return nil
		}
		return problem(404, "not_found", "页面或接口不存在")
	}
	if r.Method != "POST" {
		return problem(405, "method_not_allowed", "请求方法不支持")
	}
	if p == "/api/login" {
		return w.login(out, r)
	}
	s, digest, ok := w.session(r)
	if !ok {
		return problem(401, "authentication_required", "请先登录")
	}
	if !hmac.Equal([]byte(r.Header.Get("X-CSRF-Token")), []byte(s.CSRF)) {
		return problem(403, "invalid_csrf", "会话校验失败，请刷新后重试")
	}
	if p == "/api/logout" {
		w.authMu.Lock()
		delete(w.sessions, digest)
		w.authMu.Unlock()
		http.SetCookie(out, w.cookie("", -1))
		writeJSON(out, 200, map[string]bool{"logged_out": true})
		return nil
	}
	if p == "/api/sync" {
		if err := w.Manager.Sync(r.Context()); err != nil {
			return err
		}
		writeJSON(out, 200, map[string]bool{"synced": true})
		return nil
	}
	if p == "/api/peers/sync" {
		if err := w.Manager.SyncPeers(r.Context()); err != nil {
			return err
		}
		writeJSON(out, 200, w.Manager.PeerSnapshot())
		return nil
	}
	if p == "/api/people" {
		var input PersonInput
		if err := decodeBody(out, r, &input); err != nil {
			return err
		}
		person, err := w.Manager.SavePerson("", input)
		if err != nil {
			return err
		}
		writeJSON(out, 201, person)
		return nil
	}
	if p == "/api/credentials" {
		var input IssueOptions
		if err := decodeBody(out, r, &input); err != nil {
			return err
		}
		issued, err := w.Manager.Issue(r.Context(), input)
		if err != nil {
			return err
		}
		writeJSON(out, 201, issued)
		return nil
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) >= 3 && parts[0] == "api" && validID(parts[2]) {
		id := parts[2]
		if parts[1] == "people" {
			if len(parts) == 3 {
				var input PersonInput
				if err := decodeBody(out, r, &input); err != nil {
					return err
				}
				person, err := w.Manager.SavePerson(id, input)
				if err != nil {
					return err
				}
				writeJSON(out, 200, person)
				return nil
			}
			if len(parts) == 4 && parts[3] == "offboard" {
				result, err := w.Manager.Offboard(r.Context(), id)
				if err != nil {
					return err
				}
				status := 200
				if !result.Complete {
					status = 207
				}
				writeJSON(out, status, result)
				return nil
			}
			if len(parts) == 4 && parts[3] == "restore" {
				if err := w.Manager.Restore(id); err != nil {
					return err
				}
				writeJSON(out, 200, map[string]bool{"restored": true})
				return nil
			}
		}
		if parts[1] == "credentials" && len(parts) == 4 {
			if parts[3] == "launch" {
				var input LaunchOptions
				if err := decodeBody(out, r, &input); err != nil {
					return err
				}
				result, err := w.Manager.SaveLaunch(r.Context(), id, input)
				if err != nil {
					return err
				}
				writeJSON(out, 200, result)
				return nil
			}
			if parts[3] == "revoke" {
				if err := w.Manager.Revoke(r.Context(), id); err != nil {
					return err
				}
				writeJSON(out, 200, map[string]bool{"revoked": true})
				return nil
			}
			if parts[3] == "assign" {
				var input struct {
					PersonID   string `json:"person_id"`
					DeviceName string `json:"device_name"`
				}
				if err := decodeBody(out, r, &input); err != nil {
					return err
				}
				if err := w.Manager.Assign(id, input.PersonID, input.DeviceName); err != nil {
					return err
				}
				writeJSON(out, 200, map[string]bool{"assigned": true})
				return nil
			}
		}
	}
	return problem(404, "not_found", "接口不存在")
}
