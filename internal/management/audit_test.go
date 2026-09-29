package management

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestAuditBoundedAndLease(t *testing.T) {
	dir, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a := &API{}
	a.Audit, e = NewDocumentStore(filepath.Join(dir, "audit"), 0)
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 105; i++ {
		a.recordAudit("/api/config", 200)
	}
	d, e := a.Audit.Read()
	if e != nil {
		t.Fatal(e)
	}
	var events []auditEvent
	if e = json.Unmarshal(d.Data, &events); e != nil || len(events) != 100 {
		t.Fatal(len(events), e)
	}
	backups, e := a.Audit.Backups()
	if e != nil || len(backups) != 0 {
		t.Fatal("audit must remain bounded", e)
	}
	lease, e := AcquireManagerLease(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer lease.Close()
	if duplicate, e := AcquireManagerLease(dir); e == nil {
		duplicate.Close()
		t.Fatal("second manager accepted")
	}
}
