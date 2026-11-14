package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type KnowledgeSource struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	URL       string    `json:"url,omitempty"`
	File      string    `json:"file,omitempty"`
	Text      string    `json:"text"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type SourceView struct {
	ID         string    `json:"id"`
	CompanyID  string    `json:"company_id"`
	Name       string    `json:"name"`
	Kind       string    `json:"kind"`
	URL        string    `json:"url,omitempty"`
	Characters int       `json:"characters"`
	Preview    string    `json:"preview"`
	Enabled    bool      `json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
}

func sourceView(s *KnowledgeSource) SourceView {
	return SourceView{s.ID, s.CompanyID, s.Name, s.Kind, s.URL, len([]rune(s.Text)), truncate(s.Text, 600), s.Enabled, s.CreatedAt}
}

func publicWebsiteURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || (u.Port() != "" && u.Port() != "80" && u.Port() != "443") {
		return nil, errors.New("Укажите публичную страницу http:// или https://")
	}
	if ip, e := netip.ParseAddr(u.Hostname()); e == nil && !publicSourceIP(ip) {
		return nil, errors.New("Внутренние сетевые адреса недоступны для импорта")
	}
	u.Fragment = ""
	return u, nil
}

func publicSourceIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}

func sourceDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, errors.New("Адрес сайта не найден")
	}
	for _, ip := range ips {
		if !publicSourceIP(ip) {
			return nil, errors.New("Сайт указывает на внутренний сетевой адрес")
		}
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	for _, ip := range ips {
		conn, e := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return conn, nil
		}
		err = e
	}
	return nil, err
}

func websiteClient() *http.Client {
	return &http.Client{Timeout: 55 * time.Second, Transport: &http.Transport{DisableCompression: true, DialContext: sourceDial, ResponseHeaderTimeout: 12 * time.Second, TLSHandshakeTimeout: 8 * time.Second}, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 4 {
			return errors.New("Слишком много перенаправлений сайта")
		}
		_, err := publicWebsiteURL(r.URL.String())
		return err
	}}
}

func (a *App) extractSource(ctx context.Context, file, kind, encoding string) (string, string, error) {
	command := exec.CommandContext(ctx, a.python, filepath.Join(filepath.Dir(a.renderScript), "extract_knowledge.py"), file, kind, "--encoding", encoding)
	for _, key := range []string{"PATH", "TMPDIR", "LANG"} {
		if v := os.Getenv(key); v != "" {
			command.Env = append(command.Env, key+"="+v)
		}
	}
	data, err := command.Output()
	var result struct {
		Text  string `json:"text"`
		Title string `json:"title"`
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &result) != nil {
		return "", "", errors.New("Не удалось прочитать источник")
	}
	if err != nil || result.Text == "" {
		if result.Error != "" {
			return "", "", errors.New(result.Error)
		}
		return "", "", errors.New("Не удалось извлечь текст из источника")
	}
	return truncate(result.Text, 60000), result.Title, nil
}

