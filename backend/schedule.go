package main

import (
	"errors"
	"time"
	_ "time/tzdata"
)

const preparationLead = 15 * time.Minute
const publicationGrace = 30 * time.Second

func (s Schedule) calendar() bool { return s.Frequency == "daily" || s.Frequency == "weekly" }

func (s Schedule) nextPublication(after time.Time) (time.Time, error) {
	if !s.calendar() {
		return time.Time{}, errors.New("Неизвестное расписание публикаций")
	}
	zone := s.TimeZone
	if zone == "" {
		zone = "Europe/Moscow"
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return time.Time{}, errors.New("Неизвестный часовой пояс")
	}
	clock, err := time.Parse("15:04", s.PublishTime)
	if err != nil || clock.Format("15:04") != s.PublishTime {
		return time.Time{}, errors.New("Укажите время публикации в формате ЧЧ:ММ")
	}
	if s.Frequency == "weekly" && (s.Weekday < 0 || s.Weekday > 6) {
		return time.Time{}, errors.New("Выберите день недели")
	}
	local := after.In(location)
	next := time.Date(local.Year(), local.Month(), local.Day(), clock.Hour(), clock.Minute(), 0, 0, location)
	if s.Frequency == "weekly" {
		days := (s.Weekday - int(next.Weekday()) + 7) % 7
		next = next.AddDate(0, 0, days)
		if !next.After(after) {
			next = next.AddDate(0, 0, 7)
		}
	} else if !next.After(after) {
		next = next.AddDate(0, 0, 1)
	}
	return next.UTC(), nil
}

func configureSchedule(s *Schedule, current time.Time) error {
	if s.Platform != "" && s.Platform != "telegram" && s.Platform != "vk" && s.Platform != "max" {
		return errors.New("Выберите Telegram, VK или MAX")
	}
	if len(s.Request) > 12000 {
		return errors.New("Укажите задачу до 12000 символов")
	}
	if s.calendar() {
		if s.TimeZone == "" {
			s.TimeZone = "Europe/Moscow"
		}
		next, err := s.nextPublication(current)
		if err != nil {
			return err
		}
		s.NextPublicationAt = &next
		s.NextRun = next.Add(-preparationLead)
		if s.NextRun.Before(current) {
			s.NextRun = current
		}
		s.EverySeconds, s.EveryMinutes = 0, 0
		return nil
	}
	if s.Frequency != "" || (s.Enabled && s.NextRun.Before(current)) || s.EveryMinutes < 0 || s.EveryMinutes > 525600 || s.EverySeconds < 0 || s.EverySeconds > 31536000 || (s.EverySeconds > 0 && s.EverySeconds < 30) {
		return errors.New("Укажите будущее время запуска и корректный интервал")
	}
	s.NextPublicationAt = nil
	return nil
}

func (a *App) retimeSchedulePosts(s *Schedule, current time.Time) {
	for _, p := range a.state.Posts {
		if p.ScheduleID != s.ID || p.Status == "published" || p.Status == "publishing" || p.Status == "uncertain" || p.Status == "rejected" || p.Status == "cancelled" {
			continue
		}
		p.Platform = s.Platform
		if s.calendar() {
			next, err := a.nextFreePublication(s, current, p.ID)
			if err != nil {
				continue
			}
			p.ScheduledAt = &next
		} else {
			p.ScheduledAt = nil
		}
		p.UpdatedAt = current
		if p.Status == "approved" || p.Status == "review" {
			p.Status = "review"
			p.ApprovedRevision = 0
			p.Revision++
			a.queuePreview(p)
		}
	}
}

func (a *App) slotTaken(scheduleID string, slot time.Time, except string) bool {
	for _, p := range a.state.Posts {
		if p.ID != except && p.ScheduleID == scheduleID && p.ScheduledAt != nil && p.ScheduledAt.Equal(slot) && p.Status != "cancelled" && p.Status != "rejected" && p.Status != "failed" {
			return true
		}
	}
	return false
}

func (a *App) nextFreePublication(s *Schedule, after time.Time, except string) (time.Time, error) {
	for i := 0; i < 366; i++ {
		slot, err := s.nextPublication(after)
		if err != nil {
			return time.Time{}, err
		}
		if !a.slotTaken(s.ID, slot, except) {
			return slot, nil
		}
		after = slot
	}
	return time.Time{}, errors.New("Все ближайшие даты этого расписания заняты")
}

