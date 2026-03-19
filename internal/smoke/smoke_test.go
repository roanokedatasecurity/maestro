// Package smoke_test contains end-to-end smoke tests that wire all Maestro
// layers together and exercise the full IPC surface through the HTTP handler.
//
// Transport: uses httptest.NewServer backed by api.Server.Handler() — no real
// Unix socket is required for the test HTTP traffic (the socket is created by
// api.New but left idle).
package smoke_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roanokedatasecurity/maestro/internal/api"
	"github.com/roanokedatasecurity/maestro/internal/bus"
	"github.com/roanokedatasecurity/maestro/internal/job"
	"github.com/roanokedatasecurity/maestro/internal/player"
	"github.com/roanokedatasecurity/maestro/internal/store"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func mustPost(t *testing.T, url, body string) *http.Response {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func mustGet(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func mustDecodeJSON(t *testing.T, r io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Fatalf("decode JSON: %v", err)
	}
}

func assertStatus(t *testing.T, label string, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("%s: want %d, got %d (body: %s)", label, want, resp.StatusCode, body)
	}
}

func assert2xx(t *testing.T, label string, resp *http.Response) {
	t.Helper()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("%s: want 2xx, got %d (body: %s)", label, resp.StatusCode, body)
	}
}

// ─── notificationsResp mirrors the JSON returned by GET /conductor/notifications
type notificationsResp struct {
	Notifications []store.Notification `json:"notifications"`
	UnreadCount   int                  `json:"unread_count"`
}

// ─── smoke test ───────────────────────────────────────────────────────────────

