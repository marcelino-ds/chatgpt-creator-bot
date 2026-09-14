package email

import (
	"context"
	"strings"
	"testing"
	"time"
)

// These checks hit mail.tm directly. They document the contract the bot relies
// on, so a provider-side break fails loudly here instead of silently costing
// every OTP. Skipped with -short.

func TestLiveDomainsReturnsActiveDomains(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	domains, err := LiveDomains(ctx)
	if err != nil {
		t.Fatalf("LiveDomains: %v", err)
	}
	if len(domains) == 0 {
		t.Fatal("LiveDomains returned no domains")
	}
	for _, d := range domains {
		if !strings.Contains(d, ".") || strings.ContainsAny(d, " @/") {
			t.Errorf("implausible domain %q", d)
		}
	}
	t.Logf("active domains (%d): %v", len(domains), domains)
}

// TestNewMailboxIsReadable verifies that NewMailbox provisions a readable inbox.
func TestNewMailboxIsReadable(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	box, err := NewMailbox(ctx, "")
	if err != nil {
		t.Fatalf("NewMailbox: %v", err)
	}
	defer box.Close()

	if _, err := box.scanOnce(ctx, nil); err != nil {
		t.Fatalf("scanOnce on fresh mailbox: %v", err)
	}
	t.Logf("provisioned readable mailbox: %s (provider: %s)", box.Address, box.provider)
}

// TestTempMailPlusMailboxIsReadable verifies TempMail.plus inbox reading.
func TestTempMailPlusMailboxIsReadable(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	box, err := NewTempMailPlusMailbox("")
	if err != nil {
		t.Fatalf("NewTempMailPlusMailbox: %v", err)
	}
	defer box.Close()

	if _, err := box.scanOnce(ctx, nil); err != nil {
		t.Fatalf("scanOnce on TempMail.plus mailbox: %v", err)
	}
	t.Logf("provisioned TempMail.plus mailbox: %s", box.Address)
}

// TestMailTMMailboxIsReadable verifies mail.tm inbox provisioning and reading.
func TestMailTMMailboxIsReadable(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	box, err := newMailTMMailbox(ctx, "")
	if err != nil {
		t.Fatalf("newMailTMMailbox: %v", err)
	}
	defer box.Close()

	at := strings.LastIndex(box.Address, "@")
	if at < 1 {
		t.Fatalf("malformed address %q", box.Address)
	}

	domains, err := LiveDomains(ctx)
	if err != nil {
		t.Fatalf("LiveDomains: %v", err)
	}
	active := make(map[string]bool, len(domains))
	for _, d := range domains {
		active[d] = true
	}
	if !active[box.Address[at+1:]] {
		t.Errorf("mailbox %q uses a domain that is not active", box.Address)
	}

	if _, err := box.scanOnce(ctx, nil); err != nil {
		t.Fatalf("scanOnce on mail.tm mailbox: %v", err)
	}
	t.Logf("provisioned mail.tm mailbox: %s", box.Address)
}

// TestWaitForCodeTimesOutCleanly verifies the wait path reports a timeout
// instead of hanging, which is what made the old flow look frozen.
func TestWaitForCodeTimesOutCleanly(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	box, err := NewMailbox(ctx, "")
	if err != nil {
		t.Fatalf("NewMailbox: %v", err)
	}
	defer box.Close()

	code, err := box.WaitForCode(ctx, 6*time.Second, nil)
	if err == nil {
		t.Logf("unexpected but harmless: received code %q", code)
		return
	}
	if !strings.Contains(err.Error(), "no verification code within") {
		t.Fatalf("expected a timeout error, got: %v", err)
	}
}

func TestCodeFromSkipsKnownBadConstant(t *testing.T) {
	if got := codeFrom("Your code is 482913"); got != "482913" {
		t.Errorf("codeFrom = %q, want 482913", got)
	}
	if got := codeFrom("ref 177010 then 553311"); got != "553311" {
		t.Errorf("codeFrom = %q, want 553311 (177010 must be skipped)", got)
	}
	if got := codeFrom("no digits here"); got != "" {
		t.Errorf("codeFrom = %q, want empty", got)
	}
}
