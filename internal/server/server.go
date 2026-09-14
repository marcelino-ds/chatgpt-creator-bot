// Package server provides the HTTP API and WebSocket server for the web UI.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"time"

	"github.com/coder/websocket"

	"github.com/verssache/chatgpt-creator/internal/job"
	"github.com/verssache/chatgpt-creator/internal/store"
	"github.com/verssache/chatgpt-creator/internal/webui"
)

type wsClient struct {
	conn *websocket.Conn
	send chan []byte
}

// Server is the HTTP + WebSocket server.
type Server struct {
	mgr   *job.JobManager
	store *store.Store
	addr  string

	mu      sync.Mutex
	clients map[*wsClient]struct{}

	mux *http.ServeMux
}

// New creates a server bound to addr.
func New(addr string, st *store.Store) *Server {
	s := &Server{
		store:   st,
		addr:    addr,
		clients: make(map[*wsClient]struct{}),
		mux:     http.NewServeMux(),
	}
	// The manager emits into the server so events are persisted and fanned
	// out to every connected client.
	s.mgr = job.NewManager(s.handleEvent)
	s.routes()
	return s
}

// handleEvent persists an event and broadcasts it to WebSocket clients.
func (s *Server) handleEvent(e job.Event) {
	switch e.Type {
	case job.EventLog:
		if err := s.store.AddLog(e.JobID, e.WorkerID, e.Tag, e.Step, e.StatusCode); err != nil {
			log.Printf("add log: %v", err)
		}
	case job.EventAccount:
		a := &store.Account{
			JobID:    e.JobID,
			Email:    e.Email,
			Password: e.Password,
			Success:  e.Success,
			Error:    e.Error,
		}
		if err := s.store.AddAccount(a); err != nil {
			log.Printf("add account: %v", err)
		}
	case job.EventStatus:
		if err := s.store.UpdateJobStatus(e.JobID, e.Status, e.SuccessCount, e.FailureCount, e.Attempts, e.ElapsedMS); err != nil {
			log.Printf("update job status: %v", err)
		}
	case job.EventComplete:
		if err := s.store.UpdateJobStatus(e.JobID, e.Status, e.SuccessCount, e.FailureCount, e.Attempts, e.ElapsedMS); err != nil {
			log.Printf("update job status: %v", err)
		}
	}
	s.broadcast(e)
}

// broadcast queues an event for every connected client. Writes happen on a
// single goroutine per connection so concurrent worker logs cannot collide.
func (s *Server) broadcast(e job.Event) {
	payload := mustJSON(e)
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.clients {
		select {
		case c.send <- payload:
		default:
			// Slow client: drop this frame rather than blocking the job.
		}
	}
}

func (s *Server) addClient(c *wsClient) {
	s.mu.Lock()
	s.clients[c] = struct{}{}
	s.mu.Unlock()
}

func (s *Server) removeClient(c *wsClient) {
	s.mu.Lock()
	if _, ok := s.clients[c]; ok {
		delete(s.clients, c)
		close(c.send)
	}
	s.mu.Unlock()
	_ = c.conn.Close(websocket.StatusNormalClosure, "removed")
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte(`{"error":"marshal failed"}`)
	}
	return b
}

// Mux returns the HTTP handler for mounting.
func (s *Server) Mux() http.Handler {
	return s.mux
}

func (s *Server) routes() {
	s.mux.Handle("/", webui.Handler())
	s.mux.HandleFunc("/ws", s.handleWebSocket)
	s.mux.HandleFunc("/api/jobs", s.handleJobs)         // POST (start), GET (list)
	s.mux.HandleFunc("/api/jobs/", s.handleJobAction)   // /{id}/pause|resume|stop
	s.mux.HandleFunc("/api/accounts", s.handleAccounts) // GET all accounts
	s.mux.HandleFunc("/api/status", s.handleSnapshot)
	s.mux.HandleFunc("/api/config", s.handleConfig)
}

