package management

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (a *API) serveOperations(w http.ResponseWriter, r *http.Request) bool {
	var result interface{}
	var err error
	switch r.Method + " " + r.URL.Path {
	case "GET /api/logs":
		result = []auditEvent{}
		if a.Audit != nil {
			var d Document
			d, err = a.Audit.Read()
			if errors.Is(err, ErrDocumentNotFound) {
				err = nil
			} else if err == nil {
				err = json.Unmarshal(d.Data, &result)
			}
		}
	case "POST /api/config/validate":
		var in struct {
			Text string `json:"text"`
		}
		if err = requestJSON(w, r, &in); err == nil {
			result, err = DecodeConfig(strings.NewReader(in.Text))
		}
	case "POST /api/apply":
		var in struct {
			Version uint64 `json:"version"`
		}
		if err = requestJSON(w, r, &in); err == nil {
			if a.Runtime == nil {
				err = errors.New("运行管理未配置")
			} else {
				err = a.Store.WithVersion(in.Version, func(doc Document) error {
					c, e := DecodeConfigJSON(bytes.NewReader(doc.Data))
					if e != nil {
						return e
					}
					assets := RuntimeAssets{}
					if c.Mode == "single" || c.Mode == "split" {
						ids := []string{}
						for id, s := range c.Services {
							if s.Enabled {
								ids = append(ids, id)
							}
						}
						assets.Keys, e = a.Keys.Material(ids)
						if e != nil {
							return e
						}
					}
					if c.Transport == "https" {
						if a.Certificates == nil {
							return errors.New("请先配置证书存储")
						}
						assets.CertificatePEM, assets.PrivateKeyPEM, e = a.Certificates.Material()
						if e != nil {
							return e
						}
					}
					return a.Runtime.Apply(*c, assets)
				})
				if err == nil {
					result = a.Runtime.Status()
				}
			}
		}
	case "POST /api/bundle/export":
		var in struct {
			Version  uint64 `json:"version"`
			Password string `json:"password"`
		}
		if err = requestJSON(w, r, &in); err == nil {
			err = a.Store.WithVersion(in.Version, func(doc Document) error {
				c, e := DecodeConfigJSON(bytes.NewReader(doc.Data))
				if e != nil {
					return e
				}
				raw, e := ExportBundle(*c, a.Keys, in.Password)
				if e == nil {
					result = map[string]string{"bundle": string(raw)}
				}
				return e
			})
		}
	case "POST /api/bundle/import":
		var in struct {
			Version  uint64 `json:"version"`
			Password string `json:"password"`
			Bundle   string `json:"bundle"`
		}
		if err = requestJSON(w, r, &in); err == nil {
			var bundle *MediaBundle
			bundle, err = OpenBundle([]byte(in.Bundle), in.Password)
			if err == nil {
				err = a.Store.locked(func() error {
					old, e := a.Store.read("current.json")
					if e != nil && !errors.Is(e, ErrDocumentNotFound) {
						return e
					}
					if old.Version != in.Version {
						return ErrVersionConflict
					}
					if old.Version > 0 {
						c, e := DecodeConfigJSON(bytes.NewReader(old.Data))
						if e != nil {
							return e
						}
						if c.Mode != "split" || c.Role != "media" {
							return errors.New("只能导入到媒体服务，不能覆盖主代理配置")
						}
					}
					c := bundle.Config()
					raw, e := json.Marshal(c)
					if e != nil {
						return e
					}
					// Only unused secrets can remain if disk saving subsequently fails; live
					// runtime and prior configuration are never changed by an import.
					if e = bundle.ImportKeys(a.Keys); e != nil {
						return e
					}
					result, e = a.Store.save(in.Version, raw)
					return e
				})
			}
		}
	case "GET /api/diagnostics":
		doc, e := a.Store.Read()
		err = e
		if e == nil {
			_, err = DecodeConfigJSON(bytes.NewReader(doc.Data))
			if err == nil {
				result = map[string]interface{}{"configuration_valid": true, "version": doc.Version, "playback_verified": false}
				if a.Runtime != nil {
					result.(map[string]interface{})["runtime"] = a.Runtime.Status()
				}
			}
		}
	default:
		if r.Method != "GET" || !strings.HasPrefix(r.URL.Path, "/api/backup/") {
			return false
		}
		var version uint64
		version, err = strconv.ParseUint(strings.TrimPrefix(r.URL.Path, "/api/backup/"), 10, 64)
		if err == nil {
			err = a.Store.locked(func() error { var e error; result, e = a.Store.read(fmt.Sprintf("backup-%d.json", version)); return e })
		}
	}
	if err != nil {
		status := 400
		if errors.Is(err, ErrVersionConflict) {
			status = 409
		}
		if errors.Is(err, ErrDocumentNotFound) {
			status = 404
		}
		respond(w, status, map[string]string{"error": err.Error()})
	} else {
		respond(w, 200, result)
	}
	return true
}
