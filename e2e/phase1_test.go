//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/proofgate/proofgate/internal/auth"
	"github.com/proofgate/proofgate/internal/store"
	"github.com/stretchr/testify/require"
)

func gatewayURLs() []string {
	if v := os.Getenv("PROOFGATE_GATEWAYS"); v != "" {
		return strings.Split(v, ",")
	}
	return []string{"http://localhost:18080", "http://localhost:18083", "http://localhost:18084"}
}

func dsn() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://proofgate:proofgate@localhost:15432/proofgate?sslmode=disable"
}

// newKey creates a fresh tenant (unique name per test run) and returns a key for it.
func newKey(t *testing.T, p store.TenantPolicy) string {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, dsn())
	require.NoError(t, err)
	defer st.Close()
	ten, err := st.CreateTenant(ctx, fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()), p)
	require.NoError(t, err)
	plain, prefix, hash, err := auth.GenerateKey("test")
	require.NoError(t, err)
	_, err = st.CreateKey(ctx, ten.ID, "e2e", prefix, hash, nil, store.KeyPolicy{})
	require.NoError(t, err)
	return plain
}

func chat(t *testing.T, base, key string) *http.Response {
	t.Helper()
	body := `{"model":"default","messages":[{"role":"user","content":"hello"}],"max_tokens":3}`
	req, _ := http.NewRequest("POST", base+"/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	return resp
}

func setMock(t *testing.T, port int, mode string) {
	req, _ := http.NewRequest("PUT", fmt.Sprintf("http://localhost:%d/admin/mode", port), bytes.NewBufferString(mode))
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
}

func TestThreeReplicasShareOneRateLimit(t *testing.T) {
	gateways := gatewayURLs()
	key := newKey(t, store.TenantPolicy{RPM: 30})
	var ok, limited atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 90; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			resp := chat(t, gateways[i%len(gateways)], key)
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			switch resp.StatusCode {
			case 200:
				ok.Add(1)
			case 429:
				limited.Add(1)
				require.NotEmpty(t, resp.Header.Get("Retry-After"))
			default:
				t.Errorf("unexpected status %d", resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	// 30 per minute refills at 0.5/s; the burst finishes in about a second, so allow one refill.
	require.GreaterOrEqual(t, ok.Load(), int32(30))
	require.LessOrEqual(t, ok.Load(), int32(31))
	require.Equal(t, int32(90), ok.Load()+limited.Load())
}

func TestFailoverIsInvisibleToClients(t *testing.T) {
	gateways := gatewayURLs()
	key := newKey(t, store.TenantPolicy{})
	setMock(t, 18081, `{"error_rate":1,"error_status":503}`)
	defer setMock(t, 18081, `{}`)
	for i := 0; i < 20; i++ {
		resp := chat(t, gateways[i%len(gateways)], key)
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		require.Equal(t, 200, resp.StatusCode)
		require.Equal(t, "mock-b/mock-large", resp.Header.Get("X-ProofGate-Target"))
	}
}

func TestBudgetStopsSpend(t *testing.T) {
	gateways := gatewayURLs()
	// mock-large costs 1 prompt token x $1 + 3 completion tokens x $4 = 13 micro-USD per call.
	// With a 20 micro-USD budget: call 1 (spent 0) and call 2 (spent 13) pass, call 3 (spent 26) gets 402.
	key := newKey(t, store.TenantPolicy{MonthlyBudgetUSD: 0.00002})
	setMock(t, 18081, `{"error_rate":1,"error_status":503}`) // force the expensive target
	defer setMock(t, 18081, `{}`)
	codes := []int{}
	for i := 0; i < 4; i++ {
		resp := chat(t, gateways[0], key)
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		codes = append(codes, resp.StatusCode)
	}
	require.Equal(t, 402, codes[len(codes)-1], "codes: %v", codes)
}
