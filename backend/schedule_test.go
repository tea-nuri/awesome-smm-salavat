package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func timestamp(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCalendarNextPublication(t *testing.T) {
	for _, tc := range []struct {
		name, frequency, after, want, zone string
		day                                int
	}{
		{"daily today", "daily", "2026-11-14T06:00:00Z", "2026-11-14T07:00:00Z", "Europe/Moscow", 0},
		{"daily tomorrow", "daily", "2026-11-14T07:00:00Z", "2026-11-15T07:00:00Z", "Europe/Moscow", 0},
		{"weekly Monday", "weekly", "2026-11-14T06:00:00Z", "2026-11-16T07:00:00Z", "Europe/Moscow", 1},
		{"weekly Sunday", "weekly", "2026-11-14T06:00:00Z", "2026-11-15T07:00:00Z", "Europe/Moscow", 0},
		{"weekly same weekday", "weekly", "2026-11-16T06:59:00Z", "2026-11-16T07:00:00Z", "Europe/Moscow", 1},
		{"weekly exact boundary", "weekly", "2026-11-16T07:00:00Z", "2026-11-23T07:00:00Z", "Europe/Moscow", 1},
		{"calendar across daylight saving", "weekly", "2026-10-24T09:00:00Z", "2026-10-25T09:00:00Z", "Europe/Berlin", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Schedule{Frequency: tc.frequency, PublishTime: "10:00", TimeZone: tc.zone, Weekday: tc.day}
			got, err := s.nextPublication(timestamp(tc.after))
			if err != nil || !got.Equal(timestamp(tc.want)) {
				t.Fatalf("got %v %v, want %s", got, err, tc.want)
			}
		})
	}
	for _, s := range []Schedule{{Frequency: "daily", PublishTime: "25:00"}, {Frequency: "daily", PublishTime: "9:00"}, {Frequency: "daily", PublishTime: "10:00", TimeZone: "invalid"}, {Frequency: "weekly", PublishTime: "10:00", Weekday: 7}, {Frequency: "unknown"}} {
		if _, err := s.nextPublication(now()); err == nil {
			t.Fatalf("invalid configuration accepted: %+v", s)
		}
	}
}

func TestCalendarPreparesAheadAndPublishesOnceAtTime(t *testing.T) {
	a := testApp(t)
	current := timestamp("2026-11-14T06:00:00Z")
	s := &Schedule{ID: "daily", CompanyID: "a", Frequency: "daily", PublishTime: "10:00", Request: "task", Enabled: true, UserID: 1, ChatID: 1}
	if err := configureSchedule(s, current); err != nil {
		t.Fatal(err)
	}
	a.state.Schedules[s.ID] = s
	a.dispatchSchedules(timestamp("2026-11-14T06:44:59Z"))
	if len(a.state.Posts) != 0 {
		t.Fatal("prepared too early")
	}
	a.dispatchSchedules(timestamp("2026-11-14T06:45:00Z"))
	if len(a.state.Posts) != 1 {
		t.Fatal("missing preparation")
	}
	var p *Post
	for _, item := range a.state.Posts {
		p = item
	}
	if p.ScheduledAt == nil || !p.ScheduledAt.Equal(timestamp("2026-11-14T07:00:00Z")) {
		t.Fatal("publication time missing")
	}
	p.Status = "approved"
	p.ApprovedRevision = p.Revision
	other := a.newPost("a", "slow AI job", 1, 1, nil)
	other.Status = "working"
	a.dispatchSchedules(timestamp("2026-11-14T06:59:59Z"))
	if p.Status != "approved" {
		t.Fatal("published early")
	}
	a.dispatchSchedules(timestamp("2026-11-14T07:00:00Z"))
	a.dispatchSchedules(timestamp("2026-11-14T07:00:01Z"))
	if p.Status != "publishing" || len(a.state.Outbox) != 1 || a.state.Outbox[0].Kind != "publish" {
		t.Fatal("publication missing or duplicated")
	}
	if len(a.state.Posts) != 2 {
		t.Fatal("duplicate preparation")
	}
}

