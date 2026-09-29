package broker

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

var ErrTabBusy = errors.New("tab busy")
var ErrLeaseExpired = errors.New("lease expired")

type Lease struct {
	Token     string    `json:"leaseToken"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type leaseKey struct {
	profile string
	tab     int
}

type LeaseStore struct {
	mutex  sync.Mutex
	leases map[leaseKey]Lease
	ttl    time.Duration
	now    func() time.Time
}

func NewLeaseStore(ttl time.Duration, now func() time.Time) *LeaseStore {
	return &LeaseStore{leases: make(map[leaseKey]Lease), ttl: ttl, now: now}
}

func (store *LeaseStore) Claim(profile string, tab int, existingToken string) (Lease, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	key := leaseKey{profile: profile, tab: tab}
	if current, ok := store.active(key); ok {
		if existingToken != "" && existingToken == current.Token {
			current.ExpiresAt = store.now().Add(store.ttl)
			store.leases[key] = current
			return current, nil
		}
		return Lease{}, ErrTabBusy
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return Lease{}, err
	}
	lease := Lease{Token: base64.RawURLEncoding.EncodeToString(tokenBytes), ExpiresAt: store.now().Add(store.ttl)}
	store.leases[key] = lease
	return lease, nil
}

func (store *LeaseStore) Renew(profile string, tab int, token string) (Lease, error) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	key := leaseKey{profile: profile, tab: tab}
	current, ok := store.active(key)
	if !ok || token == "" || current.Token != token {
		return Lease{}, ErrLeaseExpired
	}
	current.ExpiresAt = store.now().Add(store.ttl)
	store.leases[key] = current
	return current, nil
}

func (store *LeaseStore) Check(profile string, tab int, token string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	current, ok := store.active(leaseKey{profile: profile, tab: tab})
	if !ok || token == "" || current.Token != token {
		return ErrLeaseExpired
	}
	return nil
}

func (store *LeaseStore) Release(profile string, tab int, token string) error {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	key := leaseKey{profile: profile, tab: tab}
	current, ok := store.active(key)
	if !ok {
		return nil
	}
	if token == "" || current.Token != token {
		return ErrTabBusy
	}
	delete(store.leases, key)
	return nil
}

func (store *LeaseStore) RevokeProfile(profile string) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for key := range store.leases {
		if key.profile == profile {
			delete(store.leases, key)
		}
	}
}

func (store *LeaseStore) RevokeTab(profile string, tab int) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.leases, leaseKey{profile: profile, tab: tab})
}

func (store *LeaseStore) ActiveCount() int {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	for key := range store.leases {
		store.active(key)
	}
	return len(store.leases)
}

func (store *LeaseStore) active(key leaseKey) (Lease, bool) {
	lease, ok := store.leases[key]
	if !ok {
		return Lease{}, false
	}
	if !store.now().Before(lease.ExpiresAt) {
		delete(store.leases, key)
		return Lease{}, false
	}
	return lease, true
}
