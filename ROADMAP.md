# Roadmap: IT- & Freelance-Dashboard

Ein selbst gehostetes Dashboard, das Daten aus **Kimai**, **Invoice Ninja**, **Snipe-IT** und **Dawarich** zusammenführt, sie im **shrippen Design Default** darstellt und daraus **Hinweise, Erinnerungen und Ratschläge** ableitet. Auslieferung als **ein Docker-Container**.

> Stand: Entwurf v0.1 · Arbeitstitel `dashboard`
>
> **Arbeitsort:** Die gesamte Entwicklung findet in diesem Repository (`shrippen/dashboard`) statt. Das Design-System-Repo `shrippen/shrippen.github.io` ist nur Quelle, es wird von hier aus nicht verändert.

---

## 1. Ziele und Nicht-Ziele

**Ziele**

- Ein Blick am Morgen genügt: Was brennt (überfällige Rechnungen, ablaufende Garantien), was ist offen (nicht abgerechnete Stunden), wie läuft das Jahr (Umsatz, Auslastung, Stundensatz).
- Hinweise sind **regelbasiert, erklärbar und konfigurierbar**: Jede Meldung sagt, *warum* sie erscheint, welche Daten dahinterstehen und wohin man klickt, um sie zu erledigen.
- **Querverbindungen** zwischen den Diensten sind der eigentliche Mehrwert (z. B. „Du warst laut Dawarich 6 h bei Kunde X, in Kimai ist nichts gebucht“).
- Nur **lesender** Zugriff auf die Dienste. Das Dashboard verändert nichts in Kimai, Invoice Ninja usw.
- Ein Container, ein Volume, Konfiguration über YAML und Umgebungsvariablen.

**Nicht-Ziele (vorerst)**

- Kein Ersatz für die Fach-UIs (Rechnungen schreiben bleibt in Invoice Ninja).
- Keine Mehrbenutzer-/Mandantenfähigkeit.
- Keine Steuerberatung: Steuerhinweise sind Erinnerungen mit konfigurierbaren Werten, keine verbindliche Auskunft.

---

## 2. Grundlage: Dashy erweitern oder eigene App?

| Kriterium | Dashy als Basis | Homepage / Glance | **Eigene schlanke App (empfohlen)** |
|---|---|---|---|
| Links/Startseite | sehr gut | sehr gut | muss man bauen (Import aus Dashy möglich) |
| Eigene Widgets | Vue-2-Komponenten, Fork + eigener Build nötig | `customapi`/`custom-api`-Widgets, nur Anzeige | frei |
| API-Aufrufe | größtenteils im Browser (CORS-Proxy, Tokens im Frontend) | serverseitig | serverseitig, Tokens bleiben im Container |
| Verlauf/Trends | nein | nein | ja (SQLite-Snapshots) |
| Regel-Engine, Snooze, Quittieren | nein | nein | ja |
| Push/Digest (ntfy, E-Mail) | nein | nein | ja |
| Styling nach Design System | Custom-CSS-Theme, begrenzt | Custom-CSS, begrenzt | nativ mit `shrippen.css` |

**Empfehlung:** Eigene App mit Backend. Die Kernanforderung „Hinweise auf Basis der Daten“ braucht einen Server mit Zeitplan, Zustand (quittiert, pausiert) und Verlauf. Das kann kein reines Startseiten-Tool leisten, und ein Dashy-Fork bindet an Vue 2 und dessen Build.

**Dashy bleibt nutzbar:** Über ein iframe-Widget (`/embed/hints`, `/embed/kpis`) und einen JSON-Endpunkt (`/api/summary`) lässt sich das Dashboard in Dashy einbetten. In Phase 6 kann das Dashboard die Dashy-`conf.yml` importieren und Dashy ganz ersetzen. Das ist aber optional.

---

## 3. Architektur

```
┌──────────────────────────── Docker-Container ────────────────────────────┐
│                                                                          │
│  Scheduler ──► Connectors ──► Snapshot-Store ──► Kennzahlen ──► Regeln    │
│  (APScheduler)  kimai          (SQLite, /data)    (Metrics)      (Rules)  │
│                 invoiceninja                                     │        │
│                 snipeit                                          ▼        │
│                 dawarich                                    Hinweise      │
│                                                     (Status, Snooze, Ack) │
│                                                          │         │      │
│  Web-UI (Jinja + HTMX, shrippen.css) ◄───────────────────┘         │      │
│  JSON-API /api/*  ·  Embeds /embed/*  ·  /healthz                   │      │
│                                                Benachrichtigungen ◄─┘      │
│                                                (ntfy, Gotify, E-Mail,     │
│                                                 Apprise)                  │
└──────────────────────────────────────────────────────────────────────────┘
```

