package main

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installTestChatGPTProfile(t *testing.T, a *App) {
	t.Helper()
	if err := os.MkdirAll(a.codexHome(), 0700); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"auth.json": `{"auth_mode":"chatgpt","tokens":{"access_token":"test-only"}}`, ".selected": "chatgpt"} {
		if err := os.WriteFile(filepath.Join(a.codexHome(), name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFailedPendingLoginPreservesRestoredCredentials(t *testing.T) {
	a := testApp(t)
	a.codex = filepath.Join(t.TempDir(), "fake-codex")
	if err := os.WriteFile(a.codex, []byte("#!/bin/sh\nwhile [ ! -f \"$CODEX_HOME/release\" ]; do sleep 0.01; done\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/operations/auth", strings.NewReader(`{"action":"start"}`))
	w := httptest.NewRecorder()
	a.accountAuthAPI(w, r, identity{ID: 1, Admin: true})
	if w.Code != 200 || a.authSession == nil {
		t.Fatalf("login did not start: %d", w.Code)
	}
	defer a.authSession.cancel()
	installTestChatGPTProfile(t, a)
	dirs, _ := filepath.Glob(filepath.Join(a.codexHome(), ".login-*"))
	if len(dirs) != 1 {
		t.Fatal("pending login did not use an isolated profile")
	}
	if err := os.WriteFile(filepath.Join(dirs[0], "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.authSession.mu.Lock()
		stopped := a.authSession.state == "stopped"
		a.authSession.mu.Unlock()
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed login did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !a.chatGPTConnected() || !(&App{dir: a.dir}).chatGPTConnected() {
		t.Fatal("failed login deleted the restored credentials")
	}
}

func TestCancelKeepsAccountUntilExplicitLogout(t *testing.T) {
	a := testApp(t)
	installTestChatGPTProfile(t, a)
	_, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.authSession = &AuthSession{state: "waiting", cancel: cancel}
	for _, action := range []string{"cancel", "logout"} {
		r := httptest.NewRequest("POST", "/api/operations/auth", strings.NewReader(`{"action":"`+action+`"}`))
		w := httptest.NewRecorder()
		a.accountAuthAPI(w, r, identity{ID: 1, Admin: true})
		if w.Code != 200 || a.chatGPTConnected() != (action == "cancel") {
			t.Fatalf("wrong account state after %s", action)
		}
	}
}
