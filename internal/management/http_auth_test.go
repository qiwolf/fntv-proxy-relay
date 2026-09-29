package management

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func testAdminAPI(t *testing.T) *API {
	t.Helper()
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	store, e := NewDocumentStore(filepath.Join(dir, "config"), 0)
	if e != nil {
		t.Fatal(e)
	}
	keys, e := NewKeyStore(filepath.Join(dir, "keys"))
	if e != nil {
		t.Fatal(e)
	}
	api, e := NewAPI(store, keys, strings.Repeat("x", 64), "localhost:18764")
	if e != nil {
		t.Fatal(e)
	}
	api.Admin, e = NewAdminAuth(filepath.Join(dir, "auth"))
	if e != nil {
		t.Fatal(e)
	}
	return api
}
func authRequest(api *API, method, path, body, auth, host, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:18764"+path, strings.NewReader(body))
	r.Host = host
	r.RemoteAddr = "127.0.0.1:1111"
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", auth)
	r.Header.Set("Origin", origin)
	w := httptest.NewRecorder()
	api.ServeHTTP(w, r)
	return w
}
func TestHTTPAdminLifecycle(t *testing.T) {
	a := testAdminAPI(t)
	call := func(method, path, body, auth string, want int) *httptest.ResponseRecorder {
		t.Helper()
		w := authRequest(a, method, path, body, auth, "localhost:18764", "http://localhost:18764")
		if w.Code != want {
			t.Fatalf("%s: status %d want %d: %s", path, w.Code, want, w.Body.String())
		}
		return w
	}
	w := call("GET", "/api/auth/status", "", "", 200)
	if !strings.Contains(w.Body.String(), `"initialized":false`) {
		t.Fatal(w.Body.String())
	}
	call("GET", "/api/status", "", "Bearer "+strings.Repeat("x", 64), 401)
	call("POST", "/api/auth/setup", `{"username":"admin","password":"short"}`, "", 400)
	w = call("POST", "/api/auth/setup", `{"username":"admin","password":"long-password-1"}`, "", 200)
	var response struct {
		Token string `json:"token"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil || len(response.Token) != 64 {
		t.Fatal("missing session", e)
	}
	token := response.Token
	call("POST", "/api/auth/setup", `{"username":"other","password":"long-password-2"}`, "", 400)
	call("GET", "/api/status", "", "Bearer "+token, 200)
	call("POST", "/api/auth/password", `{"old_password":"wrong-password","new_password":"long-password-2"}`, "Bearer "+token, 401)
	call("POST", "/api/auth/password", `{"old_password":"long-password-1","new_password":"long-password-2"}`, "Bearer "+token, 200)
	call("GET", "/api/status", "", "Bearer "+token, 401)
	call("POST", "/api/auth/login", `{"username":"admin","password":"long-password-1"}`, "", 401)
	w = call("POST", "/api/auth/login", `{"username":"admin","password":"long-password-2"}`, "", 200)
	if e := json.Unmarshal(w.Body.Bytes(), &response); e != nil {
		t.Fatal(e)
	}
	call("POST", "/api/auth/logout", `{}`, "Bearer "+response.Token, 200)
	call("GET", "/api/status", "", "Bearer "+response.Token, 401)
}
func TestHTTPAdminRejectCrossOriginAndInvalidJSON(t *testing.T) {
	a := testAdminAPI(t)
	body := `{"username":"admin","password":"long-password-1"}`
	for _, tc := range []struct{ host, origin string }{{"evil.test", ""}, {"localhost:18764", "https://evil.test"}, {"localhost:18764", "null"}} {
		w := authRequest(a, "POST", "/api/auth/setup", body, "", tc.host, tc.origin)
		if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
	if yes, e := a.Admin.Initialized(); yes || e != nil {
		t.Fatal("rejected setup mutated state", yes, e)
	}
	for _, payload := range []string{body + `{}`, `{"username":"admin","password":"long-password-1","extra":true}`} {
		w := authRequest(a, "POST", "/api/auth/setup", payload, "", "localhost:18764", "")
		if w.Code != 400 {
			t.Fatal(w.Code)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost:18764/api/auth/setup", strings.NewReader(body))
	r.Header.Set("Content-Type", "text/plain")
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatal(w.Code)
	}
}
func TestHTTPAdminLoginRateLimit(t *testing.T) {
	a := testAdminAPI(t)
	if e := a.Admin.Setup("admin", "long-password-1"); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 6; i++ {
		w := authRequest(a, "POST", "/api/auth/login", `{"username":"wrong","password":"wrong"}`, "", "localhost:18764", "")
		want := 401
		if i == 5 {
			want = 429
		}
		if w.Code != want {
			t.Fatal(i, w.Code)
		}
	}
}
