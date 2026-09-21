package guard

import (
	"net"
	"regexp"
	"sort"
	"strings"
)

type Kind string

const (
	Secret Kind = "SECRET"
	PII    Kind = "PII"
)

type Span struct {
	Kind  Kind
	Value string
	Start int
	End   int
}

var secretRegexes = []*regexp.Regexp{
	regexp.MustCompile(`\b(?:sk-(?:proj-|ant-)?[A-Za-z0-9_-]{20,})\b`),
	regexp.MustCompile(`\b(?:pg_(?:live|test|dev)_[A-Za-z0-9_-]{20,})\b`),
	regexp.MustCompile(`\b(?:AKIA[0-9A-Z]{16})\b`),
	regexp.MustCompile(`\b(?:ghp_[A-Za-z0-9]{36})\b`),
	regexp.MustCompile(`\b(?:hvs\.[A-Za-z0-9_-]{20,})\b`),
}

func Detect(text string) []Span {
	var spans []Span
	for _, re := range secretRegexes {
		for _, idx := range re.FindAllStringIndex(text, -1) {
			spans = append(spans, Span{
				Kind:  Secret,
				Value: text[idx[0]:idx[1]],
				Start: idx[0],
				End:   idx[1],
			})
		}
	}
	for _, m := range DetectPII(text) {
		spans = append(spans, Span{
			Kind:  PII,
			Value: m.Value,
			Start: m.Start,
			End:   m.End,
		})
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End > spans[j].End
	})
	return spans
}

type PIIType string

const (
	PIIEmail      PIIType = "EMAIL"
	PIIIPv4       PIIType = "IPV4"
	PIIIPv6       PIIType = "IPV6"
	PIICreditCard PIIType = "CREDIT_CARD"
	PIIUSSSN      PIIType = "US_SSN"
	PIIAadhaar    PIIType = "AADHAAR"
	PIIPhone      PIIType = "PHONE"
)

type PIIMatch struct {
	Type  PIIType
	Value string
	Start int
	End   int
}

