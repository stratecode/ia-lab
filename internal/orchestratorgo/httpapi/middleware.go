package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/stratecode/lab/internal/orchestratorgo/domain"
	"github.com/stratecode/lab/internal/orchestratorgo/store"
)

type contextKey string

const authenticatedKeyContextKey contextKey = "authenticated_key"

var publicPaths = map[string]struct{}{
	"/health":       {},
	"/ready":        {},
	"/metrics":      {},
	"/docs":         {},
	"/openapi.json": {},
	"/redoc":        {},
}

var adminOnlyPrefixes = []string{"/config/", "/audit"}

var scopeAllowedMethods = map[domain.ApiKeyScope]map[string]struct{}{
	domain.ScopeAdmin:    {"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {}, "HEAD": {}, "OPTIONS": {}},
	domain.ScopeOperator: {"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {}, "HEAD": {}, "OPTIONS": {}},
	domain.ScopeBot:      {"GET": {}, "POST": {}, "HEAD": {}, "OPTIONS": {}},
	domain.ScopeReadonly: {"GET": {}, "HEAD": {}, "OPTIONS": {}},
}

type Authenticator struct {
	apiKeys *store.PostgresStore
	limiter *bruteForceTracker
}

func NewAuthenticator(apiKeys *store.PostgresStore, maxAttempts, windowSeconds, blockSeconds int) *Authenticator {
	return &Authenticator{
		apiKeys: apiKeys,
		limiter: newBruteForceTracker(maxAttempts, windowSeconds, blockSeconds),
	}
}

func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isPublicPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		clientIP := getClientIP(r)
		if a.limiter.IsBlocked(clientIP) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error_code": "source_blocked",
				"message":    "Too many failed authentication attempts. Try again later.",
			})
			return
		}
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, "Bearer ") {
			a.limiter.RecordFailure(clientIP)
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error_code": "missing_token",
				"message":    "Authorization header with Bearer token is required.",
			})
			return
		}
		rawToken := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if rawToken == "" {
			a.limiter.RecordFailure(clientIP)
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error_code": "missing_token",
				"message":    "Authorization header with Bearer token is required.",
			})
			return
		}
		sum := sha256.Sum256([]byte(rawToken))
		record, err := a.apiKeys.GetAPIKeyByHash(r.Context(), hex.EncodeToString(sum[:]))
		if err != nil {
			writeDetail(w, http.StatusInternalServerError, "failed to validate API key")
			return
		}
		if record == nil {
			a.limiter.RecordFailure(clientIP)
			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error_code": "invalid_token",
				"message":    "Invalid API key.",
			})
			return
		}
		if errText := checkScope(record.Scope, r.Method, r.URL.Path); errText != "" {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error_code": "insufficient_scope",
				"message":    errText,
			})
			return
		}
		a.limiter.Reset(clientIP)
		ctx := context.WithValue(r.Context(), authenticatedKeyContextKey, record)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func CorrelationMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		correlationID := r.Header.Get("X-Correlation-ID")
		if strings.TrimSpace(correlationID) == "" {
			correlationID = middleware.GetReqID(r.Context())
		}
		w.Header().Set("X-Correlation-ID", correlationID)
		next.ServeHTTP(w, r)
	})
}

func isPublicPath(path string) bool {
	if _, ok := publicPaths[path]; ok {
		return true
	}
	return strings.HasPrefix(path, "/docs") || strings.HasPrefix(path, "/redoc")
}

func checkScope(scope domain.ApiKeyScope, method, path string) string {
	for _, prefix := range adminOnlyPrefixes {
		if strings.HasPrefix(path, prefix) && scope != domain.ScopeAdmin {
			return "Path '" + path + "' requires 'admin' scope, got '" + string(scope) + "'"
		}
	}
	if _, ok := scopeAllowedMethods[scope][method]; !ok {
		return "Method '" + method + "' not allowed for scope '" + string(scope) + "'. Required scope: 'operator' or 'admin'"
	}
	return ""
}

func getClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); strings.TrimSpace(forwarded) != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

type bruteForceTracker struct {
	maxAttempts int
	window      time.Duration
	block       time.Duration
	mu          sync.Mutex
	failures    map[string][]time.Time
	blocked     map[string]time.Time
}

func newBruteForceTracker(maxAttempts, windowSeconds, blockSeconds int) *bruteForceTracker {
	return &bruteForceTracker{
		maxAttempts: maxAttempts,
		window:      time.Duration(windowSeconds) * time.Second,
		block:       time.Duration(blockSeconds) * time.Second,
		failures:    map[string][]time.Time{},
		blocked:     map[string]time.Time{},
	}
}

func (b *bruteForceTracker) IsBlocked(source string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	expiry, ok := b.blocked[source]
	if !ok {
		return false
	}
	if time.Now().After(expiry) {
		delete(b.blocked, source)
		delete(b.failures, source)
		return false
	}
	return true
}

func (b *bruteForceTracker) RecordFailure(source string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-b.window)
	current := b.failures[source][:0]
	for _, ts := range b.failures[source] {
		if ts.After(cutoff) {
			current = append(current, ts)
		}
	}
	current = append(current, now)
	b.failures[source] = current
	if len(current) >= b.maxAttempts {
		b.blocked[source] = now.Add(b.block)
	}
}

func (b *bruteForceTracker) Reset(source string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.failures, source)
}
