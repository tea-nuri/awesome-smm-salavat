package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testRoundTrip func(*http.Request) (*http.Response, error)

func (f testRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBrandingResolvesPackAndPersistsOnlyCompanyAssets(t *testing.T) {
	a := testApp(t)
	a.state.Assets = map[string]*Asset{"logo": {CompanyID: "a", File: "logo.png"}, "style": {CompanyID: "a", File: "style.jpg"}, "foreign": {CompanyID: "b", File: "foreign.png"}}
	a.httpClient = &http.Client{Transport: testRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "api.telegram.org" || !strings.HasSuffix(r.URL.Path, "/getStickerSet") {
			t.Fatal("pack lookup used an unexpected destination")
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "DifferentPack" {
			t.Fatal("ignored user-selected pack")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"ok":true,"result":{"name":"DifferentPack","title":"Other brand","sticker_type":"custom_emoji","stickers":[{"emoji":"💙","custom_emoji_id":"12345"}]}}`)), Header: http.Header{}}, nil
	})}
	r := httptest.NewRequest("PUT", "/api/companies/a/branding", strings.NewReader(`{"logo_asset_id":"logo","style_assets":["style"],"style_notes":"Use the uploaded style","emoji_source":"pack","emoji_pack":"https://t.me/addemoji/DifferentPack"}`))
	w := httptest.NewRecorder()
	a.brandingAPI(w, r, a.user(1), "a")
	if w.Code != 200 {
		t.Fatalf("save branding: %d %s", w.Code, w.Body.String())
	}
	c := *a.state.Companies["a"]
	if c.LogoAssetID != "logo" || len(c.StyleAssets) != 1 || len(c.EmojiItems) != 1 || c.EmojiItems[0].ID != "12345" {
		t.Fatal("branding settings lost")
	}
	styles, logo, photos, err := a.artworkInputs(Post{CompanyID: "a"}, c)
	if err != nil || len(styles) != 1 || !strings.HasSuffix(styles[0], "style.jpg") || !strings.HasSuffix(logo, "logo.png") || len(photos) != 0 {
		t.Fatal("generator ignored company settings")
	}
	if _, _, _, err := a.artworkInputs(Post{CompanyID: "a", TaskMedia: TaskMedia{LogoAssetID: "foreign"}}, c); err == nil {
		t.Fatal("accepted another company's logo")
	}
	b := &App{dir: a.dir, aiDir: a.aiDir, admins: a.admins}
	if err := b.load(); err != nil || b.state.Companies["a"].EmojiPack != c.EmojiPack || b.state.Companies["a"].LogoAssetID != "logo" {
		t.Fatal("branding lost on restart")
	}
}

func TestBrandingRejectsForeignAssetsAndUnsafePackLinks(t *testing.T) {
	a := testApp(t)
	a.state.Assets = map[string]*Asset{"foreign": {CompanyID: "b"}}
	w := httptest.NewRecorder()
	a.brandingAPI(w, httptest.NewRequest("PUT", "/api/companies/a/branding", strings.NewReader(`{"logo_asset_id":"foreign"}`)), a.user(1), "a")
	if w.Code != 400 || a.state.Companies["a"].LogoAssetID != "" {
		t.Fatal("cross-company logo saved")
	}
	w = httptest.NewRecorder()
	a.brandingAPI(w, httptest.NewRequest("PUT", "/api/companies/a/branding", strings.NewReader(`{}`)), a.user(2), "a")
	if w.Code != 403 {
		t.Fatal("member of another company could change branding")
	}
	for _, link := range []string{"https://evil.example/addemoji/foo", "https://t.me@evil.example/addemoji/foo", "https://t.me/addstickers/foo", "../../secret"} {
		if _, err := emojiPackName(link); err == nil {
			t.Fatalf("unsafe pack link accepted: %s", link)
		}
	}
}

func TestTextOnlyCallsTextProviderOnceAndKeepsImageBytes(t *testing.T) {
	a := testApp(t)
	c := *a.state.Companies["a"]
	job := filepath.Join(a.aiDir, c.ID)
	os.MkdirAll(job, 0700)
	os.MkdirAll(filepath.Join(a.dir, "media"), 0700)
	image := []byte("original existing image bytes")
	os.WriteFile(filepath.Join(a.dir, "media", "original.png"), image, 0600)
	provider := filepath.Join(t.TempDir(), "text-provider")
	os.WriteFile(provider, []byte(`#!/bin/sh
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then shift; output="$1"; fi
  if [ "$1" = "-C" ]; then shift; job="$1"; fi
  shift
done
cat >/dev/null
printf '%s' '{"kind":"post","title":"New title","body":"New requested text"}' > "$output"
printf 'called\n' >> "$job/calls"
`), 0700)
	a.codex = provider
	p := Post{ID: "post", CompanyID: c.ID, Revision: 2, Image: "original.png", ImageSource: "Original source", Visual: AIVisual{Headline: "Old visual"}, TaskMedia: TaskMedia{TextOnly: true}}
	result, file, err := a.generate(p, c, nil)
	if err != nil || file != "original.png" || result.Body != "New requested text" || result.Visual.Headline != "Old visual" {
		t.Fatalf("text-only did not preserve image: %s %v", file, err)
	}
	data, _ := os.ReadFile(filepath.Join(a.dir, "media", file))
	calls, _ := os.ReadFile(filepath.Join(job, "calls"))
	if string(data) != string(image) || string(calls) != "called\n" {
		t.Fatal("text-only regenerated or altered the image")
	}
}
