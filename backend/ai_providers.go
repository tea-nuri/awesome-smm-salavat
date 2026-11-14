package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func (a *App) providerModel(provider string) string {
	if cfg, ok := a.state.Control.Providers[provider]; ok && cfg.Model != "" {
		return cfg.Model
	}
	if provider == a.state.Control.Provider || provider == "codex" && a.state.Control.Provider == "" {
		return a.state.Control.Model
	}
	return ""
}
func (a *App) providerConfigAPI(w http.ResponseWriter, r *http.Request, u identity, provider string) {
	if !u.Admin || r.Method != "PUT" {
		fail(w, 403, "Только администратор")
		return
	}
	if provider != "deepseek" && provider != "gemini" && provider != "ollama" && provider != "codex" {
		fail(w, 400, "Неизвестный исполнитель")
		return
	}
	var in struct {
		Model    string `json:"model"`
		BaseURL  string `json:"base_url"`
		AuthMode string `json:"auth_mode"`
		Key      string `json:"api_key"`
		Clear    bool   `json:"clear_key"`
	}
	if decode(r, &in) != nil || (in.Model != "" && !modelName.MatchString(in.Model)) || len(in.Key) > 1024 || strings.ContainsAny(in.Key, "\r\n\x00") {
		fail(w, 400, "Проверьте модель и ключ")
		return
	}
	cfg := a.state.Control.Providers[provider]
	cfg.Model = in.Model
	if provider == "ollama" {
		baseURL, err := ollamaBaseURL(in.BaseURL)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		mode := in.AuthMode
		if mode == "" {
			mode = "none"
		}
		if mode != "none" && mode != "bearer" {
			fail(w, 400, "Выберите способ авторизации Ollama")
			return
		}
		cfg.BaseURL, cfg.AuthMode = baseURL, mode
	}
	if (provider == "deepseek" || provider == "gemini" || provider == "ollama") && (in.Key != "" || in.Clear) {
		key := strings.TrimSpace(in.Key)
		if in.Clear {
			key = ""
		}
		if err := a.setConnectionSecret("ai", provider, key); err != nil {
			fail(w, 500, "Не удалось сохранить ключ")
			return
		}
		cfg.KeySet = key != ""
	}
	a.state.Control.Providers[provider] = cfg
	active := a.state.Control.Provider
	if active == "" {
		active = "codex"
	}
	if active == provider {
		a.state.Control.Model = cfg.Model
	}
	a.event("", "Сохранены настройки AI: "+provider, "settings")
	a.persist()
	reply(w, 200, cfg)
}

// DeepSeek and Gemini expose official OpenAI-compatible chat endpoints.
// Keys remain encrypted on disk and are never returned to the browser.
func (a *App) executeRemoteText(ctx context.Context, p Post, prompt, outFile, uid, provider, model, key string) error {
	endpoint := "https://api.deepseek.com/chat/completions"
	if provider == "gemini" {
		endpoint = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
	}
	if key == "" || model == "" {
		err := errors.New("Добавьте API-ключ и модель в разделе «Лимиты и доступы»")
		a.finishUsage(uid, "", err)
		return err
	}
	messages := []map[string]any{{"role": "user", "content": prompt}}
	// Input photos are passed as references to the text model as well as the image pipeline.
	content := []map[string]any{{"type": "text", "text": prompt}}
	for _, path := range a.taskImagePaths(p) {
		image, err := os.ReadFile(path)
		if err != nil || len(image) > 12<<20 {
			continue
		}
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]string{"url": imageDataURL(image)}})
	}
	if len(content) > 1 {
		messages[0]["content"] = content
	}
	body, _ := json.Marshal(map[string]any{"model": model, "messages": messages, "stream": false, "response_format": map[string]string{"type": "json_object"}})
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(body))
	if err != nil {
		a.finishUsage(uid, "", err)
		return errors.New("Не удалось подготовить запрос AI")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 150 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		a.finishUsage(uid, "", err)
		return errors.New("AI не ответил за отведённое время")
	}
	defer res.Body.Close()
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			Input   *int64 `json:"prompt_tokens"`
			Output  *int64 `json:"completion_tokens"`
			Hit     int64  `json:"prompt_cache_hit_tokens"`
			Details struct {
				Cached int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&result)
	var callErr error
	if res.StatusCode < 200 || res.StatusCode >= 300 || decodeErr != nil || len(result.Choices) == 0 || result.Choices[0].Message.Content == "" {
		callErr = errors.New("AI не вернул результат. Проверьте ключ, модель и доступный лимит провайдера")
	}
	events := ""
	if result.Usage != nil && result.Usage.Input != nil && result.Usage.Output != nil {
		cached := result.Usage.Details.Cached
		if result.Usage.Hit > cached {
			cached = result.Usage.Hit
		}
		raw, _ := json.Marshal(map[string]any{"type": "turn.completed", "usage": map[string]int64{"input_tokens": *result.Usage.Input, "output_tokens": *result.Usage.Output, "cached_input_tokens": cached}})
		events = string(raw)
	}
	if callErr == nil {
		callErr = os.WriteFile(outFile, []byte(result.Choices[0].Message.Content), 0600)
	}
	a.finishUsage(uid, events, callErr)
	return callErr
}

func imageDataURL(raw []byte) string {
	return "data:" + http.DetectContentType(raw) + ";base64," + base64.StdEncoding.EncodeToString(raw)
}

// Explicit administrator configuration supports remote private-network endpoints as well as HTTPS gateways.
func ollamaBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	u, err := url.Parse(value)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("Укажите адрес API Ollama: https://ollama.example.ru или http://IP:11434")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	for _, suffix := range []string{"/api/generate", "/api/chat", "/api"} {
		if strings.HasSuffix(u.Path, suffix) {
			u.Path = strings.TrimSuffix(u.Path, suffix)
			break
		}
	}
	u.RawPath = ""
	return strings.TrimRight(u.String(), "/"), nil
}
