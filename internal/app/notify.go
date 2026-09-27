package app

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/augustoroman/taskmaster/internal/engine"
	"github.com/augustoroman/taskmaster/internal/store"
)

// Notification rules (docs/design.md §5.1).
const (
	// maxNotifications is the most sent at once; beyond that, the last one
	// summarizes the rest.
	maxNotifications = 4
	// Tasks that recur this rarely or less get a reminder leadReminderDays
	// before they're due.
	leadReminderPeriodDays = 180
	leadReminderDays       = 7
	// How long a push service keeps trying to deliver.
	notificationTTL = 12 * time.Hour
)

// PushSender delivers push messages (push.Sender).
type PushSender interface {
	PublicKey() string
	Send(ctx context.Context, sub *store.PushSubscription, payload []byte, ttl time.Duration) (gone bool, err error)
}

// SetPush enables push notifications.
func (s *Service) SetPush(p PushSender) { s.push = p }

// ErrPushDisabled means the server has no push sender configured.
var ErrPushDisabled = errors.New("push notifications aren't set up on this server")

// Notification is the payload the service worker turns into a system
// notification.
type Notification struct {
	Type  string `json:"type"` // "task", "summary" or "test"
	Title string `json:"title"`
	Body  string `json:"body"`
	// Tag replaces an earlier notification with the same tag.
	Tag string `json:"tag"`
	URL string `json:"url"`

	// Type "task": what the Done and Tomorrow buttons act on.
	TaskID     string      `json:"task_id,omitempty"`
	Occurrence engine.Date `json:"occurrence,omitzero"`
	Today      engine.Date `json:"today,omitzero"`
	Tomorrow   engine.Date `json:"tomorrow,omitzero"`
	// Actions shows Done and Tomorrow buttons.
	Actions bool `json:"actions,omitempty"`
}

// PushPublicKey is the key browsers subscribe with.
func (s *Service) PushPublicKey() (string, error) {
	if s.push == nil {
		return "", ErrPushDisabled
	}
	return s.push.PublicKey(), nil
}

// RegisterPush saves a device's push subscription for u.
func (s *Service) RegisterPush(ctx context.Context, u *store.User, endpoint, p256dh, auth, userAgent string) error {
	if !strings.HasPrefix(endpoint, "https://") || p256dh == "" || auth == "" {
		return invalid("invalid push subscription")
	}
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		return tx.SavePushSubscription(&store.PushSubscription{
			UserID: u.ID, Endpoint: endpoint, P256dh: p256dh, Auth: auth, UserAgent: userAgent, CreatedAt: s.now(),
		})
	})
}

func (s *Service) UnregisterPush(ctx context.Context, u *store.User, endpoint string) error {
	return s.db.Tx(ctx, func(tx *store.Tx) error { return tx.DeletePushSubscription(u.ID, endpoint) })
}

// PushDeviceCount is how many devices u receives notifications on.
func (s *Service) PushDeviceCount(ctx context.Context, u *store.User) (int, error) {
	var n int
	err := s.db.Tx(ctx, func(tx *store.Tx) error {
		subs, err := tx.PushSubscriptions(u.ID)
		n = len(subs)
		return err
	})
	return n, err
}

var notifyTimeRE = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// SetNotifySettings turns u's daily notifications on or off and sets when
// they arrive ("HH:MM" in u's time zone; "" leaves it alone).
func (s *Service) SetNotifySettings(ctx context.Context, u *store.User, notify bool, at string) (*store.User, error) {
	if at != "" && !notifyTimeRE.MatchString(at) {
		return nil, invalid("notification time must look like 08:00")
	}
	var me *store.User
	err := s.db.Tx(ctx, func(tx *store.Tx) (err error) {
		if me, err = tx.GetUser(u.ID); err != nil {
			return err
		}
		if at != "" && at != me.NotifyTime {
			me.NotifyTime = at
			me.NotifiedOn = engine.Date{} // today's may not have been sent yet at the new time
		}
		me.Notify = notify
		return tx.UpdateUser(me)
	})
	return me, err
}

