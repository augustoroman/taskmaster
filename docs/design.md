# Taskmaster design

Status: agreed · 2026-09-26

Taskmaster is a self-hosted, multi-user task tracker for long-running household chores. It covers
chores that repeat N units after you last did them, chores tied to an external calendar, and
rotations of chores that share one recurring slot. It is a Go JSON API over SQLite with a small
TypeScript web frontend. Google login is handled by Caddy.

## 1. Scope

**v1 includes**
- Four schedule kinds: interval (N days/weeks/months/years after completion), fixed (calendar
  rule; misses are recorded and skipped, or optionally kept until done), cycle (a rotation of slots on a calendar rule), and once (a single
  task with an optional due date).
- Markdown descriptions, a history of completions and misses, timestamped notes, and editing past
  history.
- Defer, pause and resume, and archive.
- Checklists (items that must all be done to complete one occurrence).
- Priorities (3 levels), lead time, and a ranked "upcoming" view.
- Tags as the unit of sharing (read / do / full), with invites by email.
- Invite-only access on top of Caddy's Google login.
- A web UI.

**Deferred**
- Start/end windows and time of day (v1 uses dates only).
- Push and email notifications (v1 clients poll).
- PWA, offline support and native apps.
- Assignees and households.
- Holiday-aware rules (you move a single occurrence by hand instead).

## 2. Concepts

| Concept | Summary |
| --- | --- |
| User | Identified by the verified `email` claim. Has a display name, picture and time zone. |
| Tag | Owned by one user. Used to organize tasks and to share them. |
| Tag share | (tag, user or invited email, level). Levels are `read` < `do` < `full`. |
| Task | Title, markdown description, priority, lead time, time zone, a schedule, tags, and current state. |
| Schedule | One of `interval`, `fixed`, `cycle`, `once`. |
| Occurrence | The single pending instance of a task: its due date, plus checklist progress. |
| Event | An entry in the task's history: done, missed, skipped, deferred, paused, resumed, note, and so on. |
| Checklist item | A named part of a task, like "hallway smoke detector". Its state is tracked per occurrence. |
| Cycle slot | One entry in a cycle's rotation, like "vacuum bedrooms". |

A task has **at most one pending occurrence** at any time. Every schedule kind reduces to "what is
the current due date, and what happens when it is completed, missed, deferred or paused".

## 3. Dates and time zones

- All scheduling uses **civil dates** (`YYYY-MM-DD`). Timestamps are recorded only on events, to
  show when something was entered.
- Each user has an IANA time zone, set in settings and defaulting to the browser's.
- Each task stores its own time zone, copied from its creator when the task is created. Users with
  full control can change it.
- "Today" for a task means today in the task's time zone. This decides whether it's overdue and
  whether an occurrence was missed.
- Adding months or years clamps to the end of the month: Jan 31 + 1 month = Feb 28 (or 29). Go's
  `AddDate` normalizes into the next month instead, so the engine does its own date arithmetic.

## 4. Scheduling semantics

The scheduling engine is a pure Go package. It takes a task's rule, its current state and an
action, and returns the new state plus the events to append:
`Apply(rule, state, action, today) → (state', events)`. It has no I/O, which makes it
exhaustively table-testable. The stored state is a cache that could be rebuilt from the rule and
the event log.

Common state: `due` (the current occurrence's due date), `deferred_from` (the rule-computed due
date if deferred, otherwise null), `paused` (plus an optional `pause_until`), and checklist
progress.

Every completion has a **completion date `c`**. Marking a task done always uses today. To record
something done earlier ("I actually changed the HVAC filter last Tuesday"), mark it done and then
edit the date on that history entry (§4.8). This works for every task kind. The UI doesn't ask for
a date on every completion, because backdating is unusual.

### 4.1 Interval: "every N units after it was done"

- Rule: `every N {days|weeks|months|years}`.
- On completion: `due = c + N`. Doing it early or late shifts all later dates.
- It is never missed. It just becomes more overdue.
- On creation, the user picks the first due date (default today).

### 4.2 Fixed: "on the calendar; if you miss it, skip it"

