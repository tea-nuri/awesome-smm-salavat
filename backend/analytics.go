package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type PostMetrics struct {
	PostID     string    `json:"post_id"`
	Title      string    `json:"title"`
	Format     string    `json:"format"`
	Views      *int64    `json:"views"`
	Reactions  *int64    `json:"reactions"`
	Comments   *int64    `json:"comments"`
	Clicks     *int64    `json:"clicks"`
	Leads      *int64    `json:"leads"`
	Sales      *int64    `json:"sales"`
	Revenue    *float64  `json:"revenue"`
	Engagement *float64  `json:"engagement"`
	Sources    []string  `json:"sources"`
	UpdatedAt  time.Time `json:"updated_at"`
}

func (a *App) storeMetric(cid string, in MetricRecord) error {
	p := a.state.Posts[in.PostID]
	if p == nil || p.CompanyID != cid || p.isConversation() {
		return errors.New("Укажите ID публикации этой компании")
	}
	date, err := time.Parse("2006-01-02", in.Date)
	if err != nil || date.After(now().Add(24*time.Hour)) {
		return errors.New("Дата показателей должна быть в формате ГГГГ-ММ-ДД и не в будущем")
	}
	for _, v := range []*int64{in.Views, in.Reactions, in.Comments} {
		if v != nil && (*v < 0 || *v > 1e12) {
			return errors.New("Показатели должны быть неотрицательными")
		}
	}
	in.Clicks, in.Leads, in.Sales, in.Revenue = nil, nil, nil, nil
	if in.Source == "" {
		in.Source = "manual"
	}
	in.CompanyID = cid
	in.ID = in.PostID + ":" + in.Date + ":" + in.Source
	if old := a.state.Metrics[in.ID]; old != nil {
		if in.Views == nil {
			in.Views = old.Views
		}
		if in.Reactions == nil {
			in.Reactions = old.Reactions
		}
		if in.Comments == nil {
			in.Comments = old.Comments
		}

	}
	in.UpdatedAt = now()
	a.state.Metrics[in.ID] = &in
	return nil
}
func parseMetricCSV(reader io.Reader) ([]MetricRecord, error) {
	csvReader := csv.NewReader(reader)
	header, err := csvReader.Read()
	if err != nil {
		return nil, errors.New("Не удалось прочитать CSV")
	}
	if len(header) == 1 && strings.Contains(header[0], ";") {
		return nil, errors.New("CSV должен использовать запятую как разделитель")
	}
	columns := map[string]int{}
	for i, h := range header {
		columns[strings.TrimSpace(strings.TrimPrefix(h, "\ufeff"))] = i
	}
	if _, ok := columns["post_id"]; !ok {
		return nil, errors.New("Нужен столбец post_id")
	}
	if _, ok := columns["date"]; !ok {
		return nil, errors.New("Нужен столбец date")
	}
	rows := []MetricRecord{}
	for {
		row, err := csvReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil || len(rows) >= 5000 {
			return nil, errors.New("Некорректный CSV или более 5000 строк")
		}
		get := func(key string) string {
			if i, ok := columns[key]; ok && i < len(row) {
				return strings.TrimSpace(row[i])
			}
			return ""
		}
		in := MetricRecord{PostID: get("post_id"), Date: get("date"), Source: "csv"}
		targets := map[string]**int64{"views": &in.Views, "reactions": &in.Reactions, "comments": &in.Comments}
		for key, target := range targets {
			if value := get(key); value != "" {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return nil, errors.New("Числовые показатели должны быть целыми")
				}
				*target = &n
			}
		}
		rows = append(rows, in)
	}
	return rows, nil
}
func (a *App) metricsAPI(w http.ResponseWriter, r *http.Request, u identity, cid string) {
	if r.Method != "POST" || !a.managing(u, cid) {
		fail(w, 403, "Импорт доступен владельцу")
		return
	}
	rows := []MetricRecord{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "text/csv") {
		var err error
		rows, err = parseMetricCSV(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
	} else {
		var in MetricRecord
		if decode(r, &in) != nil {
			fail(w, 400, "Некорректные показатели")
			return
		}
		in.Source = "manual"
		rows = append(rows, in)
	}
	// Validate the entire import before changing any stored metric.
	pending := map[string]*MetricRecord{}
	old := a.state.Metrics
	a.state.Metrics = pending
	for _, v := range rows {
		if err := a.storeMetric(cid, v); err != nil {
			a.state.Metrics = old
			fail(w, 400, err.Error())
			return
		}
	}
	a.state.Metrics = old
	for key, v := range pending {
		a.state.Metrics[key] = v
	}
	a.event(cid, "Импортированы фактические показатели публикаций", "metrics")
	a.persist()
	reply(w, 200, map[string]int{"imported": len(rows)})
}
func (a *App) analytics(u identity, cid string, from, to time.Time) map[string]any {
	posts := []map[string]any{}
	totals := map[string]any{"posts": 0, "reactions": nil, "comments": nil}
	coverage := map[string]int{}
	for _, p := range a.state.Posts {
		if p.isConversation() || p.Status != "published" || (cid != "" && p.CompanyID != cid) || !a.allowed(u, a.state.Companies[p.CompanyID]) {
			continue
		}
		if (!from.IsZero() && p.UpdatedAt.Before(from)) || (!to.IsZero() && !p.UpdatedAt.Before(to)) {
			continue
		}
		row := map[string]any{"post_id": p.ID, "title": p.Title, "url": p.URL, "published_at": p.UpdatedAt, "reactions": nil, "comments": nil, "sources": []string{}}
		metrics := []*MetricRecord{}
		for _, m := range a.state.Metrics {
			if m.CompanyID == p.CompanyID && m.PostID == p.ID {
				date, err := time.Parse("2006-01-02", m.Date)
				if err == nil && (to.IsZero() || date.Before(to)) {
					metrics = append(metrics, m)
				}
			}
		}
		sort.Slice(metrics, func(i, j int) bool {
			if metrics[i].Date == metrics[j].Date {
				return metrics[i].UpdatedAt.Before(metrics[j].UpdatedAt)
			}
			return metrics[i].Date < metrics[j].Date
		})
		sources := map[string]bool{}
		for _, m := range metrics {
			if m.Reactions != nil {
				row["reactions"] = *m.Reactions
				sources[m.Source] = true
			}
			if m.Comments != nil {
				row["comments"] = *m.Comments
				sources[m.Source] = true
			}
		}
		for source := range sources {
			row["sources"] = append(row["sources"].([]string), source)
		}
		for _, key := range []string{"reactions", "comments"} {
			if n, ok := row[key].(int64); ok {
				previous, _ := totals[key].(int64)
				totals[key] = previous + n
				coverage[key]++
			}
		}
		posts = append(posts, row)
	}
	sort.Slice(posts, func(i, j int) bool {
		return posts[i]["published_at"].(time.Time).After(posts[j]["published_at"].(time.Time))
	})
	totals["posts"] = len(posts)
	return map[string]any{"from": from, "to_exclusive": to, "totals": totals, "coverage": coverage, "posts": posts, "note": "Посты — опубликованные через приложение в выбранные даты. Реакции и комментарии — сумма последних доступных накопительных счётчиков этих постов на конец периода, а не новые события за период. Снимки одного поста не суммируются. Источники: события Telegram, публичная лента, импорт CSV или ручной ввод. Прочерк — данных нет; сумма может быть неполной. Комментарии из группы обсуждения учитываются только с момента подключения бота."}
}

func metricInt(value int64) *int64 { return &value }
func (a *App) recordReaction(chat, message int64, count int64) {
	for _, p := range a.state.Posts {
		if p.MessageID == message && p.Status == "published" {
			c := a.state.Companies[p.CompanyID]
			if c == nil {
				continue
			}
			matches := strconv.FormatInt(chat, 10) == c.Channel
			for _, source := range []string{p.URL} {
				if strings.Contains(source, "/c/"+strings.TrimPrefix(strconv.FormatInt(chat, 10), "-100")+"/") {
					matches = true
				}
			}
			if !matches {
				continue
			}
			a.initOperations()
			a.storeMetric(p.CompanyID, MetricRecord{PostID: p.ID, Date: now().Format("2006-01-02"), Reactions: metricInt(count), Source: "telegram"})
		}
	}
}

func marshalReport(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
