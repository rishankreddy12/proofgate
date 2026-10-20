// Package control provides enterprise-grade capabilities, configuration, and structural components for the control subsystem.
package control

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// ProtocolVersion is the version of the control-bus envelope. Receivers may
	// continue to accept version zero for compatibility with pre-versioned
	// messages, while publishers always emit the current version.
	ProtocolVersion = 1

	// DefaultChannel defines a specific variation or structural setting for DefaultChannel.
	DefaultChannel = "proofgate:ctl"

	// OpPurgeSecrets defines a specific variation or structural setting for OpPurgeSecrets.
	OpPurgeSecrets = "purge_secrets"
	// OpPurgeCache defines a specific variation or structural setting for OpPurgeCache.
	OpPurgeCache = "purge_cache"
	// OpReload defines a specific variation or structural setting for OpReload.
	OpReload = "reload"
	// OpKeyRevoked defines a specific variation or structural setting for OpKeyRevoked.
	OpKeyRevoked = "key_revoked"
	// OpCredentialInvalidated tells replicas to evict one provider's decrypted
	// credential without carrying the credential itself.
	OpCredentialInvalidated = "credential_invalidated" //nolint:gosec // protocol operation identifier; it carries no credential
	// OpTenantErased tells replicas to drop tenant-scoped in-memory state.
	OpTenantErased = "tenant_erased"
)

// Message defines the core enterprise configuration and state for Message.
// It is responsible for managing the lifecycle, validation, and schema of the Message entity.
type Message struct {
	Version int               `json:"version"`
	Op      string            `json:"op"`
	Args    map[string]string `json:"args,omitempty"`
	TS      time.Time         `json:"ts"`
	Sender  string            `json:"sender,omitempty"`
}

// Validate checks the control envelope and rejects fields that could carry a
// secret. Control messages are intentionally identifiers and invalidation
// metadata only; provider/API credentials must never cross this bus.
func (m Message) Validate() error {
	if m.Version != 0 && m.Version != ProtocolVersion {
		return fmt.Errorf("unsupported control message version %d", m.Version)
	}
	if m.Op == "" {
		return errors.New("control operation required")
	}
	allowed := map[string]map[string]bool{
		OpPurgeSecrets:          {"reason": true},
		OpPurgeCache:            {"tenant_id": true, "route": true, "tags": true},
		OpReload:                {},
		OpKeyRevoked:            {"key_hash": true},
		OpCredentialInvalidated: {"provider": true, "version": true},
		OpTenantErased:          {"tenant_id": true, "receipt_id": true},
	}
	keys, knownOp := allowed[m.Op]
	if !knownOp {
		return fmt.Errorf("unsupported control operation %q", m.Op)
	}
	for key, value := range m.Args {
		if !keys[key] {
			return fmt.Errorf("argument %q is not allowed for %s", key, m.Op)
		}
		lowerKey := strings.ToLower(key)
		if strings.Contains(lowerKey, "secret") || strings.Contains(lowerKey, "token") ||
			strings.Contains(lowerKey, "password") || strings.Contains(lowerKey, "api_key") ||
			strings.Contains(lowerKey, "plaintext") {
			return fmt.Errorf("secret-bearing argument %q is not allowed", key)
		}
		if strings.HasPrefix(value, "pg_") || strings.HasPrefix(value, "sk-") || strings.HasPrefix(value, "sk_") {
			return fmt.Errorf("secret-looking value is not allowed for %q", key)
		}
	}
	switch m.Op {
	case OpKeyRevoked:
		if strings.TrimSpace(m.Args["key_hash"]) == "" {
			return errors.New("key_hash required")
		}
	case OpCredentialInvalidated:
		if strings.TrimSpace(m.Args["provider"]) == "" {
			return errors.New("provider required")
		}
		version, err := strconv.Atoi(m.Args["version"])
		if err != nil || version <= 0 {
			return errors.New("positive credential version required")
		}
	case OpTenantErased:
		if strings.TrimSpace(m.Args["tenant_id"]) == "" {
			return errors.New("tenant_id required")
		}
	}
	return nil
}

// KeyRevokedArgs creates the only supported API-key revocation payload from a
// digest. It is impossible for the plaintext key to enter the message.
func KeyRevokedArgs(hash []byte) map[string]string {
	return map[string]string{"key_hash": hex.EncodeToString(hash)}
}

// CredentialInvalidatedArgs creates a provider invalidation payload containing
// only the provider identifier and committed credential version.
func CredentialInvalidatedArgs(provider string, version int) map[string]string {
	return map[string]string{"provider": provider, "version": fmt.Sprintf("%d", version)}
}

