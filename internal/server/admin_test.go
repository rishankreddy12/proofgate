package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminCachePurgeHandlerNilRDB(t *testing.T) {
	handler := AdminCachePurgeHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/cache/purge", bytes.NewBufferString(`{"tenant_id":"t1"}`))
	rec := httptest.NewRecorder()

	handler(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"deleted_keys":0}`, rec.Body.String())
}

func TestAdminCachePurgeHandlerInvalidBody(t *testing.T) {
	handler := AdminCachePurgeHandler(nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/cache/purge", bytes.NewBufferString(`{invalid json}`))
	rec := httptest.NewRecorder()

	handler(rec, req)
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
