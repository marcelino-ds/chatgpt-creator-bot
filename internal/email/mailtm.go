package email

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	"github.com/verssache/chatgpt-creator/internal/util"
)

// mail.tm exposes a documented JSON API, which the bot now uses instead of
// scraping generator.email.
//
// generator.email was abandoned because it broke in ways that made OTP reading
// impossible, verified directly against the live site:
//
//   - GET /{domain}/{user} answers 302 -> /inbox2/
//   - the CSS class prefix changed (e7m -> g8r)
//   - mail is pushed over wss://.../notificon/ws and history is never replayed
//   - the message body slot (.mess_bodiyy) renders empty on /inbox1|2|3/ and
//     leaks raw PHP debug text, so the verification code is simply not there
//
// mail.tm needs an account per mailbox: POST /accounts, then POST /token to get
// a JWT used for reading mail.
const (
	mailTMBase = "https://api.mail.tm"

	// mail.tm documents a rate limit of 8 requests/second per IP; polling is
	// deliberately unhurried so a batch of workers stays well under it.
	mailPollInterval = 3 * time.Second
)

type mailDomain struct {
	Domain   string `json:"domain"`
	IsActive bool   `json:"isActive"`
}

type mailAccount struct {
	ID      string `json:"id"`
	Address string `json:"address"`
}

type mailToken struct {
	Token string `json:"token"`
	ID    string `json:"id"`
}

type mailMessageSummary struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Intro   string `json:"intro"`
}

type mailMessageFull struct {
	ID      string   `json:"id"`
	Subject string   `json:"subject"`
	Intro   string   `json:"intro"`
	Text    string   `json:"text"`
	HTML    []string `json:"html"`
}

// Mailbox is a provisioned inbox with a token for reading it.
type Mailbox struct {
	Address string

	provider string
	password string
	token    string
	client   tls_client.HttpClient

	mu   sync.Mutex
	seen map[string]bool
}

type domainCache struct {
	mu        sync.Mutex
	domains   []string
	fetchedAt time.Time
}

var (
	liveDomainCache domainCache
	domainsTTL      = 10 * time.Minute
)

func newHTTPClient() (tls_client.HttpClient, error) {
	return tls_client.NewHttpClient(
		tls_client.NewNoopLogger(),
		tls_client.WithClientProfile(profiles.Chrome_131),
		tls_client.WithTimeoutSeconds(25),
	)
}

// mail.tm rate limits aggressively per IP: sending four account creations back
// to back returns 429 for two of them. Every request therefore passes through a
// process-wide spacer, and 429 responses are retried with backoff instead of
// failing the whole registration.
var mailLimiter struct {
	mu       sync.Mutex
	nextFree time.Time
}

const (
	mailMinInterval = 700 * time.Millisecond
	mailMaxAttempts = 5
)

// throttle reserves the next slot in the request stream and waits for it.
func throttle(ctx context.Context) error {
	mailLimiter.mu.Lock()
	now := time.Now()
	slot := mailLimiter.nextFree
	if slot.Before(now) {
		slot = now
	}
	mailLimiter.nextFree = slot.Add(mailMinInterval)
	mailLimiter.mu.Unlock()

	return sleepCtx(ctx, time.Until(slot))
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// doJSON performs a throttled request, retrying on 429.
func doJSON(ctx context.Context, client tls_client.HttpClient, method, url, token string, payload any) (int, []byte, error) {
	var lastStatus int
	var lastBody []byte

	for attempt := 1; attempt <= mailMaxAttempts; attempt++ {
		if err := throttle(ctx); err != nil {
			return 0, nil, err
		}

		status, body, err := doJSONOnce(client, method, url, token, payload)
		if err != nil {
			return status, body, err
		}
		if status != http.StatusTooManyRequests {
			return status, body, nil
		}

		lastStatus, lastBody = status, body
		if attempt == mailMaxAttempts {
			break
		}
		// Linear backoff on top of the spacer; the limit is per-second, so
		// waiting a beat is enough to clear it.
		if err := sleepCtx(ctx, time.Duration(attempt)*time.Second); err != nil {
			return status, body, err
		}
	}

	return lastStatus, lastBody, fmt.Errorf("rate limited after %d attempts", mailMaxAttempts)
}

func doJSONOnce(client tls_client.HttpClient, method, url, token string, payload any) (int, []byte, error) {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return 0, nil, err
		}
		body = bytes.NewReader(raw)
	}

	req, err := fhttp.NewRequest(method, url, body)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Accept", "application/json")
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, nil, err
	}
	return resp.StatusCode, data, nil
}

