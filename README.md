# Taskmaster

A self-hosted task tracker for long-running household chores. See
[docs/design.md](docs/design.md) for how scheduling, sharing and ranking work.

## Building

```sh
./scripts/build.sh        # builds web/ then ./taskmaster with the web app embedded
```

The web app uses [mde](https://github.com/augustoroman/mde) for editing descriptions, pinned to a
tag in `web/package.json`; npm builds it on install. To upgrade, change the tag and run
`npm install` and `npm approve-scripts mde` in `web/` (npm asks you to approve its build step for
each new version).

To update a server running as the `taskmaster` systemd user service, run `./scripts/deploy.sh`.
It pulls `main`, rebuilds, restarts the service and checks `/healthz`.

## Running locally

```sh
TASKS_DEV_USER=you@example.com ./taskmaster
```

Then open http://localhost:8080/.

`TASKS_DEV_USER` logs every request in as that email (loopback addresses only). The API is
Connect-RPC at `http://localhost:8080/taskmaster.v1.TaskmasterService/<Method>` and accepts plain
JSON:

```sh
curl -H 'Content-Type: application/json' -d '{}' \
  http://localhost:8080/taskmaster.v1.TaskmasterService/Upcoming
```

## Configuration

| Variable | Meaning |
| --- | --- |
| `TASKS_JWT_KEY` | HMAC key shared with the Caddy auth portal. Required unless `TASKS_DEV_USER` is set. |
| `TASKS_REALM` | If set, tokens must carry this realm. |
| `TASKS_ADMIN_EMAILS` | Comma-separated emails that can log in without an invitation. |
| `TASKS_DB` | SQLite database path (default `tasks.db`). |
| `TASKS_ADDR` | Listen address (default `localhost:8080`). |
| `TASKS_DEV_USER` | Development login; see above. |
| `TASKS_LOGIN_URL` | Where to send page requests with no login, e.g. `https://auth.example.com/oauth2/tasks`. |
| `TASKS_LOGOUT_URL` | Offered on the "you need an invitation" page to switch accounts, e.g. `https://auth.example.com/logout`. |
| `TASKS_BACKUP_DIR` | If set, consistent database snapshots are written here (`tasks-<time>.db`). |
| `TASKS_BACKUP_KEEP` | How many snapshots to keep (default 14). |
| `TASKS_BACKUP_EVERY` | How often to take one, as a Go duration (default `24h`). |

To restore, stop the server and copy a snapshot over the database file (removing any `-wal` and
`-shm` files next to it).

## Development

```sh
go test ./...
./scripts/gen-proto.sh   # after editing proto/; needs protoc and web/node_modules
```

For frontend work, run the Go server as above and `npm run dev` in `web/`; Vite serves the app
with hot reload on http://localhost:5173 and proxies API calls to the Go server on port 8080.
