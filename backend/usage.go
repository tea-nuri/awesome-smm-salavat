package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type LimitWindow struct {
	UsedPercent        float64 `json:"usedPercent"`
	WindowDurationMins int     `json:"windowDurationMins"`
	ResetsAt           int64   `json:"resetsAt"`
}
type AccountLimit struct {
	LimitID             string       `json:"limitId"`
	LimitName           string       `json:"limitName"`
	Primary             *LimitWindow `json:"primary"`
	Secondary           *LimitWindow `json:"secondary"`
	PlanType            string       `json:"planType"`
	SpendControlReached bool         `json:"spendControlReached"`
	Credits             struct {
		HasCredits bool   `json:"hasCredits"`
		Unlimited  bool   `json:"unlimited"`
		Balance    string `json:"balance"`
	} `json:"credits"`
}
type QuotaSnapshot struct {
	Limits map[string]AccountLimit `json:"limits"`
	At     time.Time               `json:"at"`
	Error  string                  `json:"error,omitempty"`
}
type UsageRecord struct {
	ID           string     `json:"id"`
	CompanyID    string     `json:"company_id"`
	PostID       string     `json:"post_id"`
	Revision     int        `json:"revision"`
	Kind         string     `json:"kind"`
	Provider     string     `json:"provider"`
	Model        string     `json:"model"`
	Status       string     `json:"status"`
	InputTokens  int64      `json:"input_tokens"`
	OutputTokens int64      `json:"output_tokens"`
	CachedTokens int64      `json:"cached_tokens"`
	Measured     bool       `json:"measured"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Error        string     `json:"error,omitempty"`
}

var customSkillID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
var modelName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:/-]{0,99}$`)

func aiEnvironment() []string {
	out := []string{}
	for _, key := range []string{"PATH", "HOME", "CODEX_HOME", "TMPDIR", "LANG", "USER", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		if value := os.Getenv(key); value != "" {
			out = append(out, key+"="+value)
		}
	}
	return out
}

// Queries only account limits. Account IDs, reset-credit identifiers and tokens
// returned by the account service never enter the application state or API.
func (a *App) readQuota(parent context.Context) (*QuotaSnapshot, error) {
	if !a.chatGPTConnected() {
		return nil, errors.New("Войдите в ChatGPT в разделе «Лимиты и доступы»")
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	args := append([]string{"app-server", "--stdio"}, a.authArguments()...)
	cmd := exec.CommandContext(ctx, a.codex, args...)
	cmd.Env = a.aiEnvironment()
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, errors.New("Не удалось проверить подписку")
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err = cmd.Start(); err != nil {
		return nil, errors.New("Codex недоступен")
	}
	defer func() { in.Close(); cancel(); cmd.Wait() }()
	enc := json.NewEncoder(in)
	if err = enc.Encode(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "awesome_smm_usage", "version": "1.0"}}}); err != nil {
		return nil, err
	}
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 2<<20)
	for scan.Scan() {
		var message struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(scan.Bytes(), &message) != nil {
			continue
		}
		if message.ID == 1 {
			if len(message.Error) > 0 {
				return nil, errors.New("Не удалось инициализировать проверку")
			}
			enc.Encode(map[string]any{"method": "initialized"})
			enc.Encode(map[string]any{"id": 2, "method": "account/rateLimits/read", "params": map[string]any{}})
		}
		if message.ID == 2 {
			if len(message.Error) > 0 {
				return nil, errors.New("Лимиты недоступны. Проверьте авторизацию Codex")
			}
			var result struct {
				ByID  map[string]AccountLimit `json:"rateLimitsByLimitId"`
				Limit AccountLimit            `json:"rateLimits"`
			}
			if json.Unmarshal(message.Result, &result) != nil {
				return nil, errors.New("Некорректный ответ о лимитах")
			}
			if len(result.ByID) == 0 {
				result.ByID = map[string]AccountLimit{}
				if result.Limit.LimitID != "" {
					result.ByID[result.Limit.LimitID] = result.Limit
				}
			}
			if len(result.ByID) == 0 {
				return nil, errors.New("Провайдер не предоставил лимиты подписки")
			}
			return &QuotaSnapshot{Limits: result.ByID, At: now()}, nil
		}
	}
	return nil, errors.New("Не удалось получить лимиты за 20 секунд")
}

