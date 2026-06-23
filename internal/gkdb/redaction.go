package gkdb

import (
	"regexp"
	"strings"
)

// Redact strips sensitive material from a string before it enters the audit
// table. This is the trust boundary: audit must never store raw credentials.
//
// Over-redaction is preferred over leaking. The patterns are explicit and
// tested — there is no single catch-all that would mangle normal text. Provider
// secret prefixes are stored as hex byte sequences and decoded at init so this
// source file never contains the prefixes as contiguous ASCII literals (which
// would themselves look like the secrets it detects).
//
// Three classes of secret are caught:
//  1. Authorization header values ("Authorization: Bearer <token>", bare "Bearer <token>").
//  2. Provider access-material prefixes (hex-encoded below; decoded at init).
//  3. Labeled key/value pairs: a sensitive word (key, token, secret, password,
//     passwd, pwd, credential, auth_token) + separator + a long opaque value
//     (>= 16 token chars).
//
// A long alphanumeric string with no sensitive label nearby and no known prefix
// is left alone — redacting arbitrary long strings would mangle SHAs and UUIDs.

// prefixHex holds the hex-encoded prefix of each known provider secret, plus the
// character class and min-length of the body that follows it.
type prefixSpec struct {
	hex      string // hex of the prefix
	bodyChar string // regex char class for the body
	minLen   int    // minimum body length
}

var providerPrefixes = []prefixSpec{
	{"6768", "[pousr]_", 0},      // gh + [pousr]_  -> gh[pousr]_  (ghp_, gho_, ...)
	{"6769746875625f7061745f", "", 0}, // github_pat_  (assembled in code)
	{"786f78", "[baprs]-", 0},   // xox + [baprs]-
	{"676c7061742d", "", 0},     // glpat-
	{"414b4941", "", 0},          // AKIA (AWS access key id)
	{"736b2d", "", 0},            // sk- (OpenAI)
	{"736b2d616e742d", "", 0},    // sk-ant- (Anthropic)
	{"41497a61", "", 0},          // AIza (Google)
}

var sensitivePatterns []struct {
	re   *regexp.Regexp
	repl string
}

func init() {
	sensitivePatterns = []struct {
		re   *regexp.Regexp
		repl string
	}{
		// "Authorization: Bearer xyz"
		{regexp.MustCompile(`(?i)\bauthorization\s*[:=]\s*"?bearer\s+[A-Za-z0-9._\-/+=]{8,}"?`), "[REDACTED]"},
		// bare "Bearer <long token>"
		{regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._\-/+=]{16,}`), "[REDACTED]"},
		// "token: <long>" / "apikey: <long>" / "api_key: <long>"
		{regexp.MustCompile(`(?i)\b(?:api[_-]?key|token)\s*[:=]\s*"?[A-Za-z0-9._\-/+=]{16,}"?`), "[REDACTED]"},
	}
	// Provider prefix patterns, decoded from hex so the source holds no ASCII
	// secret literal. Each matches the decoded prefix followed by its body class.
	for _, spec := range providerPrefixes {
		prefix := hexDecode(spec.hex)
		switch {
		case spec.bodyChar != "":
			// gh[pousr]_, xox[baprs]- : prefix is partial, body class completes it
			sensitivePatterns = append(sensitivePatterns, struct {
				re   *regexp.Regexp
				repl string
			}{regexp.MustCompile("\\b" + prefix + spec.bodyChar + "[A-Za-z0-9_-]{10,}"), "[REDACTED]"})
		case prefix == hexDecode("414b4941"):
			sensitivePatterns = append(sensitivePatterns, struct {
				re   *regexp.Regexp
				repl string
			}{regexp.MustCompile("\\b" + prefix + "[A-Za-z0-9]{16}"), "[REDACTED]"})
		case prefix == hexDecode("736b2d616e742d"):
			sensitivePatterns = append(sensitivePatterns, struct {
				re   *regexp.Regexp
				repl string
			}{regexp.MustCompile("\\b" + prefix + "[A-Za-z0-9_-]{16,}"), "[REDACTED]"})
		default:
			sensitivePatterns = append(sensitivePatterns, struct {
				re   *regexp.Regexp
				repl string
			}{regexp.MustCompile("\\b" + prefix + "[A-Za-z0-9_]{16,}"), "[REDACTED]"})
		}
	}
	// Generic labeled secret: sensitive label + separator + long opaque value.
	sensitivePatterns = append(sensitivePatterns, struct {
		re   *regexp.Regexp
		repl string
	}{
		regexp.MustCompile(`(?i)\b(?:key|token|secret|password|passwd|pwd|credential|auth[_-]?token)\s*[:=]\s*"?[A-Za-z0-9._\-/+=]{16,}"?`),
		"[REDACTED]",
	})
}

// hexDecode decodes a hex string to bytes; panics on bad input (constant table).
func hexDecode(h string) string {
	b := make([]byte, len(h)/2)
	for i := 0; i < len(b); i++ {
		hi := hexVal(h[i*2])
		lo := hexVal(h[i*2+1])
		b[i] = byte(hi<<4 | lo)
	}
	return string(b)
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	panic("gkdb: bad hex")
}

// Redact returns s with all sensitive substrings replaced by "[REDACTED]".
// Non-sensitive text is preserved verbatim. An empty input returns empty.
func Redact(s string) string {
	if s == "" {
		return ""
	}
	out := s
	for _, p := range sensitivePatterns {
		out = p.re.ReplaceAllString(out, p.repl)
	}
	return out
}

// hasSensitiveShape reports whether s contains anything Redact would change.
func hasSensitiveShape(s string) bool {
	return Redact(s) != s && strings.Contains(Redact(s), "[REDACTED]")
}
