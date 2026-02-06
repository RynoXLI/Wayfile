// Package middleware provides HTTP middleware functions for the API server
package middleware

import (
	"net/http"

	"golang.org/x/time/rate"
)

// RateLimiter creates a middleware that limits requests per second
func RateLimiter(rps, burst int) func(http.Handler) http.Handler {
	limiter := rate.NewLimiter(rate.Limit(rps), burst)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.Allow() {
				http.Error(w, "Rate limit exceeded", http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
