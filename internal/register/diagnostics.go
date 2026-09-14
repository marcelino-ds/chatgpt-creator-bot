package register

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"

	http "github.com/bogdanfinn/fhttp"
)

var diagnosticIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// These labels describe observed client routes, not a server state-machine contract.
func diagnosticStep(value string) string {
	switch value {
	case "about-you", "create-account/password", "email-verification", "email-otp":
		return value
	default:
		return "unrecognized"
	}
}

func continuationDiagnostic(data map[string]interface{}) string {
	step := "unrecognized"
	if raw, ok := data["continue_url"].(string); ok {
		if u, err := url.Parse(raw); err == nil && u.User == nil &&
			((u.Scheme == "" && u.Host == "") || (u.Scheme == "https" && u.Host == "auth.openai.com")) {
			if len(u.Path) > 0 && u.Path[0] == '/' {
				step = diagnosticStep(u.Path[1:])
			}
		}
	}
	pageStep := "unrecognized"
	if page, ok := data["page"].(map[string]interface{}); ok {
		if value, ok := page["type"].(string); ok {
			pageStep = diagnosticStep(value)
		}
	}
	return fmt.Sprintf("continuation_step=%s; page_step=%s", step, pageStep)
}

func responseDiagnostic(stage string, status int, requestID string, data map[string]interface{}) string {
	if !diagnosticIdentifier.MatchString(requestID) {
		requestID = "unavailable"
	}
	code := "unspecified"
	if detail, ok := data["error"].(map[string]interface{}); ok {
		if value, ok := detail["code"].(string); ok && diagnosticIdentifier.MatchString(value) {
			code = value
		}
	}
	_, continuation := data["continue_url"]
	_, page := data["page"]
	return fmt.Sprintf("%s: HTTP %d; error_code=%s; request_id=%s; continue_url_present=%t; page_present=%t; %s", stage, status, code, requestID, continuation, page, continuationDiagnostic(data))
}

// Only allowlisted metadata enters logs; response bodies may hold credentials.
//
// A non-2xx status is reported in the diagnostic line but is deliberately not
// turned into an error here: callers distinguish "retry the step" from "abort"
// by inspecting the returned status, and returning an error would skip that
// decision (this previously made the OTP resend path unreachable).
func (c *Client) inspectAuthResponse(stage string, resp *http.Response) (map[string]interface{}, error) {
	var data map[string]interface{}
	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&data)
	if decodeErr != nil {
		data = map[string]interface{}{}
	}
	c.print(responseDiagnostic(stage, resp.StatusCode, resp.Header.Get("x-request-id"), data))
	if decodeErr != nil {
		return data, fmt.Errorf("%s: response body was not readable JSON: %w", stage, decodeErr)
	}
	return data, nil
}
