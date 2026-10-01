package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProfileManagement(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "proofgatectl-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Override config dir for testing
	origConfigDir := os.Getenv("APPDATA")
	if origConfigDir == "" {
		origConfigDir = os.Getenv("HOME")
	}
	t.Setenv("APPDATA", tmpDir)
	t.Setenv("HOME", tmpDir)
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	cfg, err := loadProfileConfig()
	require.NoError(t, err)
	require.Empty(t, cfg.Profiles)

	// Add profile
	cfg.Profiles["staging"] = Profile{
		Server:   "https://staging.internal:9090",
		Insecure: true,
	}
	cfg.ActiveProfile = "staging"
	require.NoError(t, saveProfileConfig(cfg))

	// Reload and check
	loaded, err := loadProfileConfig()
	require.NoError(t, err)
	require.Equal(t, "staging", loaded.ActiveProfile)
	require.Equal(t, "https://staging.internal:9090", loaded.Profiles["staging"].Server)
	require.True(t, loaded.Profiles["staging"].Insecure)

	// Test session storage
	now := time.Now().UTC().Truncate(time.Second)
	sess := &SessionFile{
		Token:     "pgadmin_test_token_12345",
		Username:  "admin",
		ExpiresAt: now.Add(12 * time.Hour),
	}
	require.NoError(t, saveSession("staging", sess))

	loadedSess, err := loadSession("staging")
	require.NoError(t, err)
	require.Equal(t, sess.Token, loadedSess.Token)
	require.Equal(t, sess.Username, loadedSess.Username)

	// Delete session
	require.NoError(t, deleteSession("staging"))
	_, err = loadSession("staging")
	require.Error(t, err)
}

func TestAtomicWriteFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "atomic-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	target := filepath.Join(tmpDir, "test.txt")
	require.NoError(t, atomicWriteFile(target, []byte("hello world"), 0600))

	b, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "hello world", string(b))
}
