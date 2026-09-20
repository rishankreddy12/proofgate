package ratelimit

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/api"
	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/pipeline"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

type fakeBackend struct {
	decision Decision
	err      error
	taken    int
	adjusted []int
}

func (f *fakeBackend) Take(_ context.Context, _ string, _ store.TenantPolicy, tokens int) (Decision, error) {
	f.taken = tokens
	return f.decision, f.err
}
func (f *fakeBackend) Adjust(_ context.Context, _ string, _ store.TenantPolicy, delta int) error {
	f.adjusted = append(f.adjusted, delta)
	return nil
}

func call(strict bool, maxTokens int) *pipeline.Call {
	req := &api.ChatRequest{MaxTokens: &maxTokens, Messages: []api.Message{{Role: "user", Content: api.Content{Text: "12345678"}}}}
	return pipeline.NewCall(auth.Principal{TenantID: "t", Tenant: store.TenantPolicy{RPM: 10, TPM: 1000, Strict: strict}}, req, nil)
}

func TestStageChargesEstimateAndReconciles(t *testing.T) {
	b := &fakeBackend{decision: Decision{Allowed: true}}
	s := NewStage(b, 4096, 1024, nil)
	c := call(false, 100)
	_, err := s.Before(context.Background(), c)
	require.NoError(t, err)
	want := c.Request.EstimatePromptTokens() + 100
	require.Equal(t, want, b.taken)

	c.Usage = api.Usage{TotalTokens: 30}
	s.After(context.Background(), c)
	require.Equal(t, []int{want - 30}, b.adjusted)
}

func TestStageCapsReserveAndRefundsOnError(t *testing.T) {
	b := &fakeBackend{decision: Decision{Allowed: true}}
	s := NewStage(b, 500, 1024, nil)
	c := call(false, 100000)
	_, _ = s.Before(context.Background(), c)
	require.Equal(t, c.Request.EstimatePromptTokens()+500, b.taken)
	c.Err = errors.New("upstream failed")
	s.After(context.Background(), c)
	require.Equal(t, []int{b.taken}, b.adjusted, "full refund when nothing was generated")
}

func TestStageDenied(t *testing.T) {
	s := NewStage(&fakeBackend{decision: Decision{RetryAfter: 3 * time.Second}}, 4096, 1024, nil)
	_, err := s.Before(context.Background(), call(false, 10))
	var ae *api.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 429, ae.Status)
	require.Equal(t, 3*time.Second, ae.RetryAfter)

	s = NewStage(&fakeBackend{decision: Decision{TooLarge: true}}, 4096, 1024, nil)
	_, err = s.Before(context.Background(), call(false, 10))
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 413, ae.Status)
}

func TestStageRedisDown(t *testing.T) {
	failOpen := 0
	s := NewStage(&fakeBackend{err: errors.New("redis down")}, 4096, 1024, func() { failOpen++ })
	_, err := s.Before(context.Background(), call(false, 10))
	require.NoError(t, err)
	require.Equal(t, 1, failOpen)

	_, err = s.Before(context.Background(), call(true, 10))
	var ae *api.Error
	require.ErrorAs(t, err, &ae)
	require.Equal(t, 503, ae.Status)
}

func TestUnlimitedTenantSkipsBackend(t *testing.T) {
	b := &fakeBackend{}
	s := NewStage(b, 4096, 1024, nil)
	c := call(false, 10)
	c.Principal.Tenant = store.TenantPolicy{}
	_, err := s.Before(context.Background(), c)
	require.NoError(t, err)
	require.Equal(t, 0, b.taken)
}
