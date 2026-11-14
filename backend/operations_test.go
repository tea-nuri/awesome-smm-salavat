package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestUsageCountsActualEventsAndEnforcesCallQuota(t *testing.T) {
	a := testApp(t)
	a.companyOperations("a").Usage.MonthlyCalls = 1
	p := a.newPost("a", "task", 1, 1, nil)
	uid, err := a.beginUsage(*p, "text")
	if err != nil {
		t.Fatal(err)
	}
	a.finishUsage(uid, `{"type":"turn.completed","usage":{"input_tokens":200,"cached_input_tokens":50,"output_tokens":30}}`, nil)
	v := a.state.Usage[0]
	if !v.Measured || v.InputTokens != 200 || v.OutputTokens != 30 || v.CachedTokens != 50 {
		t.Fatal("usage was not measured")
	}
	if _, err := a.beginUsage(*p, "text"); err == nil {
		t.Fatal("monthly quota ignored")
	}
	if _, _, _, measured := usageFromEvents("provider did not return metrics"); measured {
		t.Fatal("unknown usage presented as measured")
	}
	a.companyOperations("a").Usage.MonthlyCalls = 0
	a.companyOperations("a").Usage.StopAtPercent = 80
	a.state.Control.Quota = &QuotaSnapshot{At: now(), Limits: map[string]AccountLimit{"codex": {Primary: &LimitWindow{UsedPercent: 85, ResetsAt: now().Add(time.Hour).Unix()}}}}
	if _, err := a.beginUsage(*p, "text"); err == nil {
		t.Fatal("subscription threshold ignored")
	}
	a.state.Control.Quota.At = now().Add(-10 * time.Minute)
	a.state.Control.Quota.Limits["codex"] = AccountLimit{Primary: &LimitWindow{UsedPercent: 5, ResetsAt: now().Add(time.Hour).Unix()}}
	if _, err := a.beginUsage(*p, "text"); err == nil {
		t.Fatal("stale snapshot accepted for expenditure control")
	}
}