// TestSmoke exercises the full Maestro IPC lifecycle:
//
//  1. Register conductor + coder players.
//  2. Send assignments; verify queue.
//  3. Signal done; verify notifications.
//  4. Signal blocked (wait=false); verify notification.
//  5. Signal blocked (wait=true); resolve via approval endpoint; verify round-trip.
func TestSmoke(t *testing.T) {
	// ── Setup: wire all layers ────────────────────────────────────────────────
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "maestro.db")

	// Unix socket: use a short /tmp path to stay well under the 104-char limit.
	sockPath := fmt.Sprintf("/tmp/maestro-smoke-%d.sock", os.Getpid())
	defer os.Remove(sockPath)

	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	ps := player.New(st)
	js := job.New(st)
	bs := bus.New(st, ps, js)

	srv, err := api.New(sockPath, bs, ps, js, st)
	if err != nil {
		t.Fatalf("api.New: %v", err)
	}

	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	base := ts.URL

	// ── Step 2: register conductor ────────────────────────────────────────────
	resp := mustPost(t, base+"/players", `{"name":"conductor","is_conductor":true}`)
	assertStatus(t, "register conductor", resp, http.StatusCreated)
	var conductor player.Player
	mustDecodeJSON(t, resp.Body, &conductor)
	resp.Body.Close()
	if conductor.ID == "" {
		t.Fatal("register conductor: ID is empty")
	}
	conductorID := conductor.ID
	t.Logf("step 2 PASS: conductorID=%s", conductorID)

	// ── Step 3: register coder ────────────────────────────────────────────────
	resp = mustPost(t, base+"/players", `{"name":"coder","is_conductor":false}`)
	assertStatus(t, "register coder", resp, http.StatusCreated)
	var coder player.Player
	mustDecodeJSON(t, resp.Body, &coder)
	resp.Body.Close()
	if coder.ID == "" {
		t.Fatal("register coder: ID is empty")
	}
	coderID := coder.ID
	t.Logf("step 3 PASS: coderID=%s", coderID)

	// ── Step 4: send first assignment (coder is Idle → delivered, 204) ────────
	//
	// Note: a delivered assignment is removed from the undelivered queue
	// immediately. To verify the queue endpoint (step 5) we send a second
	// assignment while coder is Running, which will be enqueued (202).
	resp = mustPost(t, base+"/players/"+coderID+"/assignment",
		`{"text":"hello from conductor"}`)
	assert2xx(t, "send first assignment", resp)
	resp.Body.Close()
	t.Logf("step 4 PASS: first assignment sent (status %d)", resp.StatusCode)

	// Retrieve job1 via the store (the assignment was delivered synchronously).
	job1List, err := st.ListJobsByPlayerAndStatus(coderID, store.JobStatusInProgress)
	if err != nil || len(job1List) == 0 {
		t.Fatalf("expected InProgress job for coder after first assignment, err=%v len=%d", err, len(job1List))
	}
	job1ID := job1List[0].ID
	t.Logf("  job1ID=%s", job1ID)

	// Send second assignment while coder is Running → enqueued (202).
	resp = mustPost(t, base+"/players/"+coderID+"/assignment",
		`{"text":"second task from conductor"}`)
	assertStatus(t, "send second assignment (queued)", resp, http.StatusAccepted)
	resp.Body.Close()

	// ── Step 5: GET queue → second assignment is present ─────────────────────
	resp = mustGet(t, base+"/players/"+coderID+"/queue")
	assertStatus(t, "get queue", resp, http.StatusOK)
	var queue []map[string]interface{}
	mustDecodeJSON(t, resp.Body, &queue)
	resp.Body.Close()
	if len(queue) == 0 {
		t.Fatal("step 5 FAIL: expected ≥1 message in queue, got 0")
	}
	t.Logf("step 5 PASS: queue has %d message(s)", len(queue))

	// ── Step 6: signal done for job1 ─────────────────────────────────────────
	resp = mustPost(t, base+"/players/"+coderID+"/done",
		fmt.Sprintf(`{"job_id":%q,"summary":"work complete"}`, job1ID))
	assert2xx(t, "done for job1", resp)
	resp.Body.Close()
	t.Logf("step 6 PASS: done signalled for job1")

	// After done, bus delivers the queued second assignment → job2 created.
	job2List, err := st.ListJobsByPlayerAndStatus(coderID, store.JobStatusInProgress)
	if err != nil || len(job2List) == 0 {
		t.Fatalf("expected InProgress job2 for coder after done, err=%v len=%d", err, len(job2List))
	}
	job2ID := job2List[0].ID
	t.Logf("  job2ID=%s", job2ID)

	// ── Step 7: GET /conductor/notifications → Done notification present ──────
	resp = mustGet(t, base+"/conductor/notifications")
	assertStatus(t, "get notifications (after done)", resp, http.StatusOK)
	var notifResp1 notificationsResp
	mustDecodeJSON(t, resp.Body, &notifResp1)
	resp.Body.Close()
	if len(notifResp1.Notifications) == 0 {
		t.Fatal("step 7 FAIL: expected ≥1 notification after done, got 0")
	}
	t.Logf("step 7 PASS: %d notification(s) present (unread_count=%d)",
		len(notifResp1.Notifications), notifResp1.UnreadCount)

	// ── Step 8: signal blocked (wait=false) for job2 ─────────────────────────
	resp = mustPost(t, base+"/players/"+coderID+"/blocked",
		fmt.Sprintf(`{"job_id":%q,"summary":"need a decision","scorecard":"{}","wait":false}`, job2ID))
	assert2xx(t, "blocked (wait=false)", resp)
	resp.Body.Close()
	t.Logf("step 8 PASS: blocked (wait=false) signalled for job2")

	// ── Step 9: GET notifications → blocked notification present ──────────────
	resp = mustGet(t, base+"/conductor/notifications")
	assertStatus(t, "get notifications (after blocked no-wait)", resp, http.StatusOK)
	var notifResp2 notificationsResp
	mustDecodeJSON(t, resp.Body, &notifResp2)
	resp.Body.Close()
	if len(notifResp2.Notifications) < 2 {
		t.Fatalf("step 9 FAIL: expected ≥2 notifications (done+blocked), got %d",
			len(notifResp2.Notifications))
	}
	t.Logf("step 9 PASS: %d notification(s) present after blocked no-wait", len(notifResp2.Notifications))

	// ── Steps 10–13: wait=true round-trip ────────────────────────────────────
	//
	// Step 10: fire the blocked (wait=true) call in a goroutine — it holds the
	// HTTP connection open until a decision is recorded.
	type blockedResult struct {
		resp *http.Response
		err  error
	}
	blockedCh := make(chan blockedResult, 1)
	go func() {
		r, err := http.Post(
			base+"/players/"+coderID+"/blocked",
			"application/json",
			strings.NewReader(fmt.Sprintf(
				`{"job_id":%q,"summary":"need approval","scorecard":"{}","wait":true}`, job2ID,
			)),
		)
		blockedCh <- blockedResult{r, err}
	}()

	// Give the handler time to call bus.HandleBlocked and register the waiter.
	time.Sleep(60 * time.Millisecond)

	// Step 11: GET /conductor/notifications → new notification for pending approval
	resp = mustGet(t, base+"/conductor/notifications")
	assertStatus(t, "get notifications (after blocked wait=true)", resp, http.StatusOK)
	var notifResp3 notificationsResp
	mustDecodeJSON(t, resp.Body, &notifResp3)
	resp.Body.Close()
	if len(notifResp3.Notifications) < 3 {
		t.Fatalf("step 11 FAIL: expected ≥3 notifications after blocked wait=true, got %d",
			len(notifResp3.Notifications))
	}
	t.Logf("step 11 PASS: %d notification(s) present", len(notifResp3.Notifications))

	// Retrieve the approvalID via the store (no HTTP endpoint to list approvals).
	approvals, err := st.ListPendingApprovals()
	if err != nil || len(approvals) == 0 {
		t.Fatalf("step 11 FAIL: expected pending approval, err=%v len=%d", err, len(approvals))
	}
	approvalID := approvals[0].ID
	t.Logf("  approvalID=%s", approvalID)

	// Step 12: record decision → releases the waiting goroutine.
	resp = mustPost(t, base+"/conductor/approvals/"+approvalID+"/decide",
		`{"decision":"Autonomous"}`)
	assertStatus(t, "decide approval", resp, http.StatusNoContent)
	resp.Body.Close()
	t.Logf("step 12 PASS: decision recorded (Autonomous)")

	// Step 13: assert the goroutine from step 10 returned 200 with correct decision.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	select {
	case res := <-blockedCh:
		if res.err != nil {
			t.Fatalf("step 13 FAIL: blocked POST error: %v", res.err)
		}
		defer res.resp.Body.Close()
		if res.resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(res.resp.Body)
			t.Fatalf("step 13 FAIL: blocked (wait=true) want 200, got %d (body: %s)",
				res.resp.StatusCode, body)
		}
		var decision map[string]string
		mustDecodeJSON(t, res.resp.Body, &decision)
		if decision["decision"] != "Autonomous" {
			t.Errorf("step 13: decision mismatch: want Autonomous got %q", decision["decision"])
		}
		t.Logf("step 13 PASS: blocked resolved with decision=%q approval_id=%q",
			decision["decision"], decision["approval_id"])
	case <-ctx.Done():
		t.Fatal("step 13 FAIL: timed out waiting for blocked goroutine to resolve")
	}

	t.Log("ALL SMOKE STEPS PASSED")
}