// SetTagNotify sets whether u gets notifications for a tag's tasks.
func (s *Service) SetTagNotify(ctx context.Context, u *store.User, tagID string, notify bool) error {
	return s.db.Tx(ctx, func(tx *store.Tx) error {
		if err := s.requireTagLevel(tx, u, tagID, store.LevelRead); err != nil {
			return err
		}
		return tx.SetTagNotify(u.ID, tagID, notify)
	})
}

type pending struct {
	n    Notification
	lead bool // a lead-time reminder
	sort float64
}

// notifyPlan is what to send a user, and which lead-time reminders that
// covers (sent individually or in the summary).
type notifyPlan struct {
	send  []Notification
	leads []Notification
}

// DueNotifications returns the notifications u would get now: tasks due today
// or overdue, plus lead-time reminders for rare tasks, at most
// maxNotifications (the last summarizing any others).
func (s *Service) DueNotifications(ctx context.Context, u *store.User) ([]Notification, error) {
	plan, err := s.planNotifications(ctx, u)
	return plan.send, err
}

func (s *Service) planNotifications(ctx context.Context, u *store.User) (notifyPlan, error) {
	var plan notifyPlan
	tasks, err := s.ListTasks(ctx, u, TaskFilter{})
	if err != nil {
		return plan, err
	}
	var items []pending
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		tags, err := tx.UserTags(u.ID)
		if err != nil {
			return err
		}
		notify := map[string]bool{}
		for _, t := range tags {
			notify[t.ID] = t.Notify
		}
		for _, v := range tasks {
			if v.Level < store.LevelDo || v.State.Paused || v.State.Done || v.State.Due.IsZero() {
				continue
			}
			if len(v.VisibleTagIDs) > 0 && !slices.ContainsFunc(v.VisibleTagIDs, func(id string) bool { return notify[id] }) {
				continue
			}
			days := v.Today.DaysUntil(v.State.Due)
			switch {
			case days <= 0:
				urgency, _ := v.Def.Rank(v.State, v.Today)
				items = append(items, pending{n: taskNotification(v, days), sort: urgency.Score})
			case days <= leadReminderDays && v.Def.PeriodDays(v.Today) >= leadReminderPeriodDays:
				sent, err := tx.LeadReminded(u.ID, v.ID, v.State.Occurrence())
				if err != nil {
					return err
				}
				if !sent {
					items = append(items, pending{n: taskNotification(v, days), lead: true, sort: -float64(days)})
				}
			}
		}
		return nil
	})
	if err != nil {
		return plan, err
	}
	// Due and overdue first (most urgent first), then reminders (soonest first).
	slices.SortStableFunc(items, func(a, b pending) int {
		if a.lead != b.lead {
			if a.lead {
				return 1
			}
			return -1
		}
		return cmp.Compare(b.sort, a.sort)
	})
	for _, it := range items {
		if it.lead {
			plan.leads = append(plan.leads, it.n)
		}
	}
	if len(items) <= maxNotifications {
		for _, it := range items {
			plan.send = append(plan.send, it.n)
		}
		return plan, nil
	}
	for _, it := range items[:maxNotifications-1] {
		plan.send = append(plan.send, it.n)
	}
	rest := items[maxNotifications-1:]
	var names []string
	for _, it := range rest[:min(3, len(rest))] {
		names = append(names, it.n.Title)
	}
	body := strings.Join(names, ", ")
	if len(rest) > 3 {
		body += fmt.Sprintf(", and %d more", len(rest)-3)
	}
	plan.send = append(plan.send, Notification{
		Type: "summary", Title: fmt.Sprintf("%d more tasks", len(rest)), Body: body, Tag: "summary", URL: "/#/",
	})
	return plan, nil
}

