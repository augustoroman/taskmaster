// Package server exposes the app service over Connect-RPC and handles
// authentication.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	pb "github.com/augustoroman/taskmaster/gen/taskmaster/v1"
	"github.com/augustoroman/taskmaster/gen/taskmaster/v1/taskmasterv1connect"
	"github.com/augustoroman/taskmaster/internal/app"
	"github.com/augustoroman/taskmaster/internal/auth"
	"github.com/augustoroman/taskmaster/internal/store"
)

// Handler implements the Connect service.
type Handler struct {
	svc *app.Service
}

var _ taskmasterv1connect.TaskmasterServiceHandler = (*Handler)(nil)

// New returns the API's HTTP path prefix and handler, with authentication.
func New(svc *app.Service, authn auth.Authenticator, pages Pages) (string, http.Handler) {
	path, h := taskmasterv1connect.NewTaskmasterServiceHandler(&Handler{svc})
	return path, Authenticate(svc, authn, pages, h)
}

// Authenticate identifies the user, applies the invite gate, and puts the
// user in the request context. API requests get Connect errors; page requests
// get a sign-in redirect or an explanation.
func Authenticate(svc *app.Service, authn auth.Authenticator, pages Pages, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isAPI := strings.HasPrefix(r.URL.Path, "/"+taskmasterv1connect.TaskmasterServiceName+"/")
		id, err := authn.Authenticate(r)
		if err != nil {
			slog.Debug("unauthenticated request", "path", r.URL.Path, "err", err)
			if isAPI {
				writeAPIError(w, connect.CodeUnauthenticated, "not logged in")
			} else {
				pages.notLoggedIn(w, r)
			}
			return
		}
		u, err := svc.Login(r.Context(), id.Email, id.Name, id.Picture)
		if errors.Is(err, app.ErrNotInvited) {
			if isAPI {
				writeAPIError(w, connect.CodePermissionDenied, "You need an invitation. Ask someone to share a tag with "+id.Email+".")
			} else {
				pages.notInvited(w, id.Email)
			}
			return
		}
		if err != nil {
			slog.Error("login failed", "email", id.Email, "err", err)
			if isAPI {
				writeAPIError(w, connect.CodeInternal, "internal error")
			} else {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

// writeAPIError writes a Connect-style JSON error.
func writeAPIError(w http.ResponseWriter, code connect.Code, msg string) {
	status := map[connect.Code]int{
		connect.CodeUnauthenticated:  http.StatusUnauthorized,
		connect.CodePermissionDenied: http.StatusForbidden,
	}[code]
	if status == 0 {
		status = http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"code": code.String(), "message": msg})
}

func user(ctx context.Context) *store.User { return auth.UserFrom(ctx) }

func invalidArg(msg string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(msg))
}

// toConnect maps app errors to Connect codes.
func toConnect(err error) error {
	var connectErr *connect.Error
	var invalidErr *app.InvalidError
	var actionErr *app.ActionError
	switch {
	case errors.As(err, &connectErr):
		return err
	case errors.Is(err, app.ErrNotFound), errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, errors.New("not found"))
	case errors.Is(err, app.ErrPermission):
		return connect.NewError(connect.CodePermissionDenied, err)
	case errors.Is(err, app.ErrConflict):
		return connect.NewError(connect.CodeAborted, err)
	case errors.As(err, &invalidErr):
		return connect.NewError(connect.CodeInvalidArgument, err)
	case errors.As(err, &actionErr):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err)
	}
	slog.Error("internal error", "err", err)
	return connect.NewError(connect.CodeInternal, errors.New("internal error"))
}

func respond[T any](msg *T, err error) (*connect.Response[T], error) {
	if err != nil {
		return nil, toConnect(err)
	}
	return connect.NewResponse(msg), nil
}

// ---- Users ----

func (h *Handler) me(ctx context.Context, u *store.User) (*pb.User, error) {
	devices, err := h.svc.PushDeviceCount(ctx, u)
	return mePB(u, devices), err
}

func (h *Handler) GetMe(ctx context.Context, _ *connect.Request[pb.GetMeRequest]) (*connect.Response[pb.GetMeResponse], error) {
	u, err := h.svc.GetMe(ctx, user(ctx))
	if err != nil {
		return nil, toConnect(err)
	}
	me, err := h.me(ctx, u)
	return respond(&pb.GetMeResponse{User: me}, err)
}

