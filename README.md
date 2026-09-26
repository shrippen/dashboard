# dashboard

Self-hosted, multi-user dashboard for an IT landscape and a freelance business. It replaces Dashy as the start page (links, status checks, search, RSS, clock, weather) and pulls data from **Kimai**, **Invoice Ninja**, **Snipe-IT** and **Dawarich** to turn it into hints and reminders.

- Own login (password, TOTP, API tokens, invitations)
- Users and teams, permissions down to single widgets, personal layouts
- Configuration editor with history
- Theme system on top of the [shrippen Design Default](https://github.com/shrippen/shrippen.github.io); only the shrippen theme ships
- German and English
- Push notifications through an existing Apprise API instance
- One Docker container, SQLite in `/data`, pure Go (no cgo — runs on a Raspberry Pi without a C toolchain)
- Database file, WAL and backups encrypted (Adiantum, key derived from `MASTER_KEY`); an older plaintext database is encrypted at first start

Plan and decisions: [ROADMAP.md](ROADMAP.md) (German). Working rules: [agent.md](agent.md).

## Run

```sh
mkdir -p secrets data && openssl rand -base64 32 > secrets/master_key
sudo chown -R 10001 data secrets && sudo chmod 400 secrets/master_key   # the container runs as uid 10001
cp docker-compose.example.yml docker-compose.yml   # adjust BASE_URL, SMTP, proxy range
docker login git.arianw.de                         # while the package is private
docker compose up -d
docker compose logs dashboard | grep "SETUP CODE"  # open /setup and enter the code
```

Images: `latest` follows `main` (development state). A tag `v1.2.3`
publishes `1.2.3`, `1.2` and `1`; set `DASHBOARD_TAG=1.2` in `.env` to
pin production to a release line. Keep `secrets/master_key` safe and
separate from backups: without it the database can't be opened.

## Develop

```sh
make run      # http://localhost:8080, data in ./data, dev master key
make check    # gofmt, go vet, go test
make build    # static binary in ./bin/dashboard
```

Layers: `web → services → repos | sources | outbound → db | drivers`. See `agent.md`.

## Operator CLI

```sh
docker compose exec dashboard dashboard backup /data/backups
docker compose exec dashboard dashboard rotate-key /run/secrets/new_master_key
```

`rotate-key` also writes `dashboard.db.rekeyed` under the new key; the
next start swaps it in. Replace the secret and restart right away:
writes in between are lost. Older backups keep the old key.
