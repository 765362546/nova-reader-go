package httpapi

import (
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/765362546/novel-reader-go/internal/model"
)

const loginExpireDays = 7
const sessionCookie = "reader3.session"

type session struct {
	Username string
	ExpireAt int64
}

type Sessions struct {
	mu sync.Mutex
	m  map[string]*session
}

func NewSessions() *Sessions {
	return &Sessions{m: make(map[string]*session)}
}

func randomToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Sessions) create(username string) string {
	id := randomToken(24)
	s.mu.Lock()
	s.m[id] = &session{Username: username, ExpireAt: time.Now().Add(loginExpireDays * 24 * time.Hour).UnixMilli()}
	s.mu.Unlock()
	return id
}

func (s *Sessions) get(id string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, ok := s.m[id]
	if !ok || ss.ExpireAt < time.Now().UnixMilli() {
		delete(s.m, id)
		return "", false
	}
	return ss.Username, true
}

func (s *Sessions) destroy(id string) {
	s.mu.Lock()
	delete(s.m, id)
	s.mu.Unlock()
}

// Cleanup evicts expired sessions; it is called periodically by the scheduler.
func (s *Sessions) Cleanup() {
	now := time.Now().UnixMilli()
	s.mu.Lock()
	for id, ss := range s.m {
		if ss.ExpireAt < now {
			delete(s.m, id)
		}
	}
	s.mu.Unlock()
}

func md5hex(v string) string {
	sum := md5.Sum([]byte(v))
	return hex.EncodeToString(sum[:])
}

func genEncryptedPassword(password, salt string) string {
	return md5hex(md5hex(password+salt) + salt)
}

func randomString(n int) string {
	const allowed = "ABCDEFGHIJKLMNOPQRSTUVWXTZabcdefghiklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i := range out {
		out[i] = allowed[int(b[i])%len(allowed)]
	}
	return string(out)
}

func (s *Server) loadUsers() map[string]*model.User {
	users := map[string]*model.User{}
	b, err := s.Store.Get("data", "users")
	if err != nil {
		return users
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) == nil {
		for name, data := range raw {
			var u model.User
			if json.Unmarshal(data, &u) == nil {
				users[name] = &u
			}
		}
	}
	return users
}

func (s *Server) saveUsers(users map[string]*model.User) error {
	return s.Store.Save(users, "data", "users")
}

func (s *Server) userByName(name string) *model.User {
	if name == "" {
		return nil
	}
	return s.loadUsers()[name]
}

// reqInfo carries per-request auth state.
type reqInfo struct {
	Username  string
	Namespace string
	User      *model.User
	Secure    bool
	NeedLogin bool
	NewCookie string
}

func (ri *reqInfo) managerNameSpace() string {
	if ri.Namespace != "" {
		return ri.Namespace
	}
	if ri.Username != "" {
		return ri.Username
	}
	return "default"
}

func (s *Server) auth(r *http.Request) *reqInfo {
	ri := &reqInfo{Secure: s.App.Secure}
	// cookie session (also honored in single-user mode for getUserInfo)
	if c, err := r.Cookie(sessionCookie); err == nil {
		if username, ok := s.Sessions.get(c.Value); ok {
			if u := s.userByName(username); u != nil {
				ri.Username = username
				ri.User = u
				if s.App.Secure {
					ri.Namespace = username
				} else {
					ri.Namespace = "default"
				}
				return ri
			}
		}
	}
	if !s.App.Secure {
		ri.Namespace = "default"
		return ri
	}
	// accessToken auto-login
	accessToken := r.URL.Query().Get("accessToken")
	if accessToken != "" {
		if user, token, ok := splitToken(accessToken); ok {
			users := s.loadUsers()
			u := users[user]
			if u != nil && token != "" {
				isLogin := false
				if u.Token != "" && u.Token == token {
					isLogin = true
				} else if u.TokenMap != nil {
					if expire, ok := u.TokenMap[token]; ok {
						if expire > time.Now().UnixMilli() {
							isLogin = true
							u.TokenMap[token] = time.Now().Add(loginExpireDays * 24 * time.Hour).UnixMilli()
						} else {
							delete(u.TokenMap, token)
						}
					}
				}
				if isLogin {
					u.LastLoginAt = time.Now().UnixMilli()
					_ = s.saveUsers(users)
					ri.NewCookie = s.Sessions.create(u.Username)
					ri.Username = u.Username
					ri.User = u
					ri.Namespace = u.Username
					return ri
				}
			}
		}
	}
	ri.NeedLogin = true
	ri.Namespace = "default"
	return ri
}

func splitToken(accessToken string) (string, string, bool) {
	for i := 0; i < len(accessToken); i++ {
		if accessToken[i] == ':' {
			return accessToken[:i], accessToken[i+1:], true
		}
	}
	return "", "", false
}

// managerOK verifies secureKey (when configured).
func (s *Server) managerOK(r *http.Request, ri *reqInfo) bool {
	if !s.App.Secure {
		return true
	}
	if s.App.SecureKey == "" {
		return true
	}
	if r.URL.Query().Get("secureKey") != s.App.SecureKey {
		return false
	}
	if ns := r.URL.Query().Get("userNS"); ns != "" {
		ri.Namespace = ns
	}
	return true
}

func (s *Server) userAssetDir(namespace string) string {
	return filepath.Join(s.Store.Root, "assets", namespace)
}

// saveUserSession mirrors BaseController.saveUserSession and returns login data.
func (s *Server) saveUserSession(w http.ResponseWriter, username string, users map[string]*model.User, u *model.User, regenerate bool) map[string]any {
	u.LastLoginAt = time.Now().UnixMilli()
	if regenerate {
		u.Token = genEncryptedPassword(u.Username, strconv.FormatInt(time.Now().UnixMilli(), 10))
		expire := time.Now().Add(loginExpireDays * 24 * time.Hour).UnixMilli()
		if u.TokenMap == nil {
			u.TokenMap = map[string]int64{}
		}
		u.TokenMap[u.Token] = expire
		for t, e := range u.TokenMap {
			if e < u.LastLoginAt {
				delete(u.TokenMap, t)
			}
		}
	}
	users[u.Username] = u
	_ = s.saveUsers(users)

	id := s.Sessions.create(u.Username)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(loginExpireDays * 24 * time.Hour),
	})
	return formatUser(u)
}

func formatUser(u *model.User) map[string]any {
	if u == nil {
		return map[string]any{}
	}
	return map[string]any{
		"username":         u.Username,
		"lastLoginAt":      u.LastLoginAt,
		"accessToken":      u.Username + ":" + u.Token,
		"enableWebdav":     u.EnableWebdav,
		"enableLocalStore": u.EnableLocalStore,
		"createdAt":        u.CreatedAt,
	}
}