func (h *Handler) UpdateMe(ctx context.Context, req *connect.Request[pb.UpdateMeRequest]) (*connect.Response[pb.UpdateMeResponse], error) {
	m := req.Msg
	u, err := h.svc.UpdateMe(ctx, user(ctx), m.Name, m.TimeZone)
	if err == nil && (m.Notify != nil || m.NotifyTime != "") {
		notify := u.Notify
		if m.Notify != nil {
			notify = *m.Notify
		}
		u, err = h.svc.SetNotifySettings(ctx, u, notify, m.NotifyTime)
	}
	if err != nil {
		return nil, toConnect(err)
	}
	me, err := h.me(ctx, u)
	return respond(&pb.UpdateMeResponse{User: me}, err)
}

func (h *Handler) GetPushConfig(ctx context.Context, _ *connect.Request[pb.GetPushConfigRequest]) (*connect.Response[pb.GetPushConfigResponse], error) {
	key, err := h.svc.PushPublicKey()
	if errors.Is(err, app.ErrPushDisabled) {
		err = nil
	}
	return respond(&pb.GetPushConfigResponse{PublicKey: key}, err)
}

func (h *Handler) RegisterPush(ctx context.Context, req *connect.Request[pb.RegisterPushRequest]) (*connect.Response[pb.RegisterPushResponse], error) {
	m := req.Msg
	return respond(&pb.RegisterPushResponse{}, h.svc.RegisterPush(ctx, user(ctx), m.Endpoint, m.P256Dh, m.Auth, req.Header().Get("User-Agent")))
}

func (h *Handler) UnregisterPush(ctx context.Context, req *connect.Request[pb.UnregisterPushRequest]) (*connect.Response[pb.UnregisterPushResponse], error) {
	return respond(&pb.UnregisterPushResponse{}, h.svc.UnregisterPush(ctx, user(ctx), req.Msg.Endpoint))
}

func (h *Handler) SendTestNotification(ctx context.Context, _ *connect.Request[pb.SendTestNotificationRequest]) (*connect.Response[pb.SendTestNotificationResponse], error) {
	n, err := h.svc.SendTestNotification(ctx, user(ctx))
	if errors.Is(err, app.ErrPushDisabled) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	return respond(&pb.SendTestNotificationResponse{Delivered: int32(n)}, err)
}

// ---- Tags ----

func (h *Handler) ListTags(ctx context.Context, _ *connect.Request[pb.ListTagsRequest]) (*connect.Response[pb.ListTagsResponse], error) {
	tags, err := h.svc.ListTags(ctx, user(ctx))
	out := &pb.ListTagsResponse{}
	for _, t := range tags {
		out.Tags = append(out.Tags, tagPB(t))
	}
	return respond(out, err)
}

func (h *Handler) CreateTag(ctx context.Context, req *connect.Request[pb.CreateTagRequest]) (*connect.Response[pb.CreateTagResponse], error) {
	tag, err := h.svc.CreateTag(ctx, user(ctx), req.Msg.Name, req.Msg.Color)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.CreateTagResponse{Tag: tagPB(tag)}, nil)
}

func (h *Handler) UpdateTag(ctx context.Context, req *connect.Request[pb.UpdateTagRequest]) (*connect.Response[pb.UpdateTagResponse], error) {
	tag, err := h.svc.UpdateTag(ctx, user(ctx), req.Msg.Id, req.Msg.Name, req.Msg.Color)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.UpdateTagResponse{Tag: tagPB(tag)}, nil)
}

func (h *Handler) DeleteTag(ctx context.Context, req *connect.Request[pb.DeleteTagRequest]) (*connect.Response[pb.DeleteTagResponse], error) {
	return respond(&pb.DeleteTagResponse{}, h.svc.DeleteTag(ctx, user(ctx), req.Msg.Id))
}

func (h *Handler) SetTagHidden(ctx context.Context, req *connect.Request[pb.SetTagHiddenRequest]) (*connect.Response[pb.SetTagHiddenResponse], error) {
	return respond(&pb.SetTagHiddenResponse{}, h.svc.SetTagHidden(ctx, user(ctx), req.Msg.Id, req.Msg.Hidden))
}