func TestTwoApprovalsRequiredAndRevokedMemberCannotAuthorize(t *testing.T) {
	a := testApp(t)
	a.state.Companies["a"].Members = []int64{2, 3}
	a.companyOperations("a").Policy = ApprovalPolicy{Required: 2, Approvers: []int64{2, 3}, SeparateAuthor: true}
	p := a.newPost("a", "post", 1, 1, nil)
	p.Status = "review"
	if _, err := a.action(a.user(1), p.ID, "approve", 1, "", "", ""); err == nil {
		t.Fatal("author approved despite policy")
	}
	if _, err := a.action(a.user(2), p.ID, "approve", 1, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if p.Status != "review" {
		t.Fatal("one approval published a two-person material")
	}
	a.action(a.user(2), p.ID, "approve", 1, "", "", "")
	if a.approvalCount(p) != 1 {
		t.Fatal("duplicate approval counted")
	}
	if _, err := a.action(a.user(3), p.ID, "approve", 1, "", "", ""); err != nil {
		t.Fatal(err)
	}
	if p.Status != "approved" {
		t.Fatal("two valid approvals not accepted")
	}
	a.state.Companies["a"].Members = []int64{3}
	a.dispatchSchedules(now())
	if p.Status != "review" {
		t.Fatal("revoked member's approval still authorized delivery")
	}
}

func TestMetricSnapshotsAreNotSummedAndMissingIsNotZero(t *testing.T) {
	a := testApp(t)
	a.initOperations()
	p := a.newPost("a", "post", 1, 1, nil)
	for _, m := range []MetricRecord{{PostID: p.ID, Date: "2026-11-12", Views: metricInt(100), Reactions: metricInt(10), Comments: metricInt(2)}, {PostID: p.ID, Date: "2026-11-13", Views: metricInt(120), Reactions: metricInt(20), Comments: metricInt(4)}} {
		if err := a.storeMetric("a", m); err != nil {
			t.Fatal(err)
		}
	}
	report := a.analytics(a.user(1), "a", time.Time{}, time.Time{})
	totals := report["totals"].(map[string]any)
	if totals["views"] != int64(120) || totals["reactions"] != int64(20) {
		t.Fatal("cumulative snapshots were summed")
	}
	if _, ok := totals["leads"]; ok {
		t.Fatal("missing leads fabricated as zero")
	}
	if err := a.storeMetric("b", MetricRecord{PostID: p.ID, Date: "2026-11-13", Views: metricInt(1)}); err == nil {
		t.Fatal("cross-company metric accepted")
	}
	rows, err := parseMetricCSV(strings.NewReader("post_id,date,views,reactions,leads\n" + p.ID + ",2026-11-13,0,2,\n"))
	if err != nil || len(rows) != 1 || rows[0].Views == nil || *rows[0].Views != 0 || rows[0].Leads != nil {
		t.Fatal("CSV conflated absent and zero values")
	}
}

func TestAutonomousMarketingAndSensitiveReplies(t *testing.T) {
	a := testApp(t)
	ops := a.companyOperations("a")
	ops.Automation = AutomationSettings{Enabled: true, TopicsDaily: true, StrategyWeekly: true, ReportsWeekly: true}
	current := now()
	a.runAutomations(current)
	if len(a.state.Posts) != 3 {
		t.Fatal("automation did not enqueue requested skills")
	}
	a.runAutomations(current.Add(2 * time.Hour))
	if len(a.state.Posts) != 3 {
		t.Fatal("automation accumulated duplicate jobs")
	}
	entry := &InboxEntry{ID: "comment", CompanyID: "a", Draft: "Ответ", Text: "Верните оплату", Status: "review", Revision: 1, CreatedAt: now()}
	if !replyNeedsHuman(entry, AIResult{Body: "Ответ", Data: json.RawMessage(`{"needs_human":false}`)}) {
		t.Fatal("sensitive payment reply allowed automatically")
	}
	if err := a.queueReply(entry, true); err == nil {
		t.Fatal("auto reply sent without permission")
	}
}

func TestMarketingArtifactIsInternalAndStrategyIsCompanyScoped(t *testing.T) {
	a := testApp(t)
	p := a.newPost("a", "strategy", 1, 1, nil)
	p.Skill = "strategy"
	a.applyGenerationResult(p, AIResult{Kind: "answer", Title: "Strategy", Body: "UNIQUE_A_MARKETING"}, "")
	if !p.isConversation() || len(a.state.Artifacts) != 1 {
		t.Fatal("strategy became a publication")
	}
	if strings.Contains(a.strategyContext("a"), "UNIQUE_A_MARKETING") {
		t.Fatal("unapproved strategy applied")
	}
	for _, v := range a.state.Artifacts {
		v.Status = "approved"
	}
	if !strings.Contains(a.strategyContext("a"), "UNIQUE_A_MARKETING") || strings.Contains(a.strategyContext("b"), "UNIQUE_A_MARKETING") {
		t.Fatal("strategy approval or isolation failed")
	}
}

func TestReplySkillCannotReadPrivateDocumentsOrAnotherCompany(t *testing.T) {
	a := testApp(t)
	a.initOperations()
	a.state.Companies["a"].Brief = "PRIVATE_BRIEF_MARKER"
	a.state.Sources = map[string]*KnowledgeSource{"private": {CompanyID: "a", Text: "PRIVATE_DOCUMENT_MARKER", Enabled: true}, "public": {CompanyID: "a", URL: "https://example.com/about", Text: "PUBLIC_COMPANY_FACT", Enabled: true}}
	a.state.Inbox["own"] = &InboxEntry{ID: "own", CompanyID: "a", Text: "Какой у вас продукт?"}
	a.state.Inbox["foreign"] = &InboxEntry{ID: "foreign", CompanyID: "b", Text: "FOREIGN_SECRET_MARKER"}
	if _, err := a.createMarketingJob("a", "reply", "reply", "foreign", 1); err == nil {
		t.Fatal("foreign inquiry accepted")
	}
	p, err := a.createMarketingJob("a", "reply", "reply", "own", 1)
	if err != nil {
		t.Fatal(err)
	}
	prompt := a.marketingPrompt(*p, *a.state.Companies["a"], nil)
	if strings.Contains(prompt, "PRIVATE_BRIEF_MARKER") || strings.Contains(prompt, "PRIVATE_DOCUMENT_MARKER") || strings.Contains(prompt, "FOREIGN_SECRET_MARKER") || !strings.Contains(prompt, "PUBLIC_COMPANY_FACT") {
		t.Fatal("public reply context exposed internal information")
	}
}

func TestUsageSettingsDoNotInvalidateApprovalAndLegacyChecksAreAvailable(t *testing.T) {
	a := testApp(t)
	a.localToken = "test-local"
	p := a.newPost("a", "Материал", 1, 1, nil)
	p.Body = "Текст материала"
	p.Status = "review"
	call := func(body string) int {
		r := httptest.NewRequest("PUT", "http://localhost/api/operations/a/settings", strings.NewReader(body))
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("X-Local-Token", a.localToken)
		w := httptest.NewRecorder()
		a.api(w, r)
		return w.Code
	}
	if code := call(`{"policy":{"required":1,"approvers":[]},"usage":{"monthly_calls":10},"auto_reply_daily_limit":10}`); code != 200 {
		t.Fatal(code)
	}
	if p.Revision != 1 {
		t.Fatal("budget setting invalidated approval without a policy change")
	}
	if code := call(`{"policy":{"required":1,"approvers":[],"confirm_checks":true},"auto_reply_daily_limit":10}`); code != 200 {
		t.Fatal(code)
	}
	if p.Revision != 2 || len(p.Checks) == 0 {
		t.Fatal("legacy material cannot complete mandatory review")
	}
}
