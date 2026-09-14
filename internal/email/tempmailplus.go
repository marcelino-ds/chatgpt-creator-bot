package email

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/url"
	"strconv"
	"strings"

	fhttp "github.com/bogdanfinn/fhttp"
)

const tempMailPlusBase = "https://tempmail.plus/api"

// TempMailPlusDomains lists public domains supported by TempMail.plus
var TempMailPlusDomains = []string{
	"mailto.plus",
	"fexpost.com",
	"fexbox.org",
	"mailbox.in.ua",
	"rover.info",
	"chitthi.in",
	"fextemp.com",
	"any.pink",
	"merepost.com",
}

// IsTempMailPlusDomain checks if a domain belongs to TempMail.plus
func IsTempMailPlusDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	for _, d := range TempMailPlusDomains {
		if d == domain {
			return true
		}
	}
	return false
}

// NewTempMailPlusMailbox creates a new mailbox for TempMail.plus.
// If domain is empty, a random non-blacklisted TempMail.plus domain is chosen.
func NewTempMailPlusMailbox(domain string) (*Mailbox, error) {
	if domain == "" {
		available := filterBlacklisted(TempMailPlusDomains)
		if len(available) == 0 {
			return nil, fmt.Errorf("all tempmail.plus domains are blacklisted")
		}
		domain = available[rand.Intn(len(available))]
	}

	client, err := newHTTPClient()
	if err != nil {
		return nil, fmt.Errorf("tls client: %w", err)
	}

	address := localPart() + "@" + domain
	return &Mailbox{
		Address:  address,
		provider: "tempmailplus",
		client:   client,
		seen:     make(map[string]bool),
	}, nil
}

type tempMailPlusListResp struct {
	Result   bool                         `json:"result"`
	Count    int                          `json:"count"`
	FirstID  int64                        `json:"first_id"`
	LastID   int64                        `json:"last_id"`
	MailList []tempMailPlusMessageSummary `json:"mail_list"`
}

type tempMailPlusMessageSummary struct {
	MailID   int64  `json:"mail_id"`
	FromMail string `json:"from_mail"`
	FromName string `json:"from_name"`
	Subject  string `json:"subject"`
	Time     string `json:"time"`
	IsNew    bool   `json:"is_new"`
}

type tempMailPlusDetailResp struct {
	Result  bool   `json:"result"`
	MailID  int64  `json:"mail_id"`
	From    string `json:"from"`
	To      string `json:"to"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

func (m *Mailbox) scanOnceTempMailPlus(ctx context.Context, onMail func(subject string)) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	u := fmt.Sprintf("%s/mails?email=%s&limit=10&epin=", tempMailPlusBase, url.QueryEscape(m.Address))
	req, err := fhttp.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("tempmail.plus list returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var listResp tempMailPlusListResp
	if err := json.Unmarshal(body, &listResp); err != nil {
		return "", err
	}

	for _, item := range listResp.MailList {
		idStr := strconv.FormatInt(item.MailID, 10)
		m.mu.Lock()
		already := m.seen[idStr]
		m.seen[idStr] = true
		m.mu.Unlock()

		if already {
			continue
		}

		if onMail != nil {
			onMail(item.Subject)
		}

		if code := codeFrom(item.Subject); code != "" {
			return code, nil
		}

		code, err := m.codeFromTempMailPlusMessage(ctx, item.MailID)
		if err == nil && code != "" {
			return code, nil
		}
	}

	return "", nil
}

func (m *Mailbox) codeFromTempMailPlusMessage(ctx context.Context, mailID int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	u := fmt.Sprintf("%s/mails/%d?email=%s&epin=", tempMailPlusBase, mailID, url.QueryEscape(m.Address))
	req, err := fhttp.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := m.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("tempmail.plus message returned %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var detail tempMailPlusDetailResp
	if err := json.Unmarshal(body, &detail); err != nil {
		return codeFrom(string(body)), nil
	}

	if code := codeFrom(detail.Subject); code != "" {
		return code, nil
	}
	if code := codeFrom(detail.Text); code != "" {
		return code, nil
	}
	if code := codeFrom(detail.HTML); code != "" {
		return code, nil
	}

	return codeFrom(string(body)), nil
}
