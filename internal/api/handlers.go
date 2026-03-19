package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/roanokedatasecurity/maestro/internal/player"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

// ─── Player endpoints ──────────────────────────────────────────────────────────

type registerPlayerReq struct {
	Name        string `json:"name"`
	IsConductor bool   `json:"is_conductor"`
}

// handleRegisterPlayer handles POST /players.
func (s *Server) handleRegisterPlayer(w http.ResponseWriter, r *http.Request) {
	var req registerPlayerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		http.Error(w, "bad request: name is required", http.StatusBadRequest)
		return
	}
	p, err := s.players.Register(req.Name, req.IsConductor)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

// handleListPlayers handles GET /players.
func (s *Server) handleListPlayers(w http.ResponseWriter, r *http.Request) {
	players, err := s.players.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(players)
}

// handleGetPlayer handles GET /players/{id}.
func (s *Server) handleGetPlayer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	p, err := s.players.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(p)
}

type sendMessageReq struct {
	Text     string `json:"text"`
	Priority string `json:"priority"` // "normal" | "high"
}

// handleSendMessage handles POST /players/{id}/message.
// Returns 204 if the player was Idle (delivered immediately) or 202 if the
// player was busy (enqueued for later delivery).
func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req sendMessageReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, "bad request: text is required", http.StatusBadRequest)
		return
	}
	conductor, err := s.findConductor()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Snapshot player status before Send to determine 202 vs 204.
	target, err := s.players.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	priority := store.PriorityNormal
	if req.Priority == "high" {
		priority = store.PriorityHigh
	}
	if err := s.bus.Send(conductor.ID, id, store.MessageTypeAssignment, priority, req.Text, false); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if target.Status == player.StatusIdle {
		w.WriteHeader(http.StatusNoContent) // delivered immediately
	} else {
		w.WriteHeader(http.StatusAccepted) // enqueued
	}
}

type doneReq struct {
	JobID   string `json:"job_id"`
	Summary string `json:"summary"`
}

// handleDone handles POST /players/{id}/done.
func (s *Server) handleDone(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req doneReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.bus.HandleDone(id, req.JobID, req.Summary); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type blockedReq struct {
	JobID     string `json:"job_id"`
	Summary   string `json:"summary"`
	Scorecard string `json:"scorecard"`
	Wait      bool   `json:"wait"`
}

// handleBlocked handles POST /players/{id}/blocked.
// When wait=false returns 204 immediately. When wait=true holds the connection
// open until a decision is recorded via POST /conductor/approvals/{id}/decide,
// then returns 200 with the approvalID and decision.
func (s *Server) handleBlocked(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req blockedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	approvalID, err := s.bus.HandleBlocked(id, req.JobID, req.Summary, req.Scorecard, req.Wait)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if req.Wait && approvalID != "" {
		// Hold the connection open until a decision is reached or ctx is cancelled.
		decision, err := s.bus.WaitForDecision(r.Context(), approvalID)
		if err != nil {
			http.Error(w, "waiting for decision: "+err.Error(), http.StatusGatewayTimeout)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"approval_id": approvalID,
			"decision":    string(decision),
		})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type backgroundReq struct {
	JobID string `json:"job_id"`
}

// handleBackground handles POST /players/{id}/background.
func (s *Server) handleBackground(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req backgroundReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := s.bus.HandleBackground(id, req.JobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type queueItem struct {
	ID             string    `json:"id"`
	Type           string    `json:"type"`
	Priority       string    `json:"priority"`
	PayloadPreview string    `json:"payload_preview"`
	CreatedAt      time.Time `json:"created_at"`
	AgeSeconds     int       `json:"age_seconds"`
}

// handleGetQueue handles GET /players/{id}/queue.
func (s *Server) handleGetQueue(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	msgs, err := s.store.ListUndelivered(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	now := time.Now()
	items := make([]queueItem, 0, len(msgs))
	for _, m := range msgs {
		preview := m.Payload
		if len(preview) > 100 {
			preview = preview[:100] + "..."
		}
		items = append(items, queueItem{
			ID:             m.ID,
			Type:           string(m.Type),
			Priority:       string(m.Priority),
			PayloadPreview: preview,
			CreatedAt:      m.CreatedAt,
			AgeSeconds:     int(now.Sub(m.CreatedAt).Seconds()),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items)
}

// ─── Job endpoints ─────────────────────────────────────────────────────────────

// handleListJobs handles GET /jobs.
func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.jobs.List()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(jobs)
}

// handleGetJob handles GET /jobs/{id}.
func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	j, err := s.jobs.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(j)
}

// ─── Conductor endpoints ───────────────────────────────────────────────────────

type notificationsResp struct {
	Notifications []*store.Notification `json:"notifications"`
	UnreadCount   int                   `json:"unread_count"`
}

// handleGetNotifications handles GET /conductor/notifications.
// Query params: limit (int, 0=all), offset (int, 0=first page).
func (s *Server) handleGetNotifications(w http.ResponseWriter, r *http.Request) {
	var limit, offset int
	fmt.Sscanf(r.URL.Query().Get("limit"), "%d", &limit)
	fmt.Sscanf(r.URL.Query().Get("offset"), "%d", &offset)

	notifs, err := s.bus.GetNotifications(limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	count, err := s.bus.CountUnreadNotifications()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if notifs == nil {
		notifs = []*store.Notification{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(notificationsResp{
		Notifications: notifs,
		UnreadCount:   count,
	})
}

// handleMarkNotificationRead handles POST /conductor/notifications/{id}/read.
func (s *Server) handleMarkNotificationRead(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.bus.MarkNotificationRead(id); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type decideReq struct {
	Decision string `json:"decision"` // "Autonomous" | "Human"
}

// handleDecide handles POST /conductor/approvals/{id}/decide.
func (s *Server) handleDecide(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req decideReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	decision := store.ApprovalDecision(req.Decision)
	if decision != store.ApprovalDecisionAutonomous && decision != store.ApprovalDecisionHuman {
		http.Error(w, "invalid decision: must be Autonomous or Human", http.StatusBadRequest)
		return
	}
	if err := s.bus.RecordDecision(id, decision); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── Helpers ───────────────────────────────────────────────────────────────────

// findConductor returns the active (non-Dead) Conductor player, or an error if
// none is registered. Used by handleSendMessage to determine the from-player for
// Assignment routing.
func (s *Server) findConductor() (*store.Player, error) {
	players, err := s.store.ListPlayers()
	if err != nil {
		return nil, fmt.Errorf("find conductor: %w", err)
	}
	for _, p := range players {
		if p.IsConductor && p.Status != store.PlayerStatusDead {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no active Conductor")
}
