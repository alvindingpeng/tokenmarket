#!/bin/sh
set -e

PUID="${PUID:-0}"
PGID="${PGID:-0}"

chmod +x /app/example

if [ "$PUID" != "0" ] || [ "$PGID" != "0" ]; then
    chown -R "$PUID:$PGID" /app
fi

cd /app

if command -v su-exec >/dev/null 2>&1; then
    exec su-exec "$PUID:$PGID" ./example -c data/config.json
elif command -v gosu >/dev/null 2>&1; then
    exec gosu "$PUID:$PGID" ./example -c data/config.json
else
    if [ "$PUID" != "0" ] || [ "$PGID" != "0" ]; then
        echo "Warning: neither su-exec nor gosu is available; running as root." >&2
    fi
    exec ./example start --config data/config.json
fi
