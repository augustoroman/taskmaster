package server

import (
	"html/template"
	"net/http"
)

// Pages configures the pages shown to people who can't use the app.
type Pages struct {
	// LoginURL, if set, is where page requests without a login are sent.
	LoginURL string
	// LogoutURL, if set, is offered to people who aren't invited so they can
	// switch Google accounts.
	LogoutURL string
}

var pageTmpl = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>Taskmaster</title>
<style>
  :root { --bg: #fafaf8; --surface: #fff; --text: #1d1d1b; --muted: #6b6b66; --border: #e3e2dd; --accent: #2f6f5e; --accent-text: #fff; }
  @media (prefers-color-scheme: dark) {
    :root { --bg: #161615; --surface: #1f1f1d; --text: #ecebe6; --muted: #a09f98; --border: #34332f; --accent: #6fbfa6; --accent-text: #0f1f1a; }
  }
  body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: var(--bg); color: var(--text);
         font: 16px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; padding: 16px; box-sizing: border-box; }
  main { max-width: 26rem; background: var(--surface); border: 1px solid var(--border); border-radius: 12px; padding: 1.5rem 1.75rem; }
  h1 { font-size: 1.25rem; margin: 0 0 0.75rem; }
  p { margin: 0.5rem 0; }
  .muted { color: var(--muted); font-size: 0.9rem; }
  a.button { display: inline-block; margin-top: 0.75rem; padding: 0.5rem 1rem; border-radius: 8px; background: var(--accent);
             color: var(--accent-text); text-decoration: none; }
</style>
</head>
<body>
<main>
{{if .Email}}
  <h1>You need an invitation</h1>
  <p>You're signed in as <strong>{{.Email}}</strong>, but nothing has been shared with that account yet.</p>
  <p class="muted">Ask someone who uses Taskmaster to share a tag with {{.Email}}, then reload this page.</p>
  {{if .LogoutURL}}<a class="button" href="{{.LogoutURL}}">Use a different account</a>{{end}}
{{else}}
  <h1>Please sign in</h1>
  <p>Your login has expired or is missing.</p>
  {{if .LoginURL}}<a class="button" href="{{.LoginURL}}">Sign in</a>{{else}}<p class="muted">Reload the page to sign in again.</p>{{end}}
{{end}}
</main>
</body>
</html>`))

func (p Pages) notLoggedIn(w http.ResponseWriter, r *http.Request) {
	if p.LoginURL != "" && r.Method == http.MethodGet {
		http.Redirect(w, r, p.LoginURL, http.StatusFound)
		return
	}
	p.render(w, http.StatusUnauthorized, "")
}

func (p Pages) notInvited(w http.ResponseWriter, email string) {
	p.render(w, http.StatusForbidden, email)
}

func (p Pages) render(w http.ResponseWriter, status int, email string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	pageTmpl.Execute(w, struct {
		Pages
		Email string
	}{p, email})
}
