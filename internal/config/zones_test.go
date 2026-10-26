package config

import (
	"testing"
	"time"
)

// Every zone the picker offers loads (embedded tzdata, so Windows too), once.
func TestZonesLoad(t *testing.T) {
	seen := map[string]bool{}
	for _, z := range Zones {
		if seen[z] {
			t.Fatalf("duplicate %s", z)
		}
		seen[z] = true
		if _, err := time.LoadLocation(z); err != nil {
			t.Fatalf("%s: %v", z, err)
		}
	}
	for _, z := range []string{"UTC", "Asia/Shanghai", "America/New_York", "Europe/London", "Asia/Kolkata"} {
		if !KnownZone(z) {
			t.Fatalf("missing %s", z)
		}
	}
	for _, z := range []string{"", "Local", "Mars/Base", "asia/shanghai"} {
		if KnownZone(z) {
			t.Fatalf("accepted %q", z)
		}
	}
}
