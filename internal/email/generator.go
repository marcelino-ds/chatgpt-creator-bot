package email

import (
	"context"
	"encoding/json"
	"os"
	"sync"
)

var (
	blacklistedDomains sync.Map
	blacklistMutex     sync.Mutex
)

func init() {
	data, err := os.ReadFile("blacklist.json")
	if err != nil {
		return // File might not exist yet
	}

	var domains []string
	if err := json.Unmarshal(data, &domains); err != nil {
		return
	}

	for _, domain := range domains {
		blacklistedDomains.Store(domain, true)
	}
}

func saveBlacklist() {
	blacklistMutex.Lock()
	defer blacklistMutex.Unlock()

	var domains []string
	blacklistedDomains.Range(func(key, value any) bool {
		if domain, ok := key.(string); ok {
			domains = append(domains, domain)
		}
		return true
	})

	data, err := json.MarshalIndent(domains, "", "  ")
	if err != nil {
		return
	}

	_ = os.WriteFile("blacklist.json", data, 0644)
}

// AddBlacklistDomain adds a domain to the global blacklist.
func AddBlacklistDomain(domain string) {
	blacklistedDomains.Store(domain, true)
	saveBlacklist()
}

// CreateTempEmail provisions a mailbox and returns only its address.
//
// Callers that need to read the OTP should use NewMailbox instead: the mailbox
// requires an authenticated token, so the address alone is not enough to fetch
// mail.
func CreateTempEmail(defaultDomain string) (string, error) {
	box, err := NewMailbox(context.Background(), defaultDomain)
	if err != nil {
		return "", err
	}
	return box.Address, nil
}
