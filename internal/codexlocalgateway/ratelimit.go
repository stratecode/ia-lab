package codexlocalgateway

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateLimiter struct {
	mu      sync.Mutex
	rpm     int
	burst   int
	buckets map[string]*rateBucket
}

type rateBucket struct {
	tokens float64
	last   time.Time
}

func newRateLimiter(rpm, burst int) *rateLimiter {
	if rpm <= 0 {
		rpm = defaultRateLimitRPM
	}
	if burst <= 0 {
		burst = defaultRateLimitBurst
	}
	return &rateLimiter{
		rpm:     rpm,
		burst:   burst,
		buckets: make(map[string]*rateBucket),
	}
}

func (l *rateLimiter) allow(r *http.Request) bool {
	key := rateLimitKey(r)
	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	bucket, ok := l.buckets[key]
	if !ok {
		l.buckets[key] = &rateBucket{tokens: float64(l.burst - 1), last: now}
		return true
	}

	elapsed := now.Sub(bucket.last).Minutes()
	bucket.tokens += elapsed * float64(l.rpm)
	if bucket.tokens > float64(l.burst) {
		bucket.tokens = float64(l.burst)
	}
	bucket.last = now

	if bucket.tokens < 1 {
		return false
	}
	bucket.tokens--
	return true
}

func rateLimitKey(r *http.Request) string {
	auth := r.Header.Get("Authorization")
	if strings.HasPrefix(auth, "Bearer ") {
		sum := sha256.Sum256([]byte(strings.TrimPrefix(auth, "Bearer ")))
		return "token:" + hex.EncodeToString(sum[:8])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return "ip:" + host
}