func (a *App) sourceAPI(w http.ResponseWriter, r *http.Request, user identity, companyID, sourceID string) {
	a.mu.Lock()
	c := a.state.Companies[companyID]
	if c == nil || (!user.Admin && c.OwnerID != user.ID) {
		a.mu.Unlock()
		fail(w, 403, "Источники доступны владельцу компании")
		return
	}
	if sourceID != "" {
		s := a.state.Sources[sourceID]
		if s == nil || s.CompanyID != companyID {
			a.mu.Unlock()
			fail(w, 404, "Источник не найден")
			return
		}
		if r.Method != "PATCH" {
			a.mu.Unlock()
			fail(w, 405, "Используйте настройку источника")
			return
		}
		var in struct {
			Enabled *bool `json:"enabled"`
		}
		if decode(r, &in) != nil || in.Enabled == nil {
			a.mu.Unlock()
			fail(w, 400, "Укажите, использовать ли источник")
			return
		}
		s.Enabled = *in.Enabled
		a.persist()
		view := sourceView(s)
		a.mu.Unlock()
		reply(w, 200, view)
		return
	}
	a.mu.Unlock()
	if r.Method != "POST" {
		fail(w, 405, "Загрузите файл или ссылку")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	s := &KnowledgeSource{ID: id(), CompanyID: companyID, Enabled: true, CreatedAt: now()}
	directory := filepath.Join(a.dir, "sources")
	if os.MkdirAll(directory, 0700) != nil {
		fail(w, 500, "Не удалось подготовить хранилище")
		return
	}
	rawFile := filepath.Join(directory, s.ID)
	stored := false
	defer func() {
		if !stored {
			os.Remove(rawFile)
		}
	}()
	encoding := ""
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, 51<<20)
		if r.ParseMultipartForm(8<<20) != nil {
			fail(w, 400, "Загрузите файл до 50 МБ")
			return
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			fail(w, 400, "Выберите документ")
			return
		}
		defer file.Close()
		s.Name = truncate(filepath.Base(header.Filename), 200)
		s.Kind = strings.TrimPrefix(strings.ToLower(filepath.Ext(s.Name)), ".")
		if s.Kind != "pdf" && s.Kind != "docx" && s.Kind != "txt" && s.Kind != "md" {
			fail(w, 400, "Поддерживаются PDF, DOCX, TXT и MD")
			return
		}
		data, e := io.ReadAll(io.LimitReader(file, (50<<20)+1))
		if e != nil || len(data) > 50<<20 {
			fail(w, 400, "Файл должен быть до 50 МБ")
			return
		}
		if os.WriteFile(rawFile, data, 0600) != nil {
			fail(w, 500, "Не удалось сохранить документ")
			return
		}
	} else {
		var in struct {
			URL string `json:"url"`
		}
		if decode(r, &in) != nil {
			fail(w, 400, "Укажите ссылку на сайт")
			return
		}
		u, err := publicWebsiteURL(in.URL)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		client := a.sourceClient
		if client == nil {
			client = websiteClient()
		}
		request, _ := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
		request.Header.Set("User-Agent", "ContentStudio/1.0 (company knowledge import)")
		request.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.8")
		response, e := client.Do(request)
		if e != nil && ctx.Err() == nil {
			// A public GET is safe to retry after a transient connection failure.
			response, e = client.Do(request.Clone(ctx))
		}
		if e != nil {
			fail(w, 400, "Сайт недоступен или не разрешает загрузку страницы")
			return
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			fail(w, 400, "Сайт не отдал страницу. Проверьте ссылку")
			return
		}
		mediaType, params, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
		encoding = params["charset"]
		s.Kind = "html"
		if mediaType == "application/pdf" {
			s.Kind = "pdf"
		} else if mediaType != "text/html" && mediaType != "application/xhtml+xml" && mediaType != "text/plain" {
			fail(w, 400, "По ссылке нет текстовой страницы или PDF")
			return
		}
		data, e := io.ReadAll(io.LimitReader(response.Body, (10<<20)+1))
		if e != nil {
			fail(w, 400, "Не удалось полностью загрузить страницу. Повторите импорт")
			return
		}
		if len(data) > 10<<20 {
			fail(w, 400, "Страница слишком большая для импорта")
			return
		}
		if os.WriteFile(rawFile, data, 0600) != nil {
			fail(w, 500, "Не удалось сохранить страницу")
			return
		}
		s.URL = u.String()
		s.Name = u.Hostname()
	}
	text, title, err := a.extractSource(ctx, rawFile, s.Kind, encoding)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	s.Text = text
	s.File = s.ID
	if s.URL != "" {
		s.Kind = "website"
		if title != "" {
			s.Name = title
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c = a.state.Companies[companyID]
	if c == nil || (!user.Admin && c.OwnerID != user.ID) {
		fail(w, 403, "Нет доступа")
		return
	}
	if a.state.Sources == nil {
		a.state.Sources = map[string]*KnowledgeSource{}
	}
	if s.URL != "" {
		for key, old := range a.state.Sources {
			if old.CompanyID == companyID && old.URL == s.URL {
				s.ID = key
				s.CreatedAt = old.CreatedAt
				break
			}
		}
	}
	a.state.Sources[s.ID] = s
	stored = true
	a.event(companyID, "Добавлен источник знаний: "+s.Name, "knowledge")
	a.persist()
	reply(w, 201, sourceView(s))
}

func (a *App) companyKnowledge(companyID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	sources := []*KnowledgeSource{}
	for _, s := range a.state.Sources {
		if s.CompanyID == companyID && s.Enabled {
			sources = append(sources, s)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].CreatedAt.Before(sources[j].CreatedAt) })
	var text strings.Builder
	remaining := 100000
	for _, s := range sources {
		if remaining <= 0 {
			break
		}
		part := truncate(s.Text, remaining)
		remaining -= len([]rune(part))
		text.WriteString("\n\nИСТОЧНИК: " + s.Name + " " + s.URL + "\n" + part)
	}
	return text.String()
}