**Stack-Vorschlag**

| Schicht | Wahl | Begründung |
|---|---|---|
| Sprache | Python 3.12 | gute HTTP-/Datums-Bibliotheken, die vorhandenen Tools (`preview/server.py`, `tools/*.py`) sind schon Python |
| Web | FastAPI + Jinja2 + HTMX | serverseitig gerendert, kaum JavaScript, passt zum CSS-only Design System |
| HTTP | `httpx` (async) | parallele Abrufe, Timeouts, Retries |
| Zeitplan | APScheduler | Intervall je Connector, dazu Cron-Jobs für Digests |
| Speicher | SQLite auf `/data` | Snapshots, Hinweiszustand, Verlauf; ein Volume, einfaches Backup |
| Diagramme | uPlot oder Chart.js, Farben aus den Tokens | leicht, keine Build-Kette |
| Konfiguration | `config.yml` + Env-Variablen bzw. Docker Secrets, validiert mit Pydantic | Fehler beim Start statt zur Laufzeit |

**Datenfluss**

1. **Connector** holt Rohdaten und normalisiert sie in Pydantic-Modelle (`TimeEntry`, `Invoice`, `Asset`, `License`, `Visit` …).
2. **Snapshot-Store** legt pro Lauf einen Zeitstempel-Snapshot ab (für Trends und „seit gestern neu“).
3. **Kennzahlen** werden daraus berechnet (Umsatz YTD, Auslastung, offene Posten …).
4. **Regeln** laufen über Kennzahlen und Rohdaten und erzeugen **Hinweise** mit stabilem Fingerprint (`regel-id + objekt-id`). Ein Hinweis bleibt so über Läufe hinweg derselbe, kann quittiert oder bis zu einem Datum pausiert werden und verschwindet von selbst, wenn die Bedingung wegfällt.
5. **UI** und **Benachrichtigungen** lesen nur die Hinweise und Kennzahlen.

**Hinweis-Modell**

```yaml
id: kimai.unbilled_hours:customer-12
severity: warn          # info | warn | critical
title: "38 h bei Kunde Muster GmbH noch nicht abgerechnet"
why: "Nicht exportierte Zeiteinträge älter als 30 Tage (ältester: 12.08.)"
action: { label: "In Kimai öffnen", url: "https://kimai.example/…" }
due: 2026-10-10         # optional, für Fristen
source: [kimai, invoiceninja]
state: open             # open | snoozed | acknowledged | resolved
```

**Regeln** gibt es in zwei Formen:

- **Deklarativ** in `config.yml` für einfache Schwellwerte (Tage, Beträge, Prozent), die man ohne Code anpasst.
- **Als Python-Klasse** für Regeln über mehrere Dienste hinweg (Abgleich Dawarich ↔ Kimai). Jede Regel hat ID, Standard-Schwellwerte, Beschreibung und einen Unit-Test mit Fixture-Daten.

---

## 4. Die Dienste: Daten und Hinweise

Alle Abrufe laufen read-only mit eigenen API-Tokens. Endpunkte beim Bau gegen die jeweilige Version prüfen (Kimai: `/api/doc`, Dawarich: `/api-docs`).

### 4.1 Kimai (Zeiterfassung)

**Daten:** `GET /api/timesheets` (Filter `begin`, `end`, `exported`), `/api/timesheets/active`, `/api/projects`, `/api/customers`, `/api/activities`. Authentifizierung per Bearer-API-Token (ältere Versionen: `X-AUTH-USER`/`X-AUTH-TOKEN`). Falls die eigenen Bundles Endpunkte anbieten, kommen Abwesenheiten und Soll-Arbeitszeit aus dem **kimai-holiday-bundle** und der Abrechnungsstatus aus dem **kimai-abrechnung-bundle**.

**Kennzahlen:** Stunden (Woche/Monat/Jahr), davon abrechenbar, Auslastung gegen Soll, Stunden je Kunde/Projekt, Budgetverbrauch je Projekt, nicht exportierte Stunden und deren Alter.

