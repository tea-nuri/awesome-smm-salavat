package main

import (
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

type CustomEmoji struct {
	Emoji string `json:"emoji"`
	ID    string `json:"custom_emoji_id"`
}

type BrandSettings struct {
	LogoAssetID string        `json:"logo_asset_id,omitempty"`
	StyleAssets []string      `json:"style_assets,omitempty"`
	StyleNotes  string        `json:"style_notes,omitempty"`
	EmojiSource string        `json:"emoji_source,omitempty"`
	EmojiPack   string        `json:"emoji_pack,omitempty"`
	EmojiTitle  string        `json:"emoji_title,omitempty"`
	EmojiItems  []CustomEmoji `json:"emoji_items,omitempty"`
}

var packNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
var emojiIDPattern = regexp.MustCompile(`^[0-9]+$`)

func emojiPackName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "t.me/") || strings.HasPrefix(value, "telegram.me/") {
		value = "https://" + value
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.User != nil || u.Port() != "" {
			return "", errors.New("Вставьте ссылку t.me/addemoji/название или имя эмодзи-пака")
		}
		switch {
		case u.Scheme == "tg" && u.Host == "addemoji":
			value = u.Query().Get("set")
		case u.Scheme == "https" && (u.Host == "t.me" || u.Host == "telegram.me"):
			parts := strings.Split(strings.Trim(u.Path, "/"), "/")
			if len(parts) != 2 || parts[0] != "addemoji" {
				return "", errors.New("Нужна ссылка на эмодзи-пак: t.me/addemoji/название")
			}
			value = parts[1]
		default:
			return "", errors.New("Нужна ссылка на Telegram-эмодзи, а не сторонний сайт")
		}
	}
	if !packNamePattern.MatchString(value) {
		return "", errors.New("Проверьте имя эмодзи-пака")
	}
	return value, nil
}

func (a *App) resolveEmojiPack(value string) (string, string, []CustomEmoji, error) {
	name, err := emojiPackName(value)
	if err != nil {
		return "", "", nil, err
	}
	var set struct {
		Name        string `json:"name"`
		Title       string `json:"title"`
		StickerType string `json:"sticker_type"`
		Stickers    []struct {
			Emoji string `json:"emoji"`
			ID    string `json:"custom_emoji_id"`
		} `json:"stickers"`
	}
	if a.tg("getStickerSet", map[string]string{"name": name}, &set) != nil {
		return "", "", nil, errors.New("Не удалось загрузить эмодзи-пак. Проверьте ссылку и доступность Telegram")
	}
	if set.StickerType != "custom_emoji" {
		return "", "", nil, errors.New("Это набор стикеров. Выберите набор пользовательских эмодзи")
	}
	items, seen := []CustomEmoji{}, map[string]bool{}
	for _, sticker := range set.Stickers {
		if sticker.Emoji != "" && len([]rune(sticker.Emoji)) <= 12 && emojiIDPattern.MatchString(sticker.ID) && !seen[sticker.Emoji] {
			items = append(items, CustomEmoji{Emoji: sticker.Emoji, ID: sticker.ID})
			seen[sticker.Emoji] = true
		}
		if len(items) == 200 {
			break
		}
	}
	if len(items) == 0 {
		return "", "", nil, errors.New("В наборе не найдены доступные эмодзи")
	}
	return "https://t.me/addemoji/" + name, set.Title, items, nil
}

func (a *App) validateBrandAssets(companyID, logo string, styles []string) error {
	if len(styles) > 8 {
		return errors.New("Можно добавить до 8 примеров визуального стиля")
	}
	keys := append([]string(nil), styles...)
	if logo != "" {
		keys = append(keys, logo)
	}
	seen := map[string]bool{}
	for _, key := range keys {
		asset := a.state.Assets[key]
		if asset == nil || asset.CompanyID != companyID || seen[key] {
			return errors.New("Логотип или пример стиля недоступен этой компании")
		}
		seen[key] = true
	}
	return nil
}

// Network resolution happens outside the state mutex; only an owner may change branding.
func (a *App) brandingAPI(w http.ResponseWriter, r *http.Request, user identity, companyID string) {
	if r.Method != "PUT" {
		fail(w, 405, "Используйте сохранение настроек")
		return
	}
	var in BrandSettings
	if decode(r, &in) != nil || len(in.StyleNotes) > 6000 {
		fail(w, 400, "Проверьте настройки визуального стиля")
		return
	}
	if in.EmojiSource == "" {
		in.EmojiSource = "references"
	}
	if in.EmojiSource != "pack" && in.EmojiSource != "references" && in.EmojiSource != "standard" {
		fail(w, 400, "Выберите источник эмодзи")
		return
	}
	a.mu.Lock()
	c := a.state.Companies[companyID]
	if c == nil || (!user.Admin && c.OwnerID != user.ID) {
		a.mu.Unlock()
		fail(w, 403, "Настройки доступны владельцу компании")
		return
	}
	old := c.BrandSettings
	err := a.validateBrandAssets(companyID, in.LogoAssetID, in.StyleAssets)
	a.mu.Unlock()
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	in.EmojiItems, in.EmojiTitle = nil, ""
	if in.EmojiSource == "pack" {
		name, e := emojiPackName(in.EmojiPack)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		if old.EmojiPack == "https://t.me/addemoji/"+name && len(old.EmojiItems) > 0 {
			in.EmojiPack, in.EmojiTitle, in.EmojiItems = old.EmojiPack, old.EmojiTitle, old.EmojiItems
		} else {
			in.EmojiPack, in.EmojiTitle, in.EmojiItems, err = a.resolveEmojiPack(in.EmojiPack)
			if err != nil {
				fail(w, 400, err.Error())
				return
			}
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c = a.state.Companies[companyID]
	if c == nil || (!user.Admin && c.OwnerID != user.ID) {
		fail(w, 403, "Нет доступа")
		return
	}
	c.BrandSettings = in
	a.event(c.ID, "Обновлены логотип, визуальный стиль и эмодзи компании", "system")
	a.persist()
	reply(w, 200, c.BrandSettings)
}

// The selected company's assets are the only source of generation inputs.
func (a *App) artworkInputs(p Post, c Company) (styles []string, logo string, photos []string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	styleIDs, logoID := c.StyleAssets, c.LogoAssetID
	if p.StyleAssets != nil {
		styleIDs = p.StyleAssets
	}
	if p.LogoAssetID != "" {
		logoID = p.LogoAssetID
	}
	if err = a.validateBrandAssets(c.ID, logoID, styleIDs); err != nil {
		return
	}
	path := func(key string) string { return filepath.Join(a.dir, "uploads", a.state.Assets[key].File) }
	for _, key := range styleIDs {
		styles = append(styles, path(key))
	}
	if logoID != "" {
		logo = path(logoID)
	}
	for _, key := range p.Attachments {
		asset := a.state.Assets[key]
		if asset == nil || asset.CompanyID != c.ID {
			err = errors.New("Исходное фото недоступно этой компании")
			return
		}
		photos = append(photos, path(key))
	}
	return
}

func emojiPalette(c Company) []string {
	palette := []string{}
	if c.EmojiSource == "pack" {
		for _, item := range c.EmojiItems {
			palette = append(palette, item.Emoji)
		}
	}
	return palette
}
