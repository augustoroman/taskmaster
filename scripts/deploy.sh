#!/bin/sh
# Pulls the latest main, rebuilds, and restarts the taskmaster user service.
# The service is ~/.config/systemd/user/taskmaster.service; its settings are in
# ~/.config/taskmaster/env.
set -eu
cd "$(dirname "$0")/.."
git pull --ff-only
./scripts/build.sh
systemctl --user restart taskmaster
sleep 1
addr=$(sed -n 's/^TASKS_ADDR=//p' "$HOME/.config/taskmaster/env")
if curl -fsS "http://${addr:-localhost:8080}/healthz" >/dev/null; then
	echo "taskmaster is running $(git log -1 --format='%h %s')"
else
	systemctl --user status taskmaster --no-pager | tail -20
	exit 1
fi
