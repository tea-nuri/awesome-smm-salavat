package main

import (
	"encoding/json"
	"image"
	"image/png"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferenceImportIsIsolatedAndIdempotent(t *testing.T) {
	a := testApp(t)
	a.internal = "secret"
	a.state.Active["1"] = "a"
	body := `{"user_id":1,"update_id":456,"text":"🏆 Наша команда\n\n#Вкоманде","source":"Пример","entities":[{"type":"custom_emoji","offset":0,"length":2,"custom_emoji_id":"12345"}]}`
	for i := 0; i < 2; i++ {
		r := httptest.NewRequest("POST", "/internal/reference", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		a.internalAPI(w, r)
		if w.Code != 200 {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	}
	if len(a.state.Companies["a"].References) != 1 || len(a.state.Companies["b"].References) != 0 {
		t.Fatal("reference duplicated or crossed company boundary")
	}
	if a.state.Companies["a"].References[0].Entities[0].CustomEmojiID != "12345" {
		t.Fatal("custom emoji lost")
	}
	if len(a.state.Posts) != 0 {
		t.Fatal("forwarded reference became a generation task")
	}
}

func TestGeneratedImageMustBelongToCurrentRun(t *testing.T) {
	root := t.TempDir()
	job := filepath.Join(root, "this-run")
	os.Mkdir(job, 0700)
	good := filepath.Join(job, "new.png")
	f, _ := os.Create(good)
	png.Encode(f, image.NewRGBA(image.Rect(0, 0, 512, 512)))
	f.Close()
	if _, err := generatedImagePath(job, good); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "other-run.png")
	data, _ := os.ReadFile(good)
	os.WriteFile(other, data, 0600)
	if _, err := generatedImagePath(job, other); err == nil {
		t.Fatal("accepted another company's generated image")
	}
	os.Symlink(other, filepath.Join(job, "escape.png"))
	if _, err := generatedImagePath(job, filepath.Join(job, "escape.png")); err == nil {
		t.Fatal("followed an escaping symlink")
	}
	os.WriteFile(filepath.Join(job, "text.png"), []byte("not an image"), 0600)
	if _, err := generatedImagePath(job, filepath.Join(job, "text.png")); err == nil {
		t.Fatal("accepted invented image file")
	}
}

func TestBrandPromptUsesReferencesAsStyleAndPreservesProfile(t *testing.T) {
	c := salavatCompany()
	c.References = []BrandReference{{Text: "Пример с уникальной фразой"}}
	prompt := salavatPrompt(Post{Request: "Задача"}, c, nil)
	if !strings.Contains(prompt, c.References[0].Text) || !strings.Contains(prompt, "не означают актуальные новости") || !strings.Contains(prompt, "#Вкоманде") {
		t.Fatal("missing brand grounding or source boundary")
	}
	data, _ := json.Marshal(c)
	var restored Company
	json.Unmarshal(data, &restored)
	if restored.BrandProfile != "salavatsteklo" || len(restored.References) != 1 {
		t.Fatal("brand state not persistent")
	}
}
