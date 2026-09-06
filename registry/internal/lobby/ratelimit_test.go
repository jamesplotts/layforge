// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package lobby_test

import (
	"testing"
	"time"

	"github.com/jamesplotts/layforge/registry/internal/lobby"
)

func TestIPRateLimiter_Allow_PermitsUpToBurstThenBlocks(t *testing.T) {
	l := lobby.NewIPRateLimiter(1, 3)
	ip := "203.0.113.1"

	for i := 0; i < 3; i++ {
		if !l.Allow(ip) {
			t.Fatalf("Allow() #%d = false, want true (within burst)", i)
		}
	}
	if l.Allow(ip) {
		t.Error("Allow() after exhausting burst = true, want false")
	}
}

func TestIPRateLimiter_Allow_TracksIPsIndependently(t *testing.T) {
	l := lobby.NewIPRateLimiter(1, 1)
	if !l.Allow("203.0.113.1") {
		t.Error("Allow(ip1) #1 = false, want true")
	}
	if !l.Allow("203.0.113.2") {
		t.Error("Allow(ip2) #1 = false, want true (independent bucket)")
	}
	if l.Allow("203.0.113.1") {
		t.Error("Allow(ip1) #2 = true, want false (burst exhausted)")
	}
}

func TestIPRateLimiter_Allow_RefillsOverTime(t *testing.T) {
	l := lobby.NewIPRateLimiter(1000, 1) // 1000 tokens/sec refill, easy to observe within a test
	ip := "203.0.113.1"
	if !l.Allow(ip) {
		t.Fatal("Allow() #1 = false, want true")
	}
	if l.Allow(ip) {
		t.Fatal("Allow() #2 = true, want false (burst exhausted)")
	}
	time.Sleep(10 * time.Millisecond)
	if !l.Allow(ip) {
		t.Error("Allow() after waiting for refill = false, want true")
	}
}

func TestIPRateLimiter_Sweep_RemovesIdleEntries(t *testing.T) {
	l := lobby.NewIPRateLimiter(1, 1)
	l.Allow("203.0.113.1")
	if got := l.Len(); got != 1 {
		t.Fatalf("Len() = %d, want 1", got)
	}
	time.Sleep(5 * time.Millisecond)
	l.Sweep(time.Millisecond)
	if got := l.Len(); got != 0 {
		t.Errorf("Len() after Sweep = %d, want 0", got)
	}
}

func TestIPRateLimiter_Sweep_KeepsRecentEntries(t *testing.T) {
	l := lobby.NewIPRateLimiter(1, 1)
	l.Allow("203.0.113.1")
	l.Sweep(time.Hour)
	if got := l.Len(); got != 1 {
		t.Errorf("Len() after Sweep(1h) on a fresh entry = %d, want 1", got)
	}
}
