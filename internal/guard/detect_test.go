package guard

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPII_Email_IPv4_IPv6(t *testing.T) {
	text := "Contact user@example.com or support.team+help@sub.domain.co.uk. Server at 192.168.1.1 and 2001:0db8:85a3:0000:0000:8a2e:0370:7334."
	matches := DetectPII(text)

	var emails, ips []string
	for _, m := range matches {
		switch m.Type {
		case PIIEmail:
			emails = append(emails, m.Value)
		case PIIIPv4, PIIIPv6:
			ips = append(ips, m.Value)
		}
	}

	require.Contains(t, emails, "user@example.com")
	require.Contains(t, emails, "support.team+help@sub.domain.co.uk")
	require.Contains(t, ips, "192.168.1.1")
	require.Contains(t, ips, "2001:0db8:85a3:0000:0000:8a2e:0370:7334")
}

func TestPII_CreditCard_LuhnValid(t *testing.T) {
	// 4111-1111-1111-1111 is a known valid Luhn card
	text := "Paid with Visa 4111-1111-1111-1111 and Mastercard 5105105105105100 on file."
	require.True(t, LuhnValid("4111-1111-1111-1111"))
	require.True(t, LuhnValid("5105105105105100"))

	matches := DetectPII(text)
	var cards []string
	for _, m := range matches {
		if m.Type == PIICreditCard {
			cards = append(cards, m.Value)
		}
	}
	require.Contains(t, cards, "4111-1111-1111-1111")
	require.Contains(t, cards, "5105105105105100")
}

func TestPII_CreditCard_LuhnInvalid(t *testing.T) {
	text := "Random numbers: 4111-1111-1111-1112 and 1234567890123456."
	require.False(t, LuhnValid("4111-1111-1111-1112"))
	require.False(t, LuhnValid("1234567890123456"))

	matches := DetectPII(text)
	for _, m := range matches {
		require.NotEqual(t, PIICreditCard, m.Type, "invalid Luhn checksum should not be detected as credit card")
	}
}

func TestPII_US_SSN(t *testing.T) {
	valid := "Valid SSN is 123-45-6789."
	matches := DetectPII(valid)
	require.Len(t, matches, 1)
	require.Equal(t, PIIUSSSN, matches[0].Type)
	require.Equal(t, "123-45-6789", matches[0].Value)

	// Invalid cases
	invalidCases := []string{
		"000-12-3456", // area 000
		"666-12-3456", // area 666
		"912-34-5678", // area 9xx
		"123-00-4567", // group 00
		"123-45-0000", // serial 0000
	}
	for _, tc := range invalidCases {
		m := DetectPII("My SSN is " + tc)
		for _, match := range m {
			require.NotEqual(t, PIIUSSSN, match.Type, "invalid SSN format %s should be rejected", tc)
		}
	}
}

func calcVerhoeff(base11 string) string {
	verhoeffInv := []int{0, 4, 3, 2, 1, 5, 6, 7, 8, 9}
	c := 0
	for i := 0; i < len(base11); i++ {
		d := int(base11[len(base11)-1-i] - '0')
		c = verhoeffD[c][verhoeffP[(i+1)%8][d]]
	}
	return fmt.Sprintf("%s%d", base11, verhoeffInv[c])
}

func TestPII_Aadhaar_VerhoeffValid(t *testing.T) {
	// Generate valid 12-digit Aadhaar using Verhoeff check digit
	validAadhaar := calcVerhoeff("23456789012")
	require.True(t, VerhoeffValid(validAadhaar))

	spacedAadhaar := fmt.Sprintf("%s %s %s", validAadhaar[:4], validAadhaar[4:8], validAadhaar[8:])
	require.True(t, VerhoeffValid(spacedAadhaar))

	text := "Citizen Aadhaar: " + spacedAadhaar + " registered."
	matches := DetectPII(text)
	var aadhaars []string
	for _, m := range matches {
		if m.Type == PIIAadhaar {
			aadhaars = append(aadhaars, m.Value)
		}
	}
	require.Contains(t, aadhaars, spacedAadhaar)
}

func TestPII_Aadhaar_VerhoeffInvalid(t *testing.T) {
	// 12 digits with wrong checksum
	invalidAadhaar := "2345 6789 0129"
	require.False(t, VerhoeffValid(invalidAadhaar))

	matches := DetectPII("Number " + invalidAadhaar)
	for _, m := range matches {
		require.NotEqual(t, PIIAadhaar, m.Type, "invalid Verhoeff checksum should not be detected as Aadhaar")
	}
}
