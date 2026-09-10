// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package admin

import (
	"net/http/httptest"
	"testing"
)

func TestClientURLForOperator(t *testing.T) {
	tests := []struct {
		name       string
		host       string
		playerAddr string
		want       string
	}{
		{"loopback panel, wildcard player bind", "127.0.0.1:8090", ":8080", "http://127.0.0.1:8080/"},
		{"lan panel, wildcard player bind", "192.168.1.5:8090", "0.0.0.0:8085", "http://192.168.1.5:8085/"},
		{"localhost panel, explicit player host", "localhost:8090", "127.0.0.1:8080", "http://localhost:8080/"},
		{"panel host has no port", "layforge.local", ":8080", "http://layforge.local:8080/"},
		{"player addr has no port", "127.0.0.1:8090", "notaport", ""},
		{"player addr empty", "127.0.0.1:8090", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/api/session", nil)
			r.Host = tt.host
			if got := clientURLForOperator(r, tt.playerAddr); got != tt.want {
				t.Errorf("clientURLForOperator(%q, %q) = %q, want %q", tt.host, tt.playerAddr, got, tt.want)
			}
		})
	}
}
