// Package net provides network-related utilities, including rate limiting.
package net

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

const (
	// RateLimitInterval defines the time window for rate limiting.
	RateLimitInterval = 30 * time.Second
	// RateLimitRequests defines the number of allowed requests per interval.
	RateLimitRequests = 1
)

// RateLimiter manages rate limiting for clients based on their IP addresses.
type RateLimiter struct {
	clients map[string]*rate.Limiter
	mu      sync.Mutex
}

// NewRateLimiter creates a new RateLimiter instance.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{
		clients: make(map[string]*rate.Limiter),
	}
}

// GetClientLimiter returns client's rate limiter or create one if it doesn't exist.
//
//nolint:varnamelen // IP is accepted here.
func (r *RateLimiter) GetClientLimiter(ip string) *rate.Limiter {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If the client already exists, return the existing limiter
	if client, exists := r.clients[ip]; exists {
		return client
	}

	limiter := rate.NewLimiter(
		rate.Every(RateLimitInterval),
		RateLimitRequests)
	r.clients[ip] = limiter

	return limiter
}
