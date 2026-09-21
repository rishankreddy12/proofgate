package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKeyFuncIsUsedPerRequest(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("Authorization"))
		w.WriteHeader(500)
	}))
	defer srv.Close()
	n := 0
	p := NewOpenAI(OpenAIConfig{Name: "o", BaseURL: srv.URL, APIKey: "unused",
		KeyFunc: func(context.Context) (string, error) { n++; return "rotating-" + string(rune('0'+n)), nil }})
	_, _ = p.Chat(context.Background(), "m", userReq("x", 1))
	_, _ = p.Chat(context.Background(), "m", userReq("x", 1))
	require.Equal(t, []string{"Bearer rotating-1", "Bearer rotating-2"}, got)
}
