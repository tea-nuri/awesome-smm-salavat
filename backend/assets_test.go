package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"testing"
)

func TestImageUploadIsPrivateAndBoundToCompany(t *testing.T) {
	a := testApp(t)
	var data bytes.Buffer
	png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 100, 150)))
	r := httptest.NewRequest("POST", "/api/assets?company_id=a", bytes.NewReader(data.Bytes()))
	w := httptest.NewRecorder()
	a.assetAPI(w, r, a.user(1), "a")
	if w.Code != 201 {
		t.Fatalf("upload rejected: %d %s", w.Code, w.Body.String())
	}
	var asset Asset
	json.Unmarshal(w.Body.Bytes(), &asset)
	if asset.CompanyID != "a" || asset.ID == "" {
		t.Fatal("asset was not bound to company")
	}
	if err := a.validateTaskMedia("b", TaskMedia{Attachments: []string{asset.ID}}); err == nil {
		t.Fatal("cross-company photo accepted")
	}
	get := httptest.NewRequest("GET", "/api/assets/"+asset.ID, nil)
	unauthorized := httptest.NewRecorder()
	a.assetAPI(unauthorized, get, a.user(2), "")
	if unauthorized.Code != 404 {
		t.Fatal("another company could read source photo")
	}
	allowed := httptest.NewRecorder()
	a.assetAPI(allowed, get, a.user(1), "")
	if allowed.Code != 200 || !bytes.Equal(allowed.Body.Bytes(), data.Bytes()) {
		t.Fatal("original source pixels were altered or unavailable")
	}
	b := &App{dir: a.dir, aiDir: t.TempDir(), admins: a.admins}
	if err := b.load(); err != nil {
		t.Fatal(err)
	}
	if b.state.Assets[asset.ID] == nil {
		t.Fatal("uploaded source lost on restart")
	}
}

func TestInvalidPhotoAndForeignRevisionDoNotModifyDraft(t *testing.T) {
	a := testApp(t)
	w := httptest.NewRecorder()
	a.assetAPI(w, httptest.NewRequest("POST", "/api/assets", bytes.NewBufferString("not a photo")), a.user(1), "a")
	if w.Code != 400 || len(a.state.Assets) != 0 {
		t.Fatal("accepted non-image upload")
	}
	a.state.Assets = map[string]*Asset{"foreign": {ID: "foreign", CompanyID: "b"}}
	p := a.newPost("a", "draft", 1, 1, nil)
	p.Status = "review"
	if err := a.validateRevisionMedia(a.user(1), p.ID, TaskMedia{Attachments: []string{"foreign"}}); err == nil {
		t.Fatal("accepted another company's photo in revision")
	}
	if p.Revision != 1 || p.Status != "review" {
		t.Fatal("invalid photo changed approval state")
	}
}

func TestPhotosSurviveClarificationButDoNotLeakToNextUnrelatedTask(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "post from photo", 1, 1, nil)
	p.Kind, p.Status, p.Attachments, p.ImageMode = "clarification", "answered", []string{"source"}, "style"
	p.AspectRatio = "4:5"
	media := a.clarificationMedia("a", 1, TaskMedia{})
	if len(media.Attachments) != 1 || media.ImageMode != "style" || media.AspectRatio != "4:5" {
		t.Fatal("lost source photos while clarifying task")
	}
	a.newPost("a", "unrelated task", 1, 1, nil)
	if len(a.clarificationMedia("a", 1, TaskMedia{}).Attachments) != 0 {
		t.Fatal("old photos attached to an unrelated new task")
	}
	if len(a.clarificationMedia("b", 1, TaskMedia{}).Attachments) != 0 {
		t.Fatal("photos crossed company boundary")
	}
}
