package management

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
)

// API is deliberately separate from the video listeners. Authentication uses a
// high-entropy bearer token, never a URL parameter or an ambient browser cookie.
type API struct {
	Store        *DocumentStore
	Keys         *KeyStore
	Certificates *CertificateStore
	Admin        *AdminAuth
	Runtime      *RuntimeManager
	Audit        *DocumentStore
	token        [32]byte
}

func NewAPI(store *DocumentStore, keys *KeyStore, token, _ string) (*API, error) {
	if store == nil || keys == nil || len(token) < 32 || strings.TrimSpace(token) != token {
		return nil, errors.New("private stores and a strong token are required")
	}
	return &API{Store: store, Keys: keys, token: sha256.Sum256([]byte(token))}, nil
}

func respond(w http.ResponseWriter, status int, value interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestJSON(w http.ResponseWriter, r *http.Request, dst interface{}) error {
	t, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || t != "application/json" {
		return errors.New("application/json required")
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err = d.Decode(dst); err != nil {
		return errors.New("invalid request")
	}
	if d.Decode(new(interface{})) != io.EOF {
		return errors.New("one JSON document required")
	}
	return nil
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		allowedActions := map[string]bool{"/api/config": true, "/api/apply": true, "/api/keys/generate": true, "/api/certificate": true, "/api/bundle/import": true, "/api/bundle/export": true, "/api/auth/setup": true, "/api/auth/login": true, "/api/auth/password": true, "/api/auth/logout": true}
		if allowedActions[r.URL.Path] {
			rec := &statusWriter{ResponseWriter: w, status: 200}
			w = rec
			defer func() { a.recordAudit(r.URL.Path, rec.status) }()
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	// No host allowlist: compare the browser origin with this request's address.
	if r.Header.Get("Sec-Fetch-Site") == "cross-site" || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+r.Host && r.Header.Get("Origin") != "https://"+r.Host) {
		respond(w, 403, map[string]string{"error": "origin denied"})
		return
	}
	if a.Admin != nil && a.serveAuth(w, r) {
		return
	}
	provided := r.Header.Get("Authorization")
	hash := sha256.Sum256([]byte(strings.TrimPrefix(provided, "Bearer ")))
	allowed := strings.HasPrefix(provided, "Bearer ") && subtle.ConstantTimeCompare(hash[:], a.token[:]) == 1
	if a.Admin != nil {
		_, err := a.Admin.Authenticate(strings.TrimPrefix(provided, "Bearer "))
		allowed = strings.HasPrefix(provided, "Bearer ") && err == nil
	}
	if !allowed {
		respond(w, 401, map[string]string{"error": "authentication required"})
		return
	}
	if a.serveOperations(w, r) {
		return
	}
	var result interface{}
	var err error
	switch r.Method + " " + r.URL.Path {
	case "GET /api/status":
		result = map[string]interface{}{"config_save": true, "runtime_apply": a.Runtime != nil, "certificate_import": a.Certificates != nil, "handoff": true}
		if a.Runtime != nil {
			result.(map[string]interface{})["runtime"] = a.Runtime.Status()
		}
	case "GET /api/certificate":
		if a.Certificates == nil {
			err = errors.New("certificate storage unavailable")
		} else {
			result, err = a.Certificates.Status()
		}
	case "POST /api/certificate":
		var input struct {
			Version     uint64 `json:"version"`
			Certificate string `json:"certificate_pem"`
			PrivateKey  string `json:"private_key_pem"`
			PublicURL   string `json:"public_url"`
		}
		if err = requestJSON(w, r, &input); err == nil {
			if a.Certificates == nil {
				err = errors.New("certificate storage unavailable")
			} else {
				result, err = a.Certificates.Import(input.Version, []byte(input.Certificate), []byte(input.PrivateKey), input.PublicURL)
			}
		}
	case "GET /api/config":
		result, err = a.Store.Read()
	case "POST /api/config":
		var input Document
		if err = requestJSON(w, r, &input); err == nil {
			var cfg *Config
			cfg, err = DecodeConfigJSON(bytes.NewReader(input.Data))
			if err == nil {
				var data []byte
				data, err = json.Marshal(cfg)
				if err == nil {
					result, err = a.Store.Save(input.Version, data)
				}
			}
		}
	case "GET /api/backups":
		result, err = a.Store.Backups()
	case "GET /api/keys":
		result, err = a.Keys.Status()
	case "POST /api/keys/generate":
		var input struct {
			Version uint64 `json:"version"`
		}
		if err = requestJSON(w, r, &input); err == nil {
			err = a.Store.WithVersion(input.Version, func(doc Document) error {
				var cfg *Config
				cfg, err := DecodeConfigJSON(bytes.NewReader(doc.Data))
				if err == nil && (cfg.Mode != "single" && !(cfg.Mode == "split" && cfg.Role == "proxy")) {
					err = errors.New("this role does not generate keys")
				}
				if err == nil {
					ids := []string{}
					for id, s := range cfg.Services {
						if s.Enabled {
							ids = append(ids, id)
						}
					}
					err = a.Keys.Generate(ids)
					if err == nil {
						result, err = a.Keys.Status()
					}
				}
				return err
			})
		}
	default:
		respond(w, 404, map[string]string{"error": "unsupported operation"})
		return
	}
	if err != nil {
		status := 400
		message := "request rejected; existing configuration preserved"
		if errors.Is(err, ErrVersionConflict) {
			status = 409
			message = "configuration changed; reload before saving"
		}
		if errors.Is(err, ErrDocumentNotFound) {
			status = 404
			message = "configuration not saved yet"
		}
		respond(w, status, map[string]string{"error": message})
		return
	}
	respond(w, 200, result)
}

func (a *API) serveAuth(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/api/auth/") {
		return false
	}
	var result interface{}
	var err error
	switch r.Method + " " + r.URL.Path {
	case "GET /api/auth/status":
		var initialized bool
		initialized, err = a.Admin.Initialized()
		result = map[string]bool{"initialized": initialized}
	case "POST /api/auth/setup", "POST /api/auth/login":
		var in struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err = requestJSON(w, r, &in); err == nil {
			if r.URL.Path == "/api/auth/setup" {
				err = a.Admin.Setup(in.Username, in.Password)
			}
			if err == nil {
				var token string
				remote, _, _ := net.SplitHostPort(r.RemoteAddr)
				token, err = a.Admin.Login(in.Username, in.Password, remote)
				result = map[string]string{"token": token}
			}
		}
	case "POST /api/auth/password":
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			respond(w, 401, map[string]string{"error": "请先登录"})
			return true
		}
		var in struct {
			Old string `json:"old_password"`
			New string `json:"new_password"`
		}
		if err = requestJSON(w, r, &in); err == nil {
			err = a.Admin.ChangePassword(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "), in.Old, in.New)
		}
		result = map[string]bool{"changed": err == nil}
	case "POST /api/auth/logout":
		a.Admin.Logout(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		result = map[string]bool{"logged_out": true}
	default:
		respond(w, 404, map[string]string{"error": "未知操作"})
		return true
	}
	if err != nil {
		status := 400
		if errors.Is(err, ErrInvalidCredentials) || errors.Is(err, ErrUnauthenticated) {
			status = 401
		}
		if errors.Is(err, ErrRateLimited) {
			status = 429
		}
		respond(w, status, map[string]string{"error": "操作未完成：请检查账号、密码或稍后重试。首次设置不能覆盖已有账号。"})
	} else {
		respond(w, 200, result)
	}
	return true
}
