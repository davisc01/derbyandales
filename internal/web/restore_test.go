package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/davisc01/derbyandales/internal/app"
)

// Going back to a backup from the status page: it has to refuse anything that
// is not one of the listed backups, and it has to stop the app, because the
// swap only happens on the next launch.
func TestABackupIsRestoredFromTheStatusPage(t *testing.T) {
	s, a := testServer(t)
	stopped := make(chan struct{}, 1)
	s.OnQuit(func() { stopped <- struct{}{} })
	h := handler(t, s)

	rec := get(t, s, "/")
	if !strings.Contains(rec.Body.String(), "Go back to this") {
		t.Fatal("the status page offers no way to restore a backup")
	}

	code, _ := postForm(t, h, "/api/backup/restore", url.Values{"name": {"../derby.sqlite3"}})
	if code != http.StatusBadRequest {
		t.Errorf("restoring an arbitrary file answered %d", code)
	}

	backups := app.ListBackups(a.Paths.Backups)
	code, resp := postForm(t, h, "/api/backup/restore", url.Values{"name": {backups[0].Name()}})
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, resp)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("staging a restore did not stop the app")
	}
	if !app.RestorePending(a.Paths) {
		t.Error("no restore is pending")
	}

	if rec := get(t, s, "/"); !strings.Contains(rec.Body.String(), "Restore waiting") {
		t.Error("the status page does not say a restore is waiting")
	}
	postForm(t, h, "/api/backup/restore/cancel", nil)
	if app.RestorePending(a.Paths) {
		t.Error("cancelling left the restore pending")
	}
}
