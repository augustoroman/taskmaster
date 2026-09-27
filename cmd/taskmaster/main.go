// Command taskmaster serves the taskmaster API and web app.
//
// Configuration is from the environment:
//
//	TASKS_JWT_KEY       HMAC key shared with the Caddy auth portal (required unless TASKS_DEV_USER)
//	TASKS_REALM         if set, tokens must carry this realm
//	TASKS_ADMIN_EMAILS  comma-separated emails that may log in without an invitation
//	TASKS_DB            SQLite database path (default ./tasks.db)
//	TASKS_ADDR          listen address (default localhost:8080)
//	TASKS_DEV_USER      log every request in as this email; loopback addresses only
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/augustoroman/taskmaster/internal/app"
	"github.com/augustoroman/taskmaster/internal/auth"
	"github.com/augustoroman/taskmaster/internal/server"
	"github.com/augustoroman/taskmaster/internal/store"
	"github.com/augustoroman/taskmaster/web"
)

const sweepInterval = time.Hour

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

type config struct {
	jwtKey  string
	realm   string
	admins  []string
	dbPath  string
	addr    string
	devUser string
}

func loadConfig() (config, error) {
	c := config{
		jwtKey:  os.Getenv("TASKS_JWT_KEY"),
		realm:   os.Getenv("TASKS_REALM"),
		dbPath:  cmpOr(os.Getenv("TASKS_DB"), "tasks.db"),
		addr:    cmpOr(os.Getenv("TASKS_ADDR"), "localhost:8080"),
		devUser: os.Getenv("TASKS_DEV_USER"),
	}
	for _, e := range strings.Split(os.Getenv("TASKS_ADMIN_EMAILS"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			c.admins = append(c.admins, e)
		}
	}
	if c.devUser != "" {
		host, _, err := net.SplitHostPort(c.addr)
		if err != nil {
			return c, fmt.Errorf("TASKS_ADDR: %w", err)
		}
		if ip := net.ParseIP(host); host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return c, fmt.Errorf("TASKS_DEV_USER is only allowed on a loopback address, not %q", c.addr)
		}
		c.admins = append(c.admins, c.devUser)
	} else if c.jwtKey == "" {
		return c, errors.New("TASKS_JWT_KEY is required")
	}
	return c, nil
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	db, err := store.Open(cfg.dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	svc := app.New(db, cfg.admins, time.Now)

	var authn auth.Authenticator = auth.JWT{Key: []byte(cfg.jwtKey), Realm: cfg.realm}
	if cfg.devUser != "" {
		slog.Warn("development mode: every request is logged in as " + cfg.devUser)
		authn = auth.DevUser{Email: cfg.devUser}
	}

	mux := http.NewServeMux()
	apiPath, api := server.New(svc, authn)
	mux.Handle(apiPath, api)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	mux.Handle("/", server.Authenticate(svc, authn, web.Handler()))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go sweepLoop(ctx, svc)

	srv := &http.Server{Addr: cfg.addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		slog.Info("serving", "addr", "http://"+cfg.addr, "db", cfg.dbPath)
		errc <- srv.ListenAndServe()
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// sweepLoop records misses and resumes paused tasks: at startup, then hourly
// (tasks are in different time zones, so their days end at different times).
func sweepLoop(ctx context.Context, svc *app.Service) {
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()
	for {
		n, err := svc.Sweep(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("sweep", "err", err)
		}
		if n > 0 {
			slog.Info("sweep", "tasks_updated", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
