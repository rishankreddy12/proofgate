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
	DefaultChannel = "proofgate:ctl"

	OpPurgeSecrets = "purge_secrets"
	OpPurgeCache   = "purge_cache"
	OpReload       = "reload"
	OpKeyRevoked   = "key_revoked"
)

type Message struct {
	Op     string            `json:"op"`
	Args   map[string]string `json:"args,omitempty"`
	TS     time.Time         `json:"ts"`
	Sender string            `json:"sender,omitempty"`
}

type HandlerFunc func(ctx context.Context, msg Message) error

// Bus provides a Redis pub/sub based cross-replica control bus.
type Bus struct {
	rdb     redis.UniversalClient
	channel string
	sender  string

	mu       sync.RWMutex
	handlers map[string][]HandlerFunc
}

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

func (b *Bus) Channel() string {
	return b.channel
}

func (b *Bus) Sender() string {
	return b.sender
}

func (b *Bus) Subscribe(op string, fn HandlerFunc) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers[op] = append(b.handlers[op], fn)
}

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
