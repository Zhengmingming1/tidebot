package webui

import (
	"bytes"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zhengmingming1/tidebot/internal/db"
)

func TestSettingsFailureReturnsAndLogsServerError(t *testing.T) {
	c := login(t)
	brokenDB, err := db.Open(filepath.Join(t.TempDir(), "broken.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := brokenDB.Close(); err != nil {
		t.Fatal(err)
	}
	c.s.deps.Cost.DB = brokenDB
	var logs bytes.Buffer
	c.s.deps.Log = slog.New(slog.NewTextHandler(&logs, nil))

	status, body := c.json(http.MethodGet, "/api/settings", nil)
	if status != http.StatusInternalServerError || !strings.Contains(body["error"].(string), "database is closed") {
		t.Fatalf("settings failure: status=%d body=%v", status, body)
	}
	for _, want := range []string{"path=/api/settings", "status=500", `operation="cost summary"`, "database is closed"} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %q: %s", want, logs.String())
		}
	}
}
