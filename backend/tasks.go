package main

import (
	"errors"
	"strings"
	"time"
)

type TaskTurn struct {
	Role string    `json:"role"`
	Kind string    `json:"kind,omitempty"`
	Text string    `json:"text"`
	At   time.Time `json:"at"`
}

type TaskView struct {
	ID            string     `json:"id"`
	CompanyID     string     `json:"company_id"`
	Title         string     `json:"title"`
	Request       string     `json:"request"`
	Status        string     `json:"status"`
	Kind          string     `json:"kind"`
	Revision      int        `json:"revision"`
	Stage         string     `json:"stage"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Error         string     `json:"error,omitempty"`
	AwaitingInput bool       `json:"awaiting_input"`
	CanReply      bool       `json:"can_reply"`
	TextOnly      bool       `json:"text_only,omitempty"`
	Dialog        []TaskTurn `json:"dialog"`
}

func taskDialog(p *Post) []TaskTurn {
	if len(p.Dialog) > 0 {
		return p.Dialog
	}
	// Existing tasks remain readable without rewriting their stored content.
	turns := []TaskTurn{{Role: "user", Text: p.Request, At: p.CreatedAt}}
	if p.Body != "" {
		kind := p.Kind
		if kind == "" && p.Status == "answered" {
			kind = "answer"
		}
		turns = append(turns, TaskTurn{Role: "assistant", Kind: kind, Text: p.Body, At: p.UpdatedAt})
	}
	return turns
}

// Called while holding a.mu, like the other state snapshots and actions.
func (a *App) taskView(p *Post) TaskView {
	stage := p.Stage
	if stage == "" {
		switch p.Status {
		case "generating":
			stage = "queued"
		case "working":
			stage = "text"
		case "answered":
			stage = "answered"
		case "failed":
			stage = "failed"
		default:
			stage = "ready"
		}
	}
	waiting := p.Status == "answered" && p.Kind == "clarification"
	if waiting {
		stage = "awaiting_input"
	}
	return TaskView{ID: p.ID, CompanyID: p.CompanyID, Title: p.Title, Request: p.Request, Status: p.Status, Kind: p.Kind, Revision: p.Revision, Stage: stage, StartedAt: p.StartedAt, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt, Error: p.Error, AwaitingInput: waiting, CanReply: p.Status == "answered" && p.isConversation(), TextOnly: p.TextOnly, Dialog: taskDialog(p)}
}

func (a *App) replyToTask(user identity, taskID string, revision int, answer string) (*Post, error) {
	p := a.state.Posts[taskID]
	if p == nil || !a.allowed(user, a.state.Companies[p.CompanyID]) {
		return nil, errors.New("Задача недоступна")
	}
	if p.Revision != revision || p.Status != "answered" || !p.isConversation() {
		return nil, errors.New("Ответ уже принят или задача изменилась. Откройте актуальный диалог")
	}
	answer = strings.TrimSpace(answer)
	if answer == "" || len([]rune(answer)) > 12000 {
		return nil, errors.New("Введите ответ до 12000 символов")
	}
	if len(p.Request)+len(answer) > 60000 {
		return nil, errors.New("Диалог стал слишком длинным. Создайте новую задачу с итоговыми деталями")
	}
	p.Dialog = append(taskDialog(p), TaskTurn{Role: "user", Text: answer, At: now()})
	p.Request += "\nОтвет в диалоге этой задачи: " + answer
	p.Body, p.Kind, p.Error = "", "", ""
	p.Revision++
	p.ApprovedRevision = 0
	p.Status, p.Stage, p.StartedAt = "generating", "queued", nil
	p.UpdatedAt = now()
	a.history(p.CompanyID, "user", "Ответ на уточнение: "+answer)
	a.event(p.CompanyID, "Получен ответ в диалоге. Продолжаем задачу", "task")
	return p, nil
}

// Worker transitions are tied to a job revision, so an obsolete job cannot
// overwrite the progress of a retry or a continued conversation.
func (a *App) setTaskStage(postID string, revision int, stage string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.state.Posts[postID]
	if p == nil || p.Revision != revision || p.Status != "working" {
		return
	}
	p.Stage, p.UpdatedAt = stage, now()
	a.persist()
}

func (a *App) latestUserTask(companyID string, userID int64) *Post {
	var latest *Post
	for _, p := range a.state.Posts {
		if p.CompanyID == companyID && p.UserID == userID && (latest == nil || p.CreatedAt.After(latest.CreatedAt)) {
			latest = p
		}
	}
	return latest
}
