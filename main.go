package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/augustoroman/sandwich"
	"github.com/augustoroman/sandwich/chain"
	"github.com/augustoroman/taskmaster/gen/api"
	"github.com/augustoroman/taskmaster/gen/api/apiconnect"
	"github.com/augustoroman/taskmaster/tasks"
	"github.com/bufbuild/connect-go"
	"golang.org/x/exp/slog"
	"gopkg.in/alecthomas/kingpin.v2"
)

//go:embed website
var website embed.FS

type ConnectMux struct{ sandwich.Router }

func (m ConnectMux) RegisterConnect(api string, handler http.Handler) {
	slog.Info(api)
	m.Post(api+":method*", handler)
}

func main() {
	addr := kingpin.Flag("addr", "Address to serve on").Default("localhost:12345").String()
	kingpin.Parse()

	var store tasks.Store = &tasks.Mem{}

	mux := ConnectMux{sandwich.TheUsual()}
	staticFiles := sandwich.ServeFS(fallthroughFS{os.DirFS("."), website}, "website", "path")
	mux.Get("/favicon.ico", sandwich.NoLog, staticFiles)
	mux.Get("/:path*", staticFiles)

	mux.RegisterConnect(apiconnect.NewTaskServiceHandler(taskServer{}, alwaysValidateMessages()))

	api := mux.SubRouter("/api/")
	api.OnErr(sandwich.HandleErrorJson)
	api.SetAs(store, (*tasks.Store)(nil))
	api.Use(requestContext, setJson)

	api.Get("/tasks", store.List, renderTaskList)
	api.Post("/tasks", parseRequestTask, store.Add, renderID)
	api.Get("/tasks/:id", idFromParam, store.Get, renderSingleTask)
	api.On("DO", "/tasks/:id", idFromParam, store.Do, store.Get, renderSingleTask)
	api.Delete("/tasks/:id", idFromParam, store.Delete, store.Get, renderSingleTask)

	l, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Serving on http://%s/", l.Addr())
	if err := http.Serve(l, mux); err != nil {
		log.Fatal(err)
	}
}

type taskServer struct{}

func (t taskServer) List(ctx context.Context, req *connect.Request[api.ListRequest]) (*connect.Response[api.ListResponse], error) {
	return connect.NewResponse(&api.ListResponse{
		Task: []*api.Task{
			{Title: "Hi there"},
		},
	}), nil
}
func (t taskServer) Get(ctx context.Context, req *connect.Request[api.GetRequest]) (*connect.Response[api.GetResponse], error) {
	return connect.NewResponse(&api.GetResponse{
		Task: &api.Task{
			Title: "Hi there",
		},
	}), nil
}

func (t taskServer) Create(ctx context.Context, req *connect.Request[api.CreateRequest]) (*connect.Response[api.CreateResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
func (t taskServer) Update(ctx context.Context, req *connect.Request[api.UpdateRequest]) (*connect.Response[api.UpdateResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
func (t taskServer) Do(ctx context.Context, req *connect.Request[api.DoRequest]) (*connect.Response[api.DoResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}
func (t taskServer) Delete(ctx context.Context, req *connect.Request[api.DeleteRequest]) (*connect.Response[api.DeleteResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, nil)
}

func setJson(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
}

func requestContext(r *http.Request) context.Context {
	return r.Context()
}

func idFromParam(p sandwich.Params) (tasks.ID, error) {
	id := p["id"]
	if id == "" {
		return "", fmt.Errorf("missing :id param")
	}
	return tasks.ID(id), nil
}

func parseRequestTask(r *http.Request) (tasks.Task, error) {
	var t tasks.Task
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		return t, err
	}
	if t.Title == "" {
		return t, sandwich.Error{Code: http.StatusBadRequest, ClientMsg: "empty title"}
	}
	if t.Start.IsZero() {
		t.Start = time.Now()
	}
	t.History = nil
	return t, nil
}

func renderTaskList(w http.ResponseWriter, tasks []tasks.TaskWithID) error {
	return json.NewEncoder(w).Encode(tasks)
}

func renderSingleTask(w http.ResponseWriter, task tasks.Task) error {
	return json.NewEncoder(w).Encode(task)
}

func renderID(w http.ResponseWriter, id tasks.ID) error {
	return json.NewEncoder(w).Encode(map[string]tasks.ID{"id": id})
}

type fallthroughFS []fs.FS

func (f fallthroughFS) Open(name string) (fs.File, error) {
	for _, sub := range f {
		file, err := sub.Open(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		return file, err
	}
	return nil, os.ErrNotExist
}

func mustSub(base fs.FS, subdir string) fs.FS {
	sub, err := fs.Sub(base, subdir)
	if err != nil {
		panic(err)
	}
	return sub
}

type PathFromVar string

func (varName PathFromVar) Apply(c chain.Func) chain.Func {
	return c.Then(func(req *http.Request, params sandwich.Params) error {
		path, exists := params[string(varName)]
		if !exists {
			return fmt.Errorf("No such url parameter %q", varName)
		}
		req.URL.Path = "/" + path
		return nil
	})
}

func alwaysValidateMessages() connect.Option {
	return connect.WithInterceptors(connect.UnaryInterceptorFunc(func(uf connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, ar connect.AnyRequest) (connect.AnyResponse, error) {
			type validator interface {
				Validate() error
			}
			if v, ok := ar.Any().(validator); ok {
				if err := v.Validate(); err != nil {
					return nil, connect.NewError(connect.CodeInvalidArgument, err)
				}
			}
			return uf(ctx, ar)
		}
	}))
}
