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
	nounceSize = 32 // 256-bit
)

// NounceStore is a thread-safe store for nounces with expiration.
type NounceStore struct {
	mu   sync.Mutex
	ttl  time.Duration
	data map[string]nounceRecord
}

type nounceRecord struct {
	expiresAt time.Time
	ip        string
}

// NewNounceStore creates a new NounceStore with the specified TTL for nounces.
func NewNounceStore(ttl time.Duration) *NounceStore {
	store := &NounceStore{
		ttl:  ttl,
		data: make(map[string]nounceRecord),
	}
	// Background reaper for expired nounces
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

// Generate creates a new nounce, stores it with an expiration time, and returns it.
func (s *NounceStore) Generate(ip string) (string, time.Time, error) {
	canonical, err := canonicalIP(ip)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to canonicalize IP: %w", err)
	}

	reservation := make([]byte, nounceSize)

	_, err = rand.Read(reservation)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate nounce: %w", err)
	}

	nounce := hex.EncodeToString(reservation)
	exp := time.Now().Add(s.ttl)

	s.mu.Lock()
	s.data[nounce] = nounceRecord{
		expiresAt: exp,
		ip:        canonical,
	}
	s.mu.Unlock()

	return nounce, exp, nil
}

// Use validates and consumes a nounce. It returns true if the nounce is valid and not expired.
func (s *NounceStore) Use(ip, nounce string) (bool, error) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	record, exists := s.data[nounce]
	if !exists {
		return false, ErrInvalidNonce
	}

	if ip != record.ip {
		return false, ErrIPMismatch
	}

	if now.After(record.expiresAt) {
		delete(s.data, nounce)

		return false, ErrExpiredNonce
	}

	// single-use
	delete(s.data, nounce)

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
