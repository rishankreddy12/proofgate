package guard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPII_RedactAndRestore_Unary(t *testing.T) {
	input := "Send invoice to user@example.com, card 4111-1111-1111-1111, SSN 123-45-6789."
	matches := DetectPII(input)
	require.Len(t, matches, 3)

	res := Redact(input, matches, "redact")
	require.Equal(t, "Send invoice to <EMAIL_1>, card <CREDIT_CARD_1>, SSN <US_SSN_1>.", res.Redacted)
	require.Len(t, res.Mapping, 3)
	require.Equal(t, "user@example.com", res.Mapping["<EMAIL_1>"])
	require.Equal(t, "4111-1111-1111-1111", res.Mapping["<CREDIT_CARD_1>"])
	require.Equal(t, "123-45-6789", res.Mapping["<US_SSN_1>"])

	// Simulate upstream LLM echoing back the placeholders
	upstreamResponse := "Confirmation: <EMAIL_1> charged on <CREDIT_CARD_1> (SSN: <US_SSN_1>)."

	restored := Restore(upstreamResponse, res.Mapping)
	require.Equal(t, "Confirmation: user@example.com charged on 4111-1111-1111-1111 (SSN: 123-45-6789).", restored)
}

func TestPII_Restore_StreamingChunks(t *testing.T) {
	mapping := map[string]string{
		"<EMAIL_1>": "alice@example.com",
	}

	restorer := NewStreamRestorer(mapping)

	// Stream with placeholder split across 3 chunks
	chunks := []string{
		"Contact <EMA",
		"IL_",
		"1> immediately for help.",
	}

	var reconstituted string
	for _, chunk := range chunks {
		reconstituted += restorer.Process(chunk)
	}
	reconstituted += restorer.Flush()

	require.Equal(t, "Contact alice@example.com immediately for help.", reconstituted)
}

func TestPII_MaskMode_NonReversible(t *testing.T) {
	input := "User alice@example.com with IP 192.168.1.5 connected."
	matches := DetectPII(input)
	require.Len(t, matches, 2)

	res := Redact(input, matches, "mask")
	require.Equal(t, "User [REDACTED:EMAIL] with IP [REDACTED:IPV4] connected.", res.Redacted)
	require.Nil(t, res.Mapping, "mask mode must produce no mapping")

	// Restoration in mask mode does nothing
	restored := Restore(res.Redacted, res.Mapping)
	require.Equal(t, "User [REDACTED:EMAIL] with IP [REDACTED:IPV4] connected.", restored)
}
