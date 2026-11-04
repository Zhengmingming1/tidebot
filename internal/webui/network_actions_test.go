package webui

import (
	"os"
	"strings"
	"testing"
)

func TestNetworkActionsUseAlignedRow(t *testing.T) {
	source, err := os.ReadFile("src/settings-security.js")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(source), `<div class="network-actions">`)
	if start < 0 {
		t.Fatal("network buttons need a shared flex row")
	}
	row := strings.SplitN(string(source)[start:], "</div>", 2)[0]
	for _, id := range []string{`id="net-test"`, `id="net-save"`} {
		if !strings.Contains(row, id) {
			t.Fatalf("%s must share the aligned row", id)
		}
	}
	if strings.Count(row, `type="button"`) != 2 {
		t.Fatal("network actions must not submit forms")
	}
	css, err := os.ReadFile("static/styles.css")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(css), ".network-actions { display: flex; align-items: center;") {
		t.Fatal("network row must align mixed icon/text buttons centrally")
	}
}