| Regel | Bedingung (Standard) | Stufe |
|---|---|---|
| `kimai.timer_running_long` | Laufender Timer > 10 h | warn |
| `kimai.missing_day` | Werktag ohne Buchung, kein Urlaub/Feiertag (holiday-bundle) | info |
| `kimai.unbilled_hours` | Nicht exportierte, abrechenbare Einträge älter als 30 Tage | warn, ab 60 Tagen critical |
| `kimai.budget_burn` | Projektbudget (Zeit oder Geld) ≥ 80 % / ≥ 100 % verbraucht | warn / critical |
| `kimai.budget_pace` | Budgetverbrauch schneller als Projektlaufzeit (Hochrechnung überschreitet Budget vor Enddatum) | warn |
| `kimai.utilization_low` | Abrechenbare Auslastung im Monat < Ziel (z. B. 70 %) | info |
| `kimai.overtime` | Wochenstunden > Grenze (z. B. 45 h) zwei Wochen in Folge | info |
| `kimai.monthly_close` | Monatsende: Einträge des Vormonats noch nicht exportiert | warn |

### 4.2 Invoice Ninja v5 (Rechnungen, Zahlungen, Ausgaben)

**Daten:** `GET /api/v1/invoices` (u. a. `client_status=unpaid|overdue`), `/payments`, `/clients`, `/quotes`, `/recurring_invoices`, `/expenses`. Header `X-API-TOKEN` und `X-Requested-With: XMLHttpRequest`.

**Kennzahlen:** Umsatz netto/brutto (Monat, YTD, Vorjahr), offene Posten und deren Alter, Zahlungsdauer je Kunde (Days Sales Outstanding), Umsatzanteil je Kunde, Ausgaben, grober Überschuss, effektiver Stundensatz (Umsatz / Kimai-Stunden).

| Regel | Bedingung (Standard) | Stufe |
|---|---|---|
| `in.invoice_overdue` | Rechnung überfällig; ab 14 Tagen Mahnvorschlag | warn → critical |
| `in.slow_payer` | Kunde zahlt im Mittel > 30 Tage | info |
| `in.draft_stale` | Rechnungsentwurf älter als 7 Tage | info |
| `in.quote_open` | Angebot versendet, nach 14 Tagen ohne Reaktion | info (Nachfassen) |
| `in.recurring_ending` | Wiederkehrende Rechnung endet in < 30 Tagen | info |
| `in.revenue_vs_goal` | Umsatz YTD unter anteiligem Jahresziel | info |
| `in.tax_reserve` | Empfohlene Steuerrücklage (x % der Zahlungseingänge) gegen erfasste Rücklage | info |
| `in.small_business_limit` | Kleinunternehmergrenze § 19 UStG: Vorjahr > 25.000 € oder laufendes Jahr nähert sich 100.000 € | warn / critical |
| `in.client_concentration` | Ein Kunde > 50 % des Umsatzes (Klumpenrisiko), > 5/6 über 12 Monate (Hinweis auf Prüfung Rentenversicherungspflicht als arbeitnehmerähnlicher Selbständiger) | info / warn |

### 4.3 Snipe-IT (Assets, Lizenzen)

**Daten:** `GET /api/v1/hardware` (inkl. `warranty_expires`, `asset_eol_date`, Status, Zuweisung), `/hardware/audit/due`, `/hardware/audit/overdue`, `/licenses`, `/consumables`, `/maintenances`. Bearer-Token.

**Kennzahlen:** Anzahl Assets nach Status/Kategorie, Gerätealter, Summe Anschaffungswerte, auslaufende Garantien/Lizenzen der nächsten 90 Tage.

