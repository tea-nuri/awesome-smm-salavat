package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceTestApp(t *testing.T) *App {
	a := testApp(t)
	a.python = "python3"
	a.renderScript, _ = filepath.Abs("../bot/render_card.py")
	return a
}

func TestUploadedKnowledgeIsPersistentScopedAndCanBeDisabled(t *testing.T) {
	a := sourceTestApp(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, _ := writer.CreateFormFile("file", "company.txt")
	io.WriteString(file, "Компания выпускает продукт KNOWLEDGE_MARKER_731 для корпоративных клиентов.")
	writer.Close()
	r := httptest.NewRequest("POST", "/api/companies/a/sources", &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	a.sourceAPI(w, r, a.user(1), "a", "")
	var source SourceView
	json.Unmarshal(w.Body.Bytes(), &source)
	if w.Code != 201 || source.Characters < 20 || !strings.Contains(a.companyKnowledge("a"), "KNOWLEDGE_MARKER_731") || a.companyKnowledge("b") != "" {
		t.Fatalf("document import or isolation failed: %d %s", w.Code, w.Body.String())
	}
	b := &App{dir: a.dir, aiDir: a.aiDir, admins: a.admins}
	if err := b.load(); err != nil || !strings.Contains(b.companyKnowledge("a"), "KNOWLEDGE_MARKER_731") {
		t.Fatal("knowledge lost on restart")
	}
	w = httptest.NewRecorder()
	a.sourceAPI(w, httptest.NewRequest("PATCH", "/", strings.NewReader(`{"enabled":false}`)), a.user(2), "a", source.ID)
	if w.Code != 403 {
		t.Fatal("another company could modify source")
	}
	w = httptest.NewRecorder()
	a.sourceAPI(w, httptest.NewRequest("PATCH", "/", strings.NewReader(`{"enabled":false}`)), a.user(1), "a", source.ID)
	if w.Code != 200 || a.companyKnowledge("a") != "" {
		t.Fatal("disabled source is still used")
	}
}

func TestWebsiteTextIsExtractedWithoutScripts(t *testing.T) {
	a := sourceTestApp(t)
	a.sourceClient = &http.Client{Transport: testRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://example.com/about" || r.Header.Get("Authorization") != "" {
			t.Fatal("wrong website request or leaked auth")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html; charset=utf-8"}}, Body: io.NopCloser(strings.NewReader(`<html><head><title>Factory</title><script>SECRET_SCRIPT_891</script></head><body><h1>About the factory</h1><p>We produce SAFE_COMPANY_FACT_921 for partners.</p></body></html>`))}, nil
	})}
	w := httptest.NewRecorder()
	a.sourceAPI(w, httptest.NewRequest("POST", "/", strings.NewReader(`{"url":"https://example.com/about"}`)), a.user(1), "a", "")
	if w.Code != 201 {
		t.Fatalf("website import: %s", w.Body.String())
	}
	knowledge := a.companyKnowledge("a")
	if !strings.Contains(knowledge, "SAFE_COMPANY_FACT_921") || strings.Contains(knowledge, "SECRET_SCRIPT_891") {
		t.Fatal("HTML extraction included script or lost company facts")
	}
}

func TestWebsiteImportBlocksPrivateAndCredentialURLs(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "http://127.0.0.1/", "http://169.254.169.254/", "http://[::1]/", "https://user:password@example.com/", "http://example.com:22/"} {
		if _, err := publicWebsiteURL(raw); err == nil {
			t.Fatalf("unsafe URL accepted: %s", raw)
		}
	}
	for _, raw := range []string{"10.0.0.1", "100.64.0.1", "::ffff:127.0.0.1", "fd00::1"} {
		if publicSourceIP(netip.MustParseAddr(raw)) {
			t.Fatalf("private address accepted: %s", raw)
		}
	}
	if conn, err := sourceDial(context.Background(), "tcp", "localhost:80"); err == nil {
		conn.Close()
		t.Fatal("DNS could connect to localhost")
	}
}

func TestImportedSourcesReachModelContextWithoutCrossCompanyLeak(t *testing.T) {
	a := sourceTestApp(t)
	a.state.Sources = map[string]*KnowledgeSource{
		"own":   {CompanyID: "a", Name: "Company document", Text: strings.Repeat("Verified company information. ", 1000) + " END_MARKER_991", Enabled: true},
		"other": {CompanyID: "b", Name: "Private B", Text: "FOREIGN_MARKER_999", Enabled: true},
	}
	provider := filepath.Join(t.TempDir(), "provider")
	os.WriteFile(provider, []byte(`#!/bin/sh
while [ "$#" -gt 0 ]; do
 if [ "$1" = "-o" ]; then shift; output="$1"; fi
 if [ "$1" = "-C" ]; then shift; job="$1"; fi
 shift
done
cat > "$job/prompt.txt"
printf '%s' '{"kind":"answer","body":"Answer based on company sources"}' > "$output"
`), 0700)
	a.codex = provider
	_, _, err := a.generate(Post{ID: "knowledge", CompanyID: "a", Request: "Explain our company"}, *a.state.Companies["a"], nil)
	if err != nil {
		t.Fatal(err)
	}
	prompt, _ := os.ReadFile(filepath.Join(a.aiDir, "a", "prompt.txt"))
	if !strings.Contains(string(prompt), "END_MARKER_991") || strings.Contains(string(prompt), "FOREIGN_MARKER_999") {
		t.Fatal("model did not receive complete selected-company knowledge")
	}
}
