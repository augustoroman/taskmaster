package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/augustoroman/taskmaster/internal/engine"
	"github.com/augustoroman/taskmaster/internal/store"
)

type fixture struct {
	t   *testing.T
	ctx context.Context
	svc *Service
	now time.Time
	// admin can log in without an invitation.
	admin *store.User
}

func newFixture(t *testing.T) *fixture {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	f := &fixture{t: t, ctx: context.Background()}
	// Thursday, Oct 1, 2026, 9am Pacific.
	f.now = time.Date(2026, 10, 1, 16, 0, 0, 0, time.UTC)
	f.svc = New(db, []string{"Admin@Example.com"}, func() time.Time { return f.now })
	f.admin = f.login("admin@example.com")
	f.admin, err = f.svc.UpdateMe(f.ctx, f.admin, "", "America/Los_Angeles")
	require.NoError(t, err)
	return f
}

func (f *fixture) login(email string) *store.User {
	u, err := f.svc.Login(f.ctx, email, "", "")
	require.NoError(f.t, err)
	return u
}

func (f *fixture) advance(days int) { f.now = f.now.AddDate(0, 0, days) }

func (f *fixture) tag(u *store.User, name string) string {
	tag, err := f.svc.CreateTag(f.ctx, u, name, "")
	require.NoError(f.t, err)
	return tag.ID
}

func (f *fixture) share(u *store.User, tagID, email string, level store.Level) {
	_, err := f.svc.ShareTag(f.ctx, u, tagID, email, level)
	require.NoError(f.t, err)
}

func (f *fixture) task(u *store.User, in TaskInput, tags ...string) *TaskView {
	if in.Title == "" {
		in.Title = "task"
	}
	v, err := f.svc.CreateTask(f.ctx, u, in, tags, engine.Date{})
	require.NoError(f.t, err)
	return v
}

func d(s string) engine.Date { return engine.MustParseDate(s) }

var trash = TaskInput{Title: "Trash", Kind: engine.KindFixed, RRule: "FREQ=WEEKLY;BYDAY=TU", RRuleStart: d("2026-01-06")}
var filter = TaskInput{Title: "HVAC filter", Kind: engine.KindInterval, Interval: engine.Interval{N: 3, Unit: engine.Months}}

func TestLoginInviteOnly(t *testing.T) {
	f := newFixture(t)
	assert.Equal(t, "admin@example.com", f.admin.Email)

	_, err := f.svc.Login(f.ctx, "stranger@example.com", "", "")
	assert.ErrorIs(t, err, ErrNotInvited)

	// Invited by email before they have an account.
	house := f.tag(f.admin, "House")
	f.share(f.admin, house, "Sam@Example.com", store.LevelDo)
	sam, err := f.svc.Login(f.ctx, "sam@example.com", "Sam", "https://pic")
	require.NoError(t, err)
	assert.Equal(t, "Sam", sam.Name)

	tags, err := f.svc.ListTags(f.ctx, sam)
	require.NoError(t, err)
	require.Len(t, tags, 1)
	assert.Equal(t, store.LevelDo, tags[0].Level)
	assert.Equal(t, f.admin.ID, tags[0].Owner.ID)

	// Returning users get in, and their profile refreshes.
	again, err := f.svc.Login(f.ctx, "sam@example.com", "Samantha", "")
	require.NoError(t, err)
	assert.Equal(t, sam.ID, again.ID)
	assert.Equal(t, "Samantha", again.Name)
}

func TestAccessLevels(t *testing.T) {
	f := newFixture(t)
	house := f.tag(f.admin, "House")
	reader, doer, full := f.inviteAll(house)
	task := f.task(f.admin, filter, house)
	private := f.task(f.admin, filter)

	// Untagged tasks are private to the creator.
	for _, u := range []*store.User{reader, doer, full} {
		_, err := f.svc.GetTask(f.ctx, u, private.ID)
		assert.ErrorIs(t, err, ErrNotFound)
		got, err := f.svc.GetTask(f.ctx, u, task.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{house}, got.VisibleTagIDs)
	}

	a := Action{TaskID: task.ID}
	_, err := f.svc.Complete(f.ctx, reader, a, "", false)
	assert.ErrorIs(t, err, ErrPermission)
	_, err = f.svc.AddNote(f.ctx, reader, task.ID, "hi", engine.Date{})
	assert.ErrorIs(t, err, ErrPermission)

	_, err = f.svc.Complete(f.ctx, doer, a, "", false)
	assert.NoError(t, err)
	_, err = f.svc.Pause(f.ctx, doer, a, engine.Date{})
	assert.ErrorIs(t, err, ErrPermission)
	_, err = f.svc.ArchiveTask(f.ctx, doer, task.ID)
	assert.ErrorIs(t, err, ErrPermission)
	_, err = f.svc.UpdateTask(f.ctx, doer, task.ID, task.Version, filter, nil, engine.Date{})
	assert.ErrorIs(t, err, ErrPermission)

	_, err = f.svc.Pause(f.ctx, full, a, engine.Date{})
	assert.NoError(t, err)

	// The highest level across a task's tags wins.
	other := f.tag(f.admin, "Other")
	f.share(f.admin, other, "reader@example.com", store.LevelFull)
	both := f.task(f.admin, filter, house, other)
	got, err := f.svc.GetTask(f.ctx, reader, both.ID)
	require.NoError(t, err)
	assert.Equal(t, store.LevelFull, got.Level)
}

