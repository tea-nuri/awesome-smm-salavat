package main

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFormatRevisionInvalidatesApprovalAndPersists(t *testing.T) {
	a := testApp(t)
	a.internal = "secret"
	p := a.newPost("a", "Original task", 1, 1, nil)
	p.Status, p.ApprovedRevision, p.Body = "approved", 1, "Original body"
	r := httptest.NewRequest("POST", "/internal/action", strings.NewReader(fmt.Sprintf(`{"user_id":1,"post_id":%q,"action":"revise","revision":1,"aspect_ratio":"4:5","image_only":true}`, p.ID)))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	a.internalAPI(w, r)
	if w.Code != 200 {
		t.Fatalf("format-only revision rejected: %d %s", w.Code, w.Body.String())
	}
	if p.AspectRatio != "4:5" || p.Revision != 2 || p.ApprovedRevision != 0 || !p.ImageOnly || p.Body != "Original body" {
		t.Fatal("format revision did not preserve body and invalidate approval")
	}
	b := &App{dir: a.dir, aiDir: a.aiDir, admins: a.admins}
	if err := b.load(); err != nil {
		t.Fatal(err)
	}
	if b.state.Posts[p.ID].AspectRatio != "4:5" {
		t.Fatal("image format lost on restart")
	}
}

func TestInvalidFormatDoesNotChangeDraft(t *testing.T) {
	a := testApp(t)
	a.internal = "secret"
	p := a.newPost("a", "Original task", 1, 1, nil)
	p.Status = "review"
	r := httptest.NewRequest("POST", "/internal/action", strings.NewReader(fmt.Sprintf(`{"user_id":1,"post_id":%q,"action":"revise","revision":1,"comment":"new format","aspect_ratio":"999:1"}`, p.ID)))
	r.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	a.internalAPI(w, r)
	if w.Code != 400 || p.Revision != 1 || p.Status != "review" {
		t.Fatal("invalid format modified a draft")
	}
}
