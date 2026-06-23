package gkdb

import (
	"strings"
	"testing"
)

func longTok(n int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = alphabet[(i*7)%len(alphabet)]
	}
	return string(b)
}

func TestRedactPreservesNonSensitiveText(t *testing.T) {
	cases := []string{
		"",
		"the quick brown fox",
		"commit abc123def456",
		"a short string",
		"thread-id-12345",
	}
	for _, c := range cases {
		if got := Redact(c); got != c {
			t.Errorf("Redact(%q) = %q, want unchanged", c, got)
		}
	}
}

func TestRedactEmptyReturnsEmpty(t *testing.T) {
	if got := Redact(""); got != "" {
		t.Fatalf(`Redact("") = %q, want empty`, got)
	}
}

func TestRedactNonSensitiveLongStringPreserved(t *testing.T) {
	long := strings.Repeat("0123456789abcdef", 3)
	if got := Redact(long); got != long {
		t.Errorf("redacted a non-sensitive long string: %q", got)
	}
}

func TestRedactCatchesProviderPrefixes(t *testing.T) {
	cases := []struct {
		name string
		hex  string
		body string
	}{
		{"ghp", "6768705f", strings.Repeat("0", 36)},
		{"slack", "786f78622d", strings.Repeat("0", 24)},
		{"gitlab", "676c7061742d", strings.Repeat("0", 20)},
		{"aws", "414b4941", strings.Repeat("0", 16)},
		{"openai", "736b2d", strings.Repeat("0", 20)},
		{"google", "41497a61", strings.Repeat("0", 20)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			input := hexDecode(c.hex) + c.body
			got := Redact(input)
			if !strings.Contains(got, "[REDACTED]") {
				t.Errorf("%s prefix not caught: %q", c.name, got)
			}
			if strings.Contains(got, c.body) {
				t.Errorf("%s body leaked: %q", c.name, got)
			}
		})
	}
}

func TestRedactCatchesLabeledSecrets(t *testing.T) {
	val := longTok(40)
	cases := []string{
		"token: " + val,
		"password: " + val,
		"secret=" + val,
		"apikey: " + val,
		"api_key=" + val,
		"credential: " + val,
		"key: " + val,
		"auth_token: " + val,
		"pwd: " + val,
		"passwd=" + val,
	}
	for _, in := range cases {
		got := Redact(in)
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("not redacted: %q -> %q", in, got)
		}
		if strings.Contains(got, val) {
			t.Errorf("leaked value: %q -> %q", in, got)
		}
	}
}

func TestRedactCatchesBearerHeader(t *testing.T) {
	val := longTok(40)
	got := Redact("Authorization: Bearer " + val)
	if !strings.Contains(got, "[REDACTED]") {
		t.Errorf("auth header not redacted: %q", got)
	}
	got2 := Redact("Bearer " + val)
	if !strings.Contains(got2, "[REDACTED]") {
		t.Errorf("bare bearer not redacted: %q", got2)
	}
}

func TestRedactMultipleSensitiveValues(t *testing.T) {
	v := longTok(40)
	got := Redact("token: " + v + " and password=" + v)
	if strings.Count(got, "[REDACTED]") < 2 {
		t.Errorf("expected >=2 redactions, got %q", got)
	}
}

func TestHasSensitiveShape(t *testing.T) {
	if !hasSensitiveShape("token: " + longTok(40)) {
		t.Error("hasSensitiveShape should be true for a labeled secret")
	}
	if hasSensitiveShape("plain text") {
		t.Error("hasSensitiveShape should be false for plain text")
	}
}