// inviteAll shares tag with a reader, a doer and a full-control user.
func (f *fixture) inviteAll(tag string) (reader, doer, full *store.User) {
	f.share(f.admin, tag, "reader@example.com", store.LevelRead)
	f.share(f.admin, tag, "doer@example.com", store.LevelDo)
	f.share(f.admin, tag, "full@example.com", store.LevelFull)
	return f.login("reader@example.com"), f.login("doer@example.com"), f.login("full@example.com")
}

func TestTaggingRules(t *testing.T) {
	f := newFixture(t)
	house := f.tag(f.admin, "House")
	_, _, full := f.inviteAll(house)
	mine := f.tag(full, "Mine")

	task := f.task(f.admin, filter, house)
	// Full on the task and on the tag: can add it (sharing the task with Mine's audience).
	got, err := f.svc.AddTaskTag(f.ctx, full, task.ID, mine)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{house, mine}, got.VisibleTagIDs)
	// The admin can't see "Mine", so it isn't listed for them.
	adminView, err := f.svc.GetTask(f.ctx, f.admin, task.ID)
	require.NoError(t, err)
	assert.Equal(t, []string{house}, adminView.VisibleTagIDs)

	// Can't add a tag you don't have full access to.
	_, err = f.svc.AddTaskTag(f.ctx, f.admin, task.ID, mine)
	assert.ErrorIs(t, err, ErrNotFound)
	_, err = f.svc.CreateTask(f.ctx, f.admin, filter, []string{mine}, engine.Date{})
	assert.ErrorIs(t, err, ErrNotFound)

	// Removing house cuts off everyone else, but the creator keeps full control.
	_, err = f.svc.RemoveTaskTag(f.ctx, full, task.ID, house)
	require.NoError(t, err)
	_, err = f.svc.RemoveTaskTag(f.ctx, full, task.ID, mine)
	require.NoError(t, err)
	_, err = f.svc.GetTask(f.ctx, full, task.ID)
	assert.ErrorIs(t, err, ErrNotFound)
	creatorView, err := f.svc.GetTask(f.ctx, f.admin, task.ID)
	require.NoError(t, err)
	assert.Equal(t, store.LevelFull, creatorView.Level)
}

func TestSharing(t *testing.T) {
	f := newFixture(t)
	house := f.tag(f.admin, "House")
	_, doer, full := f.inviteAll(house)

	// Full-control users can share; others can't see the shares.
	_, err := f.svc.ShareTag(f.ctx, full, house, "new@example.com", store.LevelRead)
	require.NoError(t, err)
	_, err = f.svc.ListShares(f.ctx, doer, house)
	assert.ErrorIs(t, err, ErrPermission)
	shares, err := f.svc.ListShares(f.ctx, full, house)
	require.NoError(t, err)
	assert.Len(t, shares, 4)

	// Sharing again changes the level.
	sv, err := f.svc.ShareTag(f.ctx, f.admin, house, "doer@example.com", store.LevelFull)
	require.NoError(t, err)
	assert.Equal(t, doer.ID, sv.User.ID)
	shares, _ = f.svc.ListShares(f.ctx, full, house)
	assert.Len(t, shares, 4)

	_, err = f.svc.ShareTag(f.ctx, full, house, "admin@example.com", store.LevelRead)
	assert.Error(t, err, "can't share with the owner")
	_, err = f.svc.ShareTag(f.ctx, full, house, "Bob <bob@example.com>", store.LevelRead)
	assert.Error(t, err)

	// Only the owner renames or deletes.
	_, err = f.svc.UpdateTag(f.ctx, full, house, "Home", "")
	assert.ErrorIs(t, err, ErrPermission)
	assert.ErrorIs(t, f.svc.DeleteTag(f.ctx, full, house), ErrPermission)

	// Leaving a tag.
	require.NoError(t, f.svc.RevokeShare(f.ctx, doer, sv.ID))
	tags, _ := f.svc.ListTags(f.ctx, doer)
	assert.Empty(t, tags)

	_, err = f.svc.CreateTag(f.ctx, f.admin, "house ", "")
	assert.NoError(t, err, "names are case-sensitive per owner")
	_, err = f.svc.CreateTag(f.ctx, f.admin, "House", "")
	var invalidErr *InvalidError
	assert.ErrorAs(t, err, &invalidErr)
}

