package secrets

import (
	"context"
	"sync"
	"time"
)

type CredentialSource interface {
	ActiveCredential(ctx context.Context, provider string) (Sealed, int, error)
}

type cached struct {
	value   string
	expires time.Time
}

type KeyCache struct {
	src      CredentialSource
	kek      KEK
	ttl      time.Duration
	onSecret func(string)
	now      func() time.Time
	mu       sync.Mutex
	m        map[string]cached
}

func NewKeyCache(src CredentialSource, kek KEK, ttl time.Duration, onSecret func(string)) *KeyCache {
	if onSecret == nil {
		onSecret = func(string) {}
	}
	return &KeyCache{src: src, kek: kek, ttl: ttl, onSecret: onSecret, now: time.Now, m: map[string]cached{}}
}

func (k *KeyCache) Get(ctx context.Context, provider string) (string, error) {
	k.mu.Lock()
	if c, ok := k.m[provider]; ok && k.now().Before(c.expires) {
		k.mu.Unlock()
		return c.value, nil
	}
	k.mu.Unlock()
	s, _, err := k.src.ActiveCredential(ctx, provider)
	if err != nil {
		return "", err
	}
	pt, err := Open(ctx, k.kek, s, AAD(provider))
	if err != nil {
		return "", err
	}
	v := string(pt)
	zero(pt)
	k.onSecret(v)
	k.mu.Lock()
	k.m[provider] = cached{value: v, expires: k.now().Add(k.ttl)}
	k.mu.Unlock()
	return v, nil
}

func (k *KeyCache) Purge() {
	k.mu.Lock()
	k.m = map[string]cached{}
	k.mu.Unlock()
}
