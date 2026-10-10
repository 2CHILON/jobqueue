package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/2CHILON/jobqueue/internal/queue"
	"github.com/2CHILON/jobqueue/internal/store"
)

// Server holds the dependencies the handlers need.
type Server struct {
	store store.Store
}

func NewServer(s store.Store) *Server {
	return &Server{store: s}
}

// Routes wires URLs to handlers and returns the router.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("POST /jobs", s.handleCreate)
	mux.HandleFunc("GET /jobs", s.handleList)
	mux.HandleFunc("GET /jobs/{id}", s.handleGet)
	mux.HandleFunc("POST /jobs/claim", s.handleClaim)
	mux.HandleFunc("POST /jobs/{id}/complete", s.handleComplete)
	mux.HandleFunc("POST /jobs/{id}/fail", s.handleFail)
	return mux
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// decodeBody reads a JSON body, capped at 1 MB.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// writeStoreError translates store errors into HTTP status codes.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, store.ErrInvalidState):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

// ---- handlers ----

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createRequest struct {
	Type     string          `json:"type"`
	Payload  json.RawMessage `json:"payload"`
	Priority int             `json:"priority"`
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Type == "" {
		writeError(w, http.StatusBadRequest, "type is required")
		return
	}
	if len(req.Payload) == 0 {
		req.Payload = json.RawMessage(`{}`)
	}

	job := queue.New(req.Type, req.Payload, req.Priority)
	if err := s.store.Add(job); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, job)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	status := queue.Status(r.URL.Query().Get("status"))
	jobs, err := s.store.List(status)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// handleClaim gives the caller the next job, or 204 if the queue is empty.
func (s *Server) handleClaim(w http.ResponseWriter, r *http.Request) {
	job, err := s.store.Claim()
	if errors.Is(err, store.ErrNoJobs) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

type completeRequest struct {
	Result string `json:"result"`
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	var req completeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	job, err := s.store.Complete(r.PathValue("id"), req.Result)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}

type failRequest struct {
	Error string `json:"error"`
}

func (s *Server) handleFail(w http.ResponseWriter, r *http.Request) {
	var req failRequest
	if !decodeBody(w, r, &req) {
		return
	}
	job, err := s.store.Fail(r.PathValue("id"), req.Error)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, job)
}