// TenantErasedArgs creates a tenant invalidation payload without tenant data.
func TenantErasedArgs(tenantID, receiptID string) map[string]string {
	args := map[string]string{"tenant_id": tenantID}
	if receiptID != "" {
		args["receipt_id"] = receiptID
	}
	return args
}

// PublishKeyRevoked publishes a digest-only key invalidation event.
func PublishKeyRevoked(ctx context.Context, bus *Bus, hash []byte) error {
	if bus == nil {
		return errors.New("control bus not configured")
	}
	return bus.Publish(ctx, OpKeyRevoked, KeyRevokedArgs(hash))
}

// PublishCredentialInvalidated publishes provider/version metadata only.
func PublishCredentialInvalidated(ctx context.Context, bus *Bus, provider string, version int) error {
	if bus == nil {
		return errors.New("control bus not configured")
	}
	return bus.Publish(ctx, OpCredentialInvalidated, CredentialInvalidatedArgs(provider, version))
}

// HandlerFunc defines the core enterprise configuration and state for HandlerFunc.
// It is responsible for managing the lifecycle, validation, and schema of the HandlerFunc entity.
type HandlerFunc func(ctx context.Context, msg Message) error

// Bus provides a Redis pub/sub based cross-replica control bus.
type Bus struct {
	rdb     redis.UniversalClient
	channel string
	sender  string

	mu       sync.RWMutex
	handlers map[string][]HandlerFunc
}

// NewBus executes the primary logic for the NewBus operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func NewBus(rdb redis.UniversalClient, sender, channel string) *Bus {
	if channel == "" {
		channel = DefaultChannel
	}
	return &Bus{
		rdb:      rdb,
		channel:  channel,
		sender:   sender,
		handlers: make(map[string][]HandlerFunc),
	}
}

// Channel executes the primary logic for the Channel operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (b *Bus) Channel() string {
	return b.channel
}

// Sender executes the primary logic for the Sender operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (b *Bus) Sender() string {
	return b.sender
}

// Subscribe executes the primary logic for the Subscribe operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (b *Bus) Subscribe(op string, fn HandlerFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[op] = append(b.handlers[op], fn)
}

// Publish executes the primary logic for the Publish operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (b *Bus) Publish(ctx context.Context, op string, args map[string]string) error {
	if b.rdb == nil {
		return errors.New("redis client not configured")
	}
	msg := Message{
		Version: ProtocolVersion,
		Op:      op,
		Args:    cloneArgs(args),
		TS:      time.Now().UTC(),
		Sender:  b.sender,
	}
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("invalid control message: %w", err)
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal control message: %w", err)
	}
	return b.rdb.Publish(ctx, b.channel, data).Err()
}

// Dispatch executes the primary logic for the Dispatch operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (b *Bus) Dispatch(ctx context.Context, msg Message) {
	if err := msg.Validate(); err != nil {
		slog.Warn("invalid control message", "op", msg.Op, "sender", msg.Sender, "err", err)
		return
	}
	if msg.Version == 0 {
		msg.Version = ProtocolVersion
	}
	b.mu.RLock()
	ops := append([]HandlerFunc(nil), b.handlers[msg.Op]...)
	wildcards := append([]HandlerFunc(nil), b.handlers["*"]...)
	b.mu.RUnlock()

	for _, h := range ops {
		if err := h(ctx, msg); err != nil {
			slog.Error("control handler failed", "op", msg.Op, "sender", msg.Sender, "err", err)
		}
	}
	for _, h := range wildcards {
		if err := h(ctx, msg); err != nil {
			slog.Error("control wildcard handler failed", "op", msg.Op, "sender", msg.Sender, "err", err)
		}
	}
}

// Run executes the primary logic for the Run operation.
// It ensures thread-safe execution, input validation, and proper error handling.
func (b *Bus) Run(ctx context.Context) error {
	if b.rdb == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		pubsub := b.rdb.Subscribe(ctx, b.channel)
		ch := pubsub.Channel()
		slog.Debug("control bus subscribed", "channel", b.channel)

		disconnected := false
		for !disconnected {
			select {
			case <-ctx.Done():
				_ = pubsub.Close()
				return ctx.Err()
			case msg, ok := <-ch:
				if !ok {
					disconnected = true
					break
				}
				var cm Message
				if err := json.Unmarshal([]byte(msg.Payload), &cm); err != nil {
					slog.Warn("malformed control message", "err", err)
					continue
				}
				b.Dispatch(ctx, cm)
			}
		}
		_ = pubsub.Close()

		// Reconnect with backoff
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
}

func cloneArgs(args map[string]string) map[string]string {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]string, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}
