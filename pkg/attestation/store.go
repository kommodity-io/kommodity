package attestation

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
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
	data map[string]time.Time // nounce -> expiresAt
}

func newNounceStore(ttl time.Duration) *NounceStore {
	store := &NounceStore{
		ttl:  ttl,
		data: make(map[string]time.Time),
	}
	// Background reaper for expired nounces
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()

		for range t.C {
			now := time.Now()

			store.mu.Lock()

			for k, exp := range store.data {
				if now.After(exp) {
					delete(store.data, k)
				}
			}

			store.mu.Unlock()
		}
	}()

	return store
}

func (s *NounceStore) generate() (string, time.Time, error) {
	reservation := make([]byte, nounceSize)

	_, err := rand.Read(reservation)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to generate nounce: %w", err)
	}

	nounce := hex.EncodeToString(reservation)
	exp := time.Now().Add(s.ttl)

	s.mu.Lock()
	s.data[nounce] = exp
	s.mu.Unlock()

	return nounce, exp, nil
}

func (s *NounceStore) use(nounce string) (bool, error) {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	exp, exists := s.data[nounce]
	if !exists {
		return false, ErrInvalidNonce
	}

	if now.After(exp) {
		delete(s.data, nounce)

		return false, ErrExpiredNonce
	}

	// single-use
	delete(s.data, nounce)

	return true, nil
}
