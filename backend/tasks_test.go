package main

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

func taskRequest(a *App, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost/api/"+path, strings.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Set("X-Local-Token", a.localToken)
	w := httptest.NewRecorder()
	a.api(w, r)
	return w
}

func TestWebClarificationRoundTripPreservesTaskAndMedia(t *testing.T) {
	a := testApp(t)
	a.localToken = "local-test"
	a.state.Assets = map[string]*Asset{"photo": {CompanyID: "a"}, "logo": {CompanyID: "a"}, "style": {CompanyID: "a"}}
	w := taskRequest(a, "POST", "tasks", `{"company_id":"a","request":"Создай анонс матча","attachments":["photo"],"logo_asset_id":"logo","style_assets":["style"],"aspect_ratio":"4:5"}`)
	var created Post
	json.Unmarshal(w.Body.Bytes(), &created)
	if w.Code != 202 || created.Stage != "queued" || len(created.Dialog) != 1 {
		t.Fatalf("new task is invisible: %d %s", w.Code, w.Body.String())
	}
	p := a.state.Posts[created.ID]
	a.applyGenerationResult(p, AIResult{Kind: "clarification", Body: "Когда состоится матч?"}, "")
	w = taskRequest(a, "GET", "bootstrap", "")
	var snapshot struct {
		Posts []Post     `json:"posts"`
		Tasks []TaskView `json:"tasks"`
	}
	json.Unmarshal(w.Body.Bytes(), &snapshot)
	if len(snapshot.Posts) != 0 || len(snapshot.Tasks) != 1 || !snapshot.Tasks[0].AwaitingInput || !snapshot.Tasks[0].CanReply || snapshot.Tasks[0].Dialog[1].Text != "Когда состоится матч?" {
		t.Fatal("clarification is missing from task dialog or leaked into materials")
	}
	path := "tasks/" + p.ID + "/reply"
	w = taskRequest(a, "POST", path, `{"revision":1,"text":"В понедельник в 17:00"}`)
	if w.Code != 202 || len(a.state.Posts) != 1 || p.Status != "generating" || p.Revision != 2 || p.Body != "" || p.Kind != "" {
		t.Fatalf("reply failed to continue original task: %s", w.Body.String())
	}
	if len(p.Attachments) != 1 || p.LogoAssetID != "logo" || len(p.StyleAssets) != 1 || p.AspectRatio != "4:5" {
		t.Fatal("reply lost media or brand settings")
	}
	w = taskRequest(a, "POST", path, `{"revision":1,"text":"Duplicate answer"}`)
	if w.Code != 409 || p.Revision != 2 || len(p.Dialog) != 3 {
		t.Fatal("duplicate reply started another generation")
	}
	a.applyGenerationResult(p, AIResult{Kind: "clarification", Body: "Где играем?"}, "")
	w = taskRequest(a, "POST", path, `{"revision":2,"text":"В спортивном зале"}`)
	if w.Code != 202 {
		t.Fatal("second clarification cannot be answered")
	}
	a.applyGenerationResult(p, AIResult{Kind: "post", Title: "Матч", Body: "Готовый анонс без служебных вопросов"}, "final.png")
	w = taskRequest(a, "GET", "bootstrap", "")
	snapshot.Posts, snapshot.Tasks = nil, nil
	json.Unmarshal(w.Body.Bytes(), &snapshot)
	if len(snapshot.Posts) != 1 || snapshot.Posts[0].ID != created.ID || snapshot.Posts[0].Body != "Готовый анонс без служебных вопросов" || snapshot.Tasks[0].CanReply || snapshot.Tasks[0].Stage != "ready" || len(snapshot.Tasks[0].Dialog) != 6 {
		t.Fatal("finished material or dialog history is incorrect")
	}
	a.persist()
	b := &App{dir: a.dir, aiDir: a.aiDir, admins: a.admins}
	if err := b.load(); err != nil || len(b.state.Posts[p.ID].Dialog) != 6 {
		t.Fatal("dialog disappeared after restart")
	}
}

