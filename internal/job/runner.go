package job

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/verssache/chatgpt-creator/internal/email"
	"github.com/verssache/chatgpt-creator/internal/register"
	"github.com/verssache/chatgpt-creator/internal/util"
)

// Config is the parameters for a new job.
type Config struct {
	Target   int
	Workers  int
	Proxy    string
	Domain   string
	Password string
}

// jobState holds the runtime state of an active batch.
type jobState struct {
	id           string
	target       int
	workers      int
	status       string // running, paused, stopping, completed, stopped, failed
	successCount int
	failureCount int
	attempts     int
	elapsedMS    int64
	startedAt    time.Time
	done         chan struct{}
	ctx          context.Context
	cancel       context.CancelFunc
	paused       atomic.Bool
}

// Start launches a new batch registration job and returns its id.
func (m *JobManager) Start(cfg Config) (string, error) {
	m.mu.Lock()
	if m.current != nil && (m.current.status == "running" || m.current.status == "paused" || m.current.status == "stopping") {
		m.mu.Unlock()
		return "", fmt.Errorf("a job is already active")
	}

	ctx, cancel := context.WithCancel(context.Background())
	id := uuid.New().String()
	state := &jobState{
		id:        id,
		target:    cfg.Target,
		workers:   cfg.Workers,
		status:    "running",
		done:      make(chan struct{}),
		ctx:       ctx,
		cancel:    cancel,
		startedAt: time.Now(),
	}
	m.current = state
	m.mu.Unlock()

	m.statusEvent(id, "running", cfg.Target, 0, 0, 0, 0)
	go m.run(state, cfg)
	return id, nil
}

// Pause requests the active job to pause. Workers finish their current
// registration before blocking.
func (m *JobManager) Pause() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return fmt.Errorf("no active job")
	}
	if m.current.status != "running" {
		return fmt.Errorf("job is not running")
	}
	m.current.status = "paused"
	m.current.paused.Store(true)
	return nil
}

// Resume unpauses a paused job.
func (m *JobManager) Resume() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return fmt.Errorf("no active job")
	}
	if m.current.status != "paused" {
		return fmt.Errorf("job is not paused")
	}
	m.current.status = "running"
	m.current.paused.Store(false)
	return nil
}

// Stop cancels the active job. In-flight HTTP may finish the current
// request, but OTP polling and queued attempts abort.
func (m *JobManager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return fmt.Errorf("no active job")
	}
	if m.current.status != "running" && m.current.status != "paused" && m.current.status != "stopping" {
		return fmt.Errorf("job is not active")
	}
	if m.current.status == "stopping" {
		return nil
	}
	m.current.status = "stopping"
	m.current.paused.Store(false)
	m.current.cancel()
	return nil
}

// Snapshot returns a read-only view of the current job.
func (m *JobManager) Snapshot() (id, status string, target, attempts, success, failures int, elapsedMS int64, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return
	}
	s := m.current
	return s.id, s.status, s.target, s.attempts, s.successCount, s.failureCount, atomic.LoadInt64(&s.elapsedMS), true
}

func (s *jobState) waitIfPaused() bool {
	for s.paused.Load() {
		if s.ctx.Err() != nil {
			return false
		}
		time.Sleep(80 * time.Millisecond)
	}
	return s.ctx.Err() == nil
}

