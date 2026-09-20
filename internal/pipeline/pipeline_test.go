package pipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/stretchr/testify/require"
)

type rec struct {
	name    string
	log     *[]string
	handled bool
	err     error
}

func (r rec) Name() string { return r.name }
func (r rec) Before(_ context.Context, _ *Call) (bool, error) {
	*r.log = append(*r.log, "before:"+r.name)
	return r.handled, r.err
}
func (r rec) After(_ context.Context, _ *Call) { *r.log = append(*r.log, "after:"+r.name) }

func newCall() *Call { return NewCall(auth.Principal{}, &api.ChatRequest{}, nil) }

func TestOrderAndReverseAfter(t *testing.T) {
	var log []string
	p := New(rec{name: "a", log: &log}, rec{name: "b", log: &log})
	c := newCall()
	handled, err := p.Before(context.Background(), c)
	require.NoError(t, err)
	require.False(t, handled)
	p.After(context.Background(), c)
	require.Equal(t, []string{"before:a", "before:b", "after:b", "after:a"}, log)
	require.NotEmpty(t, c.ID)
	require.NotNil(t, c.Header)
	require.NotNil(t, c.Values)
}

func TestShortCircuitAndError(t *testing.T) {
	var log []string
	p := New(rec{name: "a", log: &log}, rec{name: "cache", log: &log, handled: true}, rec{name: "c", log: &log})
	c := newCall()
	handled, _ := p.Before(context.Background(), c)
	require.True(t, handled)
	p.After(context.Background(), c)
	require.Equal(t, []string{"before:a", "before:cache", "after:cache", "after:a"}, log)

	log = nil
	boom := errors.New("boom")
	p = New(rec{name: "a", log: &log}, rec{name: "limit", log: &log, err: boom}, rec{name: "c", log: &log})
	c = newCall()
	_, err := p.Before(context.Background(), c)
	require.ErrorIs(t, err, boom)
	require.ErrorIs(t, c.Err, boom)
	p.After(context.Background(), c)
	require.Equal(t, []string{"before:a", "before:limit", "after:limit", "after:a"}, log)
}
