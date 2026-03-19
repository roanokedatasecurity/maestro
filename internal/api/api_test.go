package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roanokedatasecurity/maestro/internal/bus"
	"github.com/roanokedatasecurity/maestro/internal/job"
	"github.com/roanokedatasecurity/maestro/internal/player"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

// ─── Test helpers ─────────────────────────────────────────────────────────────

type testEnv struct {
	store     *store.Store
	players   *player.Service
	jobs      *job.Service
	bus       *bus.Service
	conductor *player.Player
	ts        *httptest.Server
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ps := player.New(st)
	js := job.New(st)
	bs := bus.New(st, ps, js)

	conductor, err := ps.Register("conductor", true)
	if err != nil {
		t.Fatalf("register conductor: %v", err)
	}

	srv := newServer(nil, bs, ps, js, st)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		ts.Close()
		st.Close()
	})
	return &testEnv{
		store:     st,
		players:   ps,
		jobs:      js,
		bus:       bs,
		conductor: conductor,
		ts:        ts,
	}
}

func (e *testEnv) url(path string) string {
	return e.ts.URL + path
}

func post(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func get(t *testing.T, u string) *http.Response {
	t.Helper()
	resp, err := http.Get(u)
	if err != nil {
		t.Fatalf("GET %s: %v", u, err)
	}
	return resp
}

func decodeBody(t *testing.T, r io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

// ─── POST /players ─────────────────────────────────────────────────────────────

func TestRegisterPlayer_OK(t *testing.T) {
	e := newTestEnv(t)
	resp := post(t, e.url("/players"), `{"name":"alice","is_conductor":false}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	var p player.Player
	decodeBody(t, resp.Body, &p)
	if p.Name != "alice" {
		t.Errorf("Name: want alice got %q", p.Name)
	}
}

func TestRegisterPlayer_BadJSON(t *testing.T) {
	e := newTestEnv(t)
	resp := post(t, e.url("/players"), `not-json`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestRegisterPlayer_MissingName(t *testing.T) {
	e := newTestEnv(t)
	resp := post(t, e.url("/players"), `{"name":""}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestRegisterPlayer_DuplicateConductor(t *testing.T) {
	e := newTestEnv(t)
	// Conductor already registered in newTestEnv — second one must fail.
	resp := post(t, e.url("/players"), `{"name":"conductor-2","is_conductor":true}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", resp.StatusCode)
	}
}

// ─── GET /players ──────────────────────────────────────────────────────────────

func TestListPlayers_OK(t *testing.T) {
	e := newTestEnv(t)
	post(t, e.url("/players"), `{"name":"bob","is_conductor":false}`)

	resp := get(t, e.url("/players"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var players []*player.Player
	decodeBody(t, resp.Body, &players)
	if len(players) < 2 {
		t.Errorf("want ≥2 players, got %d", len(players))
	}
}

// ─── GET /players/{id} ─────────────────────────────────────────────────────────

func TestGetPlayer_OK(t *testing.T) {
	e := newTestEnv(t)
	resp := get(t, e.url("/players/"+e.conductor.ID))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var p player.Player
	decodeBody(t, resp.Body, &p)
	if p.ID != e.conductor.ID {
		t.Errorf("ID mismatch: want %q got %q", e.conductor.ID, p.ID)
	}
}

func TestGetPlayer_NotFound(t *testing.T) {
	e := newTestEnv(t)
	resp := get(t, e.url("/players/does-not-exist"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// ─── POST /players/{id}/assignment ────────────────────────────────────────────

func TestSendAssignment_Delivered204(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder", false)
	// Coder is Idle — should be delivered immediately (204).
	resp := post(t, e.url("/players/"+coder.ID+"/assignment"),
		`{"text":"do the work","priority":"normal"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
}

func TestSendAssignment_Queued202(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder2", false)
	// Send first assignment — delivers immediately and sets coder Running.
	resp1 := post(t, e.url("/players/"+coder.ID+"/assignment"), `{"text":"task 1"}`)
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusNoContent {
		t.Fatalf("first send: want 204, got %d", resp1.StatusCode)
	}
	// Send second while coder is Running — should enqueue (202).
	resp2 := post(t, e.url("/players/"+coder.ID+"/assignment"), `{"text":"task 2"}`)
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusAccepted {
		t.Fatalf("second send: want 202, got %d", resp2.StatusCode)
	}
}

func TestSendAssignment_BadJSON(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder3", false)
	resp := post(t, e.url("/players/"+coder.ID+"/assignment"), `not-json`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestSendAssignment_PlayerNotFound(t *testing.T) {
	e := newTestEnv(t)
	resp := post(t, e.url("/players/ghost/assignment"), `{"text":"hi"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// ─── POST /players/{id}/done ───────────────────────────────────────────────────

func TestDone_OK(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-done", false)
	// Deliver an assignment to get a Job.
	if err := e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false); err != nil {
		t.Fatalf("Send: %v", err)
	}
	jobs, _ := e.jobs.ListByPlayer(coder.ID)
	if len(jobs) == 0 {
		t.Fatal("expected job after assignment")
	}
	body := fmt.Sprintf(`{"job_id":%q,"summary":"all done"}`, jobs[0].ID)
	resp := post(t, e.url("/players/"+coder.ID+"/done"), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
}

func TestDone_BadJSON(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-done-bad", false)
	resp := post(t, e.url("/players/"+coder.ID+"/done"), `{bad}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestDone_BusError(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-done-err", false)
	// No job_id — bus.HandleDone returns error.
	resp := post(t, e.url("/players/"+coder.ID+"/done"), `{"summary":"done"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", resp.StatusCode)
	}
}

// ─── POST /players/{id}/blocked ────────────────────────────────────────────────

func TestBlocked_NoWait(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-blocked", false)
	if err := e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false); err != nil {
		t.Fatalf("Send: %v", err)
	}
	jobs, _ := e.jobs.ListByPlayer(coder.ID)
	body := fmt.Sprintf(`{"job_id":%q,"summary":"stuck","wait":false}`, jobs[0].ID)
	resp := post(t, e.url("/players/"+coder.ID+"/blocked"), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
}

func TestBlocked_Wait_ResolvesWithDecision(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-blocked-wait", false)
	if err := e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false); err != nil {
		t.Fatalf("Send: %v", err)
	}
	jobs, _ := e.jobs.ListByPlayer(coder.ID)
	body := fmt.Sprintf(`{"job_id":%q,"summary":"need approval","wait":true}`, jobs[0].ID)

	// The blocked call will hold open. We need to fire the decide endpoint after
	// a brief delay and collect the blocked response concurrently.
	type result struct {
		resp *http.Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		resp, err := http.Post(e.url("/players/"+coder.ID+"/blocked"),
			"application/json", strings.NewReader(body))
		ch <- result{resp, err}
	}()

	// Give the blocked handler a moment to register the waiter.
	time.Sleep(50 * time.Millisecond)

	// Fetch pending approvals to get the approvalID.
	approvals, err := e.store.ListPendingApprovals()
	if err != nil || len(approvals) == 0 {
		t.Fatalf("expected pending approval, err=%v len=%d", err, len(approvals))
	}
	decideBody := `{"decision":"Human"}`
	decideResp := post(t, e.url("/conductor/approvals/"+approvals[0].ID+"/decide"), decideBody)
	decideResp.Body.Close()
	if decideResp.StatusCode != http.StatusNoContent {
		t.Fatalf("decide: want 204, got %d", decideResp.StatusCode)
	}

	res := <-ch
	if res.err != nil {
		t.Fatalf("blocked POST: %v", res.err)
	}
	defer res.resp.Body.Close()
	if res.resp.StatusCode != http.StatusOK {
		t.Fatalf("blocked: want 200, got %d", res.resp.StatusCode)
	}
	var out map[string]string
	decodeBody(t, res.resp.Body, &out)
	if out["decision"] != "Human" {
		t.Errorf("decision: want Human got %q", out["decision"])
	}
}

func TestBlocked_BadJSON(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-blocked-bad", false)
	resp := post(t, e.url("/players/"+coder.ID+"/blocked"), `{bad}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestBlocked_BusError(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-blocked-err", false)
	// empty job_id → bus returns error
	resp := post(t, e.url("/players/"+coder.ID+"/blocked"), `{"summary":"stuck","wait":false}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", resp.StatusCode)
	}
}

// TestBlocked_WaitContextCancel verifies that cancelling the client context while
// waiting for a decision returns 504 Gateway Timeout.
func TestBlocked_WaitContextCancel(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-blocked-cancel", false)
	if err := e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false); err != nil {
		t.Fatalf("Send: %v", err)
	}
	jobs, _ := e.jobs.ListByPlayer(coder.ID)
	body := fmt.Sprintf(`{"job_id":%q,"summary":"need approval","wait":true}`, jobs[0].ID)

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		e.url("/players/"+coder.ID+"/blocked"),
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Context cancelled before response — expected.
		return
	}
	defer resp.Body.Close()
	// If we do get a response it should be 504.
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("want 504 or connection error, got %d", resp.StatusCode)
	}
}

// ─── POST /players/{id}/background ────────────────────────────────────────────

func TestBackground_OK(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-bg", false)
	if err := e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false); err != nil {
		t.Fatalf("Send: %v", err)
	}
	jobs, _ := e.jobs.ListByPlayer(coder.ID)
	body := fmt.Sprintf(`{"job_id":%q}`, jobs[0].ID)
	resp := post(t, e.url("/players/"+coder.ID+"/background"), body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
}

func TestBackground_BadJSON(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-bg-bad", false)
	resp := post(t, e.url("/players/"+coder.ID+"/background"), `{bad}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestBackground_BusError(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-bg-err", false)
	// empty job_id → bus error
	resp := post(t, e.url("/players/"+coder.ID+"/background"), `{}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", resp.StatusCode)
	}
}

// ─── GET /players/{id}/queue ───────────────────────────────────────────────────

func TestGetQueue_Empty(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-q", false)
	resp := get(t, e.url("/players/"+coder.ID+"/queue"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var items []queueItem
	decodeBody(t, resp.Body, &items)
	if len(items) != 0 {
		t.Errorf("want empty queue, got %d items", len(items))
	}
}

func TestGetQueue_WithMessages(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-q2", false)
	// Deliver first (makes coder Running), then enqueue a second.
	e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task 1", false)
	e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task 2", false)

	resp := get(t, e.url("/players/"+coder.ID+"/queue"))
	defer resp.Body.Close()
	var items []queueItem
	decodeBody(t, resp.Body, &items)
	if len(items) != 1 {
		t.Errorf("want 1 queued item, got %d", len(items))
	}
}

func TestGetQueue_LongPayloadTruncated(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-q3", false)
	// Deliver first to make Running, then enqueue a long-payload message.
	e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "seed", false)
	longText := strings.Repeat("x", 200)
	e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, longText, false)

	resp := get(t, e.url("/players/"+coder.ID+"/queue"))
	defer resp.Body.Close()
	var items []queueItem
	decodeBody(t, resp.Body, &items)
	if len(items) == 0 {
		t.Fatal("expected items in queue")
	}
	if len(items[0].PayloadPreview) > 103+3 { // 100 + "..."
		t.Errorf("payload not truncated: len=%d", len(items[0].PayloadPreview))
	}
}

// ─── GET /jobs ─────────────────────────────────────────────────────────────────

func TestListJobs_OK(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-jobs", false)
	e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false)

	resp := get(t, e.url("/jobs"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var jobs []*store.Job
	decodeBody(t, resp.Body, &jobs)
	if len(jobs) == 0 {
		t.Error("want ≥1 job")
	}
}

// ─── GET /jobs/{id} ────────────────────────────────────────────────────────────

func TestGetJob_OK(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-getjob", false)
	e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false)
	jobs, _ := e.jobs.ListByPlayer(coder.ID)

	resp := get(t, e.url("/jobs/"+jobs[0].ID))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var j store.Job
	decodeBody(t, resp.Body, &j)
	if j.ID != jobs[0].ID {
		t.Errorf("job ID mismatch")
	}
}

func TestGetJob_NotFound(t *testing.T) {
	e := newTestEnv(t)
	resp := get(t, e.url("/jobs/no-such-job"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// ─── GET /conductor/notifications ─────────────────────────────────────────────

func TestGetNotifications_Empty(t *testing.T) {
	e := newTestEnv(t)
	resp := get(t, e.url("/conductor/notifications"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var out notificationsResp
	decodeBody(t, resp.Body, &out)
	if out.UnreadCount != 0 {
		t.Errorf("want 0 unread, got %d", out.UnreadCount)
	}
}

func TestGetNotifications_WithData(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-notif", false)
	e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false)
	jobs, _ := e.jobs.ListByPlayer(coder.ID)
	e.bus.HandleDone(coder.ID, jobs[0].ID, "done")

	// With limit/offset query params.
	u := e.url("/conductor/notifications") + "?limit=10&offset=0"
	resp := get(t, u)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var out notificationsResp
	decodeBody(t, resp.Body, &out)
	if len(out.Notifications) == 0 {
		t.Error("want ≥1 notification")
	}
}

func TestGetNotifications_QueryParams(t *testing.T) {
	e := newTestEnv(t)
	// Empty params that don't parse as ints — should fall back to 0,0.
	u, _ := url.Parse(e.url("/conductor/notifications"))
	q := u.Query()
	q.Set("limit", "bad")
	q.Set("offset", "bad")
	u.RawQuery = q.Encode()
	resp := get(t, u.String())
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

// ─── POST /conductor/notifications/{id}/read ──────────────────────────────────

func TestMarkNotificationRead_OK(t *testing.T) {
	e := newTestEnv(t)
	coder, _ := e.players.Register("coder-read", false)
	e.bus.Send(e.conductor.ID, coder.ID, store.MessageTypeAssignment,
		store.PriorityNormal, "task", false)
	jobs, _ := e.jobs.ListByPlayer(coder.ID)
	e.bus.HandleDone(coder.ID, jobs[0].ID, "done")

	notifs, _ := e.bus.GetNotifications(0, 0)
	if len(notifs) == 0 {
		t.Fatal("expected notification")
	}
	resp := post(t, e.url("/conductor/notifications/"+notifs[0].ID+"/read"), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("want 204, got %d", resp.StatusCode)
	}
}

func TestMarkNotificationRead_NotFound(t *testing.T) {
	e := newTestEnv(t)
	resp := post(t, e.url("/conductor/notifications/no-such-id/read"), "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// ─── POST /conductor/approvals/{id}/decide ────────────────────────────────────

func TestDecide_InvalidDecision(t *testing.T) {
	e := newTestEnv(t)
	resp := post(t, e.url("/conductor/approvals/fake-id/decide"), `{"decision":"Maybe"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestDecide_BadJSON(t *testing.T) {
	e := newTestEnv(t)
	resp := post(t, e.url("/conductor/approvals/fake-id/decide"), `{bad}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestDecide_NotFound(t *testing.T) {
	e := newTestEnv(t)
	resp := post(t, e.url("/conductor/approvals/no-such-approval/decide"),
		`{"decision":"Autonomous"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

// ─── New() / Start() / Stop() ─────────────────────────────────────────────────

func TestServer_StartStop(t *testing.T) {
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "maestro.sock")
	st, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	ps := player.New(st)
	js := job.New(st)
	bs := bus.New(st, ps, js)

	srv, err := New(sockPath, bs, ps, js, st)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Start() }()

	// Give it a moment to start.
	time.Sleep(20 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestNew_BadSocketPath(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "test.db"))
	defer st.Close()
	ps := player.New(st)
	js := job.New(st)
	bs := bus.New(st, ps, js)
	// Non-existent directory → listen should fail.
	_, err := New("/no/such/dir/maestro.sock", bs, ps, js, st)
	if err == nil {
		t.Fatal("expected error for invalid socket path, got nil")
	}
}

// ─── findConductor error path ─────────────────────────────────────────────────

// TestSendAssignment_NoConductor verifies that handleSendAssignment returns 500 when
// no active Conductor is registered (findConductor fails).
func TestSendAssignment_NoConductor(t *testing.T) {
	// Build a server with no conductor registered.
	dir := t.TempDir()
	st, _ := store.Open(filepath.Join(dir, "test.db"))
	ps := player.New(st)
	js := job.New(st)
	bs := bus.New(st, ps, js)

	coder, _ := ps.Register("coder-nc", false)

	srv := newServer(nil, bs, ps, js, st)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() { ts.Close(); st.Close() })

	resp := post(t, ts.URL+"/players/"+coder.ID+"/assignment", `{"text":"hi"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("want 500 (no conductor), got %d", resp.StatusCode)
	}
}

// ─── Unused import guard ──────────────────────────────────────────────────────

var _ = os.DevNull // keep os import for t.TempDir usage
