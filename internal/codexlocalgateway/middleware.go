package codexlocalgateway

import (
	"crypto/subtle"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", requestID)

		endpoint := routePattern(r)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		s.active.Inc()
		defer s.active.Dec()
		defer func() {
			latency := time.Since(start)
			status := strconv.Itoa(rec.status)
			s.requests.WithLabelValues(endpoint, r.Method, status).Inc()
			s.durations.WithLabelValues(endpoint, r.Method).Observe(latency.Seconds())
			s.logger.Info().
				Str("request_id", requestID).
				Str("endpoint", endpoint).
				Str("method", r.Method).
				Int("status", rec.status).
				Dur("latency", latency).
				Msg("request completed")
		}()
		next.ServeHTTP(rec, r)
	})
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.APIKey == "" {
			next.ServeHTTP(w, r)
			return
		}
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "unauthorized", "missing bearer token")
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.APIKey)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "invalid bearer token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) limit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.allow(r) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("rate limit exceeded: %d rpm", s.cfg.RateLimitRPM))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func routePattern(r *http.Request) string {
	if pattern := chi.RouteContext(r.Context()).RoutePattern(); pattern != "" {
		return pattern
	}
	return r.URL.Path
}