// LiveDomains returns the mailbox domains mail.tm currently accepts, excluding
// blacklisted ones. Results are cached briefly so a burst of workers does not
// hit the rate limit.
func LiveDomains(ctx context.Context) ([]string, error) {
	liveDomainCache.mu.Lock()
	defer liveDomainCache.mu.Unlock()

	if len(liveDomainCache.domains) > 0 && time.Since(liveDomainCache.fetchedAt) < domainsTTL {
		return filterBlacklisted(liveDomainCache.domains), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	client, err := newHTTPClient()
	if err != nil {
		return nil, fmt.Errorf("tls client: %w", err)
	}

	status, body, err := doJSON(ctx, client, http.MethodGet, mailTMBase+"/domains?page=1", "", nil)
	if err != nil {
		return nil, fmt.Errorf("domains request: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("domains endpoint returned %d: %s", status, snippet(body))
	}

	var entries []mailDomain
	if err := json.Unmarshal(body, &entries); err != nil {
		// The API can wrap collections in a hydra envelope.
		var envelope struct {
			Member []mailDomain `json:"hydra:member"`
		}
		if err2 := json.Unmarshal(body, &envelope); err2 != nil {
			return nil, fmt.Errorf("decode domains: %w", err)
		}
		entries = envelope.Member
	}

	all := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.Domain != "" && e.IsActive {
			all = append(all, e.Domain)
		}
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("no active mail.tm domains available")
	}

	liveDomainCache.domains = all
	liveDomainCache.fetchedAt = time.Now()
	return filterBlacklisted(all), nil
}

func filterBlacklisted(in []string) []string {
	out := make([]string, 0, len(in))
	for _, d := range in {
		if _, bad := blacklistedDomains.Load(d); !bad {
			out = append(out, d)
		}
	}
	return out
}

// NewMailbox provisions a mailbox. When domain is specified, it routes to the
// appropriate provider. When domain is empty, it chooses from available
// active domains on mail.tm (since TempMail.plus domains are blocked by OpenAI mail servers).
func NewMailbox(ctx context.Context, domain string) (*Mailbox, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if domain != "" {
		if IsTempMailPlusDomain(domain) {
			return NewTempMailPlusMailbox(domain)
		}
		return newMailTMMailbox(ctx, domain)
	}

	// Default to mail.tm because its MX reliably receives OpenAI verification emails
	return newMailTMMailbox(ctx, "")
}

func newMailTMMailbox(ctx context.Context, domain string) (*Mailbox, error) {
	if domain == "" {
		domains, err := LiveDomains(ctx)
		if err != nil {
			return nil, err
		}
		if len(domains) == 0 {
			return nil, fmt.Errorf("all mail.tm domains are blacklisted")
		}
		domain = domains[rand.Intn(len(domains))]
	}

	client, err := newHTTPClient()
	if err != nil {
		return nil, fmt.Errorf("tls client: %w", err)
	}

	address := localPart() + "@" + domain
	password := util.GeneratePassword(16)
	creds := map[string]string{"address": address, "password": password}

	status, body, err := doJSON(ctx, client, http.MethodPost, mailTMBase+"/accounts", "", creds)
	if err != nil {
		return nil, fmt.Errorf("create mailbox: %w", err)
	}
	if status != http.StatusCreated && status != http.StatusOK {
		return nil, fmt.Errorf("create mailbox returned %d: %s", status, snippet(body))
	}

	var acct mailAccount
	_ = json.Unmarshal(body, &acct)
	if acct.Address != "" {
		address = acct.Address
	}

	status, body, err = doJSON(ctx, client, http.MethodPost, mailTMBase+"/token", "", creds)
	if err != nil {
		return nil, fmt.Errorf("mailbox token: %w", err)
	}
	if status != http.StatusOK && status != http.StatusCreated {
		return nil, fmt.Errorf("mailbox token returned %d: %s", status, snippet(body))
	}

	var tok mailToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode token: %w", err)
	}
	if tok.Token == "" {
		return nil, fmt.Errorf("mail.tm returned an empty token")
	}

	return &Mailbox{
		Address:  address,
		provider: "mailtm",
		password: password,
		token:    tok.Token,
		client:   client,
		seen:     make(map[string]bool),
	}, nil
}

