package rest

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

const (
	nonceSize = 32 // 256-bit
)

// NonceStore is a thread-safe store for nonces with expiration.
type NonceStore struct {
	mu   sync.Mutex
	ttl  time.Duration
	data map[string]nonceRecord
}

type nonceRecord struct {
	expiresAt time.Time
	ip        string
}

// NewNonceStore creates a new NonceStore with the specified TTL for nonces.
func NewNonceStore(ttl time.Duration) *NonceStore {
	store := &NonceStore{
		ttl:  ttl,
		data: make(map[string]nonceRecord),
	}
	// Background reaper for expired nonces
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()

		for range t.C {
			now := time.Now()

			store.mu.Lock()

			for k, record := range store.data {
				if now.After(record.expiresAt) {
					delete(store.data, k)
				}
			}

			store.mu.Unlock()
		}
	}()

	return store
}

// Generate creates a new nonce, stores it with an expiration time, and returns it.
func (s *NonceStore) Generate(ip string) (string, time.Time, error) {
	canonical, err := canonicalIP(ip)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to canonicalize IP: %w", err)
	}

	reservation := make([]byte, nonceSize)

	_, err = rand.Read(reservation)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate nonce: %w", err)
	}

	nonce := hex.EncodeToString(reservation)
	exp := time.Now().Add(s.ttl)

	s.mu.Lock()
	s.data[nonce] = nonceRecord{
		expiresAt: exp,
		ip:        canonical,
	}
	s.mu.Unlock()

	return nonce, exp, nil
}

// Use validates and consumes a nonce. It returns true if the nonce is valid and not expired.
//
//nolint:varnamelen // Variable name ip is appropriate for the context.
func (s *NonceStore) Use(ip, nonce string) (bool, error) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.data[nonce]
	if !exists {
		return false, ErrInvalidNonce
	}

	if ip != record.ip {
		return false, ErrIPMismatch
	}

	if now.After(record.expiresAt) {
		delete(s.data, nonce)

		return false, ErrExpiredNonce
	}

	// single-use
	delete(s.data, nonce)

	return true, nil
}

func canonicalIP(hostport string) (string, error) {
	// Strip optional port if present.
	host, _, err := net.SplitHostPort(hostport)
	if err == nil {
		hostport = host
	}

	addr, err := netip.ParseAddr(hostport)
	if err != nil {
		return "", fmt.Errorf("failed to parse IP address: %w", err)
	}

	return addr.String(), nil
}