func TestMissesRecordedOnRead(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, trash)
	assert.Equal(t, d("2026-10-06"), task.State.Due)

	f.advance(14) // Oct 15
	got, err := f.svc.GetTask(f.ctx, f.admin, task.ID)
	require.NoError(t, err)
	assert.Equal(t, d("2026-10-20"), got.State.Due)

	events, _, err := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 0, "")
	require.NoError(t, err)
	require.Len(t, events, 2)
	assert.Equal(t, engine.EventMissed, events[0].Kind)
	assert.Equal(t, d("2026-10-13"), events[0].Date)
	assert.Nil(t, events[0].User, "system event")

	// Change a miss to done: history only.
	missed := events[1]
	_, tv, err := f.svc.EditEvent(f.ctx, f.admin, missed.ID, EventEdit{MarkDone: true})
	require.NoError(t, err)
	assert.Equal(t, d("2026-10-20"), tv.State.Due)
	events, _, _ = f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 0, "")
	assert.Equal(t, engine.EventDone, events[1].Kind)
	assert.Equal(t, f.admin.ID, events[1].User.ID)
}

func TestSweep(t *testing.T) {
	f := newFixture(t)
	f.task(f.admin, trash)
	f.task(f.admin, filter)
	n, err := f.svc.Sweep(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)
	f.advance(7)
	n, err = f.svc.Sweep(f.ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	n, _ = f.svc.Sweep(f.ctx)
	assert.Equal(t, 0, n, "idempotent")
}

func TestBackdateCompletion(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, filter)
	f.advance(9) // Oct 10
	res, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID, Note: "used the MERV 13"}, "", false)
	require.NoError(t, err)
	assert.Equal(t, d("2027-01-10"), res.Task.State.Due)
	require.Len(t, res.Events, 1)
	assert.Equal(t, "used the MERV 13", res.Events[0].Note)

	// "Actually I did it last Tuesday."
	lastTuesday := d("2026-10-06")
	_, tv, err := f.svc.EditEvent(f.ctx, f.admin, res.Events[0].ID, EventEdit{Date: &lastTuesday})
	require.NoError(t, err)
	assert.Equal(t, d("2027-01-06"), tv.State.Due)

	future := d("2026-10-11")
	_, _, err = f.svc.EditEvent(f.ctx, f.admin, res.Events[0].ID, EventEdit{Date: &future})
	assert.Error(t, err)

	// Deleting the only completion leaves the due date alone.
	tv, err = f.svc.DeleteEvent(f.ctx, f.admin, res.Events[0].ID)
	require.NoError(t, err)
	assert.Equal(t, d("2027-01-06"), tv.State.Due)
}

func TestEditEventPermissions(t *testing.T) {
	f := newFixture(t)
	house := f.tag(f.admin, "House")
	_, doer, _ := f.inviteAll(house)
	task := f.task(f.admin, filter, house)
	res, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID}, "", false)
	require.NoError(t, err)
	note := "edited"
	_, _, err = f.svc.EditEvent(f.ctx, doer, res.Events[0].ID, EventEdit{Note: &note})
	assert.ErrorIs(t, err, ErrPermission, "do access can't edit others' events")

	own, err := f.svc.AddNote(f.ctx, doer, task.ID, "need a new filter size", engine.Date{})
	require.NoError(t, err)
	_, _, err = f.svc.EditEvent(f.ctx, doer, own.ID, EventEdit{Note: &note})
	assert.NoError(t, err)
}

func TestVersionConflict(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, filter)
	_, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID, Version: task.Version}, "", false)
	require.NoError(t, err)
	// A second person acting on the same stale version.
	_, err = f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID, Version: task.Version}, "", false)
	assert.ErrorIs(t, err, ErrConflict)
	_, err = f.svc.UpdateTask(f.ctx, f.admin, task.ID, task.Version, filter, nil, engine.Date{})
	assert.ErrorIs(t, err, ErrConflict)
}

func TestVersionNotStaleAfterCatchup(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, trash)
	f.advance(7) // a miss gets recorded on this read
	got, err := f.svc.GetTask(f.ctx, f.admin, task.ID)
	require.NoError(t, err)
	_, err = f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID, Version: got.Version}, "", false)
	assert.NoError(t, err)
}