// ListenAndServe starts the HTTP server.
func (s *Server) ListenAndServe() error {
	srv := &http.Server{
		Addr:    s.addr,
		Handler: s.mux,
	}
	log.Printf("web server listening on http://%s", s.addr)
	return srv.ListenAndServe()
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.startJob(w, r)
	case http.MethodGet:
		s.listJobs(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type startJobReq struct {
	Target   int    `json:"target"`
	Workers  int    `json:"workers"`
	Proxy    string `json:"proxy"`
	Domain   string `json:"domain"`
	Password string `json:"password"`
}

func (s *Server) startJob(w http.ResponseWriter, r *http.Request) {
	var req startJobReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}
	if req.Target <= 0 || req.Workers <= 0 {
		http.Error(w, "target and workers must be positive", http.StatusBadRequest)
		return
	}

	cfg := job.Config{
		Target:   req.Target,
		Workers:  req.Workers,
		Proxy:    req.Proxy,
		Domain:   req.Domain,
		Password: req.Password,
	}

	id, err := s.mgr.Start(cfg)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	if err := s.store.CreateJob(&store.Job{
		ID:      id,
		Target:  req.Target,
		Workers: req.Workers,
		Proxy:   req.Proxy,
		Domain:  req.Domain,
	}); err != nil {
		log.Printf("create job: %v", err)
	}

	_, status, target, attempts, success, failures, elapsed, _ := s.mgr.Snapshot()
	writeJSON(w, map[string]any{
		"job_id":     id,
		"status":     status,
		"target":     target,
		"attempts":   attempts,
		"success":    success,
		"failures":   failures,
		"elapsed_ms": elapsed,
	})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	jobs, err := s.store.ListJobs(limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, jobs)
}

func (s *Server) handleJobAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 1 && parts[0] != "" {
		s.getJob(w, r, parts[0])
		return
	}
	if len(parts) != 2 {
		http.Error(w, "bad path", http.StatusBadRequest)
		return
	}
	// action is what matters; id is ignored (manager handles one active job)
	_ = parts[0]
	action := parts[1]

	var err error
	switch action {
	case "pause":
		err = s.mgr.Pause()
	case "resume":
		err = s.mgr.Resume()
	case "stop":
		err = s.mgr.Stop()
	default:
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, map[string]any{"ok": true, "action": action})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request, id string) {
	j, err := s.store.GetJob(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, j)
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 100
	}
	var (
		accounts []*store.Account
		err      error
	)
	if jobID := r.URL.Query().Get("job_id"); jobID != "" {
		accounts, err = s.store.ListAccounts(jobID, limit)
	} else {
		accounts, err = s.store.AllAccounts(limit)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, accounts)
}

func (s *Server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	id, status, target, attempts, success, failures, elapsed, ok := s.mgr.Snapshot()
	if !ok {
		writeJSON(w, map[string]any{"active": false})
		return
	}
	writeJSON(w, map[string]any{
		"active":     true,
		"job_id":     id,
		"status":     status,
		"target":     target,
		"attempts":   attempts,
		"success":    success,
		"failures":   failures,
		"elapsed_ms": elapsed,
	})
}

type configResp struct {
	Address string `json:"address"`
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, configResp{Address: s.addr})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // allow self-signed/localhost dev
	})
	if err != nil {
		log.Printf("ws accept: %v", err)
		return
	}

	c := &wsClient{
		conn: conn,
		send: make(chan []byte, 256),
	}
	s.addClient(c)
	defer s.removeClient(c)

	go s.writePump(c)

	ctx := r.Context()
	for {
		_, _, err := conn.Read(ctx)
		if err != nil {
			return
		}
	}
}

func (s *Server) writePump(c *wsClient) {
	for payload := range c.send {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := c.conn.Write(ctx, websocket.MessageText, payload)
		cancel()
		if err != nil {
			log.Printf("ws write: %v", err)
			return
		}
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("writeJSON: %v", err)
	}
}
