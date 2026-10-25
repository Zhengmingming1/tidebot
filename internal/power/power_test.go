package power

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParsePmsetBatt(t *testing.T) {
	cases := []struct {
		name, out string
		want      State
	}{
		{"laptop unplugged", "Now drawing from 'Battery Power'\n -InternalBattery-0 (id=4653155)\t85%; discharging; 5:12 remaining present: true\n",
			State{Known: true, HasBattery: true, OnBattery: true, Percent: 85}},
		{"laptop plugged, CRLF", "Now drawing from 'AC Power'\r\n -InternalBattery-0 (id=4653155)\t100%; charged; 0:00 remaining present: true\r\n",
			State{Known: true, HasBattery: true, Percent: 100}},
		{"mac mini", "Now drawing from 'AC Power'\n", State{Known: true, Percent: -1}},
		{"ups", "Now drawing from 'UPS Power'\n", State{Known: true, OnBattery: true, Percent: -1}},
		{"no pmset", "", Unknown},
		{"garbage", "pmset: command not found", Unknown},
	}
	for _, c := range cases {
		if got := ParsePmsetBatt(c.out); got != c.want {
			t.Errorf("%s: got %+v want %+v", c.name, got, c.want)
		}
	}
}

func TestParsePmsetLowPower(t *testing.T) {
	for out, want := range map[string]bool{
		"System-wide power settings:\n lowpowermode         1\n sleep 1\n": true,
		" powermode            1\n":                                        true,
		" powermode            2\n":                                        false, // High Power Mode
		" lowpowermode         0\n":                                        false,
		" standby              1\n":                                        false,
		"":                                                                 false,
		" lowpowermode 1\r\n hibernate":                                    true,
	} {
		if got := ParsePmsetLowPower(out); got != want {
			t.Errorf("%q: got %v", out, got)
		}
	}
}

func writeSupply(t *testing.T, root, name string, files map[string]string) {
	t.Helper()
	d := filepath.Join(root, name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	for k, v := range files {
		if err := os.WriteFile(filepath.Join(d, k), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReadSysfs(t *testing.T) {
	// Laptop unplugged, with a Bluetooth mouse battery that must not count.
	root := t.TempDir()
	writeSupply(t, root, "AC", map[string]string{"type": "Mains", "online": "0"})
	writeSupply(t, root, "BAT0", map[string]string{"type": "Battery", "capacity": "17", "status": "Discharging", "present": "1"})
	writeSupply(t, root, "hidpp_battery_0", map[string]string{"type": "Battery", "scope": "Device", "capacity": "90", "status": "Discharging"})
	prof := filepath.Join(t.TempDir(), "platform_profile")
	_ = os.WriteFile(prof, []byte("low-power\n"), 0o644)
	if got := ReadSysfs(root, prof); got != (State{Known: true, HasBattery: true, OnBattery: true, Percent: 17, Saver: true}) {
		t.Fatalf("unplugged %+v", got)
	}
	// Plugged in again.
	writeSupply(t, root, "AC", map[string]string{"online": "1"})
	if got := ReadSysfs(root, ""); got.OnBattery || !got.HasBattery || got.Percent != 17 {
		t.Fatalf("plugged %+v", got)
	}
	// USB-C charger online, mains adapter offline: still on AC.
	writeSupply(t, root, "AC", map[string]string{"online": "0"})
	writeSupply(t, root, "ucsi-source-psy-USBC000:001", map[string]string{"type": "USB", "online": "1"})
	if got := ReadSysfs(root, ""); got.OnBattery {
		t.Fatalf("usb-c %+v", got)
	}
	// Server: only a mouse battery, or nothing at all.
	srv := t.TempDir()
	writeSupply(t, srv, "hid-mouse", map[string]string{"type": "Battery", "scope": "Device", "capacity": "5", "status": "Discharging"})
	if got := ReadSysfs(srv, ""); got != (State{Known: true, Percent: -1}) {
		t.Fatalf("server %+v", got)
	}
	// No mains entry, battery discharging (some boards).
	odd := t.TempDir()
	writeSupply(t, odd, "BAT1", map[string]string{"type": "Battery", "capacity": "60", "status": "Discharging"})
	if got := ReadSysfs(odd, ""); !got.OnBattery || got.Percent != 60 {
		t.Fatalf("no mains %+v", got)
	}
	if got := ReadSysfs(filepath.Join(srv, "missing"), ""); got != Unknown {
		t.Fatalf("missing root %+v", got)
	}
}

func TestFromWindows(t *testing.T) {
	cases := []struct {
		ac, flag, pct, saver byte
		want                 State
	}{
		{0, 0, 42, 1, State{Known: true, HasBattery: true, OnBattery: true, Percent: 42, Saver: true}},
		{1, 8, 80, 0, State{Known: true, HasBattery: true, Percent: 80}}, // charging
		{1, 128, 255, 0, State{Known: true, Percent: -1}},                // desktop: no battery
		{255, 255, 255, 0, Unknown},
		{0, 2, 255, 0, State{Known: true, HasBattery: true, OnBattery: true, Percent: -1}}, // low, % unknown
	}
	for i, c := range cases {
		if got := FromWindows(c.ac, c.flag, c.pct, c.saver); got != c.want {
			t.Errorf("%d: got %+v want %+v", i, got, c.want)
		}
	}
}

func TestReadDoesNotPanic(t *testing.T) {
	st := Read()
	if st.Percent < -1 || st.Percent > 100 {
		t.Fatalf("percent %d", st.Percent)
	}
}