func TestChecklist(t *testing.T) {
	f := newFixture(t)
	house := f.tag(f.admin, "House")
	_, doer, _ := f.inviteAll(house)
	in := filter
	in.Title = "Smoke detector batteries"
	in.Checklist = []store.ChecklistItem{{Title: "Hall"}, {Title: "Garage"}}
	task := f.task(f.admin, in, house)
	hall, garage := task.Checklist[0].ID, task.Checklist[1].ID

	res, err := f.svc.CheckItem(f.ctx, doer, Action{TaskID: task.ID, Note: "used 9V from drawer"}, hall)
	require.NoError(t, err)
	assert.Equal(t, doer.ID, res.Task.CheckedBy[engine.ItemID(hall)])
	require.Len(t, res.Events, 1)
	assert.Equal(t, store.EventNote, res.Events[0].Kind)
	assert.Equal(t, hall, res.Events[0].Data.ItemID)

	_, err = f.svc.Complete(f.ctx, doer, Action{TaskID: task.ID}, "", false)
	var actionErr *ActionError
	assert.ErrorAs(t, err, &actionErr)
	assert.ErrorIs(t, err, engine.ErrChecklistIncomplete)

	f.advance(3)
	res, err = f.svc.CheckItem(f.ctx, f.admin, Action{TaskID: task.ID}, garage)
	require.NoError(t, err)
	require.Len(t, res.Events, 1)
	assert.Equal(t, engine.EventDone, res.Events[0].Kind)
	assert.Equal(t, d("2027-01-04"), res.Task.State.Due)
	assert.Empty(t, res.Task.CheckedBy)

	// Removing an item via update keeps it for history but drops it from the checklist.
	got, err := f.svc.GetTask(f.ctx, f.admin, task.ID)
	require.NoError(t, err)
	in.Checklist = []store.ChecklistItem{{ID: garage, Title: "Garage"}, {Title: "Attic"}}
	updated, err := f.svc.UpdateTask(f.ctx, f.admin, task.ID, got.Version, in, nil, engine.Date{})
	require.NoError(t, err)
	require.Len(t, updated.Checklist, 3)
	assert.True(t, updated.Checklist[2].Removed)
	assert.Equal(t, hall, updated.Checklist[2].ID)
	assert.Len(t, updated.Def.Checklist, 2)
}

func TestScheduleChange(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, filter)
	_, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID}, "", false)
	require.NoError(t, err)
	got, _ := f.svc.GetTask(f.ctx, f.admin, task.ID)
	assert.Equal(t, d("2027-01-01"), got.State.Due)

	in := filter
	in.Interval = engine.Interval{N: 1, Unit: engine.Months}
	updated, err := f.svc.UpdateTask(f.ctx, f.admin, task.ID, got.Version, in, nil, engine.Date{})
	require.NoError(t, err)
	assert.Equal(t, d("2026-11-01"), updated.State.Due, "recomputed from the last completion")

	events, _, _ := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 0, "")
	assert.Equal(t, store.EventScheduleChanged, events[0].Kind)
}

func TestCycleSlots(t *testing.T) {
	f := newFixture(t)
	in := TaskInput{
		Title: "Weekend chores", Kind: engine.KindCycle, RRule: "FREQ=WEEKLY;BYDAY=SA", RRuleStart: d("2026-01-03"),
		Slots: []store.Slot{{Title: "Vacuum bedrooms"}, {Title: "Clean bathrooms"}},
	}
	task := f.task(f.admin, in)
	bedrooms, bathrooms := task.Slots[0].ID, task.Slots[1].ID
	assert.Equal(t, engine.SlotID(bedrooms), task.State.Slot)
	assert.Equal(t, d("2026-10-03"), task.State.Due)

	res, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID}, bathrooms, false)
	require.NoError(t, err)
	assert.Equal(t, bathrooms, res.Events[0].SlotID)
	assert.Equal(t, engine.SlotID(bedrooms), res.Task.State.Slot)

	// Removing the current slot moves to the first remaining one.
	in.Slots = []store.Slot{{ID: bathrooms, Title: "Clean bathrooms"}}
	updated, err := f.svc.UpdateTask(f.ctx, f.admin, task.ID, res.Task.Version, in, nil, engine.Date{})
	require.NoError(t, err)
	assert.Equal(t, engine.SlotID(bathrooms), updated.State.Slot)

	// Slot history.
	events, _, err := f.svc.ListEvents(f.ctx, f.admin, task.ID, bathrooms, 0, "")
	require.NoError(t, err)
	assert.Len(t, events, 1)
}

func TestUpcomingAndHidden(t *testing.T) {
	f := newFixture(t)
	house := f.tag(f.admin, "House")
	yearly := f.task(f.admin, TaskInput{Title: "yearly", Kind: engine.KindInterval, Interval: engine.Interval{N: 1, Unit: engine.Years}}, house)
	f.task(f.admin, TaskInput{Title: "yearly late", Kind: engine.KindInterval, Interval: engine.Interval{N: 1, Unit: engine.Years}}, house)
	f.task(f.admin, TaskInput{Title: "no tags", Kind: engine.KindOnce})
	f.task(f.admin, TaskInput{Title: "weekly", Kind: engine.KindInterval, Interval: engine.Interval{N: 1, Unit: engine.Weeks}})

	// Everything is due today (Oct 1); defer "yearly" by a week.
	_, err := f.svc.Defer(f.ctx, f.admin, Action{TaskID: yearly.ID}, d("2026-10-08"))
	require.NoError(t, err)
	// On Oct 3, both undeferred tasks are 2 days overdue, but that's much
	// later for a weekly task (lead 2 days) than a yearly one (lead 14).
	f.advance(2)
	items, err := f.svc.Upcoming(f.ctx, f.admin, nil, false)
	require.NoError(t, err)
	var titles []string
	for _, it := range items {
		titles = append(titles, it.Task.Title)
	}
	assert.Equal(t, []string{"weekly", "yearly late", "yearly"}, titles)
	assert.Equal(t, engine.GroupOverdue, items[0].Urgency.Group)
	assert.Equal(t, engine.GroupSoon, items[2].Urgency.Group)

	require.NoError(t, f.svc.SetTagHidden(f.ctx, f.admin, house, true))
	items, _ = f.svc.Upcoming(f.ctx, f.admin, nil, false)
	assert.Len(t, items, 1, "tasks whose tags are all hidden drop out; untagged ones stay")
	items, _ = f.svc.Upcoming(f.ctx, f.admin, nil, true)
	assert.Len(t, items, 3)
}