func (h *Handler) SetTagColor(ctx context.Context, req *connect.Request[pb.SetTagColorRequest]) (*connect.Response[pb.SetTagColorResponse], error) {
	return respond(&pb.SetTagColorResponse{}, h.svc.SetTagColor(ctx, user(ctx), req.Msg.Id, req.Msg.Color))
}

func (h *Handler) SetTagNotify(ctx context.Context, req *connect.Request[pb.SetTagNotifyRequest]) (*connect.Response[pb.SetTagNotifyResponse], error) {
	return respond(&pb.SetTagNotifyResponse{}, h.svc.SetTagNotify(ctx, user(ctx), req.Msg.Id, req.Msg.Notify))
}

// ---- Sharing ----

func (h *Handler) ListShares(ctx context.Context, req *connect.Request[pb.ListSharesRequest]) (*connect.Response[pb.ListSharesResponse], error) {
	shares, err := h.svc.ListShares(ctx, user(ctx), req.Msg.TagId)
	out := &pb.ListSharesResponse{}
	for _, s := range shares {
		out.Shares = append(out.Shares, sharePB(s))
	}
	return respond(out, err)
}

func (h *Handler) ShareTag(ctx context.Context, req *connect.Request[pb.ShareTagRequest]) (*connect.Response[pb.ShareTagResponse], error) {
	share, err := h.svc.ShareTag(ctx, user(ctx), req.Msg.TagId, req.Msg.Email, levelFrom(req.Msg.Level))
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.ShareTagResponse{Share: sharePB(share)}, nil)
}

func (h *Handler) UpdateShare(ctx context.Context, req *connect.Request[pb.UpdateShareRequest]) (*connect.Response[pb.UpdateShareResponse], error) {
	share, err := h.svc.UpdateShare(ctx, user(ctx), req.Msg.Id, levelFrom(req.Msg.Level))
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.UpdateShareResponse{Share: sharePB(share)}, nil)
}

func (h *Handler) RevokeShare(ctx context.Context, req *connect.Request[pb.RevokeShareRequest]) (*connect.Response[pb.RevokeShareResponse], error) {
	return respond(&pb.RevokeShareResponse{}, h.svc.RevokeShare(ctx, user(ctx), req.Msg.Id))
}

// ---- Tasks ----

func (h *Handler) ListTasks(ctx context.Context, req *connect.Request[pb.ListTasksRequest]) (*connect.Response[pb.ListTasksResponse], error) {
	f := app.TaskFilter{
		TagIDs:          req.Msg.TagIds,
		IncludeArchived: req.Msg.IncludeArchived,
		IncludeDone:     req.Msg.IncludeDone,
		IncludeHidden:   req.Msg.IncludeHidden,
	}
	if req.Msg.UpdatedSince != nil {
		f.UpdatedSince = req.Msg.UpdatedSince.AsTime()
	}
	tasks, err := h.svc.ListTasks(ctx, user(ctx), f)
	return respond(&pb.ListTasksResponse{Tasks: tasksPB(tasks)}, err)
}

func (h *Handler) GetTask(ctx context.Context, req *connect.Request[pb.GetTaskRequest]) (*connect.Response[pb.GetTaskResponse], error) {
	t, err := h.svc.GetTask(ctx, user(ctx), req.Msg.Id)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.GetTaskResponse{Task: taskPB(t)}, nil)
}

func (h *Handler) CreateTask(ctx context.Context, req *connect.Request[pb.CreateTaskRequest]) (*connect.Response[pb.CreateTaskResponse], error) {
	in, err := taskInputFrom(req.Msg.Task)
	if err != nil {
		return nil, err
	}
	first, err := parseDate("first_due", req.Msg.FirstDue)
	if err != nil {
		return nil, err
	}
	t, err := h.svc.CreateTask(ctx, user(ctx), in, req.Msg.TagIds, first)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.CreateTaskResponse{Task: taskPB(t)}, nil)
}

