// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package main

import "testing"

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
