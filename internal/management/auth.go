package management

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrRateLimited        = errors.New("too many attempts; try again later")
	ErrAlreadyInitialized = errors.New("administrator already initialized")
	ErrUnauthenticated    = errors.New("authentication required")
)

const SessionTTL = 12 * time.Hour

type adminRecord struct {
	Username     string `json:"username"`
	PasswordHash string `json:"password_hash"`
}
type adminSession struct {
	Username string
	Version  uint64
	Expires  time.Time
}
type authAttempts struct {
	Count int
	Until time.Time
}

// AdminAuth manages exactly one administrator. Its directory must be separate
// from the editable configuration store and must never be exported by the UI.
type AdminAuth struct {
	mu       sync.Mutex
	store    *DocumentStore
	sessions map[[32]byte]adminSession
	attempts map[string]authAttempts
	global   authAttempts
	dummy    []byte
	now      func() time.Time
}

func NewAdminAuth(dir string) (*AdminAuth, error) {
	s, e := NewDocumentStore(dir, 4096)
	if e != nil {
		return nil, e
	}
	dummy, e := bcrypt.GenerateFromPassword([]byte("unmatchable-dummy-password"), bcrypt.DefaultCost)
	if e != nil {
		return nil, e
	}
	return &AdminAuth{store: s, sessions: make(map[[32]byte]adminSession), attempts: make(map[string]authAttempts), dummy: dummy, now: time.Now}, nil
}
func (a *AdminAuth) record() (Document, adminRecord, error) {
	d, e := a.store.Read()
	if e != nil {
		return d, adminRecord{}, e
	}
	var r adminRecord
	if e = json.Unmarshal(d.Data, &r); e != nil {
		return d, r, errors.New("invalid administrator record")
	}
	if r.Username == "" {
		return d, r, errors.New("invalid administrator record")
	}
	if _, e = bcrypt.Cost([]byte(r.PasswordHash)); e != nil {
		return d, r, errors.New("invalid administrator record")
	}
	return d, r, nil
}
func (a *AdminAuth) Initialized() (bool, error) {
	_, _, e := a.record()
	if errors.Is(e, ErrDocumentNotFound) {
		return false, nil
	}
	return e == nil, e
}
func validPassword(p string) bool {
	return utf8.ValidString(p) && utf8.RuneCountInString(p) >= 12 && len(p) <= 72
}
func (a *AdminAuth) Setup(username, password string) error {
	if username != strings.TrimSpace(username) || len(username) < 1 || len(username) > 64 || strings.IndexFunc(username, unicode.IsControl) >= 0 {
		return errors.New("username must contain 1 to 64 bytes without surrounding whitespace or control characters")
	}
	if !validPassword(password) {
		return errors.New("password must contain at least 12 characters and at most 72 UTF-8 bytes")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if yes, e := a.Initialized(); e != nil {
		return e
	} else if yes {
		return ErrAlreadyInitialized
	}
	h, e := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	b, _ := json.Marshal(adminRecord{username, string(h)})
	_, e = a.store.Save(0, b)
	if errors.Is(e, ErrVersionConflict) {
		return ErrAlreadyInitialized
	}
	return e
}
func (a *AdminAuth) allow(remote string) bool {
	now := a.now()
	for k, v := range a.attempts {
		if !now.Before(v.Until) {
			delete(a.attempts, k)
		}
	}
	if !now.Before(a.global.Until) {
		a.global = authAttempts{Until: now.Add(time.Minute)}
	}
	v := a.attempts[remote]
	if !now.Before(v.Until) {
		v = authAttempts{Until: now.Add(time.Minute)}
	}
	if a.global.Count >= 20 || v.Count >= 5 {
		return false
	}
	a.global.Count++
	v.Count++
	a.attempts[remote] = v
	return true
}
func (a *AdminAuth) Login(username, password, remoteKey string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.allow(remoteKey) {
		return "", ErrRateLimited
	}
	d, r, e := a.record()
	hash := a.dummy
	if e == nil {
		hash = []byte(r.PasswordHash)
	}
	// Always perform a password comparison, including unknown usernames.
	passErr := bcrypt.CompareHashAndPassword(hash, []byte(password))
	if e != nil || passErr != nil || r.Username != username {
		return "", ErrInvalidCredentials
	}
	now := a.now()
	for k, s := range a.sessions {
		if !now.Before(s.Expires) {
			delete(a.sessions, k)
		}
	}
	if len(a.sessions) >= 128 {
		return "", ErrRateLimited
	}
	raw := make([]byte, 32)
	if _, e = rand.Read(raw); e != nil {
		return "", e
	}
	token := hex.EncodeToString(raw)
	a.sessions[sha256.Sum256([]byte(token))] = adminSession{r.Username, d.Version, now.Add(SessionTTL)}
	return token, nil
}
func (a *AdminAuth) authenticate(token string) (adminSession, error) {
	if len(token) != 64 {
		return adminSession{}, ErrUnauthenticated
	}
	k := sha256.Sum256([]byte(token))
	s, ok := a.sessions[k]
	if !ok || !a.now().Before(s.Expires) {
		delete(a.sessions, k)
		return adminSession{}, ErrUnauthenticated
	}
	d, _, e := a.record()
	if e != nil || d.Version != s.Version {
		delete(a.sessions, k)
		return adminSession{}, ErrUnauthenticated
	}
	return s, nil
}
func (a *AdminAuth) Authenticate(token string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	s, e := a.authenticate(token)
	return s.Username, e
}
func (a *AdminAuth) Logout(token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.sessions, sha256.Sum256([]byte(token)))
}
func (a *AdminAuth) ChangePassword(token, oldPassword, newPassword string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, e := a.authenticate(token); e != nil {
		return e
	}
	if !a.allow("password-change") {
		return ErrRateLimited
	}
	if !validPassword(newPassword) {
		return errors.New("password must contain at least 12 characters and at most 72 UTF-8 bytes")
	}
	d, r, e := a.record()
	if e != nil {
		return e
	}
	if bcrypt.CompareHashAndPassword([]byte(r.PasswordHash), []byte(oldPassword)) != nil {
		return ErrInvalidCredentials
	}
	h, e := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if e != nil {
		return e
	}
	r.PasswordHash = string(h)
	b, _ := json.Marshal(r)
	if _, e = a.store.Save(d.Version, b); e != nil {
		return e
	}
	a.sessions = make(map[[32]byte]adminSession)
	return nil
}
