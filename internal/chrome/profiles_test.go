package chrome

import (
	"strconv"
	"strings"
	"testing"
)

// TestProfileUAMatchesTLSFingerprint guards the regression where the profile
// table advertised Chrome 136/142 while tls-client actually fingerprinted as
// Chrome 133/144. A UA that contradicts the TLS handshake is trivially
// detectable and produced edge 403s during homepage warm-up.
func TestProfileUAMatchesTLSFingerprint(t *testing.T) {
	for _, p := range chromeProfiles {
		if got := TLSMajor(p.Impersonate); got != p.Major {
			t.Errorf(
				"profile %q advertises Chrome %d but fingerprints as Chrome %d",
				p.Impersonate, p.Major, got,
			)
		}
	}
}

// TestSupportedImpersonatesCoversTable keeps the exported list honest.
func TestSupportedImpersonatesCoversTable(t *testing.T) {
	supported := map[string]bool{}
	for _, s := range SupportedImpersonates() {
		supported[s] = true
	}
	for _, p := range chromeProfiles {
		if !supported[p.Impersonate] {
			t.Errorf("profile %q is selectable but missing from SupportedImpersonates()", p.Impersonate)
		}
	}
}

// TestRandomChromeVersionConsistency checks the generated UA string carries the
// same major version as the TLS profile that will be used with it.
func TestRandomChromeVersionConsistency(t *testing.T) {
	for i := 0; i < 200; i++ {
		profile, fullVersion, ua := RandomChromeVersion()

		if !strings.Contains(ua, "Chrome/"+fullVersion) {
			t.Fatalf("UA %q does not embed full version %q", ua, fullVersion)
		}

		majorFromVersion, _, ok := strings.Cut(fullVersion, ".")
		if !ok {
			t.Fatalf("unexpected version format %q", fullVersion)
		}
		n, err := strconv.Atoi(majorFromVersion)
		if err != nil {
			t.Fatalf("bad major in %q: %v", fullVersion, err)
		}
		if n != profile.Major {
			t.Fatalf("version %q does not match profile major %d", fullVersion, profile.Major)
		}
		if got := TLSMajor(profile.Impersonate); got != profile.Major {
			t.Fatalf("UA Chrome %d paired with TLS fingerprint Chrome %d", profile.Major, got)
		}
		if !strings.Contains(profile.SecChUA, strconv.Itoa(profile.Major)) {
			t.Fatalf("sec-ch-ua %q missing major %d", profile.SecChUA, profile.Major)
		}
	}
}