| Regel | Bedingung (Standard) | Stufe |
|---|---|---|
| `snipe.warranty_expiring` | Garantie endet in ≤ 60 / ≤ 14 Tagen | info / warn |
| `snipe.eol_reached` | EOL-Datum erreicht oder Gerät älter als x Jahre | info (Ersatz einplanen) |
| `snipe.license_expiring` | Lizenz/Abo läuft in ≤ 30 Tagen ab | warn |
| `snipe.license_seats` | Keine freien Lizenzplätze mehr | info |
| `snipe.audit_overdue` | Audit überfällig | warn |
| `snipe.consumable_low` | Verbrauchsmaterial unter Mindestbestand | info |
| `snipe.unassigned_deployable` | Einsatzbereites Gerät seit > 90 Tagen ungenutzt | info (verkaufen?) |
| `snipe.expense_missing` | Asset mit Kaufdatum im laufenden Jahr ohne passende Ausgabe in Invoice Ninja (Betrag ± Toleranz, Datum ± 14 Tage) | info |
| `snipe.gwg_hint` | Anschaffung > 800 € netto → Abschreibung statt GWG-Sofortabzug prüfen | info |

### 4.4 Dawarich (Standortverlauf)

**Daten:** `GET /api/v1/points` (`start_at`, `end_at`), `/api/v1/visits`, `/api/v1/areas`, `/api/v1/stats`. Authentifizierung per API-Key. **Sensible Daten:** Es werden nur Aggregate gespeichert (Aufenthalte in definierten Bereichen, Tages-km), keine Rohpunkte.

**Idee:** In Dawarich werden **Areas** für Kundenstandorte, Büro und Zuhause angelegt. In `config.yml` wird jede Area einem Kimai-Kunden zugeordnet.

**Kennzahlen:** Tage beim Kunden je Monat, Fahrtstrecke je Tag, Abwesenheitsdauer von zu Hause, besuchte Länder/Orte (Reisen).

| Regel | Bedingung (Standard) | Stufe |
|---|---|---|
| `geo.visit_without_time` | Aufenthalt in Kunden-Area ≥ 2 h, aber keine Kimai-Buchung für diesen Kunden an dem Tag | warn |
| `geo.time_without_visit` | Kimai-Buchung mit Aktivität „vor Ort“, aber kein Aufenthalt in der Kunden-Area | info (Plausibilität) |
| `geo.travel_costs` | Fahrten zu Kunden im Monat: km × Kilometersatz (Standard 0,30 €/km), nicht als Ausgabe/Rechnungsposten erfasst | info |
| `geo.per_diem` | Abwesenheit > 8 h / 24 h an Kundentagen → Verpflegungsmehraufwand (Sätze konfigurierbar) | info |
| `geo.no_data` | Seit > 24 h keine neuen Punkte (Tracking-App aus?) | warn |

### 4.5 Übergreifend: Fristen und Wochenrückblick

| Regel | Inhalt |
|---|---|
| `tax.vat_return` | Umsatzsteuer-Voranmeldung zum 10. (Monat/Quartal, Dauerfristverlängerung konfigurierbar) |
| `tax.prepayment` | ESt-Vorauszahlungen 10.03., 10.06., 10.09., 10.12. mit Betrag aus Konfiguration |
| `tax.annual` | Jahresabschluss/EÜR-Erinnerung mit eigener Frist |
| `digest.weekly` | Montag: Stunden, Umsatz, offene Posten, neue Hinweise der Woche |
| `digest.monthly` | Monatsanfang: Vormonat abschließen (Export in Kimai → Rechnung in Invoice Ninja → Fahrtkosten aus Dawarich) |

Alle Beträge, Sätze und Fristen stehen in `config.yml` und werden pro Jahr gepflegt.

---

## 5. Design: shrippen Design Default

Das Dashboard ist eine **App** im Sinne des Design Systems und nutzt daher die App-Komponenten und optional das Light-Theme „Leinen“.

**Einbindung**

- `shrippen.css`, `shrippen.js` und die Schriften werden als **Kopie in dieses Repo** gelegt (`app/static/vendor/shrippen/`, Quelle und Stand in einer `VERSION`-Datei vermerkt). Das Dashboard funktioniert so auch ohne Internet und wandert nicht ungeprüft mit dem CDN mit. Aktualisiert wird bewusst per Skript (`tools/sync-design.sh`).
- Schriften (Rajdhani 500/600/700, JetBrains Mono 400/500) werden **lokal** aus `fonts/` ausgeliefert, nicht von Google Fonts (Datenschutz, offline).
- Theme-Umschalter dunkel/hell über `<html data-theme="light">`, Wahl im `localStorage`.
- Sprachumschaltung DE/EN kommt über den vorhandenen `.lang`-Mechanismus mit.

**Vorhandene Komponenten wiederverwenden**

