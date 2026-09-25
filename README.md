# dashboard

Self-hosted, multi-user dashboard for an IT landscape and a freelance business. It replaces Dashy as the start page (links, status checks, search, RSS, clock, weather) and pulls data from **Kimai**, **Invoice Ninja**, **Snipe-IT** and **Dawarich** to turn it into hints and reminders.

- Own login (password, TOTP, API tokens, invitations), optional single sign-on with authentik (OIDC)
- Users and teams, permissions down to single widgets, personal layouts
- Configuration editor with history, YAML import/export, Dashy `conf.yml` import
- Theme system on top of the [shrippen Design Default](https://github.com/shrippen/shrippen.github.io); only the shrippen theme ships
- German and English
- One Docker container, SQLite in `/data`

Plan and decisions: [ROADMAP.md](ROADMAP.md) (German). Working rules: [agent.md](agent.md).

## Run

```sh
mkdir -p secrets data && openssl rand -base64 32 > secrets/master_key
cp docker-compose.example.yml docker-compose.yml   # adjust BASE_URL, SMTP, proxy range
docker compose up -d
docker compose logs dashboard | grep "SETUP CODE"  # open /setup and enter the code
```

Demo with made-up data: `DASHBOARD_DEMO=1` (users `admin@demo.local` / `alex@demo.local`, password `demo-password-1`).

## Develop

```sh
python3 -m venv .venv && .venv/bin/pip install -e ".[dev]"
make run      # http://localhost:8080, data in ./data, dev master key
make check    # ruff, token-only CSS check, pytest
```

Layers: `web → services → repos | sources | outbound → db | drivers`. See `agent.md`.
