package management

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func authTestDir(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(root, "auth")
}
func TestAdminLifecycle(t *testing.T) {
	dir := authTestDir(t)
	a, e := NewAdminAuth(dir)
	if e != nil {
		t.Fatal(e)
	}
	if initialized, e := a.Initialized(); e != nil || initialized {
		t.Fatal(initialized, e)
	}
	if e = a.Setup("admin", "strong-password-1"); e != nil {
		t.Fatal(e)
	}
	if e = a.Setup("other", "strong-password-2"); !errors.Is(e, ErrAlreadyInitialized) {
		t.Fatal(e)
	}
	b, e := os.ReadFile(filepath.Join(dir, "current.json"))
	if e != nil || strings.Contains(string(b), "strong-password-1") {
		t.Fatal("plaintext password persisted", e)
	}
	token, e := a.Login("admin", "strong-password-1", "client")
	if e != nil {
		t.Fatal(e)
	}
	if name, e := a.Authenticate(token); e != nil || name != "admin" {
		t.Fatal(name, e)
	}
	if e = a.ChangePassword(token, "wrong-old-password", "strong-password-2"); !errors.Is(e, ErrInvalidCredentials) {
		t.Fatal(e)
	}
	if e = a.ChangePassword(token, "strong-password-1", "strong-password-2"); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Authenticate(token); !errors.Is(e, ErrUnauthenticated) {
		t.Fatal(e)
	}
	if _, e = a.Login("admin", "strong-password-1", "client"); !errors.Is(e, ErrInvalidCredentials) {
		t.Fatal(e)
	}
	token, e = a.Login("admin", "strong-password-2", "client")
	if e != nil {
		t.Fatal(e)
	}
	a.Logout(token)
	if _, e = a.Authenticate(token); !errors.Is(e, ErrUnauthenticated) {
		t.Fatal(e)
	}
	restarted, e := NewAdminAuth(dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = restarted.Login("admin", "strong-password-2", "client"); e != nil {
		t.Fatal(e)
	}
}

func TestAdminRateLimitExpiryAndValidation(t *testing.T) {
	a, e := NewAdminAuth(authTestDir(t))
	if e != nil {
		t.Fatal(e)
	}
	if e = a.Setup("admin", "short"); e == nil {
		t.Fatal("weak password accepted")
	}
	if e = a.Setup(" admin ", "strong-password-1"); e == nil {
		t.Fatal("ambiguous username accepted")
	}
	if e = a.Setup("admin", "strong-password-1"); e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	a.now = func() time.Time { return now }
	for i := 0; i < 5; i++ {
		if _, e = a.Login("unknown", "wrong", "client"); !errors.Is(e, ErrInvalidCredentials) {
			t.Fatal(e)
		}
	}
	if _, e = a.Login("admin", "strong-password-1", "client"); !errors.Is(e, ErrRateLimited) {
		t.Fatal(e)
	}
	now = now.Add(time.Minute)
	token, e := a.Login("admin", "strong-password-1", "client")
	if e != nil {
		t.Fatal(e)
	}
	now = now.Add(SessionTTL)
	if _, e = a.Authenticate(token); !errors.Is(e, ErrUnauthenticated) {
		t.Fatal(e)
	}
}

func TestAdminConcurrentSetupAndCrossInstanceRevocation(t *testing.T) {
	dir := authTestDir(t)
	a, e := NewAdminAuth(dir)
	if e != nil {
		t.Fatal(e)
	}
	b, e := NewAdminAuth(dir)
	if e != nil {
		t.Fatal(e)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, auth := range []*AdminAuth{a, b} {
		wg.Add(1)
		go func(auth *AdminAuth) { defer wg.Done(); results <- auth.Setup("admin", "strong-password-1") }(auth)
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if !errors.Is(e, ErrAlreadyInitialized) {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal(success)
	}
	ta, e := a.Login("admin", "strong-password-1", "client")
	if e != nil {
		t.Fatal(e)
	}
	tb, e := b.Login("admin", "strong-password-1", "client")
	if e != nil {
		t.Fatal(e)
	}
	if e = a.ChangePassword(ta, "strong-password-1", "strong-password-2"); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Authenticate(tb); !errors.Is(e, ErrUnauthenticated) {
		t.Fatal(e)
	}
}

func TestAdminCorruptionDoesNotReopenSetup(t *testing.T) {
	a, e := NewAdminAuth(authTestDir(t))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = a.store.Save(0, []byte(`{"username":"admin","password_hash":"invalid"}`)); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Initialized(); e == nil {
		t.Fatal("corruption ignored")
	}
	if e = a.Setup("admin", "strong-password-1"); e == nil {
		t.Fatal("corrupted account replaced")
	}
}
