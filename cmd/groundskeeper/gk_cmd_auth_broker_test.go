package main

import (
	"os"
	"strings"
	"testing"
)

func TestVerifyOmpModelPassesAuthBrokerToken(t *testing.T) {
	ompPath, callLog := writeOmpModelStub(t, `#!/usr/bin/env sh
printf '%s\n' "$*" >> "$GK_OMP_CALL_LOG"
[ "$(printenv OMP_AUTH_BROKER_TOKEN)" = "$GK_EXPECTED_BROKER_VALUE" ] || exit 41
[ -z "$(printenv TEST_PROVIDER_API_KEY)" ] || exit 42
[ -z "$(printenv GITHUB_TOKEN)" ] || exit 43
if [ "$1" = "models" ] && [ "$2" = "refresh" ]; then
  exit 0
fi
if [ "$1" = "--model" ] && [ "$2" = "test-provider/model" ] && [ "$3" = "--no-session" ] && [ "$4" = "--max-time=60" ] && [ "$5" = "-p" ]; then
  printf 'GK_OK\n'
  exit 0
fi
exit 44
`)
	token := "auth-broker-fixture-value"
	t.Setenv("OMP_AUTH_BROKER_TOKEN", token)
	t.Setenv("TEST_PROVIDER_API_KEY", "")
	t.Setenv("GITHUB_TOKEN", "generic-fixture-value")
	t.Setenv("GK_OMP_CALL_LOG", callLog)
	t.Setenv("GK_EXPECTED_BROKER_VALUE", token)

	if err := verifyOmpModel(ompPath, "test-provider/model"); err != nil {
		t.Fatalf("verifyOmpModel failed: %v", err)
	}

	calls, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(calls), "\n"); got != 2 {
		t.Fatalf("verifyOmpModel made %d OMP calls, want 2\n--- calls ---\n%s", got, calls)
	}
}