func TestArchive(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, trash)
	_, err := f.svc.ArchiveTask(f.ctx, f.admin, task.ID)
	require.NoError(t, err)
	list, _ := f.svc.ListTasks(f.ctx, f.admin, TaskFilter{})
	assert.Empty(t, list)

	f.advance(30)
	_, err = f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID}, "", false)
	assert.Error(t, err)
	got, err := f.svc.UnarchiveTask(f.ctx, f.admin, task.ID)
	require.NoError(t, err)
	assert.Equal(t, d("2026-11-03"), got.State.Due)
	events, _, _ := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 0, "")
	assert.Empty(t, events, "no misses recorded while archived")

	require.NoError(t, f.svc.DeleteTask(f.ctx, f.admin, task.ID))
	_, err = f.svc.GetTask(f.ctx, f.admin, task.ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestEventPaging(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, filter)
	for i := 0; i < 5; i++ {
		_, err := f.svc.AddNote(f.ctx, f.admin, task.ID, "note", engine.Date{})
		require.NoError(t, err)
	}
	page1, token, err := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 3, "")
	require.NoError(t, err)
	assert.Len(t, page1, 3)
	require.NotEmpty(t, token)
	page2, token, err := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 3, token)
	require.NoError(t, err)
	assert.Len(t, page2, 2)
	assert.Empty(t, token)
	assert.NotEqual(t, page1[2].ID, page2[0].ID)
}

func TestLastDone(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, filter)
	assert.True(t, task.LastDone.IsZero())
	f.advance(4)
	res, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID}, "", false)
	require.NoError(t, err)
	assert.Equal(t, d("2026-10-05"), res.Task.LastDone)

	back := d("2026-10-02")
	_, tv, err := f.svc.EditEvent(f.ctx, f.admin, res.Events[0].ID, EventEdit{Date: &back})
	require.NoError(t, err)
	assert.Equal(t, back, tv.LastDone)

	list, err := f.svc.ListTasks(f.ctx, f.admin, TaskFilter{})
	require.NoError(t, err)
	assert.Equal(t, back, list[0].LastDone)

	tv, err = f.svc.DeleteEvent(f.ctx, f.admin, res.Events[0].ID)
	require.NoError(t, err)
	assert.True(t, tv.LastDone.IsZero())
}

func TestUpdateTaskTags(t *testing.T) {
	f := newFixture(t)
	house, yard := f.tag(f.admin, "House"), f.tag(f.admin, "Yard")
	_, _, full := f.inviteAll(house)
	secret := f.tag(full, "Secret")
	task := f.task(f.admin, filter, house)

	// full adds their own tag while editing.
	got, err := f.svc.UpdateTask(f.ctx, full, task.ID, task.Version, filter, []string{house, secret}, engine.Date{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{house, secret}, got.VisibleTagIDs)

	// The admin can't see "Secret"; replacing tags keeps it.
	got, err = f.svc.UpdateTask(f.ctx, f.admin, task.ID, got.Version, filter, []string{yard}, engine.Date{})
	require.NoError(t, err)
	assert.Equal(t, []string{yard}, got.VisibleTagIDs)
	assert.ElementsMatch(t, []string{yard, secret}, got.TagIDs)

	// Can't add a tag without full access to it; nothing changes.
	other := f.tag(f.admin, "Other")
	_, err = f.svc.UpdateTask(f.ctx, full, task.ID, got.Version, filter, []string{secret, other}, engine.Date{})
	assert.ErrorIs(t, err, ErrNotFound, "full can't see Other")
	again, _ := f.svc.GetTask(f.ctx, f.admin, task.ID)
	assert.Equal(t, got.Version, again.Version)

	// nil leaves tags alone.
	got, err = f.svc.UpdateTask(f.ctx, f.admin, task.ID, got.Version, filter, nil, engine.Date{})
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{yard, secret}, got.TagIDs)
}

