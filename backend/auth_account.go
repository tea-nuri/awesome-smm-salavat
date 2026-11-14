package main

import (
	"context"
	"encoding/json"
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
	mu                       sync.Mutex
	state, url, code, output string
	expires                  time.Time
	cancel                   context.CancelFunc
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

// Application credentials never fall back to the machine's Codex profile or keychain.
func (a *App) codexHome() string {
	dir, _ := filepath.Abs(filepath.Join(a.dir, "codex-profile"))
	return dir
}
func (a *App) chatGPTConnected() bool {
	dir := a.codexHome()
	if _, err := os.Stat(filepath.Join(dir, ".selected")); err != nil {
		return false
	}
	raw, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		return false
	}
	var auth struct {
		Mode   string `json:"auth_mode"`
		Tokens *struct {
			Access string `json:"access_token"`
		} `json:"tokens"`
	}
	return json.Unmarshal(raw, &auth) == nil && (auth.Mode == "chatgpt" || auth.Mode == "") && auth.Tokens != nil && auth.Tokens.Access != ""
}
func (a *App) aiEnvironment() []string {
	out := []string{}
	for _, v := range aiEnvironment() {
		if !strings.HasPrefix(v, "CODEX_HOME=") {
			out = append(out, v)
		}
	}
	return append(out, "CODEX_HOME="+a.codexHome())
}
func (a *App) authArguments() []string { return []string{"-c", `cli_auth_credentials_store="file"`} }
func (a *App) providerConfigured() bool {
	provider := a.state.Control.Provider
	if provider == "" || provider == "codex" {
		return a.chatGPTConnected()
	}
	cfg := a.state.Control.Providers[provider]
	if provider == "ollama" {
		return cfg.BaseURL != "" && cfg.Model != "" && (cfg.AuthMode != "bearer" || cfg.KeySet)
	}
	return cfg.KeySet && cfg.Model != ""
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
		if in.Action == "cancel" || in.Action == "logout" {
			if a.authSession != nil {
				a.authSession.cancel()
				a.authSession.mu.Lock()
				if a.authSession.state == "waiting" || a.authSession.state == "stopping" {
					a.authSession.state = "stopping"
				} else {
					a.authSession.state = "stopped"
				}
				a.authSession.mu.Unlock()
			}
			os.Remove(filepath.Join(a.codexHome(), ".selected"))
			if in.Action == "logout" {
				os.Remove(filepath.Join(a.codexHome(), "auth.json"))
			}
			a.mu.Lock()
			a.state.Control.Quota = nil
			a.persist()
			a.mu.Unlock()
			reply(w, 200, map[string]string{"state": "disconnected"})
			return
		}
		if in.Action != "start" {
			fail(w, 400, "Неизвестное действие")
			return
		}
		if a.authSession != nil {
			a.authSession.mu.Lock()
			active := a.authSession.state == "waiting" || a.authSession.state == "stopping"
			a.authSession.mu.Unlock()
			if active {
				fail(w, 409, "Предыдущий вход ещё завершается")
				return
			}
		}
		if a.chatGPTConnected() {
			reply(w, 200, map[string]string{"state": "connected"})
			return
		}
		dir := a.codexHome()
		if os.MkdirAll(dir, 0700) != nil {
			fail(w, 500, "Не удалось подготовить вход")
			return
		}
		// Any unfinished login is discarded; only an explicit successful login selects this profile.
		os.Remove(filepath.Join(dir, "auth.json"))
		os.Remove(filepath.Join(dir, ".selected"))
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		session := &AuthSession{state: "waiting", expires: now().Add(15 * time.Minute), cancel: cancel}
		args := append([]string{"login", "--device-auth"}, a.authArguments()...)
		cmd := exec.CommandContext(ctx, a.codex, args...)
		cmd.Env = a.aiEnvironment()
		cmd.Stdout = session
		cmd.Stderr = session
		if cmd.Start() != nil {
			cancel()
			fail(w, 500, "Не удалось начать вход в ChatGPT")
			return
		}
		a.authSession = session
		go func() {
			err := cmd.Wait()
			a.authMu.Lock()
			if err == nil && ctx.Err() == nil {
				err = os.WriteFile(filepath.Join(dir, ".selected"), []byte("chatgpt"), 0600)
			}
			connected := err == nil && ctx.Err() == nil && a.chatGPTConnected()
			if !connected {
				os.Remove(filepath.Join(dir, ".selected"))
				os.Remove(filepath.Join(dir, "auth.json"))
			}
			session.mu.Lock()
			if connected {
				session.state = "connected"
			} else {
				session.state = "stopped"
			}
			session.output = ""
			session.code = ""
			session.mu.Unlock()
			a.authMu.Unlock()
			cancel()
			if connected {
				a.quotaWorker()
			}
		}()
	} else if r.Method != "GET" {
		fail(w, 405, "Метод не поддерживается")
		return
	}
	if a.chatGPTConnected() {
		reply(w, 200, map[string]string{"state": "connected"})
		return
	}
	if a.authSession == nil {
		reply(w, 200, map[string]string{"state": "disconnected"})
		return
	}
	s := a.authSession
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.state
	if state == "connected" {
		state = "disconnected"
	}
	reply(w, 200, map[string]any{"state": state, "url": s.url, "code": s.code, "expires_at": s.expires})
}
