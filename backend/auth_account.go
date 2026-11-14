package main

import (
	"context"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type AuthSession struct {
	mu      sync.Mutex
	state   string
	url     string
	code    string
	expires time.Time
	output  string
	cancel  context.CancelFunc
}

var deviceCode = regexp.MustCompile(`\b[A-Z0-9]{4,5}-[A-Z0-9]{4,5}\b`)
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func (s *AuthSession) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.output += string(p)
	if len(s.output) > 16000 {
		s.output = s.output[len(s.output)-16000:]
	}
	clean := ansiEscape.ReplaceAllString(s.output, "")
	if strings.Contains(clean, "https://auth.openai.com/codex/device") {
		s.url = "https://auth.openai.com/codex/device"
	}
	if code := deviceCode.FindString(clean); code != "" {
		s.code = code
	}
	return len(p), nil
}
func (a *App) codexHome() string {
	isolated := filepath.Join(a.dir, "codex-profile")
	if _, err := os.Stat(filepath.Join(isolated, ".selected")); err == nil {
		return isolated
	}
	if value := os.Getenv("CODEX_HOME"); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}
func (a *App) aiEnvironment() []string {
	env := aiEnvironment()
	home := a.codexHome()
	out := []string{}
	for _, v := range env {
		if !strings.HasPrefix(v, "CODEX_HOME=") {
			out = append(out, v)
		}
	}
	return append(out, "CODEX_HOME="+home)
}
func (a *App) authArguments() []string {
	if _, err := os.Stat(filepath.Join(a.dir, "codex-profile", ".selected")); err == nil {
		return []string{"-c", `cli_auth_credentials_store="file"`}
	}
	return nil
}
func (a *App) accountAuthAPI(w http.ResponseWriter, r *http.Request, u identity) {
	if !u.Admin {
		fail(w, 403, "Только администратор")
		return
	}
	a.authMu.Lock()
	defer a.authMu.Unlock()
	if r.Method == "POST" {
		var in struct {
			Action string `json:"action"`
		}
		if decode(r, &in) != nil {
			fail(w, 400, "Проверьте действие")
			return
		}
		if in.Action == "cancel" {
			if a.authSession != nil {
				a.authSession.cancel()
			}
			reply(w, 200, map[string]bool{"ok": true})
			return
		}
		if in.Action != "start" {
			fail(w, 400, "Неизвестное действие")
			return
		}
		if a.authSession != nil {
			a.authSession.mu.Lock()
			active := a.authSession.state == "waiting"
			a.authSession.mu.Unlock()
			if active {
				fail(w, 409, "Вход уже запущен")
				return
			}
		}
		dir := filepath.Join(a.dir, "codex-profile")
		if os.MkdirAll(dir, 0700) != nil {
			fail(w, 500, "Не удалось подготовить профиль")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		session := &AuthSession{state: "waiting", expires: now().Add(15 * time.Minute), cancel: cancel}
		cmd := exec.CommandContext(ctx, a.codex, "login", "--device-auth", "-c", `cli_auth_credentials_store="file"`)
		env := aiEnvironment()
		for i, v := range env {
			if strings.HasPrefix(v, "CODEX_HOME=") {
				env = append(env[:i], env[i+1:]...)
				break
			}
		}
		cmd.Env = append(env, "CODEX_HOME="+dir)
		cmd.Stdout = session
		cmd.Stderr = session
		if cmd.Start() != nil {
			cancel()
			fail(w, 500, "Не удалось начать вход в Codex")
			return
		}
		a.authSession = session
		go func() {
			err := cmd.Wait()
			if err == nil {
				err = os.WriteFile(filepath.Join(dir, ".selected"), []byte("chatgpt"), 0600)
			}
			session.mu.Lock()
			if err == nil {
				session.state = "connected"
			} else {
				session.state = "stopped"
			}
			session.output = ""
			session.code = ""
			session.mu.Unlock()
			cancel()
			if err == nil {
				a.quotaWorker()
			}
		}()
	}
	if a.authSession == nil {
		reply(w, 200, map[string]any{"state": "idle", "uses_app_profile": strings.HasPrefix(a.codexHome(), a.dir+string(os.PathSeparator))})
		return
	}
	s := a.authSession
	s.mu.Lock()
	defer s.mu.Unlock()
	reply(w, 200, map[string]any{"state": s.state, "url": s.url, "code": s.code, "expires_at": s.expires})
}
