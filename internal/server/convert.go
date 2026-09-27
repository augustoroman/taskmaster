package server

import (
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "github.com/augustoroman/taskmaster/gen/taskmaster/v1"
	"github.com/augustoroman/taskmaster/internal/app"
	"github.com/augustoroman/taskmaster/internal/engine"
	"github.com/augustoroman/taskmaster/internal/store"
)

// projectedCount is how many upcoming occurrences a task response includes.
const projectedCount = 5

func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func parseDate(field, s string) (engine.Date, error) {
	d, err := engine.ParseDate(s)
	if err != nil {
		return d, connect.NewError(connect.CodeInvalidArgument, &app.InvalidError{Msg: field + ": " + err.Error()})
	}
	return d, nil
}

func userPB(u *store.User) *pb.User {
	if u == nil {
		return nil
	}
	return &pb.User{Id: u.ID, Email: u.Email, Name: u.Name, PictureUrl: u.Picture, TimeZone: u.TZ}
}

var levels = map[store.Level]pb.AccessLevel{
	store.LevelNone: pb.AccessLevel_ACCESS_LEVEL_UNSPECIFIED,
	store.LevelRead: pb.AccessLevel_ACCESS_LEVEL_READ,
	store.LevelDo:   pb.AccessLevel_ACCESS_LEVEL_DO,
	store.LevelFull: pb.AccessLevel_ACCESS_LEVEL_FULL,
}

func levelFrom(l pb.AccessLevel) store.Level {
	for k, v := range levels {
		if v == l {
			return k
		}
	}
	return store.LevelNone
}

func tagPB(t *app.TagView) *pb.Tag {
	return &pb.Tag{Id: t.ID, Name: t.Name, Color: t.Color, Owner: userPB(t.Owner), MyAccess: levels[t.Level], Hidden: t.Hidden}
}

func sharePB(s *app.ShareView) *pb.Share {
	return &pb.Share{Id: s.ID, TagId: s.TagID, Email: s.Email, User: userPB(s.User), Level: levels[s.Level], CreatedAt: timestamp(s.CreatedAt)}
}

var kinds = map[engine.Kind]pb.ScheduleKind{
	engine.KindInterval: pb.ScheduleKind_SCHEDULE_KIND_INTERVAL,
	engine.KindFixed:    pb.ScheduleKind_SCHEDULE_KIND_FIXED,
	engine.KindCycle:    pb.ScheduleKind_SCHEDULE_KIND_CYCLE,
	engine.KindOnce:     pb.ScheduleKind_SCHEDULE_KIND_ONCE,
}

var units = map[engine.Unit]pb.IntervalUnit{
	engine.Days:   pb.IntervalUnit_INTERVAL_UNIT_DAYS,
	engine.Weeks:  pb.IntervalUnit_INTERVAL_UNIT_WEEKS,
	engine.Months: pb.IntervalUnit_INTERVAL_UNIT_MONTHS,
	engine.Years:  pb.IntervalUnit_INTERVAL_UNIT_YEARS,
}

func reverse[K, V comparable](m map[K]V, v V) (K, bool) {
	for k, vv := range m {
		if vv == v {
			return k, true
		}
	}
	var zero K
	return zero, false
}

func taskPB(v *app.TaskView) *pb.Task {
	t := &pb.Task{
		Id:          v.ID,
		Title:       v.Title,
		Description: v.Description,
		Priority:    int32(v.Priority),
		LeadDays:    int32(v.LeadDays),
		TimeZone:    v.TZ,
		Schedule: &pb.Schedule{
			Kind:         kinds[v.Kind],
			IntervalN:    int32(v.Interval.N),
			IntervalUnit: units[v.Interval.Unit],
			Rrule:        v.RRule,
			RruleStart:   v.RRuleStart.String(),
		},
		TagIds:            v.VisibleTagIDs,
		Creator:           userPB(v.Users[v.CreatorID]),
		CreatedAt:         timestamp(v.CreatedAt),
		UpdatedAt:         timestamp(v.UpdatedAt),
		Version:           v.Version,
		Archived:          !v.ArchivedAt.IsZero(),
		MyAccess:          levels[v.Level],
		PeriodDays:        v.Def.PeriodDays(v.Today),
		EffectiveLeadDays: int32(v.Def.LeadDays(v.Today)),
		State: &pb.TaskState{
			Due:           v.State.Due.String(),
			Deferred:      v.State.Deferred,
			DeferredFrom:  v.State.DeferredFrom.String(),
			Paused:        v.State.Paused,
			PauseUntil:    v.State.PauseUntil.String(),
			CurrentSlotId: string(v.State.Slot),
			Done:          v.State.Done,
		},
	}
	for _, s := range v.Slots {
		t.Slots = append(t.Slots, &pb.Slot{Id: s.ID, Title: s.Title, Description: s.Description, Removed: s.Removed})
	}
	for _, item := range v.Checklist {
		t.Checklist = append(t.Checklist, &pb.ChecklistItem{Id: item.ID, Title: item.Title, Removed: item.Removed})
		if date, ok := v.State.Checks[engine.ItemID(item.ID)]; ok {
			t.State.Checks = append(t.State.Checks, &pb.Check{
				ItemId: item.ID, Date: date.String(), User: userPB(v.Users[v.CheckedBy[engine.ItemID(item.ID)]]),
			})
		}
	}
	for _, p := range v.Def.Project(v.State, projectedCount) {
		t.Projected = append(t.Projected, &pb.Projected{Due: p.Due.String(), SlotId: string(p.Slot)})
	}
	return t
}