func TestUndo(t *testing.T) {
	f := newFixture(t)
	house := f.tag(f.admin, "House")
	_, doer, _ := f.inviteAll(house)
	task := f.task(f.admin, filter, house)

	res, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID, Note: "oops, wrong task"}, "", false)
	require.NoError(t, err)
	require.NotEqual(t, task.State.Due, res.Task.State.Due)

	// Someone else can't undo my action.
	_, err = f.svc.Undo(f.ctx, doer, task.ID, res.Task.Version)
	var invalidErr *InvalidError
	assert.ErrorAs(t, err, &invalidErr)

	got, err := f.svc.Undo(f.ctx, f.admin, task.ID, res.Task.Version)
	require.NoError(t, err)
	assert.Equal(t, task.State, got.State)
	assert.True(t, got.LastDone.IsZero())
	events, _, _ := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 0, "")
	assert.Empty(t, events)

	// Only once.
	_, err = f.svc.Undo(f.ctx, f.admin, task.ID, 0)
	assert.ErrorAs(t, err, &invalidErr)

	// Not after something else changed the task.
	res, err = f.svc.Defer(f.ctx, f.admin, Action{TaskID: task.ID}, d("2026-10-20"))
	require.NoError(t, err)
	_, err = f.svc.AddTaskTag(f.ctx, f.admin, task.ID, f.tag(f.admin, "Other"))
	require.NoError(t, err)
	_, err = f.svc.Undo(f.ctx, f.admin, task.ID, 0)
	assert.ErrorAs(t, err, &invalidErr)

	// Not after the window.
	res, err = f.svc.Skip(f.ctx, f.admin, Action{TaskID: task.ID})
	require.NoError(t, err)
	f.now = f.now.Add(16 * time.Minute)
	_, err = f.svc.Undo(f.ctx, f.admin, task.ID, res.Task.Version)
	assert.ErrorAs(t, err, &invalidErr)
}

func TestUndoChecklistCompletion(t *testing.T) {
	f := newFixture(t)
	in := filter
	in.Checklist = []store.ChecklistItem{{Title: "a"}, {Title: "b"}}
	task := f.task(f.admin, in)
	a, b := task.Checklist[0].ID, task.Checklist[1].ID
	res, err := f.svc.CheckItem(f.ctx, f.admin, Action{TaskID: task.ID}, a)
	require.NoError(t, err)
	before := res.Task.State
	res, err = f.svc.CheckItem(f.ctx, f.admin, Action{TaskID: task.ID}, b)
	require.NoError(t, err)
	require.Empty(t, res.Task.State.Checks, "completed")

	got, err := f.svc.Undo(f.ctx, f.admin, task.ID, res.Task.Version)
	require.NoError(t, err)
	assert.Equal(t, before, got.State, "back to one item checked")
	assert.Equal(t, f.admin.ID, got.CheckedBy[engine.ItemID(a)])
	events, _, _ := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 0, "")
	assert.Empty(t, events)
}

func TestEditDueDate(t *testing.T) {
	f := newFixture(t)
	task := f.task(f.admin, filter)
	assert.True(t, task.DueEditable)

	// Before any history, the due date can be set directly, with no event.
	got, err := f.svc.UpdateTask(f.ctx, f.admin, task.ID, task.Version, filter, nil, d("2026-11-15"))
	require.NoError(t, err)
	assert.Equal(t, d("2026-11-15"), got.State.Due)
	assert.False(t, got.State.Deferred)
	events, _, _ := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 0, "")
	assert.Empty(t, events)

	// Notes don't count as history.
	_, err = f.svc.AddNote(f.ctx, f.admin, task.ID, "remember the ladder", engine.Date{})
	require.NoError(t, err)
	got, err = f.svc.UpdateTask(f.ctx, f.admin, task.ID, got.Version, filter, nil, d("2026-11-20"))
	require.NoError(t, err)
	assert.Equal(t, d("2026-11-20"), got.State.Due)

	// Same schedule change plus a new due date: the due date wins.
	monthly := filter
	monthly.Interval = engine.Interval{N: 1, Unit: engine.Months}
	got, err = f.svc.UpdateTask(f.ctx, f.admin, task.ID, got.Version, monthly, nil, d("2026-12-01"))
	require.NoError(t, err)
	assert.Equal(t, d("2026-12-01"), got.State.Due)

	// Once it's been deferred (or done), use Defer.
	res, err := f.svc.Defer(f.ctx, f.admin, Action{TaskID: task.ID}, d("2026-12-05"))
	require.NoError(t, err)
	assert.False(t, res.Task.DueEditable)
	_, err = f.svc.UpdateTask(f.ctx, f.admin, task.ID, res.Task.Version, monthly, nil, d("2026-12-10"))
	var invalidErr *InvalidError
	assert.ErrorAs(t, err, &invalidErr)
	// Passing the current due date unchanged is fine.
	_, err = f.svc.UpdateTask(f.ctx, f.admin, task.ID, res.Task.Version, monthly, nil, d("2026-12-05"))
	assert.NoError(t, err)

	// Fixed tasks follow their rule.
	trashTask := f.task(f.admin, trash)
	assert.False(t, trashTask.DueEditable)
	_, err = f.svc.UpdateTask(f.ctx, f.admin, trashTask.ID, trashTask.Version, trash, nil, d("2026-10-08"))
	assert.ErrorAs(t, err, &invalidErr)
}