func (a *App) beginUsage(p Post, kind string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initOperations()
	policy := a.companyOperations(p.CompanyID).Usage
	provider := a.state.Control.Provider
	if provider == "" || kind == "image" {
		provider = "codex"
	}
	if provider == "codex" && !a.chatGPTConnected() {
		return "", errors.New("Войдите в ChatGPT в разделе «Лимиты и доступы»")
	}
	if provider == "ollama" {
		cfg := a.state.Control.Providers[provider]
		if cfg.BaseURL == "" || cfg.Model == "" {
			return "", errors.New("Укажите адрес API и модель удалённого сервера Ollama")
		}
		if cfg.AuthMode == "bearer" && !cfg.KeySet {
			return "", errors.New("Добавьте ключ доступа к серверу Ollama")
		}
	}
	if (provider == "deepseek" || provider == "gemini") && !a.state.Control.Providers[provider].KeySet {
		return "", errors.New("Добавьте API-ключ в разделе «Лимиты и доступы»")
	}

	if a.state.Control.AIPaused || policy.Paused {
		return "", errors.New("Генерация приостановлена администратором")
	}
	calls, images := 0, 0
	var tokens int64
	unknownTokens := false
	current := now()
	for _, v := range a.state.Usage {
		if v.CompanyID == p.CompanyID && v.StartedAt.Year() == current.Year() && v.StartedAt.Month() == current.Month() {
			calls++
			if v.Kind == "image" {
				images++
			}
			if v.Provider != "codex" {
				tokens += v.InputTokens + v.OutputTokens
			}
			if v.Provider != "codex" && !v.Measured {
				unknownTokens = true
			}
		}
	}
	if policy.MonthlyCalls > 0 && calls >= policy.MonthlyCalls {
		return "", errors.New("Достигнут месячный лимит AI-вызовов компании")
	}
	if kind == "image" && policy.MonthlyImages > 0 && images >= policy.MonthlyImages {
		return "", errors.New("Достигнут месячный лимит изображений компании")
	}
	if provider != "codex" && policy.MonthlyTokens > 0 && tokens >= policy.MonthlyTokens {
		return "", errors.New("Достигнут порог учтённых токенов компании")
	}
	if provider != "codex" && policy.MonthlyTokens > 0 && unknownTokens {
		return "", errors.New("Есть вызовы с неизвестным расходом токенов. Для продолжения измените токен-порог и используйте лимит числа вызовов или расхода подписки")
	}
	threshold := policy.StopAtPercent
	if a.state.Control.StopAtPercent > 0 && (threshold == 0 || a.state.Control.StopAtPercent < threshold) {
		threshold = a.state.Control.StopAtPercent
	}
	q := a.state.Control.Quota
	if provider == "codex" && threshold > 0 {
		if q == nil || q.Error != "" || current.Sub(q.At) > 5*time.Minute {
			return "", errors.New("Для контроля расхода нужен свежий снимок лимитов подписки. Обновите его в разделе «Лимиты и доступы»")
		}
		for _, limit := range q.Limits {
			if limit.SpendControlReached {
				return "", errors.New("Провайдер остановил расход аккаунта")
			}
			for _, window := range []*LimitWindow{limit.Primary, limit.Secondary} {
				if window != nil && window.UsedPercent >= threshold && window.ResetsAt > current.Unix() {
					return "", errors.New("Достигнут установленный порог расхода подписки")
				}
			}
		}
	}
	v := &UsageRecord{ID: id(), CompanyID: p.CompanyID, PostID: p.ID, Revision: p.Revision, Kind: kind, Provider: provider, Model: a.providerModel(provider), Status: "running", StartedAt: current}
	a.state.Usage = append(a.state.Usage, v)
	a.persist()
	return v.ID, nil
}

