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

func FuzzRedactRestore(f *testing.F) {
	f.Add("Send invoice to user@example.com, card 4111-1111-1111-1111.")
	f.Add("Normal text without any PII data whatsoever.")
	f.Add("Multiple emails: a@b.com c@d.org e@f.net")
	f.Add("Edge: <EMAIL_1> literal placeholder text")
	f.Add("")
	f.Fuzz(func(t *testing.T, text string) {
		matches := DetectPII(text)
		res := Redact(text, matches, "redact")
		restored := Restore(res.Redacted, res.Mapping)
		if len(matches) == 0 {
			// No PII → text must be unchanged through the round-trip
			require.Equal(t, text, restored)
		}
		// In all cases, restored text must not contain any active placeholders
		for ph := range res.Mapping {
			require.NotContains(t, restored, ph, "placeholder %q not restored", ph)
		}
	})
}