func TestCarryOver(t *testing.T) {
	f := newFixture(t)
	meds := TaskInput{Title: "Heartworm meds", Kind: engine.KindFixed, RRule: "FREQ=MONTHLY;BYMONTHDAY=1", RRuleStart: d("2026-01-01"), CarryOver: true}
	task := f.task(f.admin, meds)
	assert.True(t, task.CarryOver)
	assert.Equal(t, d("2026-10-01"), task.State.Due)

	f.advance(3) // Oct 4: overdue, nothing recorded as missed
	items, err := f.svc.Upcoming(f.ctx, f.admin, nil, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, engine.GroupOverdue, items[0].Urgency.Group)
	res, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: task.ID}, "", false)
	require.NoError(t, err)
	assert.Equal(t, d("2026-11-01"), res.Task.State.Due)
	events, _, _ := f.svc.ListEvents(f.ctx, f.admin, task.ID, "", 0, "")
	assert.Len(t, events, 1)

	// Switching to skip mode (not a schedule change) takes effect on the next miss.
	meds.CarryOver = false
	got, err := f.svc.UpdateTask(f.ctx, f.admin, task.ID, res.Task.Version, meds, nil, engine.Date{})
	require.NoError(t, err)
	assert.False(t, got.CarryOver)
	f.advance(30) // Nov 3
	got, err = f.svc.GetTask(f.ctx, f.admin, task.ID)
	require.NoError(t, err)
	assert.Equal(t, d("2026-12-01"), got.State.Due, "Nov 1 was missed and skipped")

	// Only fixed tasks keep the flag.
	other := filter
	other.CarryOver = true
	it := f.task(f.admin, other)
	assert.False(t, it.CarryOver)
}

func TestTagColors(t *testing.T) {
	f := newFixture(t)
	colorOf := func(u *store.User, id string) string {
		tags, err := f.svc.ListTags(f.ctx, u)
		require.NoError(t, err)
		for _, tag := range tags {
			if tag.ID == id {
				return tag.Color
			}
		}
		return "not visible"
	}

	// New tags get distinct palette colors.
	seen := map[string]bool{}
	for i := 0; i < len(tagPalette); i++ {
		tag, err := f.svc.CreateTag(f.ctx, f.admin, fmt.Sprint("tag", i), "")
		require.NoError(t, err)
		assert.Contains(t, tagPalette, tag.Color)
		assert.False(t, seen[tag.Color], "reused %s while others were free", tag.Color)
		seen[tag.Color] = true
	}

	house := f.tag(f.admin, "House")
	require.NoError(t, f.svc.SetTagColor(f.ctx, f.admin, house, "#FF0000"))
	assert.Equal(t, "#ff0000", colorOf(f.admin, house))

	// Shared: the recipient starts with the sharer's color, before and after
	// they have an account.
	f.share(f.admin, house, "sam@example.com", store.LevelDo)
	f.share(f.admin, house, "existing@example.com", store.LevelRead)
	sam := f.login("sam@example.com")
	assert.Equal(t, "#ff0000", colorOf(sam, house))

	// Each person's color is their own.
	require.NoError(t, f.svc.SetTagColor(f.ctx, sam, house, "#00aa00"))
	require.NoError(t, f.svc.SetTagColor(f.ctx, f.admin, house, "#0000ff"))
	assert.Equal(t, "#00aa00", colorOf(sam, house))
	assert.Equal(t, "#0000ff", colorOf(f.admin, house))
	existing := f.login("existing@example.com")
	assert.Equal(t, "#ff0000", colorOf(existing, house), "kept the color it was shared with")

	// Renaming without a color keeps it.
	_, err := f.svc.UpdateTag(f.ctx, f.admin, house, "Home", "")
	require.NoError(t, err)
	assert.Equal(t, "#0000ff", colorOf(f.admin, house))

	var invalidErr *InvalidError
	assert.ErrorAs(t, f.svc.SetTagColor(f.ctx, sam, house, "red"), &invalidErr)
	assert.ErrorIs(t, f.svc.SetTagColor(f.ctx, sam, f.tag(f.admin, "Mine"), "#123456"), ErrNotFound)
}

