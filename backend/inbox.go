package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func replyNeedsHuman(e *InboxEntry, result AIResult) bool {
	if e.Author == "Посетитель" {
		return true
	}
	var assessment struct {
		NeedsHuman bool `json:"needs_human"`
	}
	if len(result.Data) == 0 || json.Unmarshal(result.Data, &assessment) != nil || assessment.NeedsHuman {
		return true
	}
	text := strings.ToLower(e.Text + " " + result.Body)
	for _, term := range []string{"жалоб", "компенсац", "паспорт", "персональн", "суд", "травм", "уволь", "зарплат", "болез", "несчастн", "оплат", "верните"} {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}
func (a *App) queueReply(e *InboxEntry, automatic bool) error {
	ops := a.companyOperations(e.CompanyID)
	if strings.TrimSpace(e.Draft) == "" || e.Status == "sent" || e.Status == "sending" || e.Status == "uncertain" {
		return errors.New("Ответ сейчас нельзя отправить")
	}
	if automatic {
		if !ops.AutoReplies || ops.AutoReplyDailyLimit <= 0 {
			return errors.New("Автоответы запрещены")
		}
		count := 0
		for _, entry := range a.state.Inbox {
			if entry.CompanyID == e.CompanyID && (entry.Status == "sent" || entry.Status == "sending") && entry.UpdatedAt.Format("2006-01-02") == now().Format("2006-01-02") {
				count++
			}
		}
		if count >= ops.AutoReplyDailyLimit {
			return errors.New("Достигнут суточный лимит автоответов")
		}
	}
	e.ApprovedRevision = e.Revision
	e.Status = "sending"
	e.UpdatedAt = now()
	a.state.Outbox = append(a.state.Outbox, &Outbox{ID: id(), Kind: "reply", ReplyID: e.ID, CompanyID: e.CompanyID, ChatID: e.ChatID, Text: e.Draft, CreatedAt: now()})
	a.event(e.CompanyID, "Согласован ответ на обращение", "reply")
	return nil
}
func (a *App) inboxAPI(w http.ResponseWriter, r *http.Request, u identity, cid string, parts []string) {
	if len(parts) != 5 || r.Method != "POST" {
		fail(w, 405, "Метод не поддерживается")
		return
	}
	entry := a.state.Inbox[parts[3]]
	if entry == nil || entry.CompanyID != cid {
		fail(w, 404, "Обращение не найдено")
		return
	}
	switch parts[4] {
	case "draft":
		p, err := a.createMarketingJob(cid, "reply", "Подготовь ответ на это обращение", entry.ID, u.ID)
		if err != nil {
			fail(w, 409, err.Error())
			return
		}
		a.persist()
		reply(w, 202, p)
	case "edit":
		var in struct {
			Draft    string `json:"draft"`
			Revision int    `json:"revision"`
		}
		if decode(r, &in) != nil || in.Revision != entry.Revision || entry.Status == "sending" || entry.Status == "sent" || entry.Status == "uncertain" || len([]rune(in.Draft)) > 1500 {
			fail(w, 409, "Проверьте текст и актуальную версию ответа")
			return
		}
		entry.Draft = strings.TrimSpace(in.Draft)
		entry.Revision++
		entry.ApprovedRevision = 0
		entry.Status = "review"
		entry.UpdatedAt = now()
		a.persist()
		reply(w, 200, entry)
	case "send":
		if !a.mayApprove(u, &Post{CompanyID: cid}) {
			fail(w, 403, "Ответ может согласовать назначенный ответственный")
			return
		}
		var in struct {
			Revision int `json:"revision"`
		}
		if decode(r, &in) != nil || in.Revision != entry.Revision {
			fail(w, 409, "Версия ответа изменилась")
			return
		}
		if err := a.queueReply(entry, false); err != nil {
			fail(w, 409, err.Error())
			return
		}
		a.persist()
		reply(w, 200, entry)
	case "archive":
		if entry.Status == "sending" {
			fail(w, 409, "Ответ уже отправляется")
			return
		}
		entry.Status = "archived"
		entry.UpdatedAt = now()
		a.persist()
		reply(w, 200, entry)
	default:
		fail(w, 404, "Не найдено")
	}
}
func (a *App) internalSocialAPI(w http.ResponseWriter, r *http.Request, path string) bool {
	if path != "social/incoming" && path != "social/thread" && path != "social/reaction" && path != "reply-check" {
		return false
	}
	a.initOperations()
	if path == "social/thread" {
		var in struct {
			ChatID           int64  `json:"chat_id"`
			MessageID        int64  `json:"message_id"`
			ChannelID        int64  `json:"channel_id"`
			ChannelUsername  string `json:"channel_username"`
			ChannelMessageID int64  `json:"channel_message_id"`
		}
		if decode(r, &in) != nil {
			fail(w, 400, "Некорректная ветка обсуждения")
			return true
		}
		for cid, ops := range a.state.Operations {
			c := a.state.Companies[cid]
			if c == nil || ops.DiscussionChatID != in.ChatID || in.ChatID == 0 {
				continue
			}
			if c.Channel != strconv.FormatInt(in.ChannelID, 10) && !(in.ChannelUsername != "" && strings.EqualFold(c.Channel, "@"+in.ChannelUsername)) {
				continue
			}
			for _, p := range a.state.Posts {
				if p.CompanyID == cid && (p.Platform == "" || p.Platform == "telegram") && p.MessageID == in.ChannelMessageID && p.Status == "published" {
					a.state.DiscussionPosts[fmt.Sprintf("%d:%d", in.ChatID, in.MessageID)] = p.ID
				}
			}
		}
		a.persist()
		reply(w, 200, map[string]bool{"ok": true})
		return true
	}
	if path == "reply-check" {
		var in struct {
			ID string `json:"id"`
		}
		decode(r, &in)
		entry := a.state.Inbox[in.ID]
		ok := entry != nil && entry.Status == "sending" && entry.ApprovedRevision == entry.Revision
		out := map[string]any{"ok": ok, "entry": entry}
		if entry != nil && (entry.Platform == "vk" || entry.Platform == "max") {
			out["connection"] = a.privateConnection(entry.CompanyID, entry.Platform)
		}
		reply(w, 200, out)
		return true
	}
	if path == "social/reaction" {
		var in struct {
			ChatID    int64  `json:"chat_id"`
			MessageID int64  `json:"message_id"`
			Count     int64  `json:"count"`
			Username  string `json:"username"`
		}
		if decode(r, &in) != nil || in.Count < 0 {
			fail(w, 400, "Некорректные реакции")
			return true
		}
		for _, p := range a.state.Posts {
			c := a.state.Companies[p.CompanyID]
			if c != nil && (p.Platform == "" || p.Platform == "telegram") && p.MessageID == in.MessageID && p.Status == "published" && (c.Channel == strconv.FormatInt(in.ChatID, 10) || strings.EqualFold(c.Channel, "@"+in.Username) && in.Username != "") {
				a.storeMetric(c.ID, MetricRecord{PostID: p.ID, Date: now().Format("2006-01-02"), Reactions: metricInt(in.Count), Source: "telegram"})
			}
		}
		a.persist()
		reply(w, 200, map[string]bool{"ok": true})
		return true
	}
	var in struct {
		ChatID           int64  `json:"chat_id"`
		MessageID        int64  `json:"message_id"`
		Author           string `json:"author"`
		Text             string `json:"text"`
		ReplyToMessageID int64  `json:"reply_to_message_id"`
		ThreadID         int64  `json:"thread_id"`
	}
	if decode(r, &in) != nil || in.ChatID == 0 || in.MessageID == 0 || len([]rune(in.Text)) > 12000 {
		fail(w, 400, "Некорректное обращение")
		return true
	}
	cid := ""
	for candidate, ops := range a.state.Operations {
		if ops.DiscussionChatID == in.ChatID && ops.DiscussionChatID != 0 {
			cid = candidate
			break
		}
	}
	if cid == "" {
		reply(w, 200, map[string]bool{"accepted": false})
		return true
	}
	key := fmt.Sprintf("telegram:%d:%d", in.ChatID, in.MessageID)
	if a.state.Inbox[key] != nil {
		reply(w, 200, map[string]bool{"accepted": true})
		return true
	}
	if strings.TrimSpace(in.Text) == "" {
		reply(w, 200, map[string]bool{"accepted": false})
		return true
	}
	e := &InboxEntry{ID: key, CompanyID: cid, ChatID: in.ChatID, MessageID: in.MessageID, Author: truncate(in.Author, 150), Text: in.Text, Revision: 1, Status: "new", CreatedAt: now(), UpdatedAt: now()}
	for _, parent := range []int64{in.ThreadID, in.ReplyToMessageID} {
		if p := a.state.DiscussionPosts[fmt.Sprintf("%d:%d", in.ChatID, parent)]; p != "" {
			e.PostID = p
			break
		}
		for _, previous := range a.state.Inbox {
			if previous.CompanyID == cid && previous.ChatID == in.ChatID && previous.MessageID == parent {
				e.PostID = previous.PostID
				break
			}
		}
	}
	a.state.Inbox[e.ID] = e
	if e.PostID != "" {
		count := int64(0)
		for _, entry := range a.state.Inbox {
			if entry.PostID == e.PostID {
				count++
			}
		}
		a.storeMetric(cid, MetricRecord{PostID: e.PostID, Date: now().Format("2006-01-02"), Comments: &count, Source: "telegram"})
	}
	a.event(cid, "Новое обращение в обсуждении канала", "comment")
	c := a.state.Companies[cid]
	a.notify(cid, c.OwnerID, "Новое обращение в обсуждении канала. Откройте раздел «Обращения» в кабинете.")
	if a.companyOperations(cid).AutoReplies {
		a.createMarketingJob(cid, "reply", "Подготовь ответ на новое обращение", e.ID, c.OwnerID)
	}
	a.persist()
	reply(w, 200, map[string]bool{"accepted": true})
	return true
}
