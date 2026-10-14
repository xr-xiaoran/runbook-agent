package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLiveSnapshotAndApprovalIsolation(t *testing.T) {
	calls := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "operations") {
			http.Error(w, "not found", 404)
			return
		}
		if r.Method == "GET" {
			if r.Header.Get("Authorization") != "Bearer read-key-123456789" {
				t.Error("wrong read credential")
			}
			json.NewEncoder(w).Encode(LiveSnapshot{DrawID: 9, Status: "dead", Version: 5, Attempt: 5, FailFirst: 5, ObservedAt: time.Now().UnixMilli()})
			return
		}
		calls++
		if r.Header.Get("Authorization") != "Bearer replay-key-1234567" || r.Header.Get("Idempotency-Key") == "" {
			t.Error("bad approval request")
		}
		json.NewEncoder(w).Encode(ReplayReceipt{OperationID: r.Header.Get("Idempotency-Key"), DrawID: 9, ExpectedVersion: 5, NewVersion: 6, Status: "accepted"})
	}))
	defer backend.Close()
	c, e := NewCampusClient(backend.URL, "read-key-123456789", "replay-key-1234567")
	if e != nil {
		t.Fatal(e)
	}
	s := NewServer(&Runtime{LoadIndex(), Demo{}}, testStore(t), "agent-reader-1234567", "agent-approval-12345")
	s.Campus = c
	call := func(path, key, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+key)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w
	}
	w := call("/api/runs", "agent-reader-1234567", `{"question":"CampusReward 发奖 dead","draw_id":9}`)
	var result Result
	json.Unmarshal(w.Body.Bytes(), &result)
	if w.Code != 200 || result.Replay == nil || result.Live == nil || result.Live.DrawID != 9 || calls != 0 {
		t.Fatal(w.Body.String())
	}
	path := "/api/runs/" + result.ID + "/replay/approve"
	if w = call(path, "agent-reader-1234567", `{"confirm":true}`); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w = call(path, "agent-approval-12345", `{"confirm":false}`); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if calls != 0 {
		t.Fatal("unapproved write")
	}
	if w = call(path, "agent-approval-12345", `{"confirm":true}`); w.Code != 200 || calls != 1 {
		t.Fatal(w.Body.String(), calls)
	}
}
func TestCampusURLRestriction(t *testing.T) {
	for _, u := range []string{"http://example.com", "http://localhost", "http://127.0.0.1/path", "http://127.0.0.1?key=x", "http://user@127.0.0.1"} {
		if _, e := NewCampusClient(u, "reader-key-12345678", "replay-key-12345678"); e == nil {
			t.Fatal(u)
		}
	}
}
func TestProposalExpiredAndReceiptRecovery(t *testing.T) {
	found := false
	writes := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			writes++
		}
		if !found {
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(ReplayReceipt{OperationID: "expired-run", Status: "accepted"})
	}))
	defer backend.Close()
	c, _ := NewCampusClient(backend.URL, "reader-key-1234567", "replay-key-1234567")
	s := NewServer(&Runtime{LoadIndex(), Demo{}}, testStore(t), "reader-key-1234567", "approval-key-123456")
	s.Campus = c
	s.Store.Save(context.Background(), "test", Result{ID: "expired-run", Status: "completed", Replay: &ReplayProposal{DrawID: 1, ExpectedVersion: 5, ExpiresAt: time.Now().Add(-time.Minute).UnixMilli()}})
	if _, e := s.ApproveReplay(context.Background(), "expired-run"); e == nil {
		t.Fatal("expected expiry")
	}
	found = true
	if r, e := s.ApproveReplay(context.Background(), "expired-run"); e != nil || r.Status != "accepted" {
		t.Fatal(r, e)
	}
	if writes != 0 {
		t.Fatal("unexpected replay")
	}
}
