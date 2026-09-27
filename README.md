# Taskmaster

A self-hosted task tracker for long-running household chores. See
[docs/design.md](docs/design.md) for how scheduling, sharing and ranking work.

## Running locally

```sh
TASKS_DEV_USER=you@example.com go run ./cmd/taskmaster
```

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

## Development

```sh
go test ./...
./scripts/gen-proto.sh   # after editing proto/; needs protoc
```