func TestTaskIsolationAndProgressRevision(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "Secret task", 1, 1, nil)
	a.applyGenerationResult(p, AIResult{Kind: "clarification", Body: "Secret question"}, "")
	if _, err := a.replyToTask(a.user(2), p.ID, p.Revision, "Foreign answer"); err == nil || len(p.Dialog) != 2 {
		t.Fatal("another company could answer the task")
	}
	a.botToken = "test-token"
	r := httptest.NewRequest("GET", "http://localhost/api/bootstrap", nil)
	r.Header.Set("X-Telegram-Init-Data", signInit(url.Values{"auth_date": {strconv.FormatInt(time.Now().Unix(), 10)}, "user": {`{"id":2}`}}, a.botToken))
	w := httptest.NewRecorder()
	a.api(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "Secret question") {
		t.Fatal("dialog leaked to another company")
	}
	p.Status, p.Stage, p.Revision = "working", "image", 2
	a.setTaskStage(p.ID, 1, "export")
	if p.Stage != "image" {
		t.Fatal("old worker overwrote current progress")
	}
	a.setTaskStage(p.ID, 2, "export")
	if p.Stage != "export" {
		t.Fatal("current progress did not update")
	}
	b := &App{dir: a.dir, aiDir: a.aiDir, admins: a.admins}
	if err := b.load(); err != nil || b.state.Posts[p.ID].Stage != "queued" || b.state.Posts[p.ID].Status != "generating" {
		t.Fatal("restarted task shows stale image progress")
	}
}

func TestBotAnswerContinuesWebTaskWithoutCreatingDuplicate(t *testing.T) {
	a := testApp(t)
	a.internal = "secret"
	a.state.Active["1"] = "a"
	a.state.Assets = map[string]*Asset{"photo": {CompanyID: "a"}}
	p := a.newPost("a", "Revise only text", 1, 1, nil)
	p.TextOnly, p.Image, p.Attachments = true, "original.png", []string{"photo"}
	a.applyGenerationResult(p, AIResult{Kind: "clarification", Body: "Which date?"}, "")
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/internal/message", strings.NewReader(`{"user_id":1,"chat_id":1,"update_id":500,"text":"16 ноября"}`))
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		a.internalAPI(w, r)
		if w.Code != 200 {
			t.Fatalf("bot reply failed: %s", w.Body.String())
		}
	}
	if len(a.state.Posts) != 1 || p.Revision != 2 || len(p.Dialog) != 3 || p.Image != "original.png" || !p.TextOnly || len(p.Attachments) != 1 {
		t.Fatal("bot reply duplicated task or lost existing image/mode")
	}
}

func TestAnsweredLegacyTaskRemainsReadable(t *testing.T) {
	a := testApp(t)
	p := &Post{ID: "legacy", CompanyID: "a", Status: "answered", Request: "Original task", Body: "Old answer", CreatedAt: now(), UpdatedAt: now()}
	view := a.taskView(p)
	if len(view.Dialog) != 2 || view.Dialog[1].Text != "Old answer" || !view.CanReply || view.AwaitingInput {
		t.Fatal(fmt.Sprintf("legacy answer missing: %+v", view))
	}
}

func TestNewTaskDoesNotHideAnUnansweredLegacyClarification(t *testing.T) {
	a := testApp(t)
	p := &Post{ID: "pending", CompanyID: "a", UserID: 1, Kind: "clarification", Status: "answered", Request: "Original task", Body: "Which person?", CreatedAt: now(), UpdatedAt: now()}
	a.state.Posts[p.ID] = p
	a.newPost("a", "Another independent task", 1, 1, nil)
	if !a.taskView(p).AwaitingInput {
		t.Fatal("another task hid an unanswered clarification")
	}
}
