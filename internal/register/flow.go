package register

import (
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	"github.com/verssache/chatgpt-creator/internal/sentinel"
	"github.com/verssache/chatgpt-creator/internal/util"
)

// visitHomepage visits chatgpt.com to initialize session
func (c *Client) visitHomepage() error {
	var resp *http.Response
	var err error
	for retry := 0; retry < 3; retry++ {
		req, _ := http.NewRequest("GET", baseURL+"/", nil)
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
		req.Header.Set("Upgrade-Insecure-Requests", "1")

		resp, err = c.do(req)
		if err != nil {
			return err
		}

		c.log(fmt.Sprintf("Visit Homepage (Try %d)", retry+1), resp.StatusCode)

		if resp.StatusCode == 200 || resp.StatusCode == 302 || resp.StatusCode == 307 {
			resp.Body.Close()
			return nil
		}
		resp.Body.Close()
		time.Sleep(1 * time.Second)
	}
	return fmt.Errorf("failed to visit homepage after 3 retries (status: %d)", resp.StatusCode)
}

// getCSRF retrieves the CSRF token from chatgpt.com
func (c *Client) getCSRF() (string, error) {
	req, _ := http.NewRequest("GET", baseURL+"/api/auth/csrf", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", baseURL+"/")

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}

	c.log("Get CSRF", resp.StatusCode)
	if data.CSRFToken == "" {
		return "", fmt.Errorf("csrf token not found")
	}
	return data.CSRFToken, nil
}

// signin initiates the signin process and returns the authorize URL
func (c *Client) signin(email, csrf string) (string, error) {
	signinURL := baseURL + "/api/auth/signin/openai"
	params := url.Values{}
	params.Set("prompt", "login")
	params.Set("ext-oai-did", c.deviceID)
	params.Set("auth_session_logging_id", util.GenerateUUID()) // Assuming util has this or use google/uuid
	params.Set("screen_hint", "login_or_signup")
	params.Set("login_hint", email)

	fullURL := signinURL + "?" + params.Encode()

	formData := url.Values{}
	formData.Set("callbackUrl", baseURL+"/")
	formData.Set("csrfToken", csrf)
	formData.Set("json", "true")

	req, _ := http.NewRequest("POST", fullURL, strings.NewReader(formData.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("Origin", baseURL)

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", err
	}

	c.log("Signin", resp.StatusCode)
	if data.URL == "" {
		return "", fmt.Errorf("authorize url not found")
	}
	return data.URL, nil
}

// authorize visits the authorize URL and returns the final redirect URL
func (c *Client) authorize(authURL string) (string, error) {
	req, _ := http.NewRequest("GET", authURL, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", baseURL+"/")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := c.do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()
	c.log("Authorize", resp.StatusCode)
	return finalURL, nil
}

// register registers the user with email and password
func (c *Client) register(email, password string) (int, map[string]interface{}, error) {
	regURL := authURL + "/api/accounts/user/register"
	payload := map[string]string{
		"username": email,
		"password": password,
	}
	jsonPayload, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", regURL, strings.NewReader(string(jsonPayload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", authURL+"/create-account/password")
	req.Header.Set("Origin", authURL)

	// Add trace headers if available in util
	traceHeaders := util.MakeTraceHeaders()
	for k, v := range traceHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data map[string]interface{}
	json.Unmarshal(body, &data)

	c.log("Register", resp.StatusCode)
	return resp.StatusCode, data, nil
}

// sendOTP sends the OTP to the user's email
func (c *Client) sendOTP() (int, map[string]interface{}, error) {
	otpURL := authURL + "/api/accounts/email-otp/send"
	req, _ := http.NewRequest("GET", otpURL, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Referer", authURL+"/create-account/password")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := c.do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		data = map[string]interface{}{"text": string(body)}
	}

	c.log("Send OTP", resp.StatusCode)
	return resp.StatusCode, data, nil
}

// validateOTP validates the OTP code
func (c *Client) validateOTP(code string) (int, map[string]interface{}, error) {
	valURL := authURL + "/api/accounts/email-otp/validate"
	payload := map[string]string{"code": code}
	jsonPayload, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", valURL, strings.NewReader(string(jsonPayload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", authURL+"/email-verification")
	req.Header.Set("Origin", authURL)

	traceHeaders := util.MakeTraceHeaders()
	for k, v := range traceHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	data, err := c.inspectAuthResponse("Validate OTP", resp)
	return resp.StatusCode, data, err
}

