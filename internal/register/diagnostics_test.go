package register

import (
	"strings"
	"testing"
)

func TestContinuationDiagnosticRedactsUnknownValues(t *testing.T) {
	for _, tc := range []struct {
		url  string
		page interface{}
		want string
	}{
		{"https://auth.openai.com/about-you?code=secret", map[string]interface{}{"type": "about-you"}, "continuation_step=about-you; page_step=about-you"},
		{"https://other.example/about-you", "secret", "continuation_step=unrecognized; page_step=unrecognized"},
		{"/email-verification?email=secret", map[string]interface{}{"type": "email-verification"}, "continuation_step=email-verification; page_step=email-verification"},
		{"/secret", nil, "continuation_step=unrecognized; page_step=unrecognized"},
	} {
		got := responseDiagnostic("Validate OTP", 200, "req-1", map[string]interface{}{"continue_url": tc.url, "page": tc.page})
		if !strings.Contains(got, tc.want) {
			t.Errorf("got %s; want %s", got, tc.want)
		}
		if strings.Contains(got, "secret") {
			t.Fatalf("leaked response: %s", got)
		}
	}
}

func TestResponseDiagnosticDoesNotExposeSecrets(t *testing.T) {
	data := map[string]interface{}{
		"accessToken":  "secret-token",
		"continue_url": "https://example.com/next?code=secret-code",
		"error":        map[string]interface{}{"code": "registration_disallowed", "message": "secret-email@example.com"},
	}
	got := responseDiagnostic("Create Account", 400, "req-123", data)
	for _, want := range []string{"registration_disallowed", "req-123", "400", "continue_url_present=true"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "secret") {
		t.Fatalf("sensitive response leaked: %s", got)
	}
}
