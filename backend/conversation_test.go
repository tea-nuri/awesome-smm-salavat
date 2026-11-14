package main

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestConversationRepliesStayOutOfMaterials(t *testing.T) {
	for _, kind := range []string{"answer", "clarification"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			a.localToken = "local-test"
			question := a.newPost("a", "Напиши пост о матче", 1, 1, nil)
			a.applyGenerationResult(question, AIResult{Kind: kind, Body: "Когда прошёл матч?"}, "must-not-be-kept.png")
			if question.Status != "answered" || question.Image != "" || question.ApprovedRevision != 0 {
				t.Fatal("conversation reply was turned into a draft")
			}
			if len(a.state.History["a"]) != 2 || a.state.History["a"][1].Text != "Когда прошёл матч?" {
				t.Fatal("conversation context was lost")
			}
			if len(a.state.Outbox) != 1 || a.state.Outbox[0].Kind != "message" {
				t.Fatal("clarification was sent for approval instead of as a message")
			}
			legacy := a.newPost("a", "Ранее заданное уточнение", 1, 1, nil)
			legacy.Status = "answered"
			post := a.newPost("a", "Готовая публикация", 1, 1, nil)
			post.Status, post.Kind = "review", "post"
			r := httptest.NewRequest("GET", "http://localhost/api/bootstrap", nil)
			r.RemoteAddr = "127.0.0.1:1234"
			r.Header.Set("X-Local-Token", a.localToken)
			w := httptest.NewRecorder()
			a.api(w, r)
			var data struct {
				Posts []Post `json:"posts"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || len(data.Posts) != 1 || data.Posts[0].ID != post.ID {
				t.Fatalf("conversation reply exposed as material: %s", w.Body.String())
			}
		})
	}
}

func TestClarificationCannotBeApprovedOrQueuedForPublication(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "Детали", 1, 1, nil)
	p.Kind, p.Status = "clarification", "review"
	if _, err := a.action(a.user(1), p.ID, "approve", 1, "", "", ""); err == nil {
		t.Fatal("clarification accepted for publication")
	}
	a.queuePreview(p)
	p.Status, p.ApprovedRevision = "approved", 1
	a.tick()
	if len(a.state.Outbox) != 0 {
		t.Fatal("clarification entered approval or publication queue")
	}
}
