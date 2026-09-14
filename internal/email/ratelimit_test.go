package email

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestConcurrentMailboxProvisioningAvoidsRateLimit is the regression guard for
// the 429 that killed a worker on every batch start. Provisioning four
// mailboxes at once previously returned "create mailbox returned 429" because
// requests went out with no spacing and no retry.
func TestConcurrentMailboxProvisioningAvoidsRateLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("network test")
	}

	const workers = 4

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, workers)
	boxes := make([]*Mailbox, workers)
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // release together, as the job runner does
			box, err := NewMailbox(ctx, "")
			boxes[idx] = box
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("worker %d failed to provision a mailbox: %v", i, err)
			continue
		}
		if boxes[i] == nil || boxes[i].Address == "" {
			t.Errorf("worker %d got an empty mailbox", i)
			continue
		}
		boxes[i].Close()
	}

	// Addresses must be distinct, otherwise workers would fight over one inbox.
	seen := map[string]int{}
	for _, b := range boxes {
		if b != nil {
			seen[b.Address]++
		}
	}
	for addr, n := range seen {
		if n > 1 {
			t.Errorf("address %q provisioned %d times", addr, n)
		}
	}
}