// run orchestrates worker goroutines until target, cancellation, or stop.
func (m *JobManager) run(s *jobState, cfg Config) {
	defer close(s.done)
	defer s.cancel()

	var (
		remaining    int64 = int64(s.target)
		successCount int64
		failureCount int64
		attempts     int64
	)

	var wg sync.WaitGroup
	for w := 1; w <= s.workers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for {
				if !s.waitIfPaused() {
					return
				}
				if s.ctx.Err() != nil {
					return
				}

				if atomic.AddInt64(&remaining, -1) < 0 {
					atomic.AddInt64(&remaining, 1)
					return
				}

				if s.ctx.Err() != nil {
					atomic.AddInt64(&remaining, 1)
					return
				}

				attempt := atomic.AddInt64(&attempts, 1)
				tag := fmt.Sprintf("%d/%d", attempt, s.target)

				done, emailAddr, password, errMsg := m.registerOne(s.ctx, s.id, workerID, tag, cfg.Proxy, cfg.Domain, cfg.Password)

				if s.ctx.Err() != nil {
					if errMsg == "" {
						errMsg = "stopped"
					}
					count := atomic.AddInt64(&failureCount, 1)
					m.accountEvent(s.id, emailAddr, password, false, errMsg)
					m.progressEvent(s.id, s.target, int(attempt), int(atomic.LoadInt64(&successCount)), int(count))
					return
				}

				if done {
					count := atomic.AddInt64(&successCount, 1)
					m.accountEvent(s.id, emailAddr, password, true, "")
					m.progressEvent(s.id, s.target, int(attempt), int(count), int(atomic.LoadInt64(&failureCount)))
					if count >= int64(s.target) {
						atomic.AddInt64(&remaining, 1)
						return
					}
				} else {
					count := atomic.AddInt64(&failureCount, 1)
					if strings.Contains(errMsg, "unsupported_email") || strings.Contains(errMsg, "registration_disallowed") {
						parts := strings.Split(emailAddr, "@")
						if len(parts) == 2 {
							email.AddBlacklistDomain(parts[1])
						}
					}
					m.accountEvent(s.id, emailAddr, password, false, errMsg)
					m.progressEvent(s.id, s.target, int(attempt), int(atomic.LoadInt64(&successCount)), int(count))
				}
			}
		}(w)
	}

	wg.Wait()

	elapsed := time.Since(s.startedAt).Milliseconds()
	finalStatus := "completed"
	if s.ctx.Err() != nil {
		finalStatus = "stopped"
	}

	m.mu.Lock()
	s.successCount = int(atomic.LoadInt64(&successCount))
	s.failureCount = int(atomic.LoadInt64(&failureCount))
	s.attempts = int(atomic.LoadInt64(&attempts))
	s.elapsedMS = elapsed
	s.status = finalStatus
	m.mu.Unlock()

	m.statusEvent(s.id, finalStatus, s.target, int(atomic.LoadInt64(&attempts)), int(atomic.LoadInt64(&successCount)), int(atomic.LoadInt64(&failureCount)), elapsed)
	m.emit(Event{
		Type:         EventComplete,
		JobID:        s.id,
		Timestamp:    time.Now(),
		Status:       finalStatus,
		Target:       s.target,
		Attempts:     int(atomic.LoadInt64(&attempts)),
		SuccessCount: int(atomic.LoadInt64(&successCount)),
		FailureCount: int(atomic.LoadInt64(&failureCount)),
		ElapsedMS:    elapsed,
	})
}

func (m *JobManager) registerOne(ctx context.Context, jobID string, workerID int, tag, proxy, domain, password string) (success bool, emailAddr, pass, errMsg string) {
	if ctx.Err() != nil {
		return false, "", "", "stopped"
	}

	var printMu, fileMu sync.Mutex
	client, err := register.NewClient(proxy, tag, workerID, &printMu, &fileMu)
	if err != nil {
		return false, "", "", fmt.Sprintf("client: %v", err)
	}
	client.SetContext(ctx)

	client.SetLogFn(func(wid int, t, msg string) {
		sc := 0
		step := msg
		if idx := findStatusSep(msg); idx > 0 {
			step = msg[:idx]
			_, _ = fmt.Sscanf(msg[idx:], " | %d", &sc)
		}
		m.logEvent(jobID, wid, t, step, sc, "")
	})

	// The mailbox is provisioned up front because reading the OTP needs the
	// provider token, not just the address.
	mailbox, err := email.NewMailbox(ctx, domain)
	if err != nil {
		return false, "", "", fmt.Sprintf("mailbox: %v", err)
	}
	defer mailbox.Close()

	addr := mailbox.Address
	client.SetMailbox(mailbox)

	pass = password
	if pass == "" {
		pass = util.GeneratePassword(14)
	}

	firstName, lastName := util.RandomName()
	birthdate := util.RandomBirthdate()

	err = client.RunRegister(addr, pass, firstName+" "+lastName, birthdate)
	if err != nil {
		return false, addr, pass, err.Error()
	}
	return true, addr, pass, ""
}

func findStatusSep(s string) int {
	for i := len(s) - 1; i >= 2; i-- {
		if s[i-2] == ' ' && s[i-1] == '|' && s[i] == ' ' {
			return i - 2
		}
	}
	return -1
}
