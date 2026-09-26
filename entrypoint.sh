#!/bin/sh
# Starts as root only to repair what Docker hands over, then runs Andon as
# PUID:PGID (default 10001:10001, the image's user andon):
#
#   /data               bind mount created by Docker → owned by root → chown
#   /run/secrets/<name> file secret, e.g. mode 400 root → read here, passed
#                       on as environment variable (settings fall back to it)
set -e

if [ "$(id -u)" != "0" ]; then
	exec andon "$@"
fi

PUID=${PUID:-10001}
PGID=${PGID:-10001}

# Only touch what is not ours yet, so large icon folders stay fast.
find "$DATA_DIR" \( ! -user "$PUID" -o ! -group "$PGID" \) -exec chown "$PUID:$PGID" {} +

for name in master_key smtp_password oidc_client_secret anthropic_api_key; do
	file="/run/secrets/$name"
	if [ -r "$file" ]; then
		export "$(echo "$name" | tr a-z A-Z)=$(cat "$file")"
	fi
done

exec su-exec "$PUID:$PGID" andon "$@"
