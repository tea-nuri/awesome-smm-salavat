package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a := &App{dir: t.TempDir(), aiDir: t.TempDir(), admins: map[int64]bool{1: true}, state: State{Companies: map[string]*Company{}, Posts: map[string]*Post{}, History: map[string][]Message{}, Schedules: map[string]*Schedule{}, Active: map[string]string{}, Updates: map[string]bool{}}}
	a.state.Companies["a"] = &Company{ID: "a", Name: "A", OwnerID: 1, Channel: "@test"}
	a.state.Companies["b"] = &Company{ID: "b", Name: "B", OwnerID: 2}
	return a
}
func TestApprovalBoundToRevision(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "test", 1, 1, nil)
	p.Status = "review"
	if _, e := a.action(a.user(1), p.ID, "approve", 1, "", "", ""); e != nil {
		t.Fatal(e)
	}
	if p.ApprovedRevision != 1 || p.Status != "approved" {
		t.Fatal("approval missing")
	}
	if _, e := a.action(a.user(1), p.ID, "edit", 1, "", "Updated", "New body"); e != nil {
		t.Fatal(e)
	}
	if p.Status != "review" || p.ApprovedRevision != 0 || p.Revision != 2 {
		t.Fatal("edit must invalidate approval")
	}
	if _, e := a.action(a.user(1), p.ID, "approve", 1, "", "", ""); e == nil {
		t.Fatal("stale approval accepted")
	}
}
func TestCrossTenantActionsRejected(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "secret", 1, 1, nil)
	p.Status = "review"
	if _, e := a.action(a.user(2), p.ID, "approve", 1, "", "", ""); e == nil {
		t.Fatal("cross-tenant approval accepted")
	}
	if a.allowed(a.user(2), a.state.Companies["a"]) {
		t.Fatal("cross-tenant access")
	}
}
func TestRejectCannotPublish(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "test", 1, 1, nil)
	p.Status = "review"
	if _, e := a.action(a.user(1), p.ID, "reject", 1, "", "", ""); e != nil {
		t.Fatal(e)
	}
	a.tick()
	if p.Status != "rejected" || len(a.state.Outbox) != 1 || a.state.Outbox[0].Kind == "publish" {
		t.Fatal("rejected post entered publication queue")
	}
}
func TestApprovalQueuesOnlyOnce(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "test", 1, 1, nil)
	p.Status = "approved"
	p.ApprovedRevision = 1
	a.tick()
	a.tick()
	if p.Status != "publishing" || len(a.state.Outbox) != 1 || a.state.Outbox[0].Kind != "publish" {
		t.Fatal("duplicate publication or missing queue")
	}
}
func TestRestartPreservesStateAndDoesNotRetryUncertainPublication(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "test", 1, 1, nil)
	p.Status = "publishing"
	p.ApprovedRevision = 1
	a.state.Outbox = append(a.state.Outbox, &Outbox{ID: "one", Kind: "publish", PostID: p.ID})
	if e := a.save(); e != nil {
		t.Fatal(e)
	}
	b := &App{dir: a.dir, aiDir: a.aiDir, admins: a.admins}
	if e := b.load(); e != nil {
		t.Fatal(e)
	}
	if b.state.Posts[p.ID].Status != "uncertain" || len(b.state.Outbox) != 0 {
		t.Fatal("publication retried after restart")
	}
	if len(b.state.Companies) != 2 {
		t.Fatal("lost companies")
	}
	if _, e := os.Stat(filepath.Join(a.dir, "state.json")); e != nil {
		t.Fatal(e)
	}
}
func signInit(v url.Values, token string) string {
	rows := []string{}
	for k := range v {
		rows = append(rows, k+"="+v.Get(k))
	}
	sort.Strings(rows)
	key := hmac.New(sha256.New, []byte("WebAppData"))
	key.Write([]byte(token))
	mac := hmac.New(sha256.New, key.Sum(nil))
	mac.Write([]byte(strings.Join(rows, "\n")))
	v.Set("hash", hex.EncodeToString(mac.Sum(nil)))
	return v.Encode()
}
func TestTelegramAuthSignatureExpiryAndTampering(t *testing.T) {
	v := url.Values{"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)}, "user": {`{"id":1,"first_name":"Admin"}`}, "signature": {"telegram-ed25519-signature"}}
	raw := signInit(v, "test-token")
	u, e := validateInit(raw, "test-token")
	if e != nil || u.ID != 1 {
		t.Fatalf("valid data rejected: %v", e)
	}
	if _, e = validateInit(strings.Replace(raw, "Admin", "Other", 1), "test-token"); e == nil {
		t.Fatal("tampered data accepted")
	}
	v.Del("hash")
	v.Set("auth_date", "1")
	if _, e = validateInit(signInit(v, "test-token"), "test-token"); e == nil {
		t.Fatal("expired data accepted")
	}
}
func TestLocalAuthNeverExposedThroughTunnel(t *testing.T) {
	r := httptest.NewRequest("GET", "https://public.example/api/local-session", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	if localRequest(r) {
		t.Fatal("public host accepted")
	}
	r.Host = "localhost:8080"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	if localRequest(r) {
		t.Fatal("forwarded traffic accepted")
	}
	r.Header.Del("X-Forwarded-For")
	if !localRequest(r) {
		t.Fatal("loopback rejected")
	}
}

func TestThirtySecondSchedule(t *testing.T) {
	a := testApp(t)
	a.state.Schedules["demo"] = &Schedule{ID: "demo", CompanyID: "a", Request: "demo", EverySeconds: 30, Enabled: true, NextRun: now().Add(-time.Second), UserID: 1, ChatID: 1}
	before := now()
	a.tick()
	s := a.state.Schedules["demo"]
	delta := s.NextRun.Sub(before)
	if delta < 29*time.Second || delta > 31*time.Second {
		t.Fatalf("expected 30-second interval, got %v", delta)
	}
	if !s.Enabled || len(a.state.Posts) != 1 {
		t.Fatal("recurring schedule did not create exactly one task")
	}
	for _, p := range a.state.Posts {
		if p.ScheduleID != "demo" {
			t.Fatal("schedule link missing")
		}
	}
}
func TestThirtySecondScheduleDoesNotAccumulateGenerationBacklog(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "existing", 1, 1, nil)
	p.ScheduleID = "demo"
	p.Status = "working"
	a.state.Schedules["demo"] = &Schedule{ID: "demo", CompanyID: "a", Request: "demo", EverySeconds: 30, Enabled: true, NextRun: now().Add(-time.Second), UserID: 1, ChatID: 1}
	a.tick()
	if len(a.state.Posts) != 1 {
		t.Fatal("created duplicate while previous generation is still running")
	}
}

func TestTelegramRevisionIsIdempotentAfterLostResponse(t *testing.T) {
	a := testApp(t)
	a.internal = "test-internal"
	p := a.newPost("a", "draft", 1, 1, nil)
	p.Status = "review"
	body := fmt.Sprintf(`{"user_id":1,"post_id":%q,"revision":1,"action":"revise","comment":"Shorter","update_id":123}`, p.ID)
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/internal/action", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer test-internal")
		w := httptest.NewRecorder()
		a.internalAPI(w, r)
		if w.Code != 200 {
			t.Fatalf("attempt %d: %d %s", i, w.Code, w.Body.String())
		}
	}
	if p.Revision != 2 || strings.Count(p.Request, "Shorter") != 1 {
		t.Fatal("same Telegram update applied twice")
	}
}
func TestPauseStopsScheduledGeneration(t *testing.T) {
	a := testApp(t)
	a.localToken = "local-test"
	p := a.newPost("a", "scheduled", 1, 1, nil)
	p.Status = "working"
	p.ScheduleID = "demo"
	a.state.Schedules["demo"] = &Schedule{ID: "demo", CompanyID: "a", EverySeconds: 30, Enabled: true}
	body := fmt.Sprintf(`{"enabled":false,"every_seconds":30,"next_run":%q}`, now().Add(time.Minute).Format(time.RFC3339))
	r := httptest.NewRequest("PUT", "http://localhost/api/schedules/demo", strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Local-Token", a.localToken)
	w := httptest.NewRecorder()
	a.api(w, r)
	if w.Code != 200 || a.state.Schedules["demo"].Enabled || p.Status != "cancelled" {
		t.Fatalf("pause failed: %d %s %s", w.Code, w.Body.String(), p.Status)
	}
	a.tick()
	if len(a.state.Posts) != 1 {
		t.Fatal("paused schedule created more tasks")
	}
}