func TestOfflineReplay(t *testing.T) {
	f := newFixture(t)
	house := f.tag(f.admin, "House")
	_, doer, _ := f.inviteAll(house)

	// Trash (due Tue Oct 6) done Tuesday while offline, synced Thursday after
	// the sweep recorded it missed: the miss becomes a done.
	trashTask := f.task(f.admin, trash, house)
	f.advance(7) // Thu Oct 8
	_, err := f.svc.Sweep(f.ctx)
	require.NoError(t, err)
	off := &Offline{Occurrence: d("2026-10-06"), Date: d("2026-10-06")}
	res, err := f.svc.Complete(f.ctx, doer, Action{TaskID: trashTask.ID, Note: "did it before the truck", Offline: off}, "", false)
	require.NoError(t, err)
	require.Len(t, res.Events, 1)
	assert.Equal(t, engine.EventDone, res.Events[0].Kind)
	assert.Equal(t, d("2026-10-06"), res.Events[0].Date)
	assert.Equal(t, doer.ID, res.Events[0].User.ID)
	assert.Equal(t, d("2026-10-13"), res.Task.State.Due)

	// Retrying the same queued action is harmless.
	res, err = f.svc.Complete(f.ctx, doer, Action{TaskID: trashTask.ID, Offline: off}, "", false)
	require.NoError(t, err)
	assert.Empty(t, res.Events)

	// An interval task done offline, synced before anything else happened:
	// applies as usual.
	filterTask := f.task(f.admin, filter, house) // due Oct 8 (created today)
	res, err = f.svc.Complete(f.ctx, doer, Action{TaskID: filterTask.ID, Offline: &Offline{Occurrence: d("2026-10-08"), Date: d("2026-10-08")}}, "", false)
	require.NoError(t, err)
	assert.Equal(t, d("2027-01-08"), res.Task.State.Due)

	// Someone else already did it: the queued one can't apply.
	_, err = f.svc.Complete(f.ctx, f.admin, Action{TaskID: filterTask.ID, Offline: &Offline{Occurrence: d("2027-01-08"), Date: d("2026-10-08")}}, "", false)
	require.NoError(t, err)
	_, err = f.svc.CheckItem(f.ctx, doer, Action{TaskID: filterTask.ID, Offline: &Offline{Occurrence: d("2027-01-08"), Date: d("2026-10-08")}}, "x")
	var actionErr *ActionError
	assert.ErrorAs(t, err, &actionErr, "checklist item that no longer applies")

	// Not in the future.
	_, err = f.svc.Complete(f.ctx, doer, Action{TaskID: filterTask.ID, Offline: &Offline{Occurrence: d("2027-04-08"), Date: d("2026-10-09")}}, "", false)
	var invalidErr *InvalidError
	assert.ErrorAs(t, err, &invalidErr)
}

func TestOfflineReplayFixedLate(t *testing.T) {
	// A carry-over task done offline on Oct 3 and synced Oct 10: next is Nov 1.
	f := newFixture(t)
	meds := f.task(f.admin, TaskInput{Title: "Meds", Kind: engine.KindFixed, RRule: "FREQ=MONTHLY;BYMONTHDAY=1", RRuleStart: d("2026-01-01"), CarryOver: true})
	f.advance(9)
	res, err := f.svc.Complete(f.ctx, f.admin, Action{TaskID: meds.ID, Offline: &Offline{Occurrence: d("2026-10-01"), Date: d("2026-10-03")}}, "", false)
	require.NoError(t, err)
	assert.Equal(t, d("2026-10-03"), res.Events[0].Date)
	assert.Equal(t, d("2026-11-01"), res.Task.State.Due)
}

func TestSkip(t *testing.T) {
	f := newFixture(t)
	// "Not this time" keeps the schedule: six months after Oct 1, even if
	// skipped later, and offline.
	vent := TaskInput{Title: "Dryer vent", Kind: engine.KindInterval, Interval: engine.Interval{N: 6, Unit: engine.Months}}
	task, err := f.svc.CreateTask(f.ctx, f.admin, vent, nil, d("2026-10-01"))
	require.NoError(t, err)
	f.advance(10)
	res, err := f.svc.Skip(f.ctx, f.admin, Action{TaskID: task.ID, Note: "out of town", Offline: &Offline{Occurrence: d("2026-10-01"), Date: d("2026-10-09")}})
	require.NoError(t, err)
	assert.Equal(t, d("2027-04-01"), res.Task.State.Due)
	assert.Equal(t, engine.EventSkipped, res.Events[0].Kind)
	assert.Equal(t, "out of town", res.Events[0].Note)
	assert.True(t, res.Task.LastDone.IsZero(), "skipping isn't doing")

	// A once task is closed as "won't do".
	reg, err := f.svc.CreateTask(f.ctx, f.admin, TaskInput{Title: "Register for the race", Kind: engine.KindOnce}, nil, d("2026-10-15"))
	require.NoError(t, err)
	res, err = f.svc.Skip(f.ctx, f.admin, Action{TaskID: reg.ID, Note: "sold out"})
	require.NoError(t, err)
	assert.True(t, res.Task.State.Done)
	list, _ := f.svc.ListTasks(f.ctx, f.admin, TaskFilter{})
	for _, v := range list {
		assert.NotEqual(t, reg.ID, v.ID, "closed tasks leave the active list")
	}
}
