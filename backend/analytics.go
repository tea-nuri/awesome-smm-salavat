package main

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"math"
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
	for _, v := range []*int64{in.Views, in.Reactions, in.Comments, in.Clicks, in.Leads, in.Sales} {
		if v != nil && (*v < 0 || *v > 1e12) {
			return errors.New("Показатели должны быть неотрицательными")
		}
	}
	if in.Revenue != nil && (math.IsNaN(*in.Revenue) || math.IsInf(*in.Revenue, 0) || *in.Revenue < 0 || *in.Revenue > 1e15) {
		return errors.New("Проверьте выручку")
	}
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
		if in.Clicks == nil {
			in.Clicks = old.Clicks
		}
		if in.Leads == nil {
			in.Leads = old.Leads
		}
		if in.Sales == nil {
			in.Sales = old.Sales
		}
		if in.Revenue == nil {
			in.Revenue = old.Revenue
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
		targets := map[string]**int64{"views": &in.Views, "reactions": &in.Reactions, "comments": &in.Comments, "clicks": &in.Clicks, "leads": &in.Leads, "sales": &in.Sales}
		for key, target := range targets {
			if value := get(key); value != "" {
				n, err := strconv.ParseInt(value, 10, 64)
				if err != nil {
					return nil, errors.New("Числовые показатели должны быть целыми")
				}
				*target = &n
			}
		}
		if value := get("revenue"); value != "" {
			n, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return nil, errors.New("Некорректная выручка")
			}
			in.Revenue = &n
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
	activity := map[string]int{"created_posts": 0, "published_posts": 0, "review_posts": 0, "failed_tasks": 0, "ai_calls": 0, "observed_comments": 0, "sent_replies": 0}
	within := func(t time.Time) bool { return (from.IsZero() || !t.Before(from)) && (to.IsZero() || t.Before(to)) }
	visible := func(company string) bool {
		return (cid == "" || cid == company) && a.allowed(u, a.state.Companies[company])
	}
	for _, p := range a.state.Posts {
		if !visible(p.CompanyID) {
			continue
		}
		if !p.isConversation() && within(p.CreatedAt) {
			activity["created_posts"]++
			if p.Status == "review" {
				activity["review_posts"]++
			}
		}
		if p.Status == "published" && within(p.UpdatedAt) {
			activity["published_posts"]++
		}
		if p.Status == "failed" && within(p.UpdatedAt) {
			activity["failed_tasks"]++
		}
	}
	for _, v := range a.state.Usage {
		if visible(v.CompanyID) && within(v.StartedAt) {
			activity["ai_calls"]++
		}
	}
	for _, v := range a.state.Inbox {
		if visible(v.CompanyID) {
			if within(v.CreatedAt) {
				activity["observed_comments"]++
			}
			if v.Status == "sent" && within(v.UpdatedAt) {
				activity["sent_replies"]++
			}
		}
	}
	byPost := map[string]*PostMetrics{}
	fieldTimes := map[string]map[string]time.Time{}
	rows := []*MetricRecord{}
	for _, r := range a.state.Metrics {
		if (cid == "" || r.CompanyID == cid) && a.allowed(u, a.state.Companies[r.CompanyID]) {
			date, _ := time.Parse("2006-01-02", r.Date)
			if (!from.IsZero() && date.Before(from.Truncate(24*time.Hour))) || (!to.IsZero() && !date.Before(to)) {
				continue
			}
			rows = append(rows, r)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Date == rows[j].Date {
			return rows[i].UpdatedAt.Before(rows[j].UpdatedAt)
		}
		return rows[i].Date < rows[j].Date
	})
	for _, r := range rows {
		p := a.state.Posts[r.PostID]
		if p == nil {
			continue
		}
		v := byPost[p.ID]
		if v == nil {
			v = &PostMetrics{PostID: p.ID, Title: p.Title, Format: aspectRatio(p.AspectRatio), Sources: []string{}}
			if p.Image == "" {
				v.Format = "Текст"
			}
			byPost[p.ID] = v
			fieldTimes[p.ID] = map[string]time.Time{}
		}
		for key, pair := range map[string]struct {
			value  *int64
			target **int64
		}{"views": {r.Views, &v.Views}, "reactions": {r.Reactions, &v.Reactions}, "comments": {r.Comments, &v.Comments}, "clicks": {r.Clicks, &v.Clicks}, "leads": {r.Leads, &v.Leads}, "sales": {r.Sales, &v.Sales}} {
			if pair.value != nil {
				*pair.target = pair.value
				fieldTimes[p.ID][key] = r.UpdatedAt
			}
		}
		if r.Revenue != nil {
			v.Revenue = r.Revenue
		}
		if r.UpdatedAt.After(v.UpdatedAt) {
			v.UpdatedAt = r.UpdatedAt
		}
		found := false
		for _, s := range v.Sources {
			if s == r.Source {
				found = true
			}
		}
		if !found {
			v.Sources = append(v.Sources, r.Source)
		}
	}
	posts := []*PostMetrics{}
	totals := map[string]any{}
	coverage := map[string]int{}
	formats := map[string]map[string]any{}
	for _, v := range byPost {
		if v.Views != nil && *v.Views > 0 && v.Reactions != nil && v.Comments != nil {
			rate := float64(*v.Reactions+*v.Comments) / float64(*v.Views) * 100
			v.Engagement = &rate
		}
		posts = append(posts, v)
		for key, n := range map[string]*int64{"views": v.Views, "reactions": v.Reactions, "comments": v.Comments, "clicks": v.Clicks, "leads": v.Leads, "sales": v.Sales} {
			if n != nil {
				previous, _ := totals[key].(int64)
				totals[key] = previous + *n
				coverage[key]++
			}
		}
		if v.Revenue != nil {
			previous, _ := totals["revenue"].(float64)
			totals["revenue"] = previous + *v.Revenue
			coverage["revenue"]++
		}
		f := formats[v.Format]
		if f == nil {
			f = map[string]any{"format": v.Format, "posts": 0, "measured_views": int64(0), "measured_engagement": int64(0), "complete_posts": 0}
			formats[v.Format] = f
		}
		f["posts"] = f["posts"].(int) + 1
		if v.Engagement != nil {
			f["measured_views"] = f["measured_views"].(int64) + *v.Views
			f["measured_engagement"] = f["measured_engagement"].(int64) + *v.Reactions + *v.Comments
			f["complete_posts"] = f["complete_posts"].(int) + 1
		}
	}
	sort.Slice(posts, func(i, j int) bool {
		if posts[i].Engagement == nil {
			return false
		}
		if posts[j].Engagement == nil {
			return true
		}
		return *posts[i].Engagement > *posts[j].Engagement
	})
	formatRows := []map[string]any{}
	for _, f := range formats {
		if views := f["measured_views"].(int64); views > 0 {
			f["engagement"] = float64(f["measured_engagement"].(int64)) / float64(views) * 100
		}
		formatRows = append(formatRows, f)
	}
	sort.Slice(formatRows, func(i, j int) bool { return formatRows[i]["format"].(string) < formatRows[j]["format"].(string) })
	return map[string]any{"activity": activity, "from": from, "to_exclusive": to, "totals": totals, "coverage": coverage, "posts": posts, "formats": formatRows, "note": "Накопительные показатели: последний доступный снимок каждого показателя публикации в выбранном периоде. Дневные снимки одного поста не суммируются. Отсутствие показателя означает, что он не измерен. Просмотры Telegram Bot API не предоставляет: импортируйте их из статистики канала."}
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