- Rule: an iCalendar RRULE plus a start date. The UI builds common rules ("every Tuesday",
  "every other Thursday", "first Saturday of the month", "every year on Mar 1") and shows the raw
  RRULE only as an advanced option. Library: `github.com/teambition/rrule-go`.
- `due` is always a date the rule produces, unless the occurrence was deferred.
- On completion (`c ≤ due`): `due = the first rule date after due`. Doing it early does **not**
  move later dates.
- **Missed:** once `today > due` with no completion, the engine records a `missed` event for that
  occurrence date. Then `due` becomes the first rule date on or after today. Several rule dates can
  pass during a long gap; each one gets its own `missed` event.
- Misses are recorded by a background sweep (hourly, because tasks are in different time zones)
  and also on read, so the stored state is never stale. Both are idempotent.
- **Skip:** "we're not doing this one" records a `skipped` event and advances `due` exactly as a
  completion would.
- **Changing a miss to done:** see §4.8. It fixes the history only.
- **"If it's missed: keep it until it's done"** (a per-task option, for things like monthly
  meds): nothing is recorded as missed; the date stays pending and overdue until it's done.
  Completion then works like a cycle: `due = the first rule date after max(c, due)`, so doing it
  on the 3rd still makes the next one due on the 1st. Deferrals don't record merged skips.

### 4.3 Cycle: a rotation of slots on a calendar rule

- Rule: an RRULE (for example, every Saturday), plus an ordered list of slots
  `[A, B, …]` and a current slot.
- A slot has a title and an optional markdown description. Events record the slot, so you can
  filter history by slot ("when did we last do bathrooms?").
- **Missed:** nothing is recorded. The current slot just stays pending and shows as overdue since
  its original date ("carried over").
- On completion: the slot advances and `due = the first rule date after max(c, due)`.
  Using the weekly Saturday example:
  - Done on Saturday the 10th, the day it was due → next slot is due Saturday the 17th.
  - Done early, on Friday the 9th → next slot is due the 17th.
  - The 10th was missed and it was done Wednesday the 14th → next slot is due the 17th. The
    schedule recovers.
  - Nothing done for three weeks, then done → the next slot is due the next Saturday after that.
- **Override:** completing it "as slot X" records the completion against X, and the next slot is
  the one after X. For example, with rotation A → B → C and A due, doing C makes A next.
  "Set the current slot" (without completing) is also available.
- Defer and pause work the same way as for the other kinds.
- Slots have no checklists in v1.

### 4.4 Once

- The due date is optional. Once completed, the task leaves the active lists but keeps its history.
- It doesn't recur. Useful for things like "fix the fence gate".

### 4.5 Defer

- "Defer to date D, with a note" sets `due = D` and remembers `deferred_from`. The rule itself is
  unchanged. "Clear deferral" puts back the due date the rule would have produced.
- **Interval tasks:** the next due date is calculated from the completion date as usual. The
  deferral affects only this occurrence.
- **Fixed tasks:** if D is on or after one or more later rule dates, those occurrences merge into
  the deferred one. Each merged date gets a `skipped` event ("merged into deferral"). On
  completion, `due` becomes the first rule date after D.
- **Cycles:** on completion, the next due date is the first rule date after `max(c, D)`.

### 4.6 Pause and resume

- Pause takes an optional note and an optional `pause_until` date. The sweep resumes the task
  automatically on that date.
- While paused: the task is hidden from Upcoming, and no misses are recorded.
- On resume:

  | Kind | New due date |
  | --- | --- |
  | Interval | resume date + N |
  | Fixed | first rule date on or after the resume date; dates inside the pause are not recorded |
  | Cycle | same slot, due on the first rule date on or after the resume date |
  | Once | unchanged |

### 4.7 Checklists

- Checklist items are defined on the task. Users with full control can add, rename, reorder or
  remove them.
- For each occurrence, every item is either unchecked or checked (with who checked it, the date,
  and an optional note). Checking or unchecking is a do-level action.
- When the **last** item is checked, the occurrence completes, with `c` = that item's check date.
  So an interval task counts from when the whole set was finished, and the items never drift apart.
- **Complete anyway** completes the occurrence and records the unchecked items as skipped in the
  `done` event.
- A new occurrence starts with every item unchecked.
- If a fixed task is missed partway through its checklist, the `missed` event records which items
  were done.