// visitPage navigates to a continuation page (e.g. /about-you) to establish session state and cookies
func (c *Client) visitPage(pageURL, referer string) error {
	target := pageURL
	if !strings.HasPrefix(target, "http://") && !strings.HasPrefix(target, "https://") {
		target = authURL + target
	}
	req, err := http.NewRequest("GET", target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := c.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	c.log("Visit Continuation Page", resp.StatusCode)
	return nil
}

// createAccount creates the user account with name and birthdate
func (c *Client) createAccount(name, birthdate string) (int, map[string]interface{}, error) {
	sentinelCreateAccount, err := sentinel.BuildSentinelToken(c.session, c.deviceID, "create_account", c.ua, c.secChUA, c.impersonate)
	if err != nil {
		return 0, nil, fmt.Errorf("failed to get sentinel auth: %v", err)
	}
	return c.createAccountWithToken(name, birthdate, sentinelCreateAccount)
}

// createAccountWithToken performs the final create_account POST using a pre-built sentinel token
func (c *Client) createAccountWithToken(name, birthdate, sentinelToken string) (int, map[string]interface{}, error) {
	createURL := authURL + "/api/accounts/create_account"
	payload := map[string]string{
		"name":      name,
		"birthdate": birthdate,
	}
	jsonPayload, _ := json.Marshal(payload)

	req, _ := http.NewRequest("POST", createURL, strings.NewReader(string(jsonPayload)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", authURL+"/about-you")
	req.Header.Set("Origin", authURL)
	req.Header.Set("openai-sentinel-token", sentinelToken)

	traceHeaders := util.MakeTraceHeaders()
	for k, v := range traceHeaders {
		req.Header.Set(k, v)
	}

	resp, err := c.do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	data, err := c.inspectAuthResponse("Create Account", resp)
	return resp.StatusCode, data, err
}

// callback handles the callback URL.
// If cbURL is empty a session check is still performed so an unattended success
// branch is not treated as a validated account.
func (c *Client) callback(cbURL string) (int, map[string]interface{}, error) {
	if cbURL == "" {
		ok, err := c.verifySession()
		if err != nil {
			return 0, nil, fmt.Errorf("empty callback url and session check failed: %v", err)
		}
		if !ok {
			return 0, nil, fmt.Errorf("empty callback url and no active session")
		}
		return 0, map[string]interface{}{"validated_session": true}, nil
	}

	req, _ := http.NewRequest("GET", cbURL, nil)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := c.do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	finalURL := resp.Request.URL.String()

	// The callback terminates the OAuth exchange; a 2xx here is not proof a
	// session was formed. Confirm via the session endpoint.
	checkErr := fmt.Errorf("callback exited at %s (status %d) without a validated session", finalURL, resp.StatusCode)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		ok, sessErr := c.verifySession()
		if sessErr != nil {
			return resp.StatusCode, map[string]interface{}{"final_url": finalURL, "validated_session": false}, fmt.Errorf("session check error: %v", sessErr)
		}
		if ok {
			c.log("Callback (session validated)", resp.StatusCode)
			return resp.StatusCode, map[string]interface{}{"final_url": finalURL, "validated_session": true}, nil
		}
	}

	return resp.StatusCode, map[string]interface{}{"final_url": finalURL, "validated_session": false}, checkErr
}

// verifySession confirms an authenticated session exists on chatgpt.com.
// A session that lacks an access token (e.g. the pre-login WARNING_BANNER
// response) is treated as not logged in.
func (c *Client) verifySession() (bool, error) {
	req, _ := http.NewRequest("GET", baseURL+"/api/auth/session", nil)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", baseURL+"/")

	resp, err := c.do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var data struct {
		AccessToken string `json:"accessToken"`
		User        any    `json:"user"`
		Error       string `json:"error"`
	}
	json.Unmarshal(body, &data)

	c.log("Verify Session", resp.StatusCode)

	if data.AccessToken != "" || data.User != nil {
		return true, nil
	}
	return false, nil
}

func (c *Client) RunRegister(emailAddr, password, name, birthdate string) error {
	c.print("Starting registration flow...")

	if err := c.visitHomepage(); err != nil {
		return err
	}
	c.randomDelay(0.3, 0.8)

	csrf, err := c.getCSRF()
	if err != nil {
		return err
	}
	c.randomDelay(0.2, 0.5)

	authURL, err := c.signin(emailAddr, csrf)
	if err != nil {
		return err
	}
	c.randomDelay(0.3, 0.8)

	finalURL, err := c.authorize(authURL)
	if err != nil {
		return err
	}
	c.randomDelay(0.3, 0.8)

	u, _ := url.Parse(finalURL)
	finalPath := u.Path
	c.print(fmt.Sprintf("Authorize landed on %s", finalPath))

	needOTP := false

	if strings.Contains(finalPath, "create-account/password") {
		c.randomDelay(0.5, 1.0)
		status, data, err := c.register(emailAddr, password)
		if err != nil {
			return err
		}
		if status != 200 {
			return fmt.Errorf("register failed (%d): %v", status, data)
		}
		c.randomDelay(0.3, 0.8)
		c.sendOTP()
		needOTP = true
	} else if strings.Contains(finalPath, "email-verification") || strings.Contains(finalPath, "email-otp") {
		c.print("Jump to OTP verification stage")
		if _, _, err := c.sendOTP(); err != nil {
			c.print(fmt.Sprintf("Send OTP failed: %v", err))
		}
		needOTP = true
	} else if strings.Contains(finalPath, "about-you") {
		c.print("Jump to fill information stage")
		c.randomDelay(0.5, 1.0)
		status, data, err := c.createAccount(name, birthdate)
		if err != nil {
			return err
		}
		if status != 200 {
			return fmt.Errorf("create account failed (%d): %v", status, data)
		}
		c.randomDelay(0.3, 0.5)

		var cbURL string
		if u, ok := data["continue_url"].(string); ok {
			cbURL = u
		} else if u, ok := data["url"].(string); ok {
			cbURL = u
		} else if u, ok := data["redirect_url"].(string); ok {
			cbURL = u
		}
		if err := c.completeCallback(cbURL); err != nil {
			return err
		}
		return nil
	} else if strings.Contains(finalPath, "callback") || strings.Contains(finalURL, "chatgpt.com") {
		c.print("Account registration completed")
		return c.completeCallback("")
	} else {
		c.print(fmt.Sprintf("Unknown jump: %s", finalURL))
		c.register(emailAddr, password)
		c.sendOTP()
		needOTP = true
	}

	if needOTP {
		if c.mailbox == nil {
			return fmt.Errorf("mailbox not provisioned; cannot receive OTP")
		}

		onMail := func(subject string) {
			c.print(fmt.Sprintf("Mail received: %s", subject))
		}

		c.print(fmt.Sprintf("Waiting for OTP at %s", emailAddr))
		otpCode, err := c.mailbox.WaitForCode(c.Context(), 60*time.Second, onMail)
		if err != nil {
			return err
		}
		c.print(fmt.Sprintf("OTP extracted: %s", otpCode))

		c.randomDelay(0.3, 0.8)
		status, data, err := c.validateOTP(otpCode)
		if err != nil {
			return err
		}

		if status != 200 {
			c.print("Verification code rejected, requesting a new one")
			c.sendOTP()
			c.randomDelay(1.0, 2.0)

			otpCode, err = c.mailbox.WaitForCode(c.Context(), 60*time.Second, onMail)
			if err != nil {
				return err
			}
			c.print(fmt.Sprintf("OTP extracted: %s", otpCode))

			c.randomDelay(0.3, 0.8)
			status, data, err = c.validateOTP(otpCode)
			if err != nil {
				return err
			}
			if status != 200 {
				return fmt.Errorf("verification code failed after retry (%d): %v", status, data)
			}
		}

		var contURL string
		if u, ok := data["continue_url"].(string); ok && u != "" {
			contURL = u
		} else if u, ok := data["url"].(string); ok && u != "" {
			contURL = u
		}
		if contURL != "" {
			c.randomDelay(0.3, 0.8)
			if err := c.visitPage(contURL, authURL+"/email-verification"); err != nil {
				c.print(fmt.Sprintf("Warning: failed to visit continuation page: %v", err))
			}
		}
	}

	c.randomDelay(0.5, 1.5)
	// Re-fetch fresh sentinel token right before create_account to avoid stale proof
	sentinelCreateAccount, err := sentinel.BuildSentinelToken(c.session, c.deviceID, "create_account", c.ua, c.secChUA, c.impersonate)
	if err != nil {
		return fmt.Errorf("failed to get fresh sentinel auth: %v", err)
	}
	status, data, err := c.createAccountWithToken(name, birthdate, sentinelCreateAccount)
	if err != nil {
		return err
	}
	if status != 200 {
		return fmt.Errorf("create account failed (%d): %v", status, data)
	}

	c.randomDelay(0.2, 0.5)
	var cbURL string
	if u, ok := data["continue_url"].(string); ok {
		cbURL = u
	} else if u, ok := data["url"].(string); ok {
		cbURL = u
	} else if u, ok := data["redirect_url"].(string); ok {
		cbURL = u
	}
	if err := c.completeCallback(cbURL); err != nil {
		return err
	}

	return nil
}

// completeCallback runs the callback exchange if a URL is present and then
// validates the resulting session. A failure to establish a session aborts the
// registration instead of silently reporting SUCCESS.
func (c *Client) completeCallback(cbURL string) error {
	c.randomDelay(0.2, 0.5)

	if cbURL != "" {
		if _, _, err := c.callback(cbURL); err != nil {
			return err
		}
		return nil
	}

	// No callback URL returned: only accept when we can still confirm a session.
	ok, err := c.verifySession()
	if err != nil {
		return fmt.Errorf("session verification failed: %v", err)
	}
	if !ok {
		return fmt.Errorf("registration finished but no active session could be confirmed")
	}
	return nil
}

func (c *Client) randomDelay(low, high float64) {
	delay := low + rand.Float64()*(high-low)
	t := time.NewTimer(time.Duration(delay * float64(time.Second)))
	defer t.Stop()
	select {
	case <-c.Context().Done():
	case <-t.C:
	}
}