| Dashboard-Element | Design-System-Komponente |
|---|---|
| Hinweis-Karte | `.callout`, `.callout-warn`, `.callout-danger`, `.callout-ok` |
| Budget, Auslastung, Umsatzziel | `.progress` mit `data-tier="green|yellow|red"` |
| Status eines Connectors (ok, lädt, Fehler) | `.pill` mit `data-state` |
| Tabellen (offene Rechnungen, Assets) | `.table-wrap` + `.table` |
| Navigation, Fuß | `.nav`, `.foot` |
| Filter, Umschalter | `.seg`, `.switch`, `.select` |
| Snooze-Dialog | `.dialog`, `.scrim` |
| Rückmeldung nach Aktion | `.toast` |

**Neue Komponenten (leben in diesem Repo, `app/static/dashboard.css`)**

| Komponente | Zweck |
|---|---|
| `.kpi` | Kennzahl-Kachel: Label (mono, versal), Wert (Rajdhani 700, tabellarische Ziffern), Delta zum Vorzeitraum, optionale Sparkline |
| `.kpi-row` | Raster `auto-fit minmax(200px, 1fr)` |
| `.hint` | Hinweis mit Stufe, Quelle, „Warum?“-Aufklappbereich, Aktionen (öffnen, pausieren, quittieren). Stufe nie nur über Farbe, sondern auch über Icon und Text |
| `.timeline` | Fristen der nächsten 30 Tage |
| Diagramm-Palette | Reihenfolge `--blue`, `--aqua`, `--yellow`, `--orange`, `--purple`, `--green`; Achsen `--fg3`, Gitter `--bg2` |

Die Komponenten nutzen ausschließlich die Tokens des Design Systems (`var(--…)`), keine eigenen Hex-Werte. Ob sie später ins Design System wandern, ist eine eigene Entscheidung außerhalb dieses Projekts.

**Layout-Skizze**