func (h *Handler) UpdateTask(ctx context.Context, req *connect.Request[pb.UpdateTaskRequest]) (*connect.Response[pb.UpdateTaskResponse], error) {
	in, err := taskInputFrom(req.Msg.Task)
	if err != nil {
		return nil, err
	}
	var tags []string
	if req.Msg.UpdateTags {
		tags = append([]string{}, req.Msg.TagIds...) // non-nil even when empty
	}
	due, err := parseDate("due", req.Msg.Due)
	if err != nil {
		return nil, err
	}
	t, err := h.svc.UpdateTask(ctx, user(ctx), req.Msg.Id, req.Msg.Version, in, tags, due)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.UpdateTaskResponse{Task: taskPB(t)}, nil)
}

func (h *Handler) ArchiveTask(ctx context.Context, req *connect.Request[pb.ArchiveTaskRequest]) (*connect.Response[pb.ArchiveTaskResponse], error) {
	t, err := h.svc.ArchiveTask(ctx, user(ctx), req.Msg.Id)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.ArchiveTaskResponse{Task: taskPB(t)}, nil)
}

func (h *Handler) UnarchiveTask(ctx context.Context, req *connect.Request[pb.UnarchiveTaskRequest]) (*connect.Response[pb.UnarchiveTaskResponse], error) {
	t, err := h.svc.UnarchiveTask(ctx, user(ctx), req.Msg.Id)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.UnarchiveTaskResponse{Task: taskPB(t)}, nil)
}

func (h *Handler) DeleteTask(ctx context.Context, req *connect.Request[pb.DeleteTaskRequest]) (*connect.Response[pb.DeleteTaskResponse], error) {
	return respond(&pb.DeleteTaskResponse{}, h.svc.DeleteTask(ctx, user(ctx), req.Msg.Id))
}

func (h *Handler) AddTaskTag(ctx context.Context, req *connect.Request[pb.AddTaskTagRequest]) (*connect.Response[pb.AddTaskTagResponse], error) {
	t, err := h.svc.AddTaskTag(ctx, user(ctx), req.Msg.TaskId, req.Msg.TagId)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.AddTaskTagResponse{Task: taskPB(t)}, nil)
}

func (h *Handler) RemoveTaskTag(ctx context.Context, req *connect.Request[pb.RemoveTaskTagRequest]) (*connect.Response[pb.RemoveTaskTagResponse], error) {
	t, err := h.svc.RemoveTaskTag(ctx, user(ctx), req.Msg.TaskId, req.Msg.TagId)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.RemoveTaskTagResponse{Task: taskPB(t)}, nil)
}

// ---- Actions ----

func actionResponse(r *app.ActionResult, err error) (*connect.Response[pb.ActionResponse], error) {
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.ActionResponse{Task: taskPB(r.Task), Events: eventsPB(r.Events)}, nil)
}

func offlineFrom(o *pb.Offline) (*app.Offline, error) {
	if o == nil {
		return nil, nil
	}
	occ, err := parseDate("offline.occurrence", o.Occurrence)
	if err != nil {
		return nil, err
	}
	date, err := parseDate("offline.date", o.Date)
	if err != nil {
		return nil, err
	}
	return &app.Offline{Occurrence: occ, Date: date}, nil
}

func (h *Handler) Complete(ctx context.Context, req *connect.Request[pb.CompleteRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	off, err := offlineFrom(m.Offline)
	if err != nil {
		return nil, err
	}
	return actionResponse(h.svc.Complete(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version, Note: m.Note, Offline: off}, m.AsSlotId, m.Force))
}

func (h *Handler) Skip(ctx context.Context, req *connect.Request[pb.SkipRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	return actionResponse(h.svc.Skip(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version, Note: m.Note}))
}

func (h *Handler) CheckItem(ctx context.Context, req *connect.Request[pb.CheckItemRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	off, err := offlineFrom(m.Offline)
	if err != nil {
		return nil, err
	}
	return actionResponse(h.svc.CheckItem(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version, Note: m.Note, Offline: off}, m.ItemId))
}

func (h *Handler) UncheckItem(ctx context.Context, req *connect.Request[pb.UncheckItemRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	return actionResponse(h.svc.UncheckItem(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version}, m.ItemId))
}

