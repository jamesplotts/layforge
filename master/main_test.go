// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// chdir switches the process's cwd to dir for the duration of the
// calling test, restoring it via t.Cleanup — used by
// defaultWebDir/defaultAdminWebDir's own tests below, which must
// observe a real os.Getwd() since that's exactly what those functions
// themselves call.
func chdir(t *testing.T, dir string) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(orig); err != nil {
			t.Fatalf("restoring cwd to %q: %v", orig, err)
		}
	})
}

// TestDefaultWebDir_PrefersCWDRelative_OverExecutableRelative covers the
// real bug this guards against: 'go run .' compiles to a throwaway
// os.TempDir() build directory and executes it from there, so
// os.Executable() alone always resolves to a "web" directory that can
// never exist — silently disabling the web client for literally every
// user following Quick Start's own documented 'go run .' instruction. A
// cwd-relative "web" (present when running from within master/, whether
// via 'go run .' or a locally built binary) must win over that.
func TestDefaultWebDir_PrefersCWDRelative_OverExecutableRelative(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "web"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	chdir(t, dir)

	if got, want := defaultWebDir(), "web"; got != want {
		t.Errorf("defaultWebDir() = %q, want %q", got, want)
	}
}

// TestDefaultWebDir_FallsBackToExecutableRelative_WhenNoCWDRelativeWebDir
// covers the other real deployment shape: a compiled binary launched
// with its own "web" directory alongside it, but from an unrelated cwd
// (e.g. a systemd unit whose WorkingDirectory differs, or an operator
// invoking it via an absolute path from their home directory) — this
// must still resolve relative to the executable, exactly as before this
// fix, rather than a bare "web" that doesn't exist from that cwd.
func TestDefaultWebDir_FallsBackToExecutableRelative_WhenNoCWDRelativeWebDir(t *testing.T) {
	chdir(t, t.TempDir())

	got := defaultWebDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	if want := filepath.Join(filepath.Dir(exe), "web"); got != want {
		t.Errorf("defaultWebDir() = %q, want %q", got, want)
	}
}

func TestDefaultAdminWebDir_PrefersCWDRelative_OverExecutableRelative(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "admin-web"), 0o755); err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	chdir(t, dir)

	if got, want := defaultAdminWebDir(), "admin-web"; got != want {
		t.Errorf("defaultAdminWebDir() = %q, want %q", got, want)
	}
}

func TestListenURL(t *testing.T) {
	tests := []struct {
		name string
		addr string
		want string
	}{
		{name: "BareColonPort_DefaultsHostToLocalhost", addr: ":8080", want: "http://localhost:8080/"},
		{name: "AllInterfaces_DefaultsHostToLocalhost", addr: "0.0.0.0:8080", want: "http://localhost:8080/"},
		{name: "IPv6AllInterfaces_DefaultsHostToLocalhost", addr: "[::]:8080", want: "http://localhost:8080/"},
		{name: "ExplicitLoopback_KeptAsIs", addr: "127.0.0.1:8090", want: "http://127.0.0.1:8090/"},
		{name: "ExplicitLANAddress_KeptAsIs", addr: "192.168.1.56:8091", want: "http://192.168.1.56:8091/"},
		{name: "Unparseable_FallsBackToRawAddr", addr: "not-a-valid-addr", want: "http://not-a-valid-addr/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listenURL(tt.addr); got != tt.want {
				t.Errorf("listenURL(%q) = %q, want %q", tt.addr, got, tt.want)
			}
		})
	}
}
