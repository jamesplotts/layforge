// Copyright (c) 2026 James Duane Plotts
// Licensed under the MIT License. See LICENSE in the repository root.

package lobby

import (
	"sync"
	"time"
)

// IPRateLimiter is a simple per-IP token-bucket limiter guarding the
// write endpoints (POST/PUT/DELETE /api/v1/listings) against a single
// source hammering the API — spam listings, or just wasted CPU/memory
// churn. Deliberately hand-rolled rather than a dependency: this is a
// small, self-contained mutex+map, the same shape Store itself already
// uses, and registry's own go.mod is intentionally dependency-free (see
// its package doc comment).
//
// This only bounds a single IP's own rate — a distributed attempt from
// many different source IPs is Store's own ErrStoreFull cap's job, not
// this type's.
type IPRateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
	rate    float64 // tokens added per second
	burst   float64 // max tokens a bucket can hold
}

type bucket struct {
	tokens   float64
	lastSeen time.Time
}

// NewIPRateLimiter creates an IPRateLimiter that refills each IP's
// bucket at rate tokens/second, up to burst tokens held at once (burst
// is also each bucket's starting balance minus the first Allow call, so
// a brand new IP can make up to burst calls in a tight cluster before
// being throttled to the steady-state rate).
func NewIPRateLimiter(rate, burst float64) *IPRateLimiter {
	return &IPRateLimiter{buckets: make(map[string]*bucket), rate: rate, burst: burst}
}

// Allow reports whether ip may make one more call right now, consuming
// one token from its bucket if so.
func (l *IPRateLimiter) Allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	b, ok := l.buckets[ip]
	if !ok {
		b = &bucket{tokens: l.burst, lastSeen: now}
		l.buckets[ip] = b
	} else {
		elapsed := now.Sub(b.lastSeen).Seconds()
		b.tokens += elapsed * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.lastSeen = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// Sweep deletes any bucket whose IP hasn't made a call in more than
// maxIdle — bounds the buckets map's own memory even under a sustained
// attempt from many distinct/spoofed source IPs, the same "periodic
// reclaim" role Store.Sweep plays for listings themselves.
func (l *IPRateLimiter) Sweep(maxIdle time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-maxIdle)
	for ip, b := range l.buckets {
		if b.lastSeen.Before(cutoff) {
			delete(l.buckets, ip)
		}
	}
}

// Len reports how many IPs currently have a tracked bucket — exposed
// only so tests can observe Sweep's effect directly.
func (l *IPRateLimiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}
