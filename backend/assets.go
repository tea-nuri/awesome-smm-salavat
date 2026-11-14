package main

import (
	"bytes"
	"errors"
	"image"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const maxImageBytes = 12 << 20
const maxTaskImages = 6

type TaskMedia struct {
	LogoAssetID string   `json:"logo_asset_id,omitempty"`
	StyleAssets []string `json:"style_assets,omitempty"`
	Attachments []string `json:"attachments,omitempty"`
	ImageMode   string   `json:"image_mode,omitempty"`
	ImageOnly   bool     `json:"image_only,omitempty"`
	TextOnly    bool     `json:"text_only,omitempty"`
	AspectRatio string   `json:"aspect_ratio,omitempty"`
}

type Asset struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	Name      string    `json:"name"`
	File      string    `json:"file"`
	CreatedAt time.Time `json:"created_at"`
}

func imageMode(_ string) string {
	// Legacy collage requests now use native image editing too.
	return "style"
}

func aspectRatio(ratio string) string {
	if ratio == "4:5" || ratio == "16:9" {
		return ratio
	}
	return "1:1"
}

func artworkSize(ratio string) (int, int) {
	switch aspectRatio(ratio) {
	case "4:5":
		return 1080, 1350
	case "16:9":
		return 1440, 810
	default:
		return 1080, 1080
	}
}

func (p *Post) applyMedia(media TaskMedia) {
	if media.LogoAssetID != "" {
		p.LogoAssetID = media.LogoAssetID
	}
	if media.StyleAssets != nil {
		p.StyleAssets = append([]string{}, media.StyleAssets...)
	}
	if media.Attachments != nil {
		p.Attachments = append([]string(nil), media.Attachments...)
	}
	if media.ImageMode != "" {
		p.ImageMode = imageMode(media.ImageMode)
	}
	p.ImageOnly = media.ImageOnly
	p.TextOnly = media.TextOnly
	if media.AspectRatio != "" {
		p.AspectRatio = aspectRatio(media.AspectRatio)
	}
}

func (a *App) validateTaskMedia(companyID string, media TaskMedia) error {
	if media.TextOnly && media.ImageOnly {
		return errors.New("Выберите доработку текста, изображения или всего материала")
	}
	if len(media.Attachments) > maxTaskImages {
		return errors.New("Можно приложить до 6 фотографий")
	}
	if media.ImageMode != "" && media.ImageMode != "style" && media.ImageMode != "collage" {
		return errors.New("Неизвестный способ обработки фото")
	}
	if media.AspectRatio != "" && media.AspectRatio != "1:1" && media.AspectRatio != "4:5" && media.AspectRatio != "16:9" {
		return errors.New("Выберите формат 1:1, 4:5 или 16:9")
	}
	seen := map[string]bool{}
	for _, key := range media.Attachments {
		asset := a.state.Assets[key]
		if asset == nil || asset.CompanyID != companyID || seen[key] {
			return errors.New("Фото недоступно для этой компании")
		}
		seen[key] = true
	}
	return a.validateBrandAssets(companyID, media.LogoAssetID, media.StyleAssets)
}

func (a *App) validateRevisionMedia(user identity, postID string, media TaskMedia) error {
	p := a.state.Posts[postID]
	if p == nil || !a.allowed(user, a.state.Companies[p.CompanyID]) {
		return errors.New("Публикация недоступна")
	}
	if media.TextOnly && (media.LogoAssetID != "" || media.StyleAssets != nil || len(media.Attachments) > 0 || (media.AspectRatio != "" && aspectRatio(media.AspectRatio) != aspectRatio(p.AspectRatio))) {
		return errors.New("Для новых фото или формата выберите доработку изображения")
	}
	if media.TextOnly && p.Image == "" {
		return errors.New("В материале ещё нет изображения, которое можно сохранить")
	}
	return a.validateTaskMedia(p.CompanyID, media)
}

func (a *App) taskImagePaths(p Post) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	paths := []string{}
	for _, key := range p.Attachments {
		asset := a.state.Assets[key]
		if asset != nil && asset.CompanyID == p.CompanyID {
			paths = append(paths, filepath.Join(a.dir, "uploads", asset.File))
		}
	}
	return paths
}

func (a *App) assetAPI(w http.ResponseWriter, r *http.Request, user identity, companyID string) {
	a.mu.Lock()
	c := a.state.Companies[companyID]
	if companyID == "" {
		c = a.active(user)
	}
	if r.Method == "GET" {
		key := strings.TrimPrefix(r.URL.Path, "/api/assets/")
		asset := a.state.Assets[key]
		if asset == nil || !a.allowed(user, a.state.Companies[asset.CompanyID]) {
			a.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		file := filepath.Join(a.dir, "uploads", asset.File)
		a.mu.Unlock()
		w.Header().Set("Cache-Control", "private, max-age=60")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeFile(w, r, file)
		return
	}
	if r.Method != "POST" || !a.allowed(user, c) {
		a.mu.Unlock()
		fail(w, 403, "Нет доступа к компании")
		return
	}
	companyID = c.ID
	a.mu.Unlock()
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImageBytes))
	if err != nil {
		fail(w, 400, "Фотография должна быть не больше 12 МБ")
		return
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") || config.Width < 64 || config.Height < 64 || config.Width > 12000 || config.Height > 12000 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		fail(w, 400, "Пришлите корректное фото JPG или PNG до 40 мегапикселей")
		return
	}
	ext := ".png"
	if format == "jpeg" {
		ext = ".jpg"
	}
	key := id()
	name, _ := url.PathUnescape(r.Header.Get("X-File-Name"))
	if name == "" {
		name = "Фото" + ext
	}
	asset := &Asset{ID: key, CompanyID: companyID, Name: truncate(filepath.Base(name), 100), File: key + ext, CreatedAt: now()}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.allowed(user, a.state.Companies[companyID]) {
		fail(w, 403, "Нет доступа")
		return
	}
	if err = os.MkdirAll(filepath.Join(a.dir, "uploads"), 0700); err != nil {
		fail(w, 500, "Не удалось сохранить фото")
		return
	}
	if err = os.WriteFile(filepath.Join(a.dir, "uploads", asset.File), data, 0600); err != nil {
		fail(w, 500, "Не удалось сохранить фото")
		return
	}
	if a.state.Assets == nil {
		a.state.Assets = map[string]*Asset{}
	}
	a.state.Assets[key] = asset
	a.persist()
	reply(w, 201, asset)
}

// A reply to a clarification keeps the source photos of that conversation.
// Called under a.mu; a new explicit photo selection always takes precedence.
func (a *App) clarificationMedia(companyID string, userID int64, media TaskMedia) TaskMedia {
	if media.Attachments != nil {
		return media
	}
	var last *Post
	for _, p := range a.state.Posts {
		if p.CompanyID == companyID && p.UserID == userID && (last == nil || p.CreatedAt.After(last.CreatedAt)) {
			last = p
		}
	}
	if last != nil && last.isConversation() {
		if len(last.Attachments) > 0 {
			media.Attachments = append([]string(nil), last.Attachments...)
		}
		if media.ImageMode == "" {
			media.ImageMode = last.ImageMode
		}
		if media.AspectRatio == "" {
			media.AspectRatio = last.AspectRatio
		}
		if media.LogoAssetID == "" {
			media.LogoAssetID = last.LogoAssetID
		}
		if media.StyleAssets == nil {
			media.StyleAssets = last.StyleAssets
		}
	}
	return media
}
