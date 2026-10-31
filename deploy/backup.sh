#!/bin/sh
set -eu
umask 077

cd /opt/tomas-club
backup_dir=/var/backups/tomas-club
mkdir -p "$backup_dir"
chmod 700 "$backup_dir"

stamp=$(date -u +%Y%m%dT%H%M%SZ)
temporary="$backup_dir/.$stamp.dump.tmp"
destination="$backup_dir/$stamp.dump"
trap 'rm -f "$temporary"' EXIT HUP INT TERM

docker compose -f compose.yaml -f deploy/compose.production.yaml exec -T postgres \
  pg_dump -U club -d club -Fc > "$temporary"
test -s "$temporary"
mv "$temporary" "$destination"

find "$backup_dir" -maxdepth 1 -type f -name '*.dump' -mtime +14 -delete
