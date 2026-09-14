package chrome

import (
	"fmt"
	"math/rand"

	"github.com/bogdanfinn/tls-client/profiles"
)

type Profile struct {
	Major       int
	Impersonate string
	Build       int
	PatchMin    int
	PatchMax    int
	SecChUA     string
}

// chromeProfiles only lists versions whose advertised major matches a real
// tls-client fingerprint. Advertising a UA that a TLS handshake contradicts
// (e.g. UA 136 over a Chrome_133 fingerprint) is trivially detectable and was
// producing edge 403s on the homepage warm-up.
var chromeProfiles = []Profile{
	{131, "chrome131", 6778, 0, 300, "\"Chromium\";v=\"131\", \"Google Chrome\";v=\"131\", \"Not_A Brand\";v=\"24\""},
	{133, "chrome133a", 6942, 0, 200, "\"Not:A-Brand\";v=\"99\", \"Google Chrome\";v=\"133\", \"Chromium\";v=\"133\""},
	{144, "chrome144", 7645, 0, 200, "\"Not:A-Brand\";v=\"99\", \"Google Chrome\";v=\"144\", \"Chromium\";v=\"144\""},
	{146, "chrome146", 7725, 0, 200, "\"Not:A-Brand\";v=\"99\", \"Google Chrome\";v=\"146\", \"Chromium\";v=\"146\""},
}

func RandomChromeVersion() (Profile, string, string) {
	profile := chromeProfiles[rand.Intn(len(chromeProfiles))]
	patch := rand.Intn(profile.PatchMax-profile.PatchMin+1) + profile.PatchMin
	fullVersion := fmt.Sprintf("%d.0.%d.%d", profile.Major, profile.Build, patch)
	userAgent := fmt.Sprintf("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/%s Safari/537.36", fullVersion)
	return profile, fullVersion, userAgent
}

// MapToTLSProfile resolves an impersonate key to a real tls-client profile.
// Every key here must fingerprint as the same major version it advertises.
func MapToTLSProfile(impersonate string) profiles.ClientProfile {
	switch impersonate {
	case "chrome131":
		return profiles.Chrome_131
	case "chrome133a":
		return profiles.Chrome_133
	case "chrome144":
		return profiles.Chrome_144
	case "chrome146":
		return profiles.Chrome_146
	default:
		return profiles.Chrome_133
	}
}

// SupportedImpersonates lists the impersonate strings that map to real
// tls-client profiles. Kept in sync with MapToTLSProfile.
func SupportedImpersonates() []string {
	return []string{"chrome131", "chrome133a", "chrome144", "chrome146"}
}

// TLSMajor reports the major Chrome version that MapToTLSProfile actually
// fingerprints as, so tests can assert UA/TLS consistency.
func TLSMajor(impersonate string) int {
	switch impersonate {
	case "chrome131":
		return 131
	case "chrome133a":
		return 133
	case "chrome144":
		return 144
	case "chrome146":
		return 146
	default:
		return 133
	}
}