func (h *Handler) Defer(ctx context.Context, req *connect.Request[pb.DeferRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	to, err := parseDate("to", m.To)
	if err != nil {
		return nil, err
	}
	return actionResponse(h.svc.Defer(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version, Note: m.Note}, to))
}

func (h *Handler) ClearDeferral(ctx context.Context, req *connect.Request[pb.ClearDeferralRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	return actionResponse(h.svc.ClearDeferral(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version}))
}

func (h *Handler) SetCycleSlot(ctx context.Context, req *connect.Request[pb.SetCycleSlotRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	return actionResponse(h.svc.SetCycleSlot(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version, Note: m.Note}, m.SlotId))
}

func (h *Handler) Pause(ctx context.Context, req *connect.Request[pb.PauseRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	until, err := parseDate("until", m.Until)
	if err != nil {
		return nil, err
	}
	return actionResponse(h.svc.Pause(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version, Note: m.Note}, until))
}

func (h *Handler) Resume(ctx context.Context, req *connect.Request[pb.ResumeRequest]) (*connect.Response[pb.ActionResponse], error) {
	m := req.Msg
	return actionResponse(h.svc.Resume(ctx, user(ctx), app.Action{TaskID: m.Id, Version: m.Version, Note: m.Note}))
}

func (h *Handler) Undo(ctx context.Context, req *connect.Request[pb.UndoRequest]) (*connect.Response[pb.ActionResponse], error) {
	t, err := h.svc.Undo(ctx, user(ctx), req.Msg.Id, req.Msg.Version)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.ActionResponse{Task: taskPB(t)}, nil)
}

// ---- History ----

func (h *Handler) ListEvents(ctx context.Context, req *connect.Request[pb.ListEventsRequest]) (*connect.Response[pb.ListEventsResponse], error) {
	m := req.Msg
	events, next, err := h.svc.ListEvents(ctx, user(ctx), m.TaskId, m.SlotId, int(m.Limit), m.PageToken)
	return respond(&pb.ListEventsResponse{Events: eventsPB(events), NextPageToken: next}, err)
}

func (h *Handler) AddNote(ctx context.Context, req *connect.Request[pb.AddNoteRequest]) (*connect.Response[pb.AddNoteResponse], error) {
	date, err := parseDate("date", req.Msg.Date)
	if err != nil {
		return nil, err
	}
	ev, err := h.svc.AddNote(ctx, user(ctx), req.Msg.TaskId, req.Msg.Note, date)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.AddNoteResponse{Event: eventPB(ev)}, nil)
}

func (h *Handler) EditEvent(ctx context.Context, req *connect.Request[pb.EditEventRequest]) (*connect.Response[pb.EditEventResponse], error) {
	m := req.Msg
	edit := app.EventEdit{Note: m.Note, MarkDone: m.MarkDone}
	if m.Date != nil {
		date, err := parseDate("date", *m.Date)
		if err != nil {
			return nil, err
		}
		edit.Date = &date
	}
	ev, t, err := h.svc.EditEvent(ctx, user(ctx), m.Id, edit)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.EditEventResponse{Event: eventPB(ev), Task: taskPB(t)}, nil)
}

func (h *Handler) DeleteEvent(ctx context.Context, req *connect.Request[pb.DeleteEventRequest]) (*connect.Response[pb.DeleteEventResponse], error) {
	t, err := h.svc.DeleteEvent(ctx, user(ctx), req.Msg.Id)
	if err != nil {
		return nil, toConnect(err)
	}
	return respond(&pb.DeleteEventResponse{Task: taskPB(t)}, nil)
}

// ---- Upcoming ----

func (h *Handler) Upcoming(ctx context.Context, req *connect.Request[pb.UpcomingRequest]) (*connect.Response[pb.UpcomingResponse], error) {
	items, err := h.svc.Upcoming(ctx, user(ctx), req.Msg.TagIds, req.Msg.IncludeHidden)
	out := &pb.UpcomingResponse{}
	for _, it := range items {
		out.Items = append(out.Items, &pb.UpcomingItem{
			Task:      taskPB(it.Task),
			Group:     groups[it.Urgency.Group],
			DaysUntil: int32(it.Urgency.DaysUntil),
			Score:     it.Urgency.Score,
		})
	}
	return respond(out, err)
}