### 4.8 Editing history

- Any event can have its date or note edited, or be deleted. The usual case is backdating a
  completion: mark it done, then change the date to when it actually happened. Dates can't be in
  the future.
- **Interval tasks:** after an edit, `due` is recalculated from the latest `done` event. So "done
  last Tuesday" moves the next due date earlier to match. An active deferral set after that event
  stays in place.
- **Fixed and cycle tasks:** history edits don't change the current `due` or slot. For fixed tasks,
  doing one early never moves the schedule anyway. For a cycle, moving a completion earlier only
  matters if it moves across a rule date, which is rare. For those cases, change the state directly
  by deferring or setting the slot.
- A `missed` event can be changed to `done` ("we did take the trash out, we just forgot to mark
  it"). This fixes the history only; the schedule is unaffected.

## 5. Priority and the Upcoming view

- **Priority** is `high` / `normal` / `low`, stored as an integer (1/2/3), so more levels can be
  added later.
- **Period `P`**, in days:
  - Interval: N converted to days (1 month = 30, 1 year = 365).
  - Fixed and cycle: the median gap between the next 5 rule dates.
  - Once: 7.
- **Lead time `L`**: how far ahead a task shows up in Upcoming. It can be set per task. The default
  is `clamp(round(P/4), 1, 14)` days, which works out to:

  | Task | Default lead time |
  | --- | --- |
  | Daily | 1 day |
  | Weekly | 2 days |
  | Monthly | 8 days |
  | Yearly | 2 weeks |

- **Urgency `u`**, where `d` = days until due (negative when overdue):
  - Not yet in the window (`d > L`): the task is not shown in Upcoming.
  - Inside the window (`0 ≤ d ≤ L`): `u = 1 − d/L`. This runs from 0 when the window opens to 1 on
    the due date.
  - Overdue (`d < 0`): `u = 1 + (−d)/L`. Measuring lateness against the task's own lead time means
    a weekly task 3 days late outranks a yearly task 3 days late.
- **Score** = `u × weight`, where the weight is 1.5 (high), 1.0 (normal) or 0.6 (low).
- **Display:** groups for Overdue, Today, and Coming up, each sorted by score. A separate "All"
  view lists everything by due date and can be filtered by tag.

With these defaults, a yearly task 7 days out (`u = 0.5`) ranks the same as a weekly task 1 day out
(`u = 0.5`). All the constants live in one file and are covered by tests, so they're easy to tune.

## 6. Access control

### 6.1 Login

- Caddy handles Google OAuth. The app reads the `access_token` cookie and verifies it following the
  Caddy JWT guide:
  - Accept only HMAC signatures (HS512). The key comes from `TASKS_JWT_KEY`.
  - Always check `exp`, and reject tokens with no `email`.
  - Optionally check `realm`.
  - Never read identity from headers or query parameters.
- **Invite-only:** a verified email gets in only if one of these is true:
  - it is listed in `TASKS_ADMIN_EMAILS`;
  - it already has a user row;
  - it has a pending tag share.

  Everyone else gets a 403 "you need an invite" page. Emails are compared lowercased.
- A user row is created on the first successful login. At that point pending shares addressed to
  that email are attached to the new user.
- The auth code sits behind an interface, so a bearer-token scheme for native apps can be added
  later without touching handlers.

### 6.2 Effective access

The access level on a task is:

- `full` if you created it (the creator always keeps full control), otherwise
- the highest level among the task's tags that are shared with you, or that you own,
  otherwise
- none. The task doesn't exist as far as you're concerned, and the API returns 404, not 403.

An untagged task is therefore private to its creator.

| Action | read | do | full |
| --- | :-: | :-: | :-: |
| View the task, its history and notes | ✓ | ✓ | ✓ |
| Complete, skip, defer, check checklist items, set the cycle slot | | ✓ | ✓ |
| Add notes; edit or delete your own notes and events | | ✓ | ✓ |
| Edit anyone's events | | | ✓ |
| Edit the title, description, schedule, priority, lead time, checklist or slots | | | ✓ |
| Pause or resume; archive or unarchive | | | ✓ |
| Hard delete | | | ✓ |
| Remove a tag from the task | | | ✓ |
| Add tag T to the task | | | ✓ on the task **and** on T |

### 6.3 Tags and shares

- A tag has one owner. Tag names only need to be unique per owner. When two tags share a name, the
  UI shows who owns each ("House · Sam").
- The owner, or anyone with `full` on a tag, can share it with an email at any level, change a
  share's level, or revoke a share. Nobody can revoke the owner.
- Only the owner can rename or delete a tag. Deleting a tag removes it from all its tasks. Tasks
  left with no tags become private to their creators again.
- A user can hide a tag that was shared with them, as a personal filter. Hiding doesn't change
  access.
- Tag colors are per person. A new tag gets a random pastel (avoiding colors the owner already
  uses). Sharing gives the recipient the sharer's current color; after that, each person's
  recolors only affect what they see.

## 7. Data model (SQLite)

- IDs are UUIDv7 strings.
- Timestamps are stored as RFC 3339 UTC; civil dates as `YYYY-MM-DD` text.
- The driver is `modernc.org/sqlite` (pure Go, no cgo).
- Migrations are embedded in the binary and run at startup.
- WAL mode is on. Every write that changes a task's state together with its events runs in one
  transaction.

```
users(id, email UNIQUE, name, picture, tz, created_at, last_login_at)

tags(id, owner_id → users, name, color, created_at)
tag_shares(id, tag_id → tags, user_id → users NULL, email, level, created_by, created_at,
           UNIQUE(tag_id, email))                -- user_id is NULL until the invitee logs in
tag_prefs(user_id, tag_id, hidden)

tasks(id, creator_id → users, title, description_md, priority, lead_days NULL, tz,
      schedule_kind,                             -- interval | fixed | cycle | once
      interval_n, interval_unit,                 -- interval only
      rrule, rrule_start,                        -- fixed, cycle
      current_slot_id NULL,                      -- cycle
      due NULL, deferred_from NULL,
      paused_at NULL, pause_until NULL,
      archived_at NULL, done_at NULL,            -- done_at: once only
      version, created_at, updated_at)
task_tags(task_id, tag_id, PRIMARY KEY(task_id, tag_id))

cycle_slots(id, task_id, position, title, description_md)
checklist_items(id, task_id, position, title, removed_at NULL)
checklist_checks(task_id, item_id, occurrence_due, user_id, date, note, created_at)

events(id, task_id, kind, user_id,
       date,                                      -- the civil date it counts for (editable)
       occurrence_due NULL,                       -- which occurrence it refers to
       slot_id NULL, note_md NULL,
       data_json NULL,                            -- details: skipped checklist items, old/new due, …
       created_at, edited_at NULL)
  kind ∈ done | missed | skipped | deferred | deferral_cleared | paused | resumed |
         note | slot_set | schedule_changed
```

- **Concurrency:** every task-changing request carries the `version` it was based on. If the
  version has moved on, the request fails with 409 and the client reloads. This also prepares for
  offline sync later.
- **Polling:** `tasks.updated_at` and `events.created_at` let a client ask "what changed since T".

## 8. API

**Transport: Connect-RPC**, defined in `proto/`:
- It speaks plain JSON over `POST /api.v1.TasksService/Method`, so it is still easy to call from
  curl.
- It generates a typed TypeScript client now, and Swift/Kotlin clients later, from one `.proto`
  file.
- Validation is declared in the schema.

Operations:

| Area | Operations |
| --- | --- |
| Me | `GetMe`, `UpdateMe` (name, time zone) |
| Tags | `ListTags` (owned and shared, with my level), `CreateTag`, `UpdateTag`, `DeleteTag` |
| Sharing | `ListShares(tag)`, `ShareTag(tag, email, level)`, `UpdateShare`, `RevokeShare`, `SetTagHidden` |
| Tasks | `ListTasks(filter: tags, kinds, include_archived, updated_since)`, `GetTask`, `CreateTask`, `UpdateTask(task, version)`, `ArchiveTask`, `UnarchiveTask`, `DeleteTask` |
| Tags on tasks | `AddTaskTag`, `RemoveTaskTag` |
| Doing | `Complete(task, note?, as_slot?, force_incomplete_checklist?)`, `Skip(task, note?)`, `CheckItem(task, item, note?)`, `UncheckItem`, `Defer(task, to_date, note?)`, `ClearDeferral`, `SetCycleSlot`, `Pause(task, until?, note?)`, `Resume` |
| History | `ListEvents(task, slot?, page)`, `AddNote(task, note, date?)`, `EditEvent(event, date?, note?, kind: missed→done?)`, `DeleteEvent` |
| Views | `Upcoming(horizon_days?)`: ranked, grouped, with score and reason ("overdue 3d", "due in 5d") |

- Every response that returns a task includes: your access level, the computed period and lead
  time, the next few projected due dates, and the current slot or checklist state. This lets
  clients stay thin.
- Errors: not found or no access → `not_found`; not allowed → `permission_denied`; version
  conflict → `aborted`; bad input → `invalid_argument`.

## 9. Web frontend

- A TypeScript single-page app built with Vite. It's embedded in the Go binary and served at `/`.
- Kept deliberately simple: a small view library (Preact) and a stylesheet, but no component
  framework or state library. Calls go through the generated API client.
- **Views:**
  - Upcoming (the home page)
  - All tasks, filterable by tag
  - Task detail: description, schedule summary, next dates, checklist or current slot, actions,
    and the history/notes timeline
  - Task editor, including the schedule builder
  - Tags and sharing
  - Settings (time zone)
- **Descriptions** use `../mde` with `images: false, videos: false`. Notes use a plain textarea
  and are rendered as markdown.
- Works on phone-width screens from the start, since it becomes the PWA later.

## 10. Server and deployment

- Go (current release) and a single binary. Configuration comes from environment variables:
  - `TASKS_JWT_KEY` (required)
  - `TASKS_REALM` (optional)
  - `TASKS_ADMIN_EMAILS`
  - `TASKS_DB` (default `./tasks.db`)
  - `TASKS_ADDR`
  - `TASKS_DEV_USER`: a fake login for local development; refused unless the listen address is
    loopback.
- Background sweep, hourly: records misses for fixed tasks and resumes paused tasks whose
  `pause_until` has arrived.
- Backups: SQLite `VACUUM INTO` on a schedule, or copy the file while WAL is checkpointed.
- The Caddyfile follows the guide: a new portal, a policy and a site block. Every path goes through
  the policy (no bypass), so the invite page is served by the app to anyone Caddy has
  authenticated.

**Existing prototype.** The repo has an early prototype (Go 1.19, Connect plus REST, an in-memory
store, a simple `schedule` package). It doesn't build as it stands: `main.go` imports
`…/gen/api`, but the generated code is in `api/`. The plan is to replace it rather than extend it.
The ideas from `schedule` (interval vs skip-missed modes) carry into the engine. Routing uses the standard library `net/http` mux.

**Package layout**

```
cmd/taskmaster/      main, config, hourly sweep
internal/engine/     pure scheduling and ranking (no I/O), with heavy tests
internal/store/      SQLite schema, migrations, queries
internal/app/        access control, catch-up, actions, history: the rules in §4 and §6
internal/auth/       JWT verification, dev user
internal/server/     Connect handlers, proto conversion, auth middleware
proto/               .proto API definition (Connect-RPC)
gen/                 generated Go code (scripts/gen-proto.sh), committed
web/                 Vite TypeScript app
docs/                this document
```

## 11. Decisions

- API transport: Connect-RPC.
- mde: `images: false, videos: false`.
- A task's creator keeps full control even after all its tags are removed.
- Routing: standard library mux (the prototype's `sandwich` router is dropped).
- Once tasks are included.

## 12. Milestones

1. **Engine:** date arithmetic, RRULE handling, the four schedule kinds, defer, pause, checklists,
   ranking. Exhaustive table tests for every scenario in section 4.
2. **Store and API:** schema, migrations, access control, auth and invite gate, API handlers,
   sweep. Integration tests against a temporary SQLite database.
3. **Web UI:** Upcoming, task detail and actions, editor with schedule builder, tags and sharing.
4. **Later:** start/end windows, time of day, notifications, PWA and offline, native apps,
   holiday rules, households or assignees.