func taskNotification(v *TaskView, days int) Notification {
	when := "Due today"
	switch {
	case days < 0:
		when = fmt.Sprintf("Overdue since %s", v.State.Due.Time().Format("Mon, Jan 2"))
	case days > 0:
		when = fmt.Sprintf("Due in %d days (%s)", days, v.State.Due.Time().Format("Mon, Jan 2"))
	}
	if v.Kind == engine.KindCycle {
		for _, slot := range v.Slots {
			if slot.ID == string(v.State.Slot) {
				when = slot.Title + " · " + strings.ToLower(when[:1]) + when[1:]
			}
		}
	}
	return Notification{
		Type:       "task",
		Title:      v.Title,
		Body:       when,
		Tag:        "task-" + v.ID,
		URL:        "/#/task/" + v.ID,
		TaskID:     v.ID,
		Occurrence: v.State.Occurrence(),
		Today:      v.Today,
		Tomorrow:   v.Today.AddDays(1),
		Actions:    len(v.Def.Checklist) == 0,
	}
}

// SendDailyNotifications sends each user their daily notifications once
// their notification time has passed today (in their time zone). It returns
// how many notifications were delivered.
func (s *Service) SendDailyNotifications(ctx context.Context) (int, error) {
	if s.push == nil {
		return 0, nil
	}
	var users []*store.User
	if err := s.db.Tx(ctx, func(tx *store.Tx) (err error) { users, err = tx.AllUsers(); return err }); err != nil {
		return 0, err
	}
	total := 0
	var errs []error
	for _, u := range users {
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		n, err := s.notifyUser(ctx, u)
		total += n
		if err != nil {
			errs = append(errs, fmt.Errorf("notifying %s: %w", u.Email, err))
		}
	}
	return total, errors.Join(errs...)
}

func (s *Service) notifyUser(ctx context.Context, u *store.User) (int, error) {
	if !u.Notify {
		return 0, nil
	}
	loc, err := s.location(u.TZ)
	if err != nil {
		return 0, nil // no time zone yet
	}
	now := s.now().In(loc)
	today := engine.DateOf(now)
	if u.NotifiedOn == today || now.Format("15:04") < u.NotifyTime {
		return 0, nil
	}
	var subs []*store.PushSubscription
	if err := s.db.Tx(ctx, func(tx *store.Tx) (err error) { subs, err = tx.PushSubscriptions(u.ID); return err }); err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	plan, err := s.planNotifications(ctx, u)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, n := range plan.send {
		sent += s.deliver(ctx, subs, n)
	}
	err = s.db.Tx(ctx, func(tx *store.Tx) error {
		for _, n := range plan.leads {
			if err := tx.AddLeadReminder(u.ID, n.TaskID, n.Occurrence); err != nil {
				return err
			}
		}
		me, err := tx.GetUser(u.ID)
		if err != nil {
			return err
		}
		me.NotifiedOn = today
		return tx.UpdateUser(me)
	})
	return sent, err
}

// deliver sends n to every subscription, removing ones that are gone. It
// returns how many deliveries succeeded.
func (s *Service) deliver(ctx context.Context, subs []*store.PushSubscription, n Notification) int {
	payload, err := json.Marshal(n)
	if err != nil {
		slog.Error("encoding notification", "err", err)
		return 0
	}
	ok := 0
	for _, sub := range subs {
		gone, err := s.push.Send(ctx, sub, payload, notificationTTL)
		switch {
		case gone:
			s.db.Tx(ctx, func(tx *store.Tx) error { return tx.DeletePushSubscription("", sub.Endpoint) })
		case err != nil:
			slog.Warn("push delivery failed", "user", sub.UserID, "err", err)
		default:
			ok++
			s.db.Tx(ctx, func(tx *store.Tx) error { return tx.TouchPushSubscription(sub.ID, s.now()) })
		}
	}
	return ok
}

// SendTestNotification sends a test notification to u's devices.
func (s *Service) SendTestNotification(ctx context.Context, u *store.User) (int, error) {
	if s.push == nil {
		return 0, ErrPushDisabled
	}
	var subs []*store.PushSubscription
	if err := s.db.Tx(ctx, func(tx *store.Tx) (err error) { subs, err = tx.PushSubscriptions(u.ID); return err }); err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, invalid("notifications aren't turned on for any of your devices")
	}
	return s.deliver(ctx, subs, Notification{
		Type: "test", Title: "Taskmaster", Body: "Notifications are working.", Tag: "test", URL: "/#/settings",
	}), nil
}