```
┌─────────────────────────────────────────────────────────────────────┐
│ ◆ dashboard      Übersicht  Freelance  IT  Reisen  Fristen   DE|EN ☼ │
├─────────────────────────────────────────────────────────────────────┤
│ [Umsatz YTD]  [Offene Posten]  [Stunden Monat]  [Auslastung]  [Ø €/h]│
├───────────────────────────────────────┬─────────────────────────────┤
│ Hinweise (3 kritisch · 5 Warnung)     │ Nächste Fristen             │
│ ▌ Rechnung R-2026-041 21 Tage überf.  │ 10.10. USt-VA September      │
│ ▌ 38 h Muster GmbH nicht abgerechnet  │ 14.10. Garantie ThinkPad     │
│ ▌ Laufender Timer seit 11 h           │ 31.10. Lizenz JetBrains      │
├───────────────────────────────────────┼─────────────────────────────┤
│ Umsatz je Monat (Balken, Vorjahr)     │ Projektbudgets (.progress)   │
├───────────────────────────────────────┴─────────────────────────────┤
│ Connector-Status: kimai ● ok · invoiceninja ● ok · snipeit ● ok …   │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 6. Phasen

Jede Phase endet mit einem lauffähigen, getaggten Image.

### Phase 0: Fundament (v0.1)

- [ ] Repo-Struktur, `pyproject.toml`, Ruff, Pytest, pre-commit
- [ ] FastAPI-Grundgerüst, Jinja-Layout mit `shrippen.css`, lokale Schriften, Theme-Umschalter
- [ ] Konfiguration: `config.yml` + Env/Secrets, Pydantic-Validierung, `config.example.yml`
- [ ] Connector-Schnittstelle (`fetch()`, `healthcheck()`), Scheduler, SQLite-Schema (Snapshots, Hinweise)
- [ ] Hinweis-Engine: Fingerprint, Zustände, Snooze/Ack
- [ ] Dockerfile (multi-stage, non-root, `HEALTHCHECK`), `docker-compose.example.yml`
- [ ] GitHub Actions: Tests, Image-Build `linux/amd64` + `linux/arm64`, Push nach GHCR
- [ ] Demo-Modus mit Fixture-Daten (für Entwicklung und Screenshots ohne echte Daten)

### Phase 1: Freelance-Kern: Kimai + Invoice Ninja (v0.2, MVP)

- [ ] Kimai-Connector und Kennzahlen, Regeln aus 4.1
- [ ] Invoice-Ninja-Connector und Kennzahlen, Regeln aus 4.2
- [ ] Abgleich Kimai ↔ Invoice Ninja: nicht abgerechnete Stunden je Kunde, effektiver Stundensatz
- [ ] Seiten „Übersicht“ und „Freelance“ mit `.kpi`, `.hint`, `.progress`, Tabelle offener Posten
- [ ] Deep-Links von jedem Hinweis in die Fach-UI

**Ergebnis:** Das Dashboard wird täglich genutzt und ersetzt den manuellen Blick in beide Tools.

### Phase 2: IT-Landschaft: Snipe-IT (v0.3)

- [ ] Snipe-IT-Connector, Regeln aus 4.3
- [ ] Seite „IT“: Assets nach Status, Garantie-/Lizenz-Zeitleiste, Audits
- [ ] Abgleich Snipe-IT ↔ Invoice-Ninja-Ausgaben (`snipe.expense_missing`)

### Phase 3: Standort: Dawarich (v0.4)

- [ ] Dawarich-Connector, nur Aggregate speichern
- [ ] Zuordnung Area → Kimai-Kunde in `config.yml`
- [ ] Regeln aus 4.4: Besuch ohne Buchung, Fahrtkosten, Verpflegungspauschalen
- [ ] Seite „Reisen“: Kundentage, km je Monat, Vorschlag für Fahrtkosten-Position

### Phase 4: Erinnerungen und Benachrichtigungen (v0.5)

- [ ] Fristen-Kalender (Abschnitt 4.5), Seite „Fristen“, iCal-Feed `/calendar.ics`
- [ ] Benachrichtigungen über Apprise (ntfy, Gotify, E-Mail, Matrix …), je Stufe konfigurierbar
- [ ] Morgen-Digest und Wochenrückblick; Ruhezeiten; keine Doppelmeldungen (Fingerprint)
- [ ] Monatsabschluss-Checkliste (Kimai-Export → Rechnung → Fahrtkosten)

### Phase 5: Trends und Prognosen (v0.6)

- [ ] Verlaufsdiagramme aus Snapshots (Umsatz, Stunden, offene Posten)
- [ ] Hochrechnung Jahresumsatz, Kleinunternehmergrenze, Steuerrücklage
- [ ] Vergleich Vorjahr, saisonale Muster, Liquiditätsvorschau (offene Posten + wiederkehrende Rechnungen − feste Ausgaben)

### Phase 6: Ausbau (v1.0)

- [ ] Dashy-Ablösung (optional): Import `conf.yml` als Link-Kacheln, oder Einbettung über `/embed/*`
- [ ] Weitere Connectors über dieselbe Schnittstelle: z. B. Uptime Kuma (Dienste down), Proxmox/Docker (Updates, Speicher), Backup-Status, Paperless-ngx (unbearbeitete Belege), Zertifikatsablauf
- [ ] Optionale Wochenzusammenfassung in Fließtext per LLM (abschaltbar, nur Aggregate, keine Standortdaten)
- [ ] Komponenten-Übersicht (`.kpi`, `.hint`, `.timeline`) als Seite `/styleguide` im Dashboard, damit sie bei Bedarf ins Design System übernommen werden können

---

## 7. Betrieb und Sicherheit

- **Tokens:** Pro Dienst ein eigener, möglichst eingeschränkter API-Benutzer (nur lesen). Übergabe per Docker Secrets oder Env, nie in `config.yml` im Repo.
- **Zugriff:** Kein eigenes Login in v0.x. Das Dashboard läuft hinter dem Reverse Proxy mit Authentifizierung (Authelia, Authentik oder Basic Auth). Optional eingebaute Basic Auth als Rückfallebene.
- **Netz:** Ausgehende Verbindungen nur zu den konfigurierten Diensten. Keine externen CDNs zur Laufzeit.
- **Daten:** SQLite unter `/data`, Aufbewahrung konfigurierbar (z. B. Snapshots 24 Monate, Dawarich-Aggregate 12 Monate).
- **Robustheit:** Ein ausgefallener Dienst lässt das Dashboard nicht ausfallen. Die Kachel zeigt den letzten Stand mit Alter und `.pill[data-state="failed"]`, dazu ein Hinweis `system.connector_down`.
- **Beobachtbarkeit:** `/healthz`, strukturierte Logs, optional `/metrics` (Prometheus).

**Beispiel `docker-compose.yml`**

```yaml
services:
  dashboard:
    image: ghcr.io/shrippen/dashboard:latest
    restart: unless-stopped
    volumes:
      - ./data:/data
      - ./config.yml:/app/config.yml:ro
    environment:
      TZ: Europe/Berlin
      KIMAI_URL: https://kimai.example.lan
      INVOICENINJA_URL: https://invoice.example.lan
      SNIPEIT_URL: https://assets.example.lan
      DAWARICH_URL: https://dawarich.example.lan
    secrets: [kimai_token, invoiceninja_token, snipeit_token, dawarich_api_key]
    ports: ["8080:8080"]

secrets:
  kimai_token:        { file: ./secrets/kimai_token }
  invoiceninja_token: { file: ./secrets/invoiceninja_token }
  snipeit_token:      { file: ./secrets/snipeit_token }
  dawarich_api_key:   { file: ./secrets/dawarich_api_key }
```

**Beispiel `config.yml` (Ausschnitt)**

```yaml
locale: de
goals:
  revenue_year: 90000
  billable_ratio: 0.7
  hours_week_max: 45

rules:
  kimai.unbilled_hours: { warn_days: 30, critical_days: 60 }
  in.invoice_overdue:   { dunning_after_days: 14 }
  snipe.warranty_expiring: { info_days: 60, warn_days: 14 }

dawarich:
  areas:
    "Muster GmbH Büro": { kimai_customer: 12 }
    "Home": { home: true }
  km_rate: 0.30
  per_diem: { over_8h: 14, full_day: 28 }

tax:
  vat_return: { interval: monthly, extension: true }
  prepayments: { amount: 1200 }

notify:
  apprise: ["ntfy://ntfy.example.lan/dashboard"]
  digest: { daily: "07:30", weekly: "mon 07:30" }
  min_severity: warn
```

---

## 8. Vorgeschlagene Repo-Struktur

```
dashboard/
├── ROADMAP.md
├── README.md
├── Dockerfile
├── docker-compose.example.yml
├── config.example.yml
├── pyproject.toml
├── app/
│   ├── main.py              ← FastAPI, Routen, Scheduler-Start
│   ├── config.py            ← Pydantic-Settings
│   ├── store.py             ← SQLite: Snapshots, Hinweise
│   ├── connectors/          ← kimai.py, invoiceninja.py, snipeit.py, dawarich.py, base.py
│   ├── metrics/             ← Kennzahlen je Bereich
│   ├── rules/               ← Regeln je Dienst + cross.py (dienstübergreifend) + deadlines.py
│   ├── notify/              ← Apprise, Digest-Vorlagen
│   ├── templates/           ← Jinja-Seiten und Partials (HTMX)
│   └── static/
│       ├── vendor/shrippen/ ← Kopie von shrippen.css / shrippen.js / Schriften + VERSION
│       └── dashboard.css    ← nur neue Komponenten (.kpi, .hint, .timeline)
├── tools/
│   └── sync-design.sh       ← holt eine bestimmte Version des Design Systems nach vendor/
└── tests/
    ├── fixtures/            ← anonymisierte API-Antworten je Dienst
    └── rules/               ← ein Test pro Regel
```

---

## 9. Offene Fragen

1. **Versionen:** Welche Kimai- und Invoice-Ninja-Versionen laufen (v5 self-hosted?), und bieten holiday-/abrechnung-bundle eigene API-Endpunkte?
2. **Dashy:** Soll das Dashboard Dashy langfristig ersetzen oder nur eingebettet werden?
3. **Benachrichtigungen:** Welcher Kanal ist vorhanden (ntfy, Gotify, E-Mail, Matrix)?
4. **Zugriffsschutz:** Welcher Reverse Proxy / welche Authentifizierung ist im Einsatz?
5. **Steuerstatus:** Kleinunternehmer oder regelbesteuert, USt-VA monatlich oder quartalsweise? Davon hängen die Standardregeln in 4.2/4.5 ab.
6. **Dawarich:** Sind Kundenstandorte schon als Areas angelegt, und ist der Abgleich mit Kimai gewünscht?
7. **Sprache:** UI zweisprachig (DE/EN wie die Landing Pages) oder nur Deutsch?
