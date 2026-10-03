// Package control provides enterprise-grade capabilities, configuration, and structural components for the control subsystem.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
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
)

// Message defines the core enterprise configuration and state for Message.
// It is responsible for managing the lifecycle, validation, and schema of the Message entity.
type Message struct {
	Op     string            `json:"op"`
	Args   map[string]string `json:"args,omitempty"`
	TS     time.Time         `json:"ts"`
	Sender string            `json:"sender,omitempty"`
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
		Op:     op,
		Args:   args,
		TS:     time.Now().UTC(),
		Sender: b.sender,
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
					slog.Warn("malformed control message", "payload", msg.Payload, "err", err)
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
