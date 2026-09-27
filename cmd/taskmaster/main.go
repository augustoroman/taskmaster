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
//	TASKS_LOGIN_URL     where to send people who aren't logged in, e.g. https://auth.example.com/oauth2/tasks
//	TASKS_LOGOUT_URL    offered to people who aren't invited, to switch accounts, e.g. https://auth.example.com/logout
//	TASKS_BACKUP_DIR    if set, write database snapshots here
//	TASKS_BACKUP_KEEP   how many snapshots to keep (default 14)
//	TASKS_BACKUP_EVERY  how often to snapshot, as a Go duration (default 24h)
//	TASKS_PUSH_SUBJECT  contact for push services (default mailto: the first admin email)
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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/augustoroman/taskmaster/internal/app"
	"github.com/augustoroman/taskmaster/internal/auth"
	"github.com/augustoroman/taskmaster/internal/backup"
	"github.com/augustoroman/taskmaster/internal/push"
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
	pages   server.Pages
	backup  backup.Config
	// pushSubject is who push services can contact about our messages.
	pushSubject string
}

func loadConfig() (config, error) {
	c := config{
		jwtKey:  os.Getenv("TASKS_JWT_KEY"),
		realm:   os.Getenv("TASKS_REALM"),
		dbPath:  cmpOr(os.Getenv("TASKS_DB"), "tasks.db"),
		addr:    cmpOr(os.Getenv("TASKS_ADDR"), "localhost:8080"),
		devUser: os.Getenv("TASKS_DEV_USER"),
		pages: server.Pages{
			LoginURL:  os.Getenv("TASKS_LOGIN_URL"),
			LogoutURL: os.Getenv("TASKS_LOGOUT_URL"),
		},
		backup: backup.Config{Dir: os.Getenv("TASKS_BACKUP_DIR"), Keep: 14, Interval: 24 * time.Hour},
	}
	if v := os.Getenv("TASKS_BACKUP_KEEP"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return c, fmt.Errorf("TASKS_BACKUP_KEEP: want a positive number, got %q", v)
		}
		c.backup.Keep = n
	}
	if v := os.Getenv("TASKS_BACKUP_EVERY"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < time.Minute {
			return c, fmt.Errorf("TASKS_BACKUP_EVERY: want a duration like 24h, got %q", v)
		}
		c.backup.Interval = d
	}
	for _, e := range strings.Split(os.Getenv("TASKS_ADMIN_EMAILS"), ",") {
		if e = strings.TrimSpace(e); e != "" {
			c.admins = append(c.admins, e)
		}
	}
	c.pushSubject = os.Getenv("TASKS_PUSH_SUBJECT")
	if c.pushSubject == "" && len(c.admins) > 0 {
		c.pushSubject = "mailto:" + c.admins[0]
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
	if cfg.pushSubject == "" {
		slog.Warn("no TASKS_PUSH_SUBJECT or TASKS_ADMIN_EMAILS; push notifications are off")
	} else {
		sender, err := push.NewSender(context.Background(), db, cfg.pushSubject)
		if err != nil {
			return fmt.Errorf("setting up push: %w", err)
		}
		svc.SetPush(sender)
	}

	var authn auth.Authenticator = auth.JWT{Key: []byte(cfg.jwtKey), Realm: cfg.realm}
	if cfg.devUser != "" {
		slog.Warn("development mode: every request is logged in as " + cfg.devUser)
		authn = auth.DevUser{Email: cfg.devUser}
	}

	mux := http.NewServeMux()
	apiPath, api := server.New(svc, authn, cfg.pages)
	mux.Handle(apiPath, api)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	webApp := web.Handler()
	// Public so browsers can fetch them without credentials when installing.
	mux.Handle("GET /manifest.webmanifest", webApp)
	mux.Handle("GET /icons/", webApp)
	mux.Handle("/", server.Authenticate(svc, authn, cfg.pages, webApp))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go sweepLoop(ctx, svc)
	go notifyLoop(ctx, svc)
	if cfg.backup.Dir != "" {
		go backup.Run(ctx, db, cfg.backup, time.Now)
	} else {
		slog.Warn("TASKS_BACKUP_DIR is not set; no backups will be made")
	}

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

// notifyLoop sends each user's daily notifications once their notification
// time has passed.
func notifyLoop(ctx context.Context, svc *app.Service) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		n, err := svc.SendDailyNotifications(ctx)
		if err != nil && ctx.Err() == nil {
			slog.Error("notifications", "err", err)
		}
		if n > 0 {
			slog.Info("notifications sent", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
