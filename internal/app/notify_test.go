package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/augustoroman/taskmaster/internal/engine"
	"github.com/augustoroman/taskmaster/internal/store"
)

type fakePush struct {
	sent map[string][]Notification // by endpoint
	gone map[string]bool
}

func (p *fakePush) PublicKey() string { return "test-key" }

func (p *fakePush) Send(_ context.Context, sub *store.PushSubscription, payload []byte, _ time.Duration) (bool, error) {
	if p.gone[sub.Endpoint] {
		return true, nil
	}
	var n Notification
	if err := json.Unmarshal(payload, &n); err != nil {
		return false, err
	}
	p.sent[sub.Endpoint] = append(p.sent[sub.Endpoint], n)
	return false, nil
}

func (p *fakePush) titles(endpoint string) []string {
	var out []string
	for _, n := range p.sent[endpoint] {
		out = append(out, n.Title)
	}
	return out
}

func notifyFixture(t *testing.T) (*fixture, *fakePush) {
	f := newFixture(t)
	push := &fakePush{sent: map[string][]Notification{}, gone: map[string]bool{}}
	f.svc.SetPush(push)
	// Thu Oct 1, 9am Pacific.
	require.NoError(t, f.svc.RegisterPush(f.ctx, f.admin, "https://push.example/admin", "p", "a", "test"))
	return f, push
}

func (f *fixture) daily() int {
	n, err := f.svc.SendDailyNotifications(f.ctx)
	require.NoError(f.t, err)
	return n
}

func TestDailyNotifications(t *testing.T) {
	f, push := notifyFixture(t)
	f.task(f.admin, TaskInput{Title: "Water plants", Kind: engine.KindInterval, Interval: engine.Interval{N: 3, Unit: engine.Days}})
	f.task(f.admin, TaskInput{Title: "Weekly", Kind: engine.KindInterval, Interval: engine.Interval{N: 1, Unit: engine.Weeks}})
	_, err := f.svc.CreateTask(f.ctx, f.admin, TaskInput{Title: "Later", Kind: engine.KindOnce}, nil, d("2026-10-05"))
	require.NoError(t, err)

	// Not before the notification time (08:00 default; it's 09:00).
	require.NoError(t, f.svc.UpdateNotify(f.admin, "10:00"))
	assert.Equal(t, 0, f.daily())
	require.NoError(t, f.svc.UpdateNotify(f.admin, "08:00"))
	assert.Equal(t, 2, f.daily())
	assert.ElementsMatch(t, []string{"Water plants", "Weekly"}, push.titles("https://push.example/admin"), "not Later (due Oct 5)")
	n := push.sent["https://push.example/admin"][0]
	assert.Equal(t, "Due today", n.Body)
	assert.True(t, n.Actions)
	assert.Equal(t, d("2026-10-02"), n.Tomorrow)

	// Once a day.
	assert.Equal(t, 0, f.daily())
	f.advance(1)
	push.sent = map[string][]Notification{}
	f.daily()
	assert.Equal(t, "Overdue since Thu, Oct 1", push.sent["https://push.example/admin"][0].Body)

	// Turned off.
	_, err = f.svc.SetNotifySettings(f.ctx, f.admin, false, "")
	require.NoError(t, err)
	f.advance(1)
	assert.Equal(t, 0, f.daily())
}

// UpdateNotify is a test helper for SetNotifySettings.
func (s *Service) UpdateNotify(u *store.User, at string) error {
	_, err := s.SetNotifySettings(context.Background(), u, true, at)
	return err
}

func TestNotificationLimit(t *testing.T) {
	f, push := notifyFixture(t)
	for _, title := range []string{"a", "b", "c", "d", "e", "f"} {
		f.task(f.admin, TaskInput{Title: title, Kind: engine.KindInterval, Interval: engine.Interval{N: 1, Unit: engine.Weeks}})
	}
	assert.Equal(t, 4, f.daily())
	sent := push.sent["https://push.example/admin"]
	require.Len(t, sent, 4)
	assert.Equal(t, "summary", sent[3].Type)
	assert.Equal(t, "3 more tasks", sent[3].Title)
}

func TestLeadReminders(t *testing.T) {
	f, push := notifyFixture(t)
	yearly := TaskInput{Title: "Smoke detectors", Kind: engine.KindInterval, Interval: engine.Interval{N: 1, Unit: engine.Years}}
	_, err := f.svc.CreateTask(f.ctx, f.admin, yearly, nil, d("2026-10-10")) // 9 days out
	require.NoError(t, err)
	monthly := TaskInput{Title: "Monthly", Kind: engine.KindInterval, Interval: engine.Interval{N: 1, Unit: engine.Months}}
	_, err = f.svc.CreateTask(f.ctx, f.admin, monthly, nil, d("2026-10-05")) // not rare enough
	require.NoError(t, err)

	assert.Equal(t, 0, f.daily(), "9 days out: too early")
	f.advance(2) // Oct 3: 7 days out
	assert.Equal(t, 1, f.daily())
	assert.Equal(t, "Due in 7 days (Sat, Oct 10)", push.sent["https://push.example/admin"][0].Body)
	f.advance(1)
	assert.Equal(t, 0, f.daily(), "reminded once")
	f.advance(6) // Oct 10: due
	assert.Equal(t, 2, f.daily(), "due today, plus the monthly one overdue")
}

func TestNotificationRecipients(t *testing.T) {
	f, push := notifyFixture(t)
	house := f.tag(f.admin, "House")
	reader, doer, _ := f.inviteAll(house)
	for _, u := range []*store.User{reader, doer} {
		require.NoError(t, f.svc.RegisterPush(f.ctx, u, "https://push.example/"+u.Email, "p", "a", ""))
		_, err := f.svc.UpdateMe(f.ctx, u, "", "America/Los_Angeles")
		require.NoError(t, err)
	}
	f.task(f.admin, TaskInput{Title: "Shared", Kind: engine.KindInterval, Interval: engine.Interval{N: 1, Unit: engine.Weeks}}, house)

	f.daily()
	assert.Equal(t, []string{"Shared"}, push.titles("https://push.example/admin"), "own tag: on by default")
	assert.Empty(t, push.titles("https://push.example/doer@example.com"), "shared tag: off by default")

	require.NoError(t, f.svc.SetTagNotify(f.ctx, doer, house, true))
	require.NoError(t, f.svc.SetTagNotify(f.ctx, reader, house, true))
	f.advance(1)
	f.daily()
	assert.Equal(t, []string{"Shared"}, push.titles("https://push.example/doer@example.com"))
	assert.Empty(t, push.titles("https://push.example/reader@example.com"), "read-only: never")

	// Gone subscriptions are removed.
	push.gone["https://push.example/doer@example.com"] = true
	f.advance(1)
	f.daily()
	n, err := f.svc.PushDeviceCount(f.ctx, doer)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
}

func TestTestNotification(t *testing.T) {
	f, push := notifyFixture(t)
	n, err := f.svc.SendTestNotification(f.ctx, f.admin)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Equal(t, "test", push.sent["https://push.example/admin"][0].Type)

	other := f.login("admin@example.com")
	require.NoError(t, f.svc.UnregisterPush(f.ctx, other, "https://push.example/admin"))
	_, err = f.svc.SendTestNotification(f.ctx, f.admin)
	var invalidErr *InvalidError
	assert.ErrorAs(t, err, &invalidErr)
}
