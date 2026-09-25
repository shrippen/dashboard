# dashboard

Self-hosted, multi-user dashboard for an IT landscape and a freelance business. It replaces Dashy as the start page (links, status checks, search, RSS, clock, weather) and pulls data from **Kimai**, **Invoice Ninja**, **Snipe-IT** and **Dawarich** to turn it into hints and reminders.

- Own login (password, TOTP, API tokens, invitations)
- Users and teams, permissions down to single widgets, personal layouts
- Configuration editor with history
- Theme system on top of the [shrippen Design Default](https://github.com/shrippen/shrippen.github.io); only the shrippen theme ships
- German and English
- Push notifications through an existing Apprise API instance
- One Docker container, SQLite in `/data`, pure Go (no cgo — runs on a Raspberry Pi without a C toolchain)

Plan and decisions: [ROADMAP.md](ROADMAP.md) (German). Working rules: [agent.md](agent.md).

## Run

```sh
mkdir -p secrets data && openssl rand -base64 32 > secrets/master_key
cp docker-compose.example.yml docker-compose.yml   # adjust BASE_URL, SMTP, proxy range
docker compose up -d
docker compose logs dashboard | grep "SETUP CODE"  # open /setup and enter the code
```

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