func usageFromEvents(events string) (input, output, cached int64, measured bool) {
	for _, line := range strings.Split(events, "\n") {
		var e struct {
			Type  string `json:"type"`
			Usage *struct {
				Input  *int64 `json:"input_tokens"`
				Output *int64 `json:"output_tokens"`
				Cached int64  `json:"cached_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(line), &e) == nil && e.Type == "turn.completed" && e.Usage != nil && e.Usage.Input != nil && e.Usage.Output != nil {
			input += *e.Usage.Input
			output += *e.Usage.Output
			cached += e.Usage.Cached
			measured = true
		}
	}
	return
}
func (a *App) finishUsage(uid, events string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, v := range a.state.Usage {
		if v.ID == uid {
			v.InputTokens, v.OutputTokens, v.CachedTokens, v.Measured = usageFromEvents(events)
			v.Status = "completed"
			if err != nil {
				v.Status = "failed"
				v.Error = "Вызов завершился ошибкой; расход при отсутствии статистики неизвестен"
			}
			t := now()
			v.FinishedAt = &t
			a.persist()
			return
		}
	}
}
func (a *App) modelArguments(image bool) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !image && a.providerModel("codex") != "" {
		return []string{"-m", a.providerModel("codex")}
	}
	return nil
}
func (a *App) quotaWorker() {
	if !a.chatGPTConnected() {
		a.mu.Lock()
		if a.state.Control.Quota != nil {
			a.state.Control.Quota = nil
			a.persist()
		}
		a.mu.Unlock()
		return
	}

	q, err := a.readQuota(context.Background())
	a.mu.Lock()
	if !a.chatGPTConnected() {
		a.state.Control.Quota = nil
		a.persist()
		a.mu.Unlock()
		return
	}
	if err == nil {
		a.state.Control.Quota = q
	} else {
		previousError := ""
		if a.state.Control.Quota != nil {
			previousError = a.state.Control.Quota.Error
		} else {
			a.state.Control.Quota = &QuotaSnapshot{Limits: map[string]AccountLimit{}}
		}
		a.state.Control.Quota.Error = err.Error()
		if previousError != err.Error() {
			a.adminNotify("", "Не удалось проверить лимиты подписки. Откройте «Лимиты и доступы» и проверьте авторизацию.", 0)
		}
	}
	a.persist()
	a.mu.Unlock()
}

func (a *App) updateControl(w http.ResponseWriter, r *http.Request, u identity) {
	if !u.Admin {
		fail(w, 403, "Только администратор")
		return
	}
	var in struct {
		Admins        []int64 `json:"admins"`
		AIPaused      bool    `json:"ai_paused"`
		Provider      string  `json:"provider"`
		Model         string  `json:"model"`
		StopAtPercent float64 `json:"stop_at_percent"`
	}
	if decode(r, &in) != nil || in.StopAtPercent < 0 || in.StopAtPercent > 100 || (in.Provider != "" && in.Provider != "codex" && in.Provider != "ollama" && in.Provider != "deepseek" && in.Provider != "gemini") || (in.Model != "" && !modelName.MatchString(in.Model)) {
		fail(w, 400, "Проверьте настройки AI")
		return
	}
	includesSelf := false
	for _, uid := range in.Admins {
		if uid <= 0 {
			fail(w, 400, "Укажите числовые Telegram ID")
			return
		}
		if uid == u.ID {
			includesSelf = true
		}
	}
	if !includesSelf {
		fail(w, 400, "Сохраните свой доступ администратора")
		return
	}
	a.state.Control.Admins = in.Admins
	a.state.Control.DisabledAdmins = nil
	for uid := range a.admins {
		found := false
		for _, chosen := range in.Admins {
			if uid == chosen {
				found = true
			}
		}
		if !found {
			a.state.Control.DisabledAdmins = append(a.state.Control.DisabledAdmins, uid)
		}
	}
	provider := in.Provider
	if provider == "" {
		provider = "codex"
	}
	a.state.Control.AIPaused, a.state.Control.Provider, a.state.Control.StopAtPercent = in.AIPaused, provider, in.StopAtPercent
	cfg := a.state.Control.Providers[provider]
	if cfg.Model == "" && in.Model != "" {
		cfg.Model = in.Model
		a.state.Control.Providers[provider] = cfg
	}
	a.state.Control.Model = cfg.Model
	a.event("", "Доступы и настройки AI изменил пользователь "+strconv.FormatInt(u.ID, 10), "access")
	a.persist()
	reply(w, 200, a.state.Control)
}
