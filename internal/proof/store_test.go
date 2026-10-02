package proof

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProcessTextModes(t *testing.T) {
	text := "Sensitive prompt with personal data"
	h := sha256.Sum256([]byte(text))
	expectedHash := "sha256:" + hex.EncodeToString(h[:])

	// 1. Full mode preserves text
	require.Equal(t, text, processText(text, "full"))

	// 2. Hash mode hashes non-empty text
	require.Equal(t, expectedHash, processText(text, "hash"))
	require.Equal(t, "", processText("", "hash"))

	// 3. None mode drops text completely
	require.Equal(t, "", processText(text, "none"))

	// 4. Default mode fallback
	require.Equal(t, text, processText(text, "unknown"))
}

func TestNewCHWithStoreText(t *testing.T) {
	chDefault := NewCH(nil)
	require.Equal(t, "full", chDefault.storeText)

	chHash := NewCHWithStoreText(nil, "hash")
	require.Equal(t, "hash", chHash.storeText)

	chNone := NewCHWithStoreText(nil, "none")
	require.Equal(t, "none", chNone.storeText)

	chEmpty := NewCHWithStoreText(nil, "")
	require.Equal(t, "hash", chEmpty.storeText)
}
