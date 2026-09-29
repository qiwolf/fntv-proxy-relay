package management

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIAuthenticationAndPersistence(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewDocumentStore(filepath.Join(dir, "config"), 0)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := NewKeyStore(filepath.Join(dir, "keys"))
	if err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("a", 64)
	api, err := NewAPI(store, keys, token, "127.0.0.1:18764")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, auth, origin, host string) int {
		r := httptest.NewRequest(method, "http://127.0.0.1:18764"+path, bytes.NewBufferString(body))
		r.Host = host
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", auth)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if strings.Contains(w.Body.String(), token) {
			t.Fatal("secret exposed")
		}
		return w.Code
	}
	host := "127.0.0.1:18764"
	auth := "Bearer " + token
	if got := call("GET", "/api/status", "", "", "", host); got != 401 {
		t.Fatal(got)
	}
	if got := call("GET", "/api/status", "", auth, "https://evil.example", host); got != 403 {
		t.Fatal(got)
	}
	if got := call("GET", "/api/status", "", auth, "", "relay.lan:19864"); got != 200 {
		t.Fatal(got)
	}
	if got := call("GET", "/api/status", "", auth, "", host); got != 200 {
		t.Fatal(got)
	}
	body := `{"version":0,"data":{"version":1,"mode":"redirect","listen":{"port":28005},"services":{"fntv":{"enabled":true,"target":"http://127.0.0.1:8005"}}}}`
	if got := call("POST", "/api/config", body, auth, "", host); got != 200 {
		t.Fatal(got)
	}
	if got := call("POST", "/api/config", body, auth, "", host); got != 409 {
		t.Fatal(got)
	}
	if got := call("POST", "/api/config", body+`{}`, auth, "", host); got != 400 {
		t.Fatal(got)
	}
	if got := call("POST", "/api/apply", "{}", auth, "", host); got != 400 {
		t.Fatal(got)
	}
	doc, err := store.Read()
	if err != nil || doc.Version != 1 {
		t.Fatal(doc.Version, err)
	}
}
