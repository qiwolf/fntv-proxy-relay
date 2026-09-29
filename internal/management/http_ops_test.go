package management

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPConfigurationAndHandoff(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	makeAPI := func(name string) *API {
		store, e := NewDocumentStore(filepath.Join(dir, name, "config"), 0)
		if e != nil {
			t.Fatal(e)
		}
		keys, e := NewKeyStore(filepath.Join(dir, name, "keys"))
		if e != nil {
			t.Fatal(e)
		}
		api, e := NewAPI(store, keys, strings.Repeat("t", 32), "localhost:19864")
		if e != nil {
			t.Fatal(e)
		}
		return api
	}
	call := func(api *API, path string, body interface{}, want int) []byte {
		raw, _ := json.Marshal(body)
		r := httptest.NewRequest("POST", "http://localhost:19864/api/"+path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("t", 32))
		w := httptest.NewRecorder()
		api.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	proxy := makeAPI("proxy")
	media := makeAPI("media")
	cfg := Config{Version: 1, Mode: "split", Role: "proxy", Listen: Listener{Port: 29000}, PublicBaseURL: "https://media.example.test", Services: map[string]ServiceConfig{"fntv": {Enabled: true, Target: "http://127.0.0.1:8005", Hosts: []string{"tv.example.test"}, AllowedUpstreams: []string{"127.0.0.1:20000"}}}}
	call(proxy, "config", map[string]interface{}{"version": 0, "data": cfg}, 200)
	call(proxy, "keys/generate", map[string]interface{}{"version": 1}, 200)
	raw := call(proxy, "bundle/export", map[string]interface{}{"version": 1, "password": "test-package-password"}, 200)
	var exported struct {
		Bundle string `json:"bundle"`
	}
	if json.Unmarshal(raw, &exported) != nil {
		t.Fatal("decode")
	}
	call(media, "bundle/import", map[string]interface{}{"version": 0, "password": "wrong-password", "bundle": exported.Bundle}, 400)
	call(media, "bundle/import", map[string]interface{}{"version": 0, "password": "test-package-password", "bundle": exported.Bundle}, 200)
	call(media, "bundle/import", map[string]interface{}{"version": 0, "password": "test-package-password", "bundle": exported.Bundle}, 409)
	call(proxy, "bundle/import", map[string]interface{}{"version": 1, "password": "test-package-password", "bundle": exported.Bundle}, 400)
	d, e := media.Store.Read()
	if e != nil {
		t.Fatal(e)
	}
	c, e := DecodeConfigJSON(bytes.NewReader(d.Data))
	if e != nil || c.Role != "media" || c.Services["fntv"].Target != "" {
		t.Fatal("invalid media import", e)
	}
	a, _ := proxy.Keys.Material([]string{"fntv"})
	b, _ := media.Keys.Material([]string{"fntv"})
	if a["fntv"] != b["fntv"] {
		t.Fatal("key mismatch")
	}
	call(media, "keys/generate", map[string]interface{}{"version": 1}, 400)
	call(proxy, "config/validate", map[string]string{"text": "version: 1\nunknown: secret"}, 400)
}
