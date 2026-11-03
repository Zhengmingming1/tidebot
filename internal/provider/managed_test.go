package provider

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProviderFilesCannotClaimManagedOwnership(t *testing.T) {
	name := filepath.Join(t.TempDir(), "providers.json")
	if err := os.WriteFile(name, []byte(`{"providers":[{"name":"kilo-free","managed_by":"kilo-free-pool","base_url":"https://example.com"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	profiles, err := LoadFile(name)
	if err != nil || len(profiles) != 1 || profiles[0].ManagedBy != "" {
		t.Fatalf("file supplied managed provider ownership: %+v %v", profiles, err)
	}
}
