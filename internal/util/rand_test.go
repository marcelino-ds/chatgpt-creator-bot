package util

import (
	"sync"
	"testing"
)

// TestRandStrConcurrentUnique guards against the seed-collision bug where each
// call built its own source from time.Now(). On Windows the clock resolution is
// coarser than goroutine start times, so workers launched together produced
// identical suffixes (and therefore duplicate emails).
func TestRandStrConcurrentUnique(t *testing.T) {
	const workers = 64

	var wg sync.WaitGroup
	out := make([]string, workers)
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start // release together to maximise collision pressure
			out[idx] = RandStr(5)
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[string]int, workers)
	for _, s := range out {
		seen[s]++
	}
	for value, count := range seen {
		if count > 1 {
			t.Errorf("RandStr produced %q %d times across %d concurrent calls", value, count, workers)
		}
	}
}

func TestGeneratePasswordConcurrentUnique(t *testing.T) {
	const workers = 64

	var wg sync.WaitGroup
	out := make([]string, workers)
	start := make(chan struct{})

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			out[idx] = GeneratePassword(14)
		}(i)
	}
	close(start)
	wg.Wait()

	seen := make(map[string]int, workers)
	for _, s := range out {
		if len(s) != 14 {
			t.Fatalf("password length = %d, want 14", len(s))
		}
		seen[s]++
	}
	for value, count := range seen {
		if count > 1 {
			t.Errorf("GeneratePassword repeated a value %d times across %d concurrent calls (%q)", count, workers, value)
		}
	}
}
