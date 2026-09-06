// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jamesplotts/layforge/registry/internal/lobby"
)

func TestClientIP_PrefersFirstForwardedForHop(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.168.1.56:12345" // the reverse proxy's own address
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 192.168.1.45")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("clientIP() = %q, want 203.0.113.9 (the original client, not the proxy hop)", got)
	}
}

func TestClientIP_FallsBackToRemoteAddr(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "203.0.113.9:54321"
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("clientIP() = %q, want 203.0.113.9", got)
	}
}

func TestSecurityHeaders_SetsExpectedHeaders(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	rec := httptest.NewRecorder()
	securityHeaders(inner).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	want := map[string]string{
		"Strict-Transport-Security": "max-age=63072000; includeSubDomains",
		"X-Content-Type-Options":    "nosniff",
		"X-Frame-Options":           "DENY",
		"Referrer-Policy":           "strict-origin-when-cross-origin",
		"Content-Security-Policy":   "default-src 'self'; frame-ancestors 'none'",
	}
	for header, expected := range want {
		if got := rec.Header().Get(header); got != expected {
			t.Errorf("header %s = %q, want %q", header, got, expected)
		}
	}
}

func TestRateLimit_AllowsWriteWithinBurstThenRejects(t *testing.T) {
	limiter := lobby.NewIPRateLimiter(1, 2)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := rateLimitWrites(limiter, inner)

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/listings", nil)
		req.RemoteAddr = "203.0.113.9:1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST #%d status = %d, want 200 (within burst)", i, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/listings", nil)
	req.RemoteAddr = "203.0.113.9:1"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("POST after burst status = %d, want 429", rec.Code)
	}
}

func TestRateLimit_NeverThrottlesGET(t *testing.T) {
	limiter := lobby.NewIPRateLimiter(1, 1)
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	handler := rateLimitWrites(limiter, inner)

	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/listings", nil)
		req.RemoteAddr = "203.0.113.9:1"
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET #%d status = %d, want 200 (reads are never rate-limited)", i, rec.Code)
		}
	}
}