func TestCalendarApprovalPauseAndMissedSlot(t *testing.T) {
	a := testApp(t)
	slot := timestamp("2026-11-14T07:00:00Z")
	s := &Schedule{ID: "daily", CompanyID: "a", Frequency: "daily", PublishTime: "10:00", Enabled: false, NextRun: slot.Add(time.Hour)}
	a.state.Schedules[s.ID] = s
	p := a.newPost("a", "draft", 1, 1, &slot)
	p.ScheduleID = s.ID
	p.Status = "review"
	a.dispatchSchedules(slot)
	if p.Status != "review" {
		t.Fatal("unapproved published")
	}
	p.Status = "approved"
	p.ApprovedRevision = p.Revision
	a.dispatchSchedules(slot)
	if p.Status != "approved" {
		t.Fatal("paused schedule published")
	}
	s.Enabled = true
	a.dispatchSchedules(slot.Add(time.Minute))
	if p.Status != "review" || p.ApprovedRevision != 0 || p.Revision != 2 || !p.ScheduledAt.Equal(slot.AddDate(0, 0, 1)) {
		t.Fatal("missed slot not rescheduled for approval")
	}
	for _, ev := range a.state.Outbox {
		if ev.Kind == "publish" {
			t.Fatal("late publication queued")
		}
	}
	delete(a.state.Schedules, s.ID)
	p.Status = "approved"
	p.ApprovedRevision = p.Revision
	a.dispatchSchedules(*p.ScheduledAt)
	if p.Status != "approved" {
		t.Fatal("deleted schedule published")
	}
}

func TestCalendarLateApprovalUsesNextFreeSlot(t *testing.T) {
	a := testApp(t)
	s := &Schedule{ID: "weekly", CompanyID: "a", Frequency: "weekly", PublishTime: "10:00", Weekday: 1, Enabled: true}
	a.state.Schedules[s.ID] = s
	old := now().Add(-time.Hour)
	p := a.newPost("a", "late draft", 1, 1, &old)
	p.ScheduleID = s.ID
	p.Status = "review"
	next, err := s.nextPublication(now())
	if err != nil {
		t.Fatal(err)
	}
	existing := a.newPost("a", "already booked", 1, 1, &next)
	existing.ScheduleID = s.ID
	existing.Status = "review"
	if _, err := a.action(a.user(1), p.ID, "approve", p.Revision, "", "", ""); err != nil {
		t.Fatal(err)
	}
	want, _ := s.nextPublication(next)
	if !p.ScheduledAt.Equal(want) || p.Status != "approved" || p.ApprovedRevision != p.Revision {
		t.Fatal("late approval did not book next available occurrence")
	}
}

func TestCalendarAPICreateEditPauseAndPersist(t *testing.T) {
	a := testApp(t)
	a.localToken = "local-test"
	call := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Local-Token", a.localToken)
		w := httptest.NewRecorder()
		a.api(w, r)
		return w
	}
	w := call("POST", "/api/schedules", `{"company_id":"a","request":"Weekly news","frequency":"weekly","publish_time":"18:30","weekday":5}`)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	var s Schedule
	json.Unmarshal(w.Body.Bytes(), &s)
	if s.TimeZone != "Europe/Moscow" || s.NextPublicationAt == nil || s.EverySeconds != 0 {
		t.Fatal("calendar fields missing")
	}
	p := a.newPost("a", "draft", 1, 1, s.NextPublicationAt)
	p.ScheduleID = s.ID
	p.Status = "approved"
	p.ApprovedRevision = p.Revision
	w = call("PUT", "/api/schedules/"+s.ID, `{"publish_time":"19:00","weekday":2}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if p.Status != "review" || p.ApprovedRevision != 0 {
		t.Fatal("old time approval still valid")
	}
	w = call("PUT", "/api/schedules/"+s.ID, `{"publish_time":"99:00"}`)
	if w.Code != 400 || a.state.Schedules[s.ID].PublishTime != "19:00" {
		t.Fatal("invalid update changed schedule")
	}
	w = call("PUT", "/api/schedules/"+s.ID, `{"enabled":false}`)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	b := &App{dir: a.dir, aiDir: a.aiDir, admins: a.admins}
	if err := b.load(); err != nil {
		t.Fatal(err)
	}
	saved := b.state.Schedules[s.ID]
	if saved.Enabled || saved.Frequency != "weekly" || saved.PublishTime != "19:00" || saved.Weekday != 2 {
		t.Fatal("calendar configuration lost after pause/restart")
	}
	w = call("PUT", "/api/schedules/"+s.ID, `{"enabled":true}`)
	if w.Code != 200 || !a.state.Schedules[s.ID].Enabled {
		t.Fatal("resume failed")
	}
}
