package analytics

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestApplyRetention_NilConn(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, ApplyRetention(ctx, nil, 72, 30))
}

func TestPurgeTenantData_Validation(t *testing.T) {
	ctx := context.Background()
	require.NoError(t, PurgeTenantData(ctx, nil, "tenant-123"))
	require.Error(t, PurgeTenantData(ctx, nil, ""))
}
