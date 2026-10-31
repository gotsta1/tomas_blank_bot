# Production deployment

The deployment at `gotsta1.online` lives in `/opt/tomas-club` on the server.
Its `.env` is root-readable only and contains `BASE_URL=https://gotsta1.online`.
The plaintext `ADMIN_PASSWORD` must not be copied to the server; the app uses
`ADMIN_PASSWORD_HASH`.

Run from `/opt/tomas-club` after updating the source:

```sh
docker compose -f compose.yaml -f deploy/compose.production.yaml up -d --build
docker compose -f compose.yaml -f deploy/compose.production.yaml ps
curl -fsS https://gotsta1.online/healthz
```

Caddy serves HTTPS on ports 80 and 443 and renews certificates automatically.
PostgreSQL is not published. The frontend's port 8080 remains bound to server
loopback only. Docker volumes `tomas-club_postgres_data`,
`tomas-club_caddy_data`, and `tomas-club_caddy_config` must survive upgrades.

After changing the bot token, channel, domain, or Telegram commands, run:

```sh
docker compose -f compose.yaml -f deploy/compose.production.yaml exec backend /app/setup
```

Lava.top must send payment and subscription webhooks to
`https://gotsta1.online/webhooks/lava`, using API-key authentication with the
server's `LAVA_WEBHOOK_SECRET`. Check webhook history after a test payment.

The `tomas-club-backup.timer` runs a daily local logical backup and retains
14 days in `/var/backups/tomas-club` (root-only). Keep an additional copy
outside this server; local backups alone do not protect against server loss.
A manual logical backup can be made with:

```sh
umask 077
docker compose -f compose.yaml -f deploy/compose.production.yaml exec -T postgres \
  pg_dump -U club -d club -Fc > club.dump
```