func tasksPB(views []*app.TaskView) []*pb.Task {
	out := make([]*pb.Task, len(views))
	for i, v := range views {
		out[i] = taskPB(v)
	}
	return out
}

func taskInputFrom(in *pb.TaskInput) (app.TaskInput, error) {
	if in == nil || in.Schedule == nil {
		return app.TaskInput{}, invalidArg("task and schedule are required")
	}
	kind, ok := reverse(kinds, in.Schedule.Kind)
	if !ok {
		return app.TaskInput{}, invalidArg("schedule kind is required")
	}
	out := app.TaskInput{
		Title:       in.Title,
		Description: in.Description,
		Priority:    engine.Priority(in.Priority),
		LeadDays:    int(in.LeadDays),
		TZ:          in.TimeZone,
		Kind:        kind,
		RRule:       in.Schedule.Rrule,
	}
	if kind == engine.KindInterval {
		unit, ok := reverse(units, in.Schedule.IntervalUnit)
		if !ok {
			return out, invalidArg("interval unit is required")
		}
		out.Interval = engine.Interval{N: int(in.Schedule.IntervalN), Unit: unit}
	}
	start, err := parseDate("rrule_start", in.Schedule.RruleStart)
	if err != nil {
		return out, err
	}
	out.RRuleStart = start
	for _, s := range in.Slots {
		if !s.Removed {
			out.Slots = append(out.Slots, store.Slot{ID: s.Id, Title: s.Title, Description: s.Description})
		}
	}
	for _, item := range in.Checklist {
		if !item.Removed {
			out.Checklist = append(out.Checklist, store.ChecklistItem{ID: item.Id, Title: item.Title})
		}
	}
	return out, nil
}

var eventKinds = map[engine.EventKind]pb.EventKind{
	engine.EventDone:            pb.EventKind_EVENT_KIND_DONE,
	engine.EventMissed:          pb.EventKind_EVENT_KIND_MISSED,
	engine.EventSkipped:         pb.EventKind_EVENT_KIND_SKIPPED,
	engine.EventDeferred:        pb.EventKind_EVENT_KIND_DEFERRED,
	engine.EventDeferralCleared: pb.EventKind_EVENT_KIND_DEFERRAL_CLEARED,
	engine.EventPaused:          pb.EventKind_EVENT_KIND_PAUSED,
	engine.EventResumed:         pb.EventKind_EVENT_KIND_RESUMED,
	engine.EventSlotSet:         pb.EventKind_EVENT_KIND_SLOT_SET,
	store.EventNote:             pb.EventKind_EVENT_KIND_NOTE,
	store.EventScheduleChanged:  pb.EventKind_EVENT_KIND_SCHEDULE_CHANGED,
}

func eventPB(e *app.EventView) *pb.Event {
	return &pb.Event{
		Id:               e.ID,
		TaskId:           e.TaskID,
		Kind:             eventKinds[e.Kind],
		User:             userPB(e.User),
		Date:             e.Date.String(),
		Occurrence:       e.Occurrence.String(),
		SlotId:           e.SlotID,
		Merged:           e.Data.Merged,
		CheckedItemIds:   e.Data.Checked,
		UncheckedItemIds: e.Data.Unchecked,
		From:             e.Data.From.String(),
		To:               e.Data.To.String(),
		Note:             e.Note,
		CreatedAt:        timestamp(e.CreatedAt),
		EditedAt:         timestamp(e.EditedAt),
		ItemId:           e.Data.ItemID,
	}
}

func eventsPB(views []*app.EventView) []*pb.Event {
	out := make([]*pb.Event, len(views))
	for i, v := range views {
		out[i] = eventPB(v)
	}
	return out
}

var groups = map[engine.Group]pb.UrgencyGroup{
	engine.GroupOverdue: pb.UrgencyGroup_URGENCY_GROUP_OVERDUE,
	engine.GroupToday:   pb.UrgencyGroup_URGENCY_GROUP_TODAY,
	engine.GroupSoon:    pb.UrgencyGroup_URGENCY_GROUP_SOON,
}
