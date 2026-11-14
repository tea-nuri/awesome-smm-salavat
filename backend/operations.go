package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Approval struct {
	UserID   int64     `json:"user_id"`
	Revision int       `json:"revision"`
	At       time.Time `json:"at"`
}
type MaterialCheck struct {
	Code  string `json:"code"`
	Level string `json:"level"`
	Text  string `json:"text"`
}
type ApprovalPolicy struct {
	Approvers      []int64 `json:"approvers"`
	Required       int     `json:"required"`
	SeparateAuthor bool    `json:"separate_author"`
	ConfirmChecks  bool    `json:"confirm_checks"`
}
type UsagePolicy struct {
	Paused        bool    `json:"paused"`
	MonthlyCalls  int     `json:"monthly_calls"`
	MonthlyImages int     `json:"monthly_images"`
	MonthlyTokens int64   `json:"monthly_tokens"`
	StopAtPercent float64 `json:"stop_at_percent"`
}
type AutomationSettings struct {
	Enabled        bool      `json:"enabled"`
	TopicsDaily    bool      `json:"topics_daily"`
	StrategyWeekly bool      `json:"strategy_weekly"`
	ReportsWeekly  bool      `json:"reports_weekly"`
	NextRun        time.Time `json:"next_run"`
}
type SkillDefinition struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Instruction string `json:"instruction"`
}
type Competitor struct {
	Username      string        `json:"username"`
	Posts         []ChannelPost `json:"posts"`
	Imported      bool          `json:"imported"`
	LastAttemptAt *time.Time    `json:"last_attempt_at,omitempty"`
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	URL           string        `json:"url"`
	Text          string        `json:"text"`
	PreviousText  string        `json:"previous_text,omitempty"`
	ScannedAt     *time.Time    `json:"scanned_at,omitempty"`
	ChangedAt     *time.Time    `json:"changed_at,omitempty"`
	Error         string        `json:"error,omitempty"`
}
type CompanyOperations struct {
	Connections         []*SocialConnection `json:"connections"`
	Policy              ApprovalPolicy      `json:"policy"`
	Usage               UsagePolicy         `json:"usage"`
	Automation          AutomationSettings  `json:"automation"`
	Competitors         []*Competitor       `json:"competitors"`
	Skills              []SkillDefinition   `json:"skills"`
	DiscussionChatID    int64               `json:"discussion_chat_id"`
	AutoReplies         bool                `json:"auto_replies"`
	AutoReplyDailyLimit int                 `json:"auto_reply_daily_limit"`
}
type ProviderConfig struct {
	BaseURL  string `json:"base_url"`
	AuthMode string `json:"auth_mode"`
	Model    string `json:"model"`
	KeySet   bool   `json:"key_set"`
}
type ControlSettings struct {
	Providers      map[string]ProviderConfig `json:"providers"`
	Admins         []int64                   `json:"admins,omitempty"`
	DisabledAdmins []int64                   `json:"disabled_admins,omitempty"`
	AIPaused       bool                      `json:"ai_paused"`
	Provider       string                    `json:"provider"`
	Model          string                    `json:"model"`
	StopAtPercent  float64                   `json:"stop_at_percent"`
	Quota          *QuotaSnapshot            `json:"quota,omitempty"`
}
type MarketingArtifact struct {
	ID         string          `json:"id"`
	CompanyID  string          `json:"company_id"`
	TaskID     string          `json:"task_id"`
	Kind       string          `json:"kind"`
	Title      string          `json:"title"`
	Body       string          `json:"body"`
	Data       json.RawMessage `json:"data,omitempty"`
	Status     string          `json:"status"`
	Version    int             `json:"version"`
	CreatedAt  time.Time       `json:"created_at"`
	ApprovedBy int64           `json:"approved_by,omitempty"`
}
type MetricRecord struct {
	ID        string    `json:"id"`
	CompanyID string    `json:"company_id"`
	PostID    string    `json:"post_id"`
	Date      string    `json:"date"`
	Views     *int64    `json:"views"`
	Reactions *int64    `json:"reactions"`
	Comments  *int64    `json:"comments"`
	Clicks    *int64    `json:"clicks,omitempty"`
	Leads     *int64    `json:"leads,omitempty"`
	Sales     *int64    `json:"sales,omitempty"`
	Revenue   *float64  `json:"revenue,omitempty"`
	Source    string    `json:"source"`
	UpdatedAt time.Time `json:"updated_at"`
}
type InboxEntry struct {
	Platform         string    `json:"platform,omitempty"`
	ExternalID       string    `json:"external_id,omitempty"`
	ParentExternalID string    `json:"parent_external_id,omitempty"`
	ID               string    `json:"id"`
	CompanyID        string    `json:"company_id"`
	PostID           string    `json:"post_id,omitempty"`
	ChatID           int64     `json:"chat_id"`
	MessageID        int64     `json:"message_id"`
	Author           string    `json:"author"`
	Text             string    `json:"text"`
	Draft            string    `json:"draft"`
	Revision         int       `json:"revision"`
	ApprovedRevision int       `json:"approved_revision"`
	Status           string    `json:"status"`
	Error            string    `json:"error,omitempty"`
	ReplyMessageID   int64     `json:"reply_message_id,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

var builtinSkills = []SkillDefinition{
	{"strategy", "Маркетинговая стратегия", "Составь или актуализируй стратегию: подтверждённые продукты, аудитории, преимущества, цели, рубрики, каналы, KPI, эксперименты и следующие действия. Используй предыдущую стратегию, конкурентов и метрики. Раздели факты, гипотезы и недостающие данные. data: {goals:[],audiences:[],rubrics:[],experiments:[],actions:[]}."},
	{"topics", "Темы публикаций", "Предложи 7 разных актуальных тем из знаний компании, стратегии, результатов и популярных свежих постов Telegram-каналов. Для внешнего повода приведи ссылку и дату; отделяй идеи от подтверждённых событий компании. Не выдумывай события. Для каждой: хук, цель, рубрика, формат и что уточнить. data: {items:[{title,hook,goal,rubric,format,brief}]}."},
	{"content_plan", "Контент-план", "Подготовь план на 7 дней: дата, тема, цель, формат, хук и задача на создание. Учитывай стратегию, историю и доступные метрики. data: {items:[{date,title,hook,goal,format,brief}]}. Не публикуй ничего самостоятельно."},
	{"video", "Сценарий короткого видео", "Подготовь пакет короткого видео: цель, хук первых 3 секунд, хронометраж 15–60 секунд, раскадровка, реплики, титры, планы съёмки, реквизит, CTA и подпись. data: {duration_seconds:30,scenes:[{seconds,shot,voice,caption}],shooting_list:[],caption:строка}. Используй только подтверждённые факты. Это сценарий и материалы для съёмки, не готовый видеофайл."},
	{"competitor", "Анализ Telegram-каналов", "Проанализируй реальные посты Telegram-каналов: темы, хуки, оформление, количество реакций и комментариев, свежесть. Укажи ссылки на конкретные посты, даты и реальные счётчики. Сравнивай посты прежде всего внутри одного канала с его средним уровнем: размер каналов разный. Отдельно выдели популярное в выборке и актуальное по дате; не называй старое трендом. Предложи применимые к компании идеи, не копируя чужие тексты. Чужие новости не являются фактами о компании. Если постов нет, явно скажи об отсутствии данных и не выдумывай анализ. data: {observations:[],opportunities:[],sources:[]}."},
	{"report", "Маркетинговый отчёт", "Составь отчёт за указанный период по предоставленным фактическим метрикам: показатели, лучшие форматы, ограничения данных, выводы, проверяемые гипотезы и действия. Отсутствующие данные не заменяй нулями. Различай снимок счётчика и сумму событий. Используй только число опубликованных постов, реакции и комментарии. Не оценивай выручку, заявки, продажи, обращения или ROI. data: {findings:[],hypotheses:[],actions:[]}."},
}

func (a *App) initOperations() {
	if a.state.Control.Providers == nil {
		a.state.Control.Providers = map[string]ProviderConfig{
			"codex": {Model: ""}, "ollama": {Model: "", AuthMode: "none"}, "deepseek": {Model: "deepseek-flash"}, "gemini": {Model: "gemini-4-hydrogen"},
		}
		current := a.state.Control.Provider
		if current == "" {
			current = "codex"
		}
		if a.state.Control.Model != "" {
			cfg := a.state.Control.Providers[current]
			cfg.Model = a.state.Control.Model
			a.state.Control.Providers[current] = cfg
		}
	}

	if a.state.DiscussionPosts == nil {
		a.state.DiscussionPosts = map[string]string{}
	}
	if a.state.Operations == nil {
		a.state.Operations = map[string]*CompanyOperations{}
	}
	if a.state.Artifacts == nil {
		a.state.Artifacts = map[string]*MarketingArtifact{}
	}
	if a.state.Metrics == nil {
		a.state.Metrics = map[string]*MetricRecord{}
	}
	if a.state.Inbox == nil {
		a.state.Inbox = map[string]*InboxEntry{}
	}
}
func (a *App) companyOperations(cid string) *CompanyOperations {
	a.initOperations()
	if a.state.Operations[cid] == nil {
		a.state.Operations[cid] = &CompanyOperations{Policy: ApprovalPolicy{Required: 1}, AutoReplyDailyLimit: 10, Competitors: []*Competitor{}, Skills: []SkillDefinition{}}
	}
	return a.state.Operations[cid]
}
func (a *App) managing(u identity, cid string) bool {
	c := a.state.Companies[cid]
	return c != nil && (u.Admin || u.ID == c.OwnerID)
}
func (a *App) isAdmin(uid int64) bool {
	for _, n := range a.state.Control.DisabledAdmins {
		if n == uid {
			return false
		}
	}
	if a.admins[uid] {
		return true
	}
	for _, n := range a.state.Control.Admins {
		if n == uid {
			return true
		}
	}
	return false
}
func (a *App) adminNotify(cid, text string, except int64) {
	recipients := map[int64]bool{}
	for uid := range a.admins {
		if a.isAdmin(uid) {
			recipients[uid] = true
		}
	}
	for _, uid := range a.state.Control.Admins {
		if a.isAdmin(uid) {
			recipients[uid] = true
		}
	}
	for uid := range recipients {
		if uid != except {
			a.notify(cid, uid, text)
		}
	}
}
func (a *App) mayApprove(u identity, p *Post) bool {
	if !a.allowed(u, a.state.Companies[p.CompanyID]) {
		return false
	}
	policy := a.companyOperations(p.CompanyID).Policy
	if policy.SeparateAuthor && p.UserID == u.ID {
		return false
	}
	if len(policy.Approvers) == 0 {
		return true
	}
	for _, uid := range policy.Approvers {
		if uid == u.ID {
			return true
		}
	}
	return false
}
func (a *App) approvalCount(p *Post) int {
	users := map[int64]bool{}
	for _, v := range p.Approvals {
		if v.Revision == p.Revision && a.mayApprove(a.user(v.UserID), p) {
			users[v.UserID] = true
		}
	}
	return len(users)
}
func (a *App) policyApproved(p *Post) bool {
	policy := a.companyOperations(p.CompanyID).Policy
	if len(p.Approvals) == 0 {
		return policy.Required <= 1 && len(policy.Approvers) == 0 && !policy.SeparateAuthor && p.ApprovedRevision == p.Revision
	}
	return a.approvalCount(p) >= max(1, policy.Required)
}

func sameApprovalPolicy(a, b ApprovalPolicy) bool {
	if a.Required != b.Required || a.SeparateAuthor != b.SeparateAuthor || a.ConfirmChecks != b.ConfirmChecks {
		return false
	}
	left, right := map[int64]bool{}, map[int64]bool{}
	for _, uid := range a.Approvers {
		left[uid] = true
	}
	for _, uid := range b.Approvers {
		right[uid] = true
	}
	if len(left) != len(right) {
		return false
	}
	for uid := range left {
		if !right[uid] {
			return false
		}
	}
	return true
}

func (a *App) skill(cid, key string) (SkillDefinition, bool) {
	if key == "reply" {
		return SkillDefinition{}, false
	}
	for _, s := range builtinSkills {
		if s.ID == key {
			return s, true
		}
	}
	for _, s := range a.companyOperations(cid).Skills {
		if s.ID == key {
			return s, true
		}
	}
	return SkillDefinition{}, false
}
func (a *App) createMarketingJob(cid, kind, request, target string, uid int64) (*Post, error) {
	if kind == "reply" {
		entry := a.state.Inbox[target]
		if entry == nil || entry.CompanyID != cid {
			return nil, errors.New("Обращение недоступно для этой компании")
		}
	}
	if _, ok := a.skill(cid, kind); !ok {
		return nil, errors.New("Навык не найден")
	}
	for _, p := range a.state.Posts {
		if p.CompanyID == cid && p.Skill == kind && p.TargetID == target && (p.Status == "working" || p.Status == "generating") {
			return nil, errors.New("Такая задача уже выполняется")
		}
	}
	p := a.newPost(cid, request, uid, uid, nil)
	p.Skill, p.TargetID = kind, target
	return p, nil
}
func (a *App) saveMarketingArtifact(p *Post, result AIResult) {
	if p.Skill == "" || result.Kind != "answer" {
		return
	}
	a.initOperations()
	version := 1
	for _, v := range a.state.Artifacts {
		if v.CompanyID == p.CompanyID && v.Kind == p.Skill && v.Version >= version {
			version = v.Version + 1
		}
	}
	artifact := &MarketingArtifact{ID: id(), CompanyID: p.CompanyID, TaskID: p.ID, Kind: p.Skill, Title: p.Title, Body: p.Body, Data: result.Data, Status: "draft", Version: version, CreatedAt: now()}
	a.state.Artifacts[artifact.ID] = artifact
	if p.Skill == "reply" {
		entry := a.state.Inbox[p.TargetID]
		if entry != nil && entry.Status != "sent" && entry.Status != "sending" {
			entry.Draft = result.Body
			entry.Revision++
			entry.ApprovedRevision = 0
			entry.Status = "review"
			entry.UpdatedAt = now()
			if a.companyOperations(p.CompanyID).AutoReplies && !replyNeedsHuman(entry, result) {
				a.queueReply(entry, true)
			}
		}
	}
}
func (a *App) operationsSnapshot(u identity) map[string]any {
	a.initOperations()
	companies := map[string]*CompanyOperations{}
	artifacts := []*MarketingArtifact{}
	metrics := []*MetricRecord{}
	usage := []*UsageRecord{}
	for cid := range a.state.Companies {
		if a.allowed(u, a.state.Companies[cid]) {
			companies[cid] = a.companyOperations(cid)
		}
	}
	for _, v := range a.state.Artifacts {
		if companies[v.CompanyID] != nil {
			artifacts = append(artifacts, v)
		}
	}
	for _, v := range a.state.Metrics {
		if companies[v.CompanyID] != nil {
			safe := *v
			safe.Clicks, safe.Leads, safe.Sales, safe.Revenue = nil, nil, nil, nil
			metrics = append(metrics, &safe)
		}
	}
	for _, v := range a.state.Usage {
		if companies[v.CompanyID] != nil {
			usage = append(usage, v)
		}
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].CreatedAt.After(artifacts[j].CreatedAt) })
	out := map[string]any{"companies": companies, "artifacts": artifacts, "metrics": metrics, "usage": usage, "skills": builtinSkills, "analytics": a.analytics(u, "", time.Time{}, time.Time{})}
	if u.Admin {
		control := a.state.Control
		if len(control.Admins) == 0 {
			control.Admins = []int64{}
			for uid := range a.admins {
				if a.isAdmin(uid) {
					control.Admins = append(control.Admins, uid)
				}
			}
		}
		out["control"] = control
		audit := []Event{}
		for i := len(a.state.Events) - 1; i >= 0 && len(audit) < 100; i-- {
			audit = append(audit, a.state.Events[i])
		}
		out["audit"] = audit
	} else {
		out["control"] = map[string]any{"ai_paused": a.state.Control.AIPaused}
	}
	return out
}

func (a *App) operationsAPI(w http.ResponseWriter, r *http.Request, u identity) bool {
	path := strings.TrimPrefix(r.URL.Path, "/api/")
	if path != "operations" && !strings.HasPrefix(path, "operations/") {
		return false
	}
	if path == "operations/auth" {
		a.accountAuthAPI(w, r, u)
		return true
	}
	if path == "operations/quota" && r.Method == "POST" {
		if !u.Admin {
			fail(w, 403, "Только администратор")
			return true
		}
		q, err := a.readQuota(r.Context())
		if err != nil {
			fail(w, 502, err.Error())
			return true
		}
		a.mu.Lock()
		a.state.Control.Quota = q
		a.persist()
		a.mu.Unlock()
		reply(w, 200, q)
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.initOperations()
	if path == "operations" && r.Method == "GET" {
		reply(w, 200, a.operationsSnapshot(u))
		return true
	}
	if strings.HasPrefix(path, "operations/providers/") {
		a.providerConfigAPI(w, r, u, strings.TrimPrefix(path, "operations/providers/"))
		return true
	}
	if path == "operations/control" && r.Method == "PUT" {
		a.updateControl(w, r, u)
		return true
	}
	parts := strings.Split(path, "/")
	if len(parts) < 3 {
		fail(w, 404, "Не найдено")
		return true
	}
	cid := parts[1]
	if !a.allowed(u, a.state.Companies[cid]) {
		fail(w, 403, "Нет доступа")
		return true
	}
	ops := a.companyOperations(cid)
	switch parts[2] {
	case "settings":
		if r.Method != "PUT" || !a.managing(u, cid) {
			fail(w, 403, "Настройки доступны владельцу")
			break
		}
		var in struct {
			Policy              ApprovalPolicy     `json:"policy"`
			Usage               UsagePolicy        `json:"usage"`
			Automation          AutomationSettings `json:"automation"`
			Skills              []SkillDefinition  `json:"skills"`
			DiscussionChatID    int64              `json:"discussion_chat_id"`
			AutoReplies         bool               `json:"auto_replies"`
			AutoReplyDailyLimit int                `json:"auto_reply_daily_limit"`
		}
		if decode(r, &in) != nil || in.Policy.Required < 1 || in.Policy.Required > 10 || in.Usage.MonthlyCalls < 0 || in.Usage.MonthlyImages < 0 || in.Usage.MonthlyTokens < 0 || in.Usage.StopAtPercent < 0 || in.Usage.StopAtPercent > 100 || in.AutoReplyDailyLimit < 0 || in.AutoReplyDailyLimit > 100 || len(in.Skills) > 20 {
			fail(w, 400, "Проверьте ограничения")
			break
		}
		valid := true
		unique := map[int64]bool{}
		for _, uid := range in.Policy.Approvers {
			if !a.allowed(a.user(uid), a.state.Companies[cid]) {
				valid = false
			}
			unique[uid] = true
		}
		if len(in.Policy.Approvers) > 0 && len(unique) < in.Policy.Required {
			valid = false
		}
		for _, s := range in.Skills {
			if !customSkillID.MatchString(s.ID) || len(s.Instruction) > 6000 || strings.TrimSpace(s.Name) == "" {
				valid = false
			}
			for _, builtin := range builtinSkills {
				if s.ID == builtin.ID {
					valid = false
				}
			}
		}
		if !valid {
			fail(w, 400, "Проверьте согласующих и навыки")
			break
		}
		if in.DiscussionChatID != 0 && in.DiscussionChatID != ops.DiscussionChatID {
			var chat struct {
				Linked int64 `json:"linked_chat_id"`
			}
			if err := a.tg("getChat", map[string]any{"chat_id": a.state.Companies[cid].Channel}, &chat); err != nil || chat.Linked != in.DiscussionChatID {
				fail(w, 400, "Укажите ID группы обсуждения, связанной с каналом компании")
				break
			}
		}
		policyChanged := !sameApprovalPolicy(ops.Policy, in.Policy)
		ops.Policy, ops.Usage, ops.Automation, ops.Skills = in.Policy, in.Usage, in.Automation, in.Skills
		ops.DiscussionChatID, ops.AutoReplies, ops.AutoReplyDailyLimit = in.DiscussionChatID, false, 0
		if ops.Automation.Enabled && ops.Automation.NextRun.IsZero() {
			ops.Automation.NextRun = now()
		}
		if policyChanged {
			for _, p := range a.state.Posts {
				if p.CompanyID == cid && (p.Status == "review" || p.Status == "approved") {
					p.Status = "review"
					p.Revision++
					p.ApprovedRevision = 0
					p.Approvals = nil
					p.Checks = a.materialChecks(p)
					a.queuePreview(p)
				}
			}
		}
		a.event(cid, "Изменены правила, лимиты и автоматизация пользователем "+strconv.FormatInt(u.ID, 10), "settings")
		a.persist()
		reply(w, 200, ops)
	case "jobs":
		if r.Method != "POST" {
			fail(w, 405, "Метод не поддерживается")
			break
		}
		var in struct {
			Kind     string `json:"kind"`
			Request  string `json:"request"`
			TargetID string `json:"target_id"`
		}
		if decode(r, &in) != nil || len(in.Request) > 12000 {
			fail(w, 400, "Проверьте задачу")
			break
		}
		p, err := a.createMarketingJob(cid, in.Kind, in.Request, in.TargetID, u.ID)
		if err != nil {
			fail(w, 409, err.Error())
			break
		}
		a.persist()
		reply(w, 202, p)
	case "artifacts":
		if len(parts) != 4 || r.Method != "POST" || !a.managing(u, cid) {
			fail(w, 403, "Нет доступа")
			break
		}
		v := a.state.Artifacts[parts[3]]
		if v == nil || v.CompanyID != cid {
			fail(w, 404, "Не найдено")
			break
		}
		v.Status = "approved"
		v.ApprovedBy = u.ID
		a.event(cid, "Утверждён материал: "+v.Title, "strategy")
		a.persist()
		reply(w, 200, v)
	case "competitors", "channels":
		a.channelAPI(w, r, u, cid, parts)
	case "metrics-refresh":
		a.refreshOwnChannel(w, r, u, cid)
	case "connections":
		a.connectionAPI(w, r, u, cid, parts)
	case "metrics":
		a.metricsAPI(w, r, u, cid)
	case "analytics":
		from, _ := time.Parse("2006-01-02", r.URL.Query().Get("from"))
		to, _ := time.Parse("2006-01-02", r.URL.Query().Get("to"))
		if !to.IsZero() {
			to = to.AddDate(0, 0, 1)
		}
		reply(w, 200, a.analytics(u, cid, from, to))
	case "inbox":
		fail(w, 410, "Раздел обращений отключён")
	case "checks":
		if r.Method != "POST" || len(parts) != 4 {
			fail(w, 405, "Метод не поддерживается")
			break
		}
		p := a.state.Posts[parts[3]]
		if p == nil || p.CompanyID != cid || !a.mayApprove(u, p) {
			fail(w, 403, "Нет права подтверждения")
			break
		}
		var in struct {
			Revision int `json:"revision"`
		}
		if decode(r, &in) != nil || in.Revision != p.Revision {
			fail(w, 409, "Версия изменилась")
			break
		}
		p.ChecksConfirmedRevision = p.Revision
		a.event(cid, fmt.Sprintf("Факты и визуал версии %d проверил пользователь %d", p.Revision, u.ID), "quality")
		a.persist()
		reply(w, 200, p)
	default:
		fail(w, 404, "Не найдено")
	}
	return true
}