// Called under a.mu independently of the AI worker, which can take minutes.
func (a *App) dispatchSchedules(current time.Time) bool {
	changed := false
	for _, s := range a.state.Schedules {
		if !s.Enabled || s.NextRun.After(current) {
			continue
		}
		if s.calendar() {
			if s.NextPublicationAt == nil || !s.NextPublicationAt.After(current) {
				next, err := s.nextPublication(current)
				if err != nil {
					continue
				}
				s.NextPublicationAt = &next
				s.NextRun = next.Add(-preparationLead)
				changed = true
				if s.NextRun.After(current) {
					continue
				}
			}
			inFlight := false
			for _, p := range a.state.Posts {
				if p.ScheduleID == s.ID && (p.Status == "working" || p.Status == "generating") {
					inFlight = true
					break
				}
			}
			if inFlight {
				s.NextRun = current.Add(30 * time.Second)
				changed = true
				continue
			}
			slot := *s.NextPublicationAt
			if !a.slotTaken(s.ID, slot, "") {
				p := a.newPost(s.CompanyID, s.Request, s.UserID, s.ChatID, &slot)
				p.Platform = s.Platform
				p.ScheduleID = s.ID
				a.event(s.CompanyID, "Подготовка поста к публикации по расписанию", "schedule")
			}
			next, _ := s.nextPublication(slot)
			s.NextPublicationAt = &next
			s.NextRun = next.Add(-preparationLead)
			changed = true
			continue
		}
		interval := s.EverySeconds
		if interval == 0 {
			interval = s.EveryMinutes * 60
		}
		inFlight := false
		for _, p := range a.state.Posts {
			if p.ScheduleID == s.ID && (p.Status == "working" || p.Status == "generating") {
				inFlight = true
				break
			}
		}
		if inFlight {
			s.NextRun = current.Add(time.Duration(max(interval, 30)) * time.Second)
			changed = true
			continue
		}
		p := a.newPost(s.CompanyID, s.Request, s.UserID, s.ChatID, nil)
		p.Platform = s.Platform
		p.ScheduleID = s.ID
		a.event(s.CompanyID, "Автономный запуск задачи по расписанию", "schedule")
		if interval > 0 {
			s.NextRun = current.Add(time.Duration(interval) * time.Second)
		} else {
			s.Enabled = false
		}
		changed = true
	}
	for _, p := range a.state.Posts {
		if p.isConversation() || p.Status != "approved" || p.ApprovedRevision != p.Revision || (p.ScheduledAt != nil && p.ScheduledAt.After(current)) {
			continue
		}
		if !a.policyApproved(p) {
			p.Status = "review"
			p.ApprovedRevision = 0
			p.UpdatedAt = current
			a.queuePreview(p)
			changed = true
			continue
		}
		if s := a.state.Schedules[p.ScheduleID]; s != nil && s.calendar() {
			if !s.Enabled {
				continue
			}
			if p.ScheduledAt != nil && current.Sub(*p.ScheduledAt) > publicationGrace {
				next, err := a.nextFreePublication(s, current, p.ID)
				if err != nil {
					continue
				}
				p.ScheduledAt = &next
				p.Status = "review"
				p.Revision++
				p.ApprovedRevision = 0
				p.UpdatedAt = current
				a.queuePreview(p)
				a.notify(p.CompanyID, p.ChatID, "Время публикации пропущено. Пост перенесён на следующий свободный выпуск и требует нового согласования.")
				changed = true
				continue
			}
		} else if p.ScheduleID != "" && p.ScheduledAt != nil {
			continue
		}
		c := a.state.Companies[p.CompanyID]
		if c == nil || a.publicationTarget(p, c) == "" {
			continue
		}
		p.Status = "publishing"
		p.UpdatedAt = current
		a.state.Outbox = append(a.state.Outbox, &Outbox{ID: id(), Kind: "publish", PostID: p.ID, CompanyID: p.CompanyID, ChatID: p.ChatID, CreatedAt: current})
		changed = true
	}
	return changed
}

func (a *App) publicationClock() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for current := range ticker.C {
		a.mu.Lock()
		if a.dispatchSchedules(current.UTC()) {
			a.persist()
		}
		a.mu.Unlock()
	}
}
