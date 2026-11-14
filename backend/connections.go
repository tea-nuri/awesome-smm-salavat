package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type SocialConnection struct {
	Platform  string `json:"platform"`
	Target    string `json:"target"`
	TokenSet  bool   `json:"token_set"`
	Enabled   bool   `json:"enabled"`
	Monitor   bool   `json:"monitor"`
	LastError string `json:"last_error,omitempty"`
	Verified  bool   `json:"verified"`
}

func (a *App) credentialCipher() (cipher.AEAD, error) {
	path := filepath.Join(a.dir, "connection.key")
	key, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		key = make([]byte, 32)
		if _, err = rand.Read(key); err == nil {
			err = os.WriteFile(path, key, 0600)
		}
	}
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func (a *App) connectionSecrets() map[string]string {
	out := map[string]string{}
	data, err := os.ReadFile(filepath.Join(a.dir, "connections.enc.json"))
	if err == nil {
		json.Unmarshal(data, &out)
	}
	return out
}
func (a *App) setConnectionSecret(cid, platform, token string) error {
	crypt, err := a.credentialCipher()
	if err != nil {
		return err
	}
	values := a.connectionSecrets()
	key := cid + ":" + platform
	if token == "" {
		delete(values, key)
	} else {
		nonce := make([]byte, crypt.NonceSize())
		if _, err = rand.Read(nonce); err != nil {
			return err
		}
		sealed := crypt.Seal(nonce, nonce, []byte(token), []byte(key))
		values[key] = base64.StdEncoding.EncodeToString(sealed)
	}
	data, _ := json.Marshal(values)
	path := filepath.Join(a.dir, "connections.enc.json")
	if err = os.WriteFile(path+".tmp", data, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (a *App) connectionSecret(cid, platform string) (string, error) {
	raw := a.connectionSecrets()[cid+":"+platform]
	if raw == "" {
		return "", errors.New("Токен не подключён")
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	crypt, err := a.credentialCipher()
	if err != nil || len(data) < crypt.NonceSize() {
		return "", errors.New("Не удалось открыть токен")
	}
	plain, err := crypt.Open(nil, data[:crypt.NonceSize()], data[crypt.NonceSize():], []byte(cid+":"+platform))
	return string(plain), err
}
func (a *App) connection(cid, platform string) *SocialConnection {
	for _, v := range a.companyOperations(cid).Connections {
		if v.Platform == platform {
			return v
		}
	}
	return nil
}
func (a *App) privateConnection(cid, platform string) map[string]any {
	v := a.connection(cid, platform)
	if v == nil || !v.Enabled {
		return nil
	}
	token, err := a.connectionSecret(cid, platform)
	if err != nil {
		return nil
	}
	return map[string]any{"platform": v.Platform, "target": v.Target, "token": token, "monitor": v.Monitor}
}
func (a *App) publicationTarget(p *Post, c *Company) string {
	if p.Platform == "" || p.Platform == "telegram" {
		return c.Channel
	}
	connection := a.connection(c.ID, p.Platform)
	if connection == nil || !connection.Enabled || !connection.TokenSet {
		return ""
	}
	return connection.Target
}
func (a *App) connectionAPI(w http.ResponseWriter, r *http.Request, u identity, cid string, parts []string) {
	if len(parts) != 4 || r.Method != "PUT" || !a.managing(u, cid) || (parts[3] != "vk" && parts[3] != "max") {
		fail(w, 403, "Подключение доступно владельцу компании")
		return
	}
	platform := parts[3]
	var in struct {
		Target     string `json:"target"`
		Token      string `json:"token"`
		ClearToken bool   `json:"clear_token"`
		Enabled    bool   `json:"enabled"`
		Monitor    bool   `json:"monitor"`
	}
	if decode(r, &in) != nil || len(in.Token) > 4096 {
		fail(w, 400, "Проверьте подключение")
		return
	}
	in.Target = strings.TrimSpace(in.Target)
	if in.Target != "" {
		n, err := strconv.ParseInt(in.Target, 10, 64)
		if err != nil || n == 0 {
			fail(w, 400, "Укажите числовой ID канала или сообщества")
			return
		}
		if platform == "vk" && n > 0 {
			n = -n
		}
		in.Target = strconv.FormatInt(n, 10)
	}
	ops := a.companyOperations(cid)
	v := a.connection(cid, platform)
	if v == nil {
		v = &SocialConnection{Platform: platform}
		ops.Connections = append(ops.Connections, v)
	}
	changed := v.Target != in.Target || in.Token != "" || in.ClearToken
	if in.ClearToken || in.Token != "" {
		if err := a.setConnectionSecret(cid, platform, in.Token); err != nil {
			fail(w, 500, "Не удалось сохранить токен")
			return
		}
		v.TokenSet = in.Token != ""
	}
	v.Target, v.Enabled, v.Monitor = in.Target, in.Enabled, in.Monitor
	if changed {
		v.Verified = false
		v.LastError = ""
		for _, p := range a.state.Posts {
			if p.CompanyID == cid && p.Platform == platform && (p.Status == "approved" || p.Status == "review") {
				p.Status = "review"
				p.ApprovedRevision = 0
				p.Revision++
				p.Approvals = nil
				a.queuePreview(p)
			}
		}
	}
	a.event(cid, "Обновлено подключение "+strings.ToUpper(platform), "connection")
	a.persist()
	reply(w, 200, v)
}

func (a *App) internalConnectionsAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if path == "social/config" {
		out := []map[string]any{}
		for cid, ops := range a.state.Operations {
			for _, connection := range ops.Connections {
				if !connection.Enabled || !connection.TokenSet || !connection.Monitor {
					continue
				}
				v := a.privateConnection(cid, connection.Platform)
				if v == nil {
					continue
				}
				v["company_id"] = cid
				posts := []map[string]any{}
				for _, p := range a.state.Posts {
					if p.CompanyID == cid && p.Platform == connection.Platform && p.Status == "published" {
						posts = append(posts, map[string]any{"id": p.ID, "external_message_id": p.ExternalMessageID, "message_id": p.MessageID, "created_at": p.CreatedAt.UnixNano()})
					}
				}
				sort.Slice(posts, func(i, j int) bool { return posts[i]["created_at"].(int64) < posts[j]["created_at"].(int64) })
				v["posts"] = posts
				out = append(out, v)
			}
		}
		reply(w, 200, out)
		return true
	}
	if path != "social/observations" {
		return false
	}
	var in struct {
		CompanyID string         `json:"company_id"`
		Platform  string         `json:"platform"`
		Error     string         `json:"error"`
		Metrics   []MetricRecord `json:"metrics"`
		Comments  []struct {
			PostID           string `json:"post_id"`
			ExternalID       string `json:"external_id"`
			ParentExternalID string `json:"parent_external_id"`
			Author           string `json:"author"`
			Text             string `json:"text"`
		} `json:"comments"`
	}
	if decode(r, &in) != nil {
		fail(w, 400, "Некорректные наблюдения")
		return true
	}
	connection := a.connection(in.CompanyID, in.Platform)
	if connection == nil || !connection.Enabled {
		fail(w, 403, "Подключение отключено")
		return true
	}
	a.initOperations()
	previousError := connection.LastError
	connection.LastError = truncate(in.Error, 200)
	if in.Error != "" && previousError != connection.LastError {
		owner := a.state.Companies[in.CompanyID].OwnerID
		a.notify(in.CompanyID, owner, "Не удалось обновить "+strings.ToUpper(in.Platform)+". Проверьте подключение в настройках компании.")
		a.adminNotify(in.CompanyID, "Ошибка мониторинга "+strings.ToUpper(in.Platform), owner)
	}
	connection.Verified = in.Error == ""
	for _, m := range in.Metrics {
		m.Source = in.Platform
		a.storeMetric(in.CompanyID, m)
	}
	for _, comment := range in.Comments {
		if comment.ExternalID == "" || strings.TrimSpace(comment.Text) == "" {
			continue
		}
		key := in.CompanyID + ":" + in.Platform + ":" + comment.ExternalID
		if a.state.Inbox[key] != nil {
			continue
		}
		entry := &InboxEntry{ID: key, CompanyID: in.CompanyID, Platform: in.Platform, PostID: comment.PostID, ExternalID: comment.ExternalID, ParentExternalID: comment.ParentExternalID, Author: truncate(comment.Author, 150), Text: truncate(comment.Text, 12000), Revision: 1, Status: "new", CreatedAt: now(), UpdatedAt: now()}
		a.state.Inbox[key] = entry
		if a.companyOperations(in.CompanyID).AutoReplies {
			a.createMarketingJob(in.CompanyID, "reply", "Подготовь ответ на обращение", entry.ID, a.state.Companies[in.CompanyID].OwnerID)
		}
	}
	a.persist()
	reply(w, 200, map[string]bool{"ok": true})
	return true
}
