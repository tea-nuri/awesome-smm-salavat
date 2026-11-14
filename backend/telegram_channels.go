package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ChannelPost struct {
	ViewsApproximate    bool      `json:"views_approximate"`
	CommentsApproximate bool      `json:"comments_approximate"`
	ID                  int64     `json:"id"`
	URL                 string    `json:"url"`
	Text                string    `json:"text"`
	Date                time.Time `json:"date"`
	Views               *int64    `json:"views"`
	Reactions           *int64    `json:"reactions"`
	Comments            *int64    `json:"comments"`
	Approximate         bool      `json:"approximate"`
	Source              string    `json:"source"`
}
type ChannelSnapshot struct {
	Name  string        `json:"name"`
	Posts []ChannelPost `json:"posts"`
}

var telegramUsername = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]{3,31}$`)

func channelUsername(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "@") {
		value = strings.TrimPrefix(value, "@")
	} else if strings.Contains(value, "/") {
		if !strings.Contains(value, "://") {
			value = "https://" + value
		}
		u, err := url.Parse(value)
		if err != nil || u.Scheme != "https" || (u.Host != "t.me" && u.Host != "telegram.me") || u.User != nil {
			return "", errors.New("Укажите @имя или ссылку https://t.me/имя публичного канала")
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) > 1 && parts[0] == "s" {
			parts = parts[1:]
		}
		value = parts[0]
	}
	if !telegramUsername.MatchString(value) || value == "share" || value == "login" || value == "addemoji" {
		return "", errors.New("Для закрытого канала загрузите JSON-выгрузку Telegram Desktop")
	}
	return strings.ToLower(value), nil
}
func (a *App) parseChannel(ctx context.Context, raw []byte, format string) (ChannelSnapshot, error) {
	command := exec.CommandContext(ctx, a.python, filepath.Join(filepath.Dir(a.renderScript), "telegram_channels.py"), format)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=en_US.UTF-8"}
	command.Stdin = bytes.NewReader(raw)
	out, err := command.Output()
	var result ChannelSnapshot
	if err != nil || json.Unmarshal(out, &result) != nil {
		return result, errors.New("Не удалось прочитать данные Telegram. Нужна публичная лента или JSON-выгрузка Telegram Desktop")
	}
	return result, nil
}
func (a *App) fetchChannel(parent context.Context, address string) (ChannelSnapshot, error) {
	username, err := channelUsername(address)
	if err != nil {
		return ChannelSnapshot{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 60*time.Second)
	defer cancel()
	client := a.sourceClient
	if client == nil {
		client = websiteClient()
	}
	result := ChannelSnapshot{Posts: []ChannelPost{}}
	seen := map[int64]bool{}
	before := int64(0)
	for page := 0; page < 3; page++ {
		address := "https://t.me/s/" + username
		if before > 0 {
			address += "?before=" + strconv.FormatInt(before, 10)
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", address, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; ContentStudio/1.0)")
		res, err := client.Do(req)
		if err != nil {
			return result, errors.New("Telegram не ответил. Попробуйте обновить канал позже")
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, (10<<20)+1))
		res.Body.Close()
		if res.StatusCode != 200 || readErr != nil || len(raw) > 10<<20 {
			return result, errors.New("Не удалось загрузить публичную ленту Telegram")
		}
		parsed, err := a.parseChannel(ctx, raw, "html")
		if err != nil {
			return result, err
		}
		if result.Name == "" {
			result.Name = parsed.Name
		}
		added := 0
		oldest := now()
		for _, p := range parsed.Posts {
			if before == 0 || p.ID < before {
				before = p.ID
			}
			if p.Date.Before(oldest) {
				oldest = p.Date
			}
			if !seen[p.ID] {
				result.Posts = append(result.Posts, p)
				seen[p.ID] = true
				added++
			}
		}
		if added == 0 || oldest.Before(now().AddDate(0, 0, -90)) {
			break
		}
	}
	if len(result.Posts) == 0 {
		return result, errors.New("Публичные посты недоступны. Загрузите JSON-выгрузку канала")
	}
	sort.Slice(result.Posts, func(i, j int) bool { return result.Posts[i].Date.After(result.Posts[j].Date) })
	return result, nil
}
func (a *App) storeChannelSnapshot(v *Competitor, snapshot ChannelSnapshot, err error) {
	t := now()
	v.LastAttemptAt = &t
	if err != nil {
		v.Error = err.Error()
		return
	}
	v.Error = ""
	v.ScannedAt = &t
	v.ChangedAt = &t
	v.Posts = snapshot.Posts
	v.Text = ""
	v.PreviousText = ""
	if snapshot.Name != "" {
		v.Name = snapshot.Name
	}
}
func (a *App) channelAPI(w http.ResponseWriter, r *http.Request, u identity, cid string, parts []string) {
	if !a.managing(u, cid) {
		fail(w, 403, "Только владелец компании")
		return
	}
	ops := a.companyOperations(cid)
	if len(parts) == 4 && parts[3] == "import" && r.Method == "POST" {
		raw, err := io.ReadAll(io.LimitReader(r.Body, (10<<20)+1))
		if err != nil || len(raw) > 10<<20 {
			fail(w, 400, "JSON должен быть до 10 МБ")
			return
		}
		a.mu.Unlock()
		snapshot, err := a.parseChannel(r.Context(), raw, "export")
		a.mu.Lock()
		if !a.managing(a.user(u.ID), cid) {
			fail(w, 403, "Доступ отозван")
			return
		}
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		if len(ops.Competitors) >= 12 {
			fail(w, 400, "Не более 12 каналов")
			return
		}
		for i := range snapshot.Posts {
			p := &snapshot.Posts[i]
			u, err := url.Parse(p.URL)
			if err != nil || u.Scheme != "https" || u.Host != "t.me" || u.User != nil {
				p.URL = ""
			}
		}
		v := &Competitor{ID: id(), Name: truncate(snapshot.Name, 120), Imported: true}
		a.storeChannelSnapshot(v, snapshot, nil)
		ops.Competitors = append(ops.Competitors, v)
		a.persist()
		reply(w, 201, v)
		return
	}
	if len(parts) == 3 && r.Method == "POST" {
		var in struct{ Name, URL string }
		if decode(r, &in) != nil || len(ops.Competitors) >= 12 {
			fail(w, 400, "Не более 12 каналов")
			return
		}
		username, err := channelUsername(in.URL)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		for _, v := range ops.Competitors {
			if v.Username == username {
				reply(w, 200, v)
				return
			}
		}
		name := truncate(strings.TrimSpace(in.Name), 120)
		if name == "" {
			name = "@" + username
		}
		v := &Competitor{ID: id(), Name: name, URL: "https://t.me/" + username, Username: username}
		ops.Competitors = append(ops.Competitors, v)
		a.persist()
		reply(w, 201, v)
		return
	}
	var selected *Competitor
	if len(parts) >= 4 {
		for _, v := range ops.Competitors {
			if v.ID == parts[3] {
				selected = v
			}
		}
	}
	if selected == nil {
		fail(w, 404, "Канал не найден")
		return
	}
	if len(parts) == 4 && r.Method == "DELETE" {
		for i, v := range ops.Competitors {
			if v.ID == selected.ID {
				ops.Competitors = append(ops.Competitors[:i], ops.Competitors[i+1:]...)
				break
			}
		}
		a.persist()
		reply(w, 200, map[string]bool{"ok": true})
		return
	}
	if len(parts) == 5 && parts[4] == "scan" && r.Method == "POST" && !selected.Imported {
		address := selected.URL
		a.mu.Unlock()
		snapshot, err := a.fetchChannel(r.Context(), address)
		a.mu.Lock()
		if !a.managing(a.user(u.ID), cid) {
			fail(w, 403, "Доступ отозван")
			return
		}
		a.storeChannelSnapshot(selected, snapshot, err)
		a.persist()
		if err != nil {
			fail(w, 502, err.Error())
			return
		}
		reply(w, 200, selected)
		return
	}
	fail(w, 405, "Метод не поддерживается")
}
func channelEvidence(channels []*Competitor) []map[string]any {
	result := []map[string]any{}
	for _, channel := range channels {
		if channel.Username == "" && !channel.Imported {
			continue
		}
		posts := []ChannelPost{}
		for _, p := range channel.Posts {
			if p.Date.Before(now().AddDate(0, 0, -90)) {
				continue
			}
			p.Text = truncate(p.Text, 1500)
			posts = append(posts, p)
		}
		recent := append([]ChannelPost{}, posts...)
		sort.Slice(recent, func(i, j int) bool { return recent[i].Date.After(recent[j].Date) })
		sort.Slice(posts, func(i, j int) bool {
			if posts[i].Reactions == nil {
				return false
			}
			if posts[j].Reactions == nil {
				return true
			}
			return *posts[i].Reactions > *posts[j].Reactions
		})
		if len(posts) > 8 {
			posts = posts[:8]
		}
		selected := map[int64]bool{}
		for _, p := range posts {
			selected[p.ID] = true
		}
		for _, p := range recent {
			if len(posts) >= 12 {
				break
			}
			if !selected[p.ID] {
				posts = append(posts, p)
				selected[p.ID] = true
			}
		}
		var sum int64
		measured := 0
		for _, p := range channel.Posts {
			if p.Reactions != nil {
				sum += *p.Reactions
				measured++
			}
		}
		baseline := float64(0)
		if measured > 0 {
			baseline = float64(sum) / float64(measured)
		}
		result = append(result, map[string]any{"name": channel.Name, "url": channel.URL, "scanned_at": channel.ScannedAt, "sample_posts": len(channel.Posts), "mean_reactions_in_sample": baseline, "posts_ranked_by_reactions": posts, "error": channel.Error})
	}
	return result
}
func (a *App) refreshOwnChannel(w http.ResponseWriter, r *http.Request, u identity, cid string) {
	if r.Method != "POST" || !a.managing(u, cid) {
		fail(w, 403, "Только владелец компании")
		return
	}
	address := a.state.Companies[cid].Channel
	a.mu.Unlock()
	snapshot, err := a.fetchChannel(r.Context(), address)
	a.mu.Lock()
	if !a.managing(a.user(u.ID), cid) {
		fail(w, 403, "Доступ отозван")
		return
	}
	if err != nil {
		fail(w, 502, err.Error())
		return
	}
	imported := 0
	for _, v := range snapshot.Posts {
		for _, p := range a.state.Posts {
			if p.CompanyID != cid || p.Status != "published" || (p.Platform != "" && p.Platform != "telegram") || p.MessageID != v.ID {
				continue
			}
			if v.Approximate {
				v.Reactions = nil
			}
			if v.CommentsApproximate {
				v.Comments = nil
			}
			if v.Reactions == nil && v.Comments == nil {
				continue
			} // Rounded public counters must not overwrite exact own-post metrics.
			if a.storeMetric(cid, MetricRecord{PostID: p.ID, Date: now().Format("2006-01-02"), Reactions: v.Reactions, Comments: v.Comments, Source: "telegram_preview"}) == nil {
				imported++
			}
		}
	}
	a.persist()
	reply(w, 200, map[string]any{"imported": imported, "note": fmt.Sprintf("Найдено публичных постов: %d. Округлённые счётчики не заменяют точные показатели.", len(snapshot.Posts))})
}
