// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package scrcpy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type fakeRunner struct {
	out  []byte
	err  error
	last struct {
		name string
		args []string
	}
}

func (r *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.last.name = name
	r.last.args = append([]string(nil), args...)
	return r.out, r.err
}

func TestLoadDevIgnore_Missing(t *testing.T) {
	got, err := LoadDevIgnore(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Errorf("missing file should be nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should return empty slice, got %v", got)
	}
}

func TestLoadDevIgnore_Parse(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".devignore")
	content := `
# This is a comment
ATMOSphere
  RedoxRaven

# trailing comment
NanoPi
`
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadDevIgnore(f)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ATMOSphere", "RedoxRaven", "NanoPi"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q want %q", i, got[i], want[i])
		}
	}
}

func TestMatchModel(t *testing.T) {
	cases := []struct {
		patterns []string
		model    string
		want     bool
	}{
		{[]string{"ATMOSphere"}, "ATMOSphere", true},
		{[]string{"ATMOSphere"}, "ATMOSphere TV", true},
		{[]string{"atmosphere"}, "ATMOSphere TV", true}, // case-insensitive
		{[]string{"ATMOSphere"}, "MiBox", false},
		{[]string{}, "anything", false},
		{[]string{"ATMOSphere"}, "", false},
		{[]string{"   ", "NanoPi"}, "NanoPi 4B", true}, // whitespace entries ignored
	}
	for _, tc := range cases {
		got := MatchModel(tc.patterns, tc.model)
		if got != tc.want {
			t.Errorf("MatchModel(%v, %q) = %v, want %v", tc.patterns, tc.model, got, tc.want)
		}
	}
}

func TestDeviceModel_HappyPath(t *testing.T) {
	r := &fakeRunner{out: []byte("MiBox  \n")}
	got, err := DeviceModel(context.Background(), r, "192.0.2.214:5555")
	if err != nil {
		t.Fatal(err)
	}
	if got != "MiBox" {
		t.Errorf("got %q want MiBox (should trim whitespace)", got)
	}
	if r.last.name != "adb" {
		t.Errorf("cmd = %q", r.last.name)
	}
	want := []string{"-s", "192.0.2.214:5555", "shell", "getprop", "ro.product.model"}
	if len(r.last.args) != len(want) {
		t.Fatalf("args = %v", r.last.args)
	}
	for i := range want {
		if r.last.args[i] != want[i] {
			t.Errorf("args[%d] = %q want %q", i, r.last.args[i], want[i])
		}
	}
}

func TestDeviceModel_NoSerial(t *testing.T) {
	r := &fakeRunner{out: []byte("Pixel 7\n")}
	got, err := DeviceModel(context.Background(), r, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Pixel 7" {
		t.Errorf("got %q", got)
	}
	// No -s should appear when serial is empty.
	for _, a := range r.last.args {
		if a == "-s" {
			t.Errorf("-s should not appear with empty serial, got args=%v", r.last.args)
		}
	}
}

func TestDeviceModel_NilRunner(t *testing.T) {
	_, err := DeviceModel(context.Background(), nil, "x")
	if err == nil {
		t.Error("nil runner must error")
	}
}

func TestDeviceModel_RunnerError(t *testing.T) {
	boom := errors.New("adb unavailable")
	r := &fakeRunner{err: boom}
	_, err := DeviceModel(context.Background(), r, "x")
	if !errors.Is(err, boom) {
		t.Errorf("want %v wrapped, got %v", boom, err)
	}
}

func TestEnforceDevIgnore_Safe(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".devignore")
	_ = os.WriteFile(f, []byte("ATMOSphere\n"), 0o644)
	r := &fakeRunner{out: []byte("Pixel 7\n")}
	if err := EnforceDevIgnore(context.Background(), r, "serial", f); err != nil {
		t.Errorf("safe device rejected: %v", err)
	}
}

func TestEnforceDevIgnore_Blocked(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".devignore")
	_ = os.WriteFile(f, []byte("ATMOSphere\n"), 0o644)
	r := &fakeRunner{out: []byte("ATMOSphere TV Box\n")}
	err := EnforceDevIgnore(context.Background(), r, "serial", f)
	if !errors.Is(err, ErrDeviceBlocked) {
		t.Errorf("want ErrDeviceBlocked, got %v", err)
	}
}

func TestEnforceDevIgnore_NoIgnoreFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	r := &fakeRunner{out: []byte("Anything\n")}
	if err := EnforceDevIgnore(context.Background(), r, "serial", missing); err != nil {
		t.Errorf("missing .devignore should not error, got %v", err)
	}
}

func TestEnforceDevIgnore_AdbFailure(t *testing.T) {
	f := filepath.Join(t.TempDir(), ".devignore")
	_ = os.WriteFile(f, []byte("ATMOSphere\n"), 0o644)
	boom := errors.New("no such device")
	r := &fakeRunner{err: boom}
	err := EnforceDevIgnore(context.Background(), r, "serial", f)
	if !errors.Is(err, boom) {
		t.Errorf("want runner error wrapped, got %v", err)
	}
}
