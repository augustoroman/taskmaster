package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	pb "github.com/augustoroman/taskmaster/gen/taskmaster/v1"
	"github.com/augustoroman/taskmaster/gen/taskmaster/v1/taskmasterv1connect"
	"github.com/augustoroman/taskmaster/internal/app"
	"github.com/augustoroman/taskmaster/internal/auth"
	"github.com/augustoroman/taskmaster/internal/store"
)

// headerAuth logs in as the X-Test-User header, if present.
type headerAuth struct{}

func (headerAuth) Authenticate(r *http.Request) (auth.Identity, error) {
	if email := r.Header.Get("X-Test-User"); email != "" {
		return auth.Identity{Email: email}, nil
	}
	return auth.Identity{}, auth.ErrNotLoggedIn
}

func setup(t *testing.T) (client func(email string) taskmasterv1connect.TaskmasterServiceClient) {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	svc := app.New(db, []string{"admin@example.com"}, time.Now)
	mux := http.NewServeMux()
	path, h := New(svc, headerAuth{}, Pages{})
	mux.Handle(path, h)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return func(email string) taskmasterv1connect.TaskmasterServiceClient {
		return taskmasterv1connect.NewTaskmasterServiceClient(srv.Client(), srv.URL,
			connect.WithProtoJSON(),
			connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
				return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
					if email != "" {
						req.Header().Set("X-Test-User", email)
					}
					return next(ctx, req)
				}
			})))
	}
}

func code(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return connect.CodeUnknown
}

func TestAuthGate(t *testing.T) {
	client := setup(t)
	ctx := context.Background()

	_, err := client("").GetMe(ctx, connect.NewRequest(&pb.GetMeRequest{}))
	assert.Equal(t, connect.CodeUnauthenticated, code(err))

	_, err = client("stranger@example.com").GetMe(ctx, connect.NewRequest(&pb.GetMeRequest{}))
	assert.Equal(t, connect.CodePermissionDenied, code(err))

	me, err := client("admin@example.com").GetMe(ctx, connect.NewRequest(&pb.GetMeRequest{}))
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", me.Msg.User.Email)
}

func TestTaskRoundTrip(t *testing.T) {
	admin := setup(t)("admin@example.com")
	ctx := context.Background()

	created, err := admin.CreateTask(ctx, connect.NewRequest(&pb.CreateTaskRequest{
		Task: &pb.TaskInput{
			Title: "Smoke detectors",
			Schedule: &pb.Schedule{
				Kind: pb.ScheduleKind_SCHEDULE_KIND_INTERVAL, IntervalN: 1, IntervalUnit: pb.IntervalUnit_INTERVAL_UNIT_YEARS,
			},
			Checklist: []*pb.ChecklistItem{{Title: "Hall"}, {Title: "Garage"}},
		},
		FirstDue: "2026-11-01",
	}))
	require.NoError(t, err)
	task := created.Msg.Task
	assert.Equal(t, "2026-11-01", task.State.Due)
	assert.Equal(t, pb.AccessLevel_ACCESS_LEVEL_FULL, task.MyAccess)
	assert.Len(t, task.Projected, 5)
	require.Len(t, task.Checklist, 2)

	res, err := admin.CheckItem(ctx, connect.NewRequest(&pb.CheckItemRequest{Id: task.Id, ItemId: task.Checklist[0].Id}))
	require.NoError(t, err)
	require.Len(t, res.Msg.Task.State.Checks, 1)
	assert.Equal(t, "admin@example.com", res.Msg.Task.State.Checks[0].User.Email)

	_, err = admin.Complete(ctx, connect.NewRequest(&pb.CompleteRequest{Id: task.Id}))
	assert.Equal(t, connect.CodeFailedPrecondition, code(err), "checklist incomplete")

	_, err = admin.Complete(ctx, connect.NewRequest(&pb.CompleteRequest{Id: task.Id, Version: task.Version}))
	assert.Equal(t, connect.CodeAborted, code(err), "stale version")

	_, err = admin.Defer(ctx, connect.NewRequest(&pb.DeferRequest{Id: task.Id, To: "someday"}))
	assert.Equal(t, connect.CodeInvalidArgument, code(err))

	_, err = admin.GetTask(ctx, connect.NewRequest(&pb.GetTaskRequest{Id: "missing"}))
	assert.Equal(t, connect.CodeNotFound, code(err))

	done, err := admin.Complete(ctx, connect.NewRequest(&pb.CompleteRequest{Id: task.Id, Force: true, Note: "garage needs a ladder"}))
	require.NoError(t, err)
	ev := done.Msg.Events[0]
	assert.Equal(t, pb.EventKind_EVENT_KIND_DONE, ev.Kind)
	assert.Equal(t, []string{task.Checklist[1].Id}, ev.UncheckedItemIds)
	assert.Equal(t, "garage needs a ladder", ev.Note)
}

func TestPages(t *testing.T) {
	db, err := store.Open(":memory:")
	require.NoError(t, err)
	defer db.Close()
	svc := app.New(db, []string{"admin@example.com"}, time.Now)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("app")) })
	h := Authenticate(svc, headerAuth{}, Pages{LoginURL: "https://auth.example.com/oauth2/tasks", LogoutURL: "https://auth.example.com/logout"}, ok)

	get := func(email string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/", nil)
		if email != "" {
			r.Header.Set("X-Test-User", email)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	w := get("")
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "https://auth.example.com/oauth2/tasks", w.Header().Get("Location"))

	w = get("stranger@example.com")
	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.Contains(t, w.Body.String(), "You need an invitation")
	assert.Contains(t, w.Body.String(), "stranger@example.com")
	assert.Contains(t, w.Body.String(), `href="https://auth.example.com/logout"`)

	w = get("admin@example.com")
	assert.Equal(t, "app", w.Body.String())
}