var (
	emailRegex = regexp.MustCompile(`[a-zA-Z0-9.!#$%&'*+/=?^_` + "`" + `{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?)+`)
	ipv4Regex  = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(?:25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\b`)
	ipv6Regex  = regexp.MustCompile(`\b(?:[0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}\b|\b(?:[0-9a-fA-F]{1,4}:){1,7}:|:(?::[0-9a-fA-F]{1,4}){1,7}\b|\b(?:[0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}\b`)
	ssnRegex   = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
	cardRegex  = regexp.MustCompile(`\b(?:\d[ -]*?){13,19}\b`)
	aadhaarReg = regexp.MustCompile(`\b\d{4}\s?\d{4}\s?\d{4}\b`)
	phoneRegex = regexp.MustCompile(`(?:\+?[1-9]\d{0,2}[ -]?)?\(?\d{3}\)?[ -]?\d{3}[ -]?\d{4}\b`)
)

func LuhnValid(s string) bool {
	digits := make([]int, 0, len(s))
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			digits = append(digits, int(ch-'0'))
		} else if ch != ' ' && ch != '-' {
			return false
		}
	}
	if len(digits) < 13 || len(digits) > 19 {
		return false
	}
	sum := 0
	alt := false
	for i := len(digits) - 1; i >= 0; i-- {
		d := digits[i]
		if alt {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		alt = !alt
	}
	return sum%10 == 0
}

var verhoeffD = [][]int{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	{1, 2, 3, 4, 0, 6, 7, 8, 9, 5},
	{2, 3, 4, 0, 1, 7, 8, 9, 5, 6},
	{3, 4, 0, 1, 2, 8, 9, 5, 6, 7},
	{4, 0, 1, 2, 3, 9, 5, 6, 7, 8},
	{5, 9, 8, 7, 6, 0, 4, 3, 2, 1},
	{6, 5, 9, 8, 7, 1, 0, 4, 3, 2},
	{7, 6, 5, 9, 8, 2, 1, 0, 4, 3},
	{8, 7, 6, 5, 9, 3, 2, 1, 0, 4},
	{9, 8, 7, 6, 5, 4, 3, 2, 1, 0},
}

var verhoeffP = [][]int{
	{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
	{1, 5, 7, 6, 2, 8, 3, 0, 9, 4},
	{5, 8, 0, 3, 7, 9, 6, 1, 4, 2},
	{8, 9, 1, 6, 0, 4, 3, 5, 2, 7},
	{9, 4, 5, 3, 1, 2, 6, 8, 7, 0},
	{4, 2, 8, 6, 5, 7, 3, 9, 0, 1},
	{2, 7, 9, 3, 8, 0, 6, 4, 1, 5},
	{7, 0, 4, 6, 9, 1, 3, 2, 5, 8},
}

func VerhoeffValid(s string) bool {
	digits := make([]int, 0, len(s))
	for _, ch := range s {
		if ch >= '0' && ch <= '9' {
			digits = append(digits, int(ch-'0'))
		} else if ch != ' ' {
			return false
		}
	}
	if len(digits) != 12 {
		return false
	}
	c := 0
	for i := 0; i < len(digits); i++ {
		d := digits[len(digits)-1-i]
		c = verhoeffD[c][verhoeffP[i%8][d]]
	}
	return c == 0
}

func ValidSSN(s string) bool {
	if len(s) != 11 || s[3] != '-' || s[6] != '-' {
		return false
	}
	area := s[0:3]
	group := s[4:6]
	serial := s[7:11]
	if area == "000" || area == "666" || area[0] == '9' {
		return false
	}
	if group == "00" {
		return false
	}
	if serial == "0000" {
		return false
	}
	return true
}

func DetectPII(text string) []PIIMatch {
	var matches []PIIMatch

	// 1. Email
	for _, idx := range emailRegex.FindAllStringIndex(text, -1) {
		matches = append(matches, PIIMatch{
			Type:  PIIEmail,
			Value: text[idx[0]:idx[1]],
			Start: idx[0],
			End:   idx[1],
		})
	}

	// 2. IPv4
	for _, idx := range ipv4Regex.FindAllStringIndex(text, -1) {
		matches = append(matches, PIIMatch{
			Type:  PIIIPv4,
			Value: text[idx[0]:idx[1]],
			Start: idx[0],
			End:   idx[1],
		})
	}

	// 3. IPv6
	for _, idx := range ipv6Regex.FindAllStringIndex(text, -1) {
		val := text[idx[0]:idx[1]]
		if ip := net.ParseIP(val); ip != nil && ip.To4() == nil {
			matches = append(matches, PIIMatch{
				Type:  PIIIPv6,
				Value: val,
				Start: idx[0],
				End:   idx[1],
			})
		}
	}

	// 4. US SSN
	for _, idx := range ssnRegex.FindAllStringIndex(text, -1) {
		val := text[idx[0]:idx[1]]
		if ValidSSN(val) {
			matches = append(matches, PIIMatch{
				Type:  PIIUSSSN,
				Value: val,
				Start: idx[0],
				End:   idx[1],
			})
		}
	}

	// 5. Aadhaar
	for _, idx := range aadhaarReg.FindAllStringIndex(text, -1) {
		val := text[idx[0]:idx[1]]
		if VerhoeffValid(val) {
			matches = append(matches, PIIMatch{
				Type:  PIIAadhaar,
				Value: val,
				Start: idx[0],
				End:   idx[1],
			})
		}
	}

	// 6. Credit Card (Luhn check)
	for _, idx := range cardRegex.FindAllStringIndex(text, -1) {
		val := text[idx[0]:idx[1]]
		cleaned := strings.ReplaceAll(strings.ReplaceAll(val, "-", ""), " ", "")
		if len(cleaned) >= 13 && len(cleaned) <= 19 && LuhnValid(val) {
			matches = append(matches, PIIMatch{
				Type:  PIICreditCard,
				Value: val,
				Start: idx[0],
				End:   idx[1],
			})
		}
	}

	// 7. Phone
	for _, idx := range phoneRegex.FindAllStringIndex(text, -1) {
		val := text[idx[0]:idx[1]]
		matches = append(matches, PIIMatch{
			Type:  PIIPhone,
			Value: val,
			Start: idx[0],
			End:   idx[1],
		})
	}

	// Sort by Start offset. If equal, longer match first.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Start != matches[j].Start {
			return matches[i].Start < matches[j].Start
		}
		return (matches[i].End - matches[i].Start) > (matches[j].End - matches[j].Start)
	})

	// Resolve overlapping matches: keep the longer match
	var filtered []PIIMatch
	for _, m := range matches {
		if len(filtered) == 0 {
			filtered = append(filtered, m)
			continue
		}
		last := &filtered[len(filtered)-1]
		if m.Start < last.End {
			if (m.End - m.Start) > (last.End - last.Start) {
				*last = m
			}
		} else {
			filtered = append(filtered, m)
		}
	}

	return filtered
}
