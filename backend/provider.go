package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func platformBrief(platform string) string {
	switch platform {
	case "vk":
		return "\nПлощадка публикации — ВКонтакте. Адаптируй текст для ленты сообщества: самостоятельный понятный хук, короткие абзацы, уместный призыв обсудить в комментариях. Без Telegram-разметки, ссылок на несуществующие кнопки и обещания custom emoji. Выбранный эмодзи-пак задаёт допустимые Unicode-символы.\n"
	case "max":
		return "\nПлощадка публикации — MAX. Подготовь компактный пост канала до 3500 символов: понятная первая строка, короткие абзацы, один призыв к действию. Без Telegram-разметки и несуществующих кнопок. Выбранный эмодзи-пак задаёт допустимые Unicode-символы.\n"
	}
	return ""
}

func (a *App) executeText(ctx context.Context, p Post, prompt, jobDir, outFile string) error {
	uid, err := a.beginUsage(p, "text")
	if err != nil {
		return err
	}
	a.mu.Lock()
	provider, model := "codex", ""
	for _, v := range a.state.Usage {
		if v.ID == uid {
			provider, model = v.Provider, v.Model
			break
		}
	}
	key := ""
	cfg := a.state.Control.Providers[provider]
	if provider == "deepseek" || provider == "gemini" || provider == "ollama" && cfg.AuthMode == "bearer" {
		key, _ = a.connectionSecret("ai", provider)
	}
	a.mu.Unlock()
	if provider == "deepseek" || provider == "gemini" {
		return a.executeRemoteText(ctx, p, prompt, outFile, uid, provider, model, key)
	}
	if provider == "ollama" {
		return a.executeOllama(ctx, p, prompt, outFile, uid, model, cfg.BaseURL, cfg.AuthMode, key)
	}
	args := []string{"exec", "--ignore-user-config", "--skip-git-repo-check", "--ephemeral", "-s", "read-only", "-c", "features.shell_tool=false", "--json", "-C", jobDir, "-o", outFile}
	if model != "" {
		args = append(args, "-m", model)
	}
	args = append(args, a.authArguments()...)
	for _, photo := range a.taskImagePaths(p) {
		args = append(args, "-i", photo)
	}
	args = append(args, "-")
	cmd := exec.CommandContext(ctx, a.codex, args...)
	cmd.Env = a.aiEnvironment()
	cmd.Stdin = strings.NewReader(prompt)
	var events strings.Builder
	cmd.Stdout = &events
	cmd.Stderr = io.Discard
	err = cmd.Run()
	a.finishUsage(uid, events.String(), err)
	if err != nil {
		return errors.New("ChatGPT не ответил за отведённое время или сессия недоступна")
	}
	return nil
}
func (a *App) executeOllama(ctx context.Context, p Post, prompt, outFile, uid, model, baseURL, authMode, key string) error {
	if model == "" || baseURL == "" || authMode == "bearer" && key == "" {
		err := errors.New("Проверьте адрес API, модель и авторизацию Ollama")
		a.finishUsage(uid, "", err)
		return err
	}
	body, _ := json.Marshal(map[string]any{"model": model, "prompt": prompt, "stream": false, "format": "json"})
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(baseURL, "/")+"/api/generate", bytes.NewReader(body))
	if err != nil {
		a.finishUsage(uid, "", err)
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if authMode == "bearer" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client := &http.Client{Timeout: 150 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		a.finishUsage(uid, "", err)
		return errors.New("Не удалось подключиться к серверу Ollama")
	}
	defer res.Body.Close()
	var result struct {
		Response string `json:"response"`
		Input    *int64 `json:"prompt_eval_count"`
		Output   *int64 `json:"eval_count"`
		Error    string `json:"error"`
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&result)
	if res.StatusCode != 200 || result.Response == "" || err != nil {
		err = errors.New("Сервер Ollama не вернул результат. Проверьте адрес, модель и авторизацию")
	}
	events := ""
	if result.Input != nil && result.Output != nil {
		event, _ := json.Marshal(map[string]any{"type": "turn.completed", "usage": map[string]int64{"input_tokens": *result.Input, "output_tokens": *result.Output}})
		events = string(event)
	}
	if err == nil {
		err = os.WriteFile(outFile, []byte(result.Response), 0600)
	}
	a.finishUsage(uid, events, err)
	return err
}