func localPart() string {
	return strings.ToLower(util.RandStr(14))
}

// WaitForCode polls the mailbox until a message yields a 6-digit code, the
// timeout elapses, or the context is cancelled. onMail is called once per new
// message so callers can surface progress.
func (m *Mailbox) WaitForCode(ctx context.Context, timeout time.Duration, onMail func(subject string)) (string, error) {
	if m == nil {
		return "", fmt.Errorf("mailbox not provisioned")
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	ticker := time.NewTicker(mailPollInterval)
	defer ticker.Stop()

	for {
		code, err := m.scanOnce(ctx, onMail)
		if err != nil && ctx.Err() != nil {
			return "", ctx.Err()
		}
		if code != "" {
			return code, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("no verification code within %s", timeout)
		case <-ticker.C:
		}
	}
}

// scanOnce fetches the message list and inspects anything new.
func (m *Mailbox) scanOnce(ctx context.Context, onMail func(subject string)) (string, error) {
	if m.provider == "tempmailplus" {
		return m.scanOnceTempMailPlus(ctx, onMail)
	}
	return m.scanOnceMailTM(ctx, onMail)
}

func (m *Mailbox) scanOnceMailTM(ctx context.Context, onMail func(subject string)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	status, body, err := doJSON(ctx, m.client, http.MethodGet, mailTMBase+"/messages?page=1", m.token, nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusUnauthorized {
		return "", fmt.Errorf("mailbox token rejected")
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("messages returned %d", status)
	}

	var list []mailMessageSummary
	if err := json.Unmarshal(body, &list); err != nil {
		var envelope struct {
			Member []mailMessageSummary `json:"hydra:member"`
		}
		if err2 := json.Unmarshal(body, &envelope); err2 != nil {
			return "", fmt.Errorf("decode messages: %w", err)
		}
		list = envelope.Member
	}

	for _, msg := range list {
		if msg.ID == "" {
			continue
		}
		m.mu.Lock()
		already := m.seen[msg.ID]
		m.seen[msg.ID] = true
		m.mu.Unlock()
		if already {
			continue
		}

		if onMail != nil {
			onMail(msg.Subject)
		}

		// Subject and preview are cheapest; fall back to the full body.
		if code := codeFrom(msg.Subject + " " + msg.Intro); code != "" {
			return code, nil
		}
		if code, err := m.codeFromMessage(ctx, msg.ID); err == nil && code != "" {
			return code, nil
		}
	}
	return "", nil
}

// codeFromMessage downloads one message and searches its body.
func (m *Mailbox) codeFromMessage(ctx context.Context, id string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	status, body, err := doJSON(ctx, m.client, http.MethodGet, mailTMBase+"/messages/"+id, m.token, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("message %s returned %d", id, status)
	}

	var full mailMessageFull
	if err := json.Unmarshal(body, &full); err != nil {
		// Fall back to scanning the raw payload.
		return codeFrom(string(body)), nil
	}

	if code := codeFrom(full.Text); code != "" {
		return code, nil
	}
	for _, h := range full.HTML {
		if code := codeFrom(h); code != "" {
			return code, nil
		}
	}
	return codeFrom(full.Subject + " " + full.Intro), nil
}

// Close is retained for symmetry with the previous provider. mail.tm mailboxes
// expire on their own, so nothing needs tearing down.
func (m *Mailbox) Close() {}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 180 {
		return s[:180]
	}
	return s
}
