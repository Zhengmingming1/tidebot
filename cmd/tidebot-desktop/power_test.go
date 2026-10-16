package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestShouldDefer(t *testing.T) {
	if d, _ := shouldDefer(powerState{}); d {
		t.Fatal("unknown platform must never defer")
	}
	if d, _ := shouldDefer(powerState{OK: true, OnBattery: true, Percent: 50}); d {
		t.Fatal("50% on battery is fine")
	}
	if d, why := shouldDefer(powerState{OK: true, OnBattery: true, Percent: 12}); !d || why == "" {
		t.Fatal("low battery should defer")
	}
	if d, _ := shouldDefer(powerState{OK: true, Percent: 5}); d {
		t.Fatal("charging should not defer")
	}
}

func TestReadLinuxPower(t *testing.T) {
	root := t.TempDir()
	bat := filepath.Join(root, "BAT0")
	_ = os.MkdirAll(bat, 0o755)
	for k, v := range map[string]string{"type": "Battery\n", "status": "Discharging\n", "capacity": "15\n"} {
		_ = os.WriteFile(filepath.Join(bat, k), []byte(v), 0o644)
	}
	t.Setenv("TIDEBOT_POWER_SUPPLY_ROOT", root)
	st := readLinuxPower()
	if !st.OnBattery || st.Percent != 15 {
		t.Fatalf("%+v", st)
	}
}

func TestAutostartLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("XDG autostart is Linux-only")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := setAutostart(true, true); err != nil {
		t.Fatal(err)
	}
	dir, _ := os.UserConfigDir()
	b, err := os.ReadFile(filepath.Join(dir, "autostart", "tidebot.desktop"))
	if err != nil || len(b) == 0 {
		t.Fatal(err)
	}
	if err := setAutostart(false, false); err != nil {
		t.Fatal(err)
	}
}
