package management

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

type auditEvent struct {
	At     time.Time `json:"at"`
	Action string    `json:"action"`
	Status int       `json:"status"`
}
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }

// Only fixed action names and status codes, never request bodies or tokens.
func (a *API) recordAudit(action string, status int) {
	if a.Audit == nil {
		return
	}
	_ = a.Audit.locked(func() error {
		d, e := a.Audit.read("current.json")
		if e != nil && !errors.Is(e, ErrDocumentNotFound) {
			return e
		}
		events := []auditEvent{}
		if d.Version > 0 {
			if e = json.Unmarshal(d.Data, &events); e != nil {
				return e
			}
		}
		events = append(events, auditEvent{time.Now().UTC(), action, status})
		if len(events) > 100 {
			events = events[len(events)-100:]
		}
		raw, _ := json.Marshal(events)
		return a.Audit.write("current.json", Document{Version: d.Version + 1, Data: raw})
	})
}
