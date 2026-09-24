# Roadmap: IT- & Freelance-Dashboard

Ein selbst gehostetes Dashboard, das als **Startseite mit Links, Statusanzeigen und Feeds** Dashy ablöst und zugleich Daten aus **Kimai**, **Invoice Ninja**, **Snipe-IT** und **Dawarich** zusammenführt, sie im **shrippen Design Default** darstellt und daraus **Hinweise, Erinnerungen und Ratschläge** ableitet. Auslieferung als **ein Docker-Container**.

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
- **Startseite wie bisher:** Die täglich genutzten Dashy-Funktionen (Links, Icons, Status, Suche, RSS, Uhr, Wetter) sind übernommen, sodass Dashy abgeschaltet werden kann.
- Ein Container, ein Volume, Konfiguration über YAML und Umgebungsvariablen.

**Nicht-Ziele (vorerst)**

- Kein Ersatz für die Fach-UIs (Rechnungen schreiben bleibt in Invoice Ninja).
- Keine Mehrbenutzer-/Mandantenfähigkeit.
- Keine Steuerberatung: Steuerhinweise sind Erinnerungen mit konfigurierbaren Werten, keine verbindliche Auskunft.

---

## 2. Grundlage: Dashy erweitern oder eigene App?

| Kriterium | Dashy als Basis | Homepage / Glance | **Eigene schlanke App (empfohlen)** |
|---|---|---|---|
| Links/Startseite | sehr gut | sehr gut | wird nachgebaut (nur genutzte Funktionen, Import aus Dashy) |
| Eigene Widgets | Vue-2-Komponenten, Fork + eigener Build nötig | `customapi`/`custom-api`-Widgets, nur Anzeige | frei |
| API-Aufrufe | größtenteils im Browser (CORS-Proxy, Tokens im Frontend) | serverseitig | serverseitig, Tokens bleiben im Container |
| Verlauf/Trends | nein | nein | ja (SQLite-Snapshots) |
| Regel-Engine, Snooze, Quittieren | nein | nein | ja |
| Push/Digest (ntfy, E-Mail) | nein | nein | ja |
| Styling nach Design System | Custom-CSS-Theme, begrenzt | Custom-CSS, begrenzt | nativ mit `shrippen.css` |

**Empfehlung:** Eigene App mit Backend. Die Kernanforderung „Hinweise auf Basis der Daten“ braucht einen Server mit Zeitplan, Zustand (quittiert, pausiert) und Verlauf. Das kann kein reines Startseiten-Tool leisten, und ein Dashy-Fork bindet an Vue 2 und dessen Build.

**Dashy wird abgelöst:** Die wahrscheinlich genutzten Dashy-Funktionen werden migriert (Abschnitt 4), die `conf.yml` wird per Importer übernommen. Das geschieht direkt nach dem Fundament in Phase 1, weil die Startseite sofort täglichen Nutzen bringt. Bis zum Umstieg laufen beide parallel; für diese Zeit lassen sich Hinweise und Kennzahlen über `/embed/*` und `/api/summary` auch in Dashy einbetten.

---

## 3. Architektur

```
┌──────────────────────────── Docker-Container ────────────────────────────┐
│                                                                          │
│  Scheduler ──► Quellen ─────► Snapshot-Store ──► Kennzahlen ──► Regeln    │
│  (APScheduler)  kimai          (SQLite, /data)    (Metrics)      (Rules)  │
│                 invoiceninja          │                          │        │
│                 snipeit, dawarich     │                          ▼        │
│                 rss, http_status,     ▼                     Hinweise      │
│                 open_meteo, glances  Widgets (link, rss, kpi, hints …)    │
│                                                     (Status, Snooze, Ack) │
│                                                          │         │      │
│  Web-UI (Jinja + HTMX, shrippen.css) ◄── Widgets + Hinweise        │      │
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
| Zeitplan | APScheduler | Intervall je Quelle, dazu Cron-Jobs für Digests |
| Fremdinhalte | `feedparser`, `nh3` | RSS lesen und HTML bereinigen |
| Speicher | SQLite auf `/data` | Snapshots, Hinweiszustand, Verlauf; ein Volume, einfaches Backup |
| Diagramme | uPlot oder Chart.js, Farben aus den Tokens | leicht, keine Build-Kette |
| Konfiguration | `config.yml` + Env-Variablen bzw. Docker Secrets, validiert mit Pydantic | Fehler beim Start statt zur Laufzeit |

**Grundprinzip:** Drei getrennte Schichten. **Quellen** holen Daten, nur auf dem Server, mit Cache und Timeout. Ein RSS-Feed oder eine Statusprüfung ist dabei eine Quelle wie Kimai. **Widgets** zeigen nur an und rufen nie selbst etwas ab. **Regeln** lesen dieselben Daten und erzeugen Hinweise. Startseite und Auswertung teilen sich damit Code und Abrufe.

**Datenfluss**

1. **Quelle** (Connector) holt Rohdaten und normalisiert sie in Pydantic-Modelle (`TimeEntry`, `Invoice`, `Asset`, `License`, `Visit` …).
2. **Snapshot-Store** legt pro Lauf einen Zeitstempel-Snapshot ab (für Trends und „seit gestern neu“).
3. **Kennzahlen** werden daraus berechnet (Umsatz YTD, Auslastung, offene Posten …).
4. **Regeln** laufen über Kennzahlen und Rohdaten und erzeugen **Hinweise** mit stabilem Fingerprint (`regel-id + objekt-id`). Ein Hinweis bleibt so über Läufe hinweg derselbe, kann quittiert oder bis zu einem Datum pausiert werden und verschwindet von selbst, wenn die Bedingung wegfällt.
5. **Widgets** und **Benachrichtigungen** lesen nur Quellen-Cache, Hinweise und Kennzahlen.

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

## 4. Startseite: Migration der Dashy-Funktionen

Das Dashboard übernimmt die Rolle von Dashy als Startseite. Migriert werden die Funktionen, die in einem typischen Homelab-Dashy tatsächlich genutzt werden. Alles andere ist bewusst ausgelassen oder durch etwas Einfacheres ersetzt.

### 4.1 Funktionsumfang

| Dashy-Funktion | Umsetzung im Dashboard | Priorität |
|---|---|---|
| Seiten (`pages`), Abschnitte (`sections`), Einträge (`items`) | Gleiche Struktur in `config.yml`: `pages → sections → widgets` | Muss |
| Eintrag: `title`, `description`, `url`, `icon`, `target` (`newtab`, `sametab`) | Widget `link`; `modal` und `workspace` entfallen (öffnen als neuer Tab) | Muss |
| Icons: `favicon`, `si-*` (Simple Icons), `hl-*` (Dashboard Icons), URL, lokale Datei | Server holt das Icon einmal, bereinigt SVGs und legt es unter `/data/icons` ab; Font-Awesome-Icons (`fas fa-*`) werden zu einem Monogramm-Icon im Design-System-Stil | Muss |
| Statusprüfung (`statusCheck`, `statusCheckUrl`, `statusCheckAcceptCodes`, `statusCheckAllowInsecure`, `statusCheckInterval`) | Quelle `http_status` auf dem Server; Punkt auf der Kachel mit Antwortzeit im Tooltip; Status nie nur über Farbe | Muss |
| Suche/Filter durch Tippen, Tastenkürzel je Eintrag (`hotkey`) | Suchfeld (`/` fokussiert), filtert Kacheln live, `Enter` öffnet den ersten Treffer, Ziffern-Hotkeys | Muss |
| Websuche als Rückfall (`webSearch`, `searchEngine`) | Keine Treffer → Suche an konfigurierte Suchmaschine (z. B. SearXNG, DuckDuckGo) | Muss |
| Abschnitt-Anzeige (`collapsed`, `cols`, `itemSize`, `sortBy`) | `collapsed`, `cols`, `size: small|medium|large`, `sort: manual|alphabetical`; Zustand „eingeklappt“ im `localStorage` | Muss |
| Seitenkopf und Fuß (`pageInfo`: Titel, Beschreibung, Nav-Links, Footer) | `site`-Block in `config.yml`, gerendert mit `.nav` und `.foot` | Muss |
| Widget `rss-feed` | Widget `rss`: Abruf und Bereinigung auf dem Server, Cache, Anzahl und Intervall einstellbar | Muss |
| Widget `clock` | Widget `clock`: rein im Browser, Zeitzonen, Datum | Muss |
| Widget `weather` / `weather-forecast` | Widget `weather` über Open-Meteo (kein API-Key); OpenWeatherMap optional | Muss |
| Widget `iframe` | Widget `iframe`; erlaubte Ziele stehen in der Content-Security-Policy (`frame-src`) aus der Konfiguration | Soll |
| Widgets `gl-*` (Glances: CPU, RAM, Disk, Load) | Quelle `glances` + Widget `sysinfo` (passt zur IT-Landschaft) | Soll |
| Widget `public-ip` | Widget `public_ip` | Soll |
| Minimal-Ansicht (`/minimal`) | Seite `?view=compact`: nur Suche und Kacheln | Soll |
| Als App installieren (PWA) | Web-App-Manifest und Icon | Soll |
| Themes, Theme-Wechsler, Custom CSS | Ersetzt durch Design System, dunkel/hell | Nein |
| Konfigurations-Editor in der UI, Cloud-Backup | Konfiguration bleibt YAML (versioniert in Git). Später ggf. schreibgeschützte Ansicht | Nein |
| Eingebaute Anmeldung, Keycloak, Gast-Sichtbarkeit (`hideForGuests` …) | Anmeldung über den Reverse Proxy | Nein |
| Übrige Dashy-Widgets (Krypto, GitHub-Trending, Sport …) | Nicht migriert, der Importer listet sie auf. Bei Bedarf als eigener Widget-Typ | Später |

### 4.2 Widget-Modell

Startseite und Auswertung nutzen dasselbe Modell (siehe Architektur): **Quellen** holen Daten auf dem Server, **Widgets** zeigen sie nur an.

| Widget | Quelle | Aktualisierung |
|---|---|---|
| `link` | `http_status` (optional), optionale Infozeile aus einer Dienst-Quelle (`info: kimai.today`) | Status alle 5 min |
| `rss` | `rss` | 30 min |
| `clock` | keine (Browser) | sekündlich im Browser |
| `weather` | `open_meteo` | 30 min |
| `iframe` | keine | – |
| `sysinfo` | `glances` | 1 min |
| `public_ip` | `public_ip` | 1 h |
| `kpi`, `hints`, `table`, `chart` | Dienst-Quellen, Regeln | je Quelle |

Jedes Widget wird als eigenes HTMX-Fragment geladen und aktualisiert. Fällt eine Quelle aus, zeigt nur dieses Widget den Fehler und den letzten Stand mit Alter an.

**Link-Kachel mit Infozeile:** Die Verbindung zwischen Launcher und Auswertung. Die Kachel verlinkt auf den Dienst und zeigt zusätzlich einen Live-Wert und die Zahl offener Hinweise:

```
┌ Kimai ────────── ● online ┐  ┌ Invoice Ninja ─── ● online ┐  ┌ Snipe-IT ──────── ● online ┐
│ 3,5 h heute · Timer läuft │  │ 2 überfällig · 4.180 € off. │  │ 1 Garantie läuft ab        │
│                    ⚠ 1    │  │                      ⚠ 2    │  │                      ⓘ 1   │
└───────────────────────────┘  └─────────────────────────────┘  └────────────────────────────┘
```

### 4.3 Importer für `conf.yml`

`dashboard import-dashy conf.yml > config.pages.yml` übersetzt eine Dashy-Konfiguration:

| Dashy | Dashboard |
|---|---|
| `pageInfo` | `site` |
| `appConfig.statusCheck`, `statusCheckInterval` | Standardwerte für `link.status` |
| `appConfig.webSearch` | `search.engine` |
| `appConfig.theme`, `customCss`, `layout` | ignoriert (im Bericht vermerkt) |
| `sections[].items[]` | Widgets `link` (inkl. Icon, Status, Hotkey, Target) |
| `sections[].widgets[]` (`rss-feed`, `clock`, `weather`, `iframe`, `gl-*`, `public-ip`) | entsprechende Widget-Typen |
| `sections[].displayData` | `collapsed`, `cols`, `size`, `sort` |
| `pages[]` (Unterseiten, eigene YAML-Dateien) | weitere Einträge unter `pages` |
| alles andere | Bericht „nicht übernommen“ am Ende der Ausgabe |

Getestet wird der Importer mit einer anonymisierten Kopie der eigenen `conf.yml` als Fixture.

### 4.4 Umstieg

1. Dashboard läuft parallel zu Dashy (anderer Port oder Subdomain).
2. `conf.yml` importieren, Bericht durchgehen, fehlende Icons oder Widgets nachziehen.
3. Einige Tage beide nutzen. Kriterium für den Wechsel: Alle täglich genutzten Links, Statusanzeigen und Feeds sind da, die Suche ist mindestens so schnell.
4. Browser-Startseite umstellen, Dashy-Container stoppen, `conf.yml` archivieren.

### 4.5 Technische Leitplanken

- **Keine Aufrufe aus dem Browser** zu Diensten oder Feeds. Kein CORS-Proxy, keine Tokens im Frontend.
- **Fremde Inhalte bereinigen:** RSS-HTML mit `nh3` (erlaubte Tags: Absätze, Links, Hervorhebungen), Links mit `rel="noopener noreferrer"`. SVG-Icons ohne Skripte und externe Verweise, ausgeliefert als `<img>`.
- **Content-Security-Policy:** Skripte nur aus dem eigenen Container, `frame-src` nur für konfigurierte iframe-Ziele.
- **Kleines JavaScript:** Suche, Hotkeys, Uhr und Einklappen in reinem JavaScript (wenige KB), ohne Build-Kette. Alles andere per HTMX.
- **Zeitlimits:** Statusprüfungen und Feeds mit kurzen Timeouts und Backoff, damit langsame Ziele nichts blockieren.

---

## 5. Die Dienste: Daten und Hinweise

Alle Abrufe laufen read-only mit eigenen API-Tokens. Endpunkte beim Bau gegen die jeweilige Version prüfen (Kimai: `/api/doc`, Dawarich: `/api-docs`).

### 5.1 Kimai (Zeiterfassung)

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

### 5.2 Invoice Ninja v5 (Rechnungen, Zahlungen, Ausgaben)

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

### 5.3 Snipe-IT (Assets, Lizenzen)

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

### 5.4 Dawarich (Standortverlauf)

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

### 5.5 Übergreifend: Fristen und Wochenrückblick

| Regel | Inhalt |
|---|---|
| `tax.vat_return` | Umsatzsteuer-Voranmeldung zum 10. (Monat/Quartal, Dauerfristverlängerung konfigurierbar) |
| `tax.prepayment` | ESt-Vorauszahlungen 10.03., 10.06., 10.09., 10.12. mit Betrag aus Konfiguration |
| `tax.annual` | Jahresabschluss/EÜR-Erinnerung mit eigener Frist |
| `digest.weekly` | Montag: Stunden, Umsatz, offene Posten, neue Hinweise der Woche |
| `digest.monthly` | Monatsanfang: Vormonat abschließen (Export in Kimai → Rechnung in Invoice Ninja → Fahrtkosten aus Dawarich) |

Alle Beträge, Sätze und Fristen stehen in `config.yml` und werden pro Jahr gepflegt.

---

## 6. Design: shrippen Design Default

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
| `.launch` | Link-Kachel: Icon, Titel, Beschreibung, Statuspunkt (mit Text/Tooltip), Infozeile, Hinweis-Zähler; Größen `small`, `medium`, `large` |
| `.launch-grid` | Raster für Kacheln eines Abschnitts, Spaltenzahl aus `cols` |
| `.section-fold` | Einklappbarer Abschnitt mit Titel und Anzahl |
| `.search` | Suchfeld für Filter und Websuche, Hotkey-Hinweis in Mono |
| `.feed` | RSS-Liste: Titel, Quelle, relatives Alter |
| `.clock`, `.weather` | Kompakte Kopf-Widgets (Rajdhani, tabellarische Ziffern) |
| `.hint` | Hinweis mit Stufe, Quelle, „Warum?“-Aufklappbereich, Aktionen (öffnen, pausieren, quittieren). Stufe nie nur über Farbe, sondern auch über Icon und Text |
| `.timeline` | Fristen der nächsten 30 Tage |
| Diagramm-Palette | Reihenfolge `--blue`, `--aqua`, `--yellow`, `--orange`, `--purple`, `--green`; Achsen `--fg3`, Gitter `--bg2` |

Die Komponenten nutzen ausschließlich die Tokens des Design Systems (`var(--…)`), keine eigenen Hex-Werte. Ob sie später ins Design System wandern, ist eine eigene Entscheidung außerhalb dieses Projekts.

**Layout-Skizze: Start (Dashy-Ersatz)**

```
┌─────────────────────────────────────────────────────────────────────┐
│ ◆ dashboard  Start  Übersicht  Freelance  IT  Reisen  Fristen DE|EN ☼│
├─────────────────────────────────────────────────────────────────────┤
│ [ / Suchen oder Websuche …                    ]   09:14 · 17° ☁     │
├─────────────────────────────────────────────────────────────────────┤
│ ▾ Freelance (3)                          │ ▾ News                    │
│ [Kimai ●] [Invoice Ninja ●] [Dawarich ●] │ · Artikel A      heise 1h │
│ ▾ IT (6)                                 │ · Artikel B      lwn   3h │
│ [Snipe-IT ●] [Proxmox ●] [Gitea ●] …     │ · Artikel C      heise 5h │
│ ▸ Medien (4)                             │ ▾ System (Glances)        │
│                                          │ CPU 12 % · RAM 61 %       │
├──────────────────────────────────────────┴──────────────────────────┤
│ Hinweise: ⚠ 3 · ⓘ 5   →  Übersicht                                   │
└─────────────────────────────────────────────────────────────────────┘
```

**Layout-Skizze: Übersicht**

```
┌─────────────────────────────────────────────────────────────────────┐
│ ◆ dashboard  Start  Übersicht  Freelance  IT  Reisen  Fristen DE|EN ☼│
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

## 7. Phasen

Jede Phase endet mit einem lauffähigen, getaggten Image.

### Phase 0: Fundament (v0.1)

- [ ] Repo-Struktur, `pyproject.toml`, Ruff, Pytest, pre-commit
- [ ] FastAPI-Grundgerüst, Jinja-Layout mit `shrippen.css`, lokale Schriften, Theme-Umschalter
- [ ] Konfiguration: `config.yml` + Env/Secrets, Pydantic-Validierung, `config.example.yml`
- [ ] Quellen-Schnittstelle (`fetch()`, `healthcheck()`, Cache, TTL), Widget-Schnittstelle (Schema + Vorlage + HTMX-Fragment), Scheduler, SQLite-Schema (Snapshots, Hinweise)
- [ ] Hinweis-Engine: Fingerprint, Zustände, Snooze/Ack
- [ ] Dockerfile (multi-stage, non-root, `HEALTHCHECK`), `docker-compose.example.yml`
- [ ] GitHub Actions: Tests, Image-Build `linux/amd64` + `linux/arm64`, Push nach GHCR
- [ ] Demo-Modus mit Fixture-Daten (für Entwicklung und Screenshots ohne echte Daten)

### Phase 1: Startseite, Dashy-Migration (v0.2)

- [ ] Widget `link` mit Icons (`favicon`, `si-*`, `hl-*`, URL, lokal, Monogramm) und Icon-Cache
- [ ] Quelle `http_status` und Statuspunkt auf den Kacheln
- [ ] Seiten, einklappbare Abschnitte, `cols`, Kachelgrößen, Sortierung
- [ ] Suche mit Filter, `Enter`, Hotkeys, Websuche als Rückfall
- [ ] Widgets `rss`, `clock`, `weather` (Open-Meteo)
- [ ] Widgets `iframe`, `sysinfo` (Glances), `public_ip`; kompakte Ansicht; PWA-Manifest
- [ ] Importer `dashboard import-dashy` mit Bericht, getestet an der eigenen `conf.yml`
- [ ] Neue Komponenten `.launch`, `.launch-grid`, `.section-fold`, `.search`, `.feed`, `.clock`, `.weather`
- [ ] Parallelbetrieb, dann Umstieg nach Checkliste (Abschnitt 4.4)

**Ergebnis:** Dashy ist abgeschaltet, das Dashboard ist die Browser-Startseite.

### Phase 2: Freelance-Kern: Kimai + Invoice Ninja (v0.3, MVP der Auswertung)

- [ ] Kimai-Connector und Kennzahlen, Regeln aus 5.1
- [ ] Invoice-Ninja-Connector und Kennzahlen, Regeln aus 5.2
- [ ] Abgleich Kimai ↔ Invoice Ninja: nicht abgerechnete Stunden je Kunde, effektiver Stundensatz
- [ ] Seiten „Übersicht“ und „Freelance“ mit `.kpi`, `.hint`, `.progress`, Tabelle offener Posten
- [ ] Deep-Links von jedem Hinweis in die Fach-UI
- [ ] Infozeilen und Hinweis-Zähler auf den Link-Kacheln von Kimai und Invoice Ninja

**Ergebnis:** Das Dashboard wird täglich genutzt und ersetzt den manuellen Blick in beide Tools.

### Phase 3: IT-Landschaft: Snipe-IT (v0.4)

- [ ] Snipe-IT-Connector, Regeln aus 5.3
- [ ] Seite „IT“: Assets nach Status, Garantie-/Lizenz-Zeitleiste, Audits
- [ ] Abgleich Snipe-IT ↔ Invoice-Ninja-Ausgaben (`snipe.expense_missing`)

### Phase 4: Standort: Dawarich (v0.5)

- [ ] Dawarich-Connector, nur Aggregate speichern
- [ ] Zuordnung Area → Kimai-Kunde in `config.yml`
- [ ] Regeln aus 5.4: Besuch ohne Buchung, Fahrtkosten, Verpflegungspauschalen
- [ ] Seite „Reisen“: Kundentage, km je Monat, Vorschlag für Fahrtkosten-Position

### Phase 5: Erinnerungen und Benachrichtigungen (v0.6)

- [ ] Fristen-Kalender (Abschnitt 5.5), Seite „Fristen“, iCal-Feed `/calendar.ics`
- [ ] Benachrichtigungen über Apprise (ntfy, Gotify, E-Mail, Matrix …), je Stufe konfigurierbar
- [ ] Morgen-Digest und Wochenrückblick; Ruhezeiten; keine Doppelmeldungen (Fingerprint)
- [ ] Monatsabschluss-Checkliste (Kimai-Export → Rechnung → Fahrtkosten)

### Phase 6: Trends und Prognosen (v0.7)

- [ ] Verlaufsdiagramme aus Snapshots (Umsatz, Stunden, offene Posten)
- [ ] Hochrechnung Jahresumsatz, Kleinunternehmergrenze, Steuerrücklage
- [ ] Vergleich Vorjahr, saisonale Muster, Liquiditätsvorschau (offene Posten + wiederkehrende Rechnungen − feste Ausgaben)

### Phase 7: Ausbau (v1.0)

- [ ] Weitere Dashy-Widgets nach Bedarf (Liste aus dem Importer-Bericht)
- [ ] Weitere Connectors über dieselbe Schnittstelle: z. B. Uptime Kuma (Dienste down), Proxmox/Docker (Updates, Speicher), Backup-Status, Paperless-ngx (unbearbeitete Belege), Zertifikatsablauf
- [ ] Optionale Wochenzusammenfassung in Fließtext per LLM (abschaltbar, nur Aggregate, keine Standortdaten)
- [ ] Komponenten-Übersicht (`.launch`, `.kpi`, `.hint`, `.timeline` …) als Seite `/styleguide` im Dashboard, damit sie bei Bedarf ins Design System übernommen werden können

---

## 8. Betrieb und Sicherheit

- **Tokens:** Pro Dienst ein eigener, möglichst eingeschränkter API-Benutzer (nur lesen). Übergabe per Docker Secrets oder Env, nie in `config.yml` im Repo.
- **Zugriff:** Kein eigenes Login in v0.x. Das Dashboard läuft hinter dem Reverse Proxy mit Authentifizierung (Authelia, Authentik oder Basic Auth). Optional eingebaute Basic Auth als Rückfallebene.
- **Netz:** Ausgehende Verbindungen nur zu den konfigurierten Diensten, Feeds, Statuszielen und Icon-Quellen. Keine externen CDNs zur Laufzeit; Icons werden einmal geholt und lokal zwischengespeichert.
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

site:
  title: dashboard
  nav: [{ title: Gitea, url: https://git.example.lan }]
search:
  engine: https://searx.example.lan/search?q={query}

pages:
  - name: Start
    sections:
      - title: Freelance
        cols: 3
        widgets:
          - { type: link, title: Kimai, url: https://kimai.example.lan, icon: hl-kimai,
              status: http, info: kimai.today, hotkey: 1 }
          - { type: link, title: Invoice Ninja, url: https://invoice.example.lan,
              icon: hl-invoiceninja, status: http, info: invoiceninja.open }
      - title: News
        widgets:
          - { type: rss, url: https://www.heise.de/rss/heise-atom.xml, limit: 8, refresh: 30m }
      - title: Kopf
        widgets:
          - { type: clock, timezones: [Europe/Berlin] }
          - { type: weather, lat: 52.52, lon: 13.40 }
  - name: Übersicht
    sections:
      - widgets: [{ type: kpi, metric: invoiceninja.revenue_ytd }, { type: hints }]
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

## 9. Vorgeschlagene Repo-Struktur

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
│   ├── sources/             ← base.py, kimai.py, invoiceninja.py, snipeit.py, dawarich.py,
│   │                          rss.py, http_status.py, open_meteo.py, glances.py, public_ip.py
│   ├── widgets/             ← base.py, link.py, rss.py, clock.py, weather.py, iframe.py,
│   │                          sysinfo.py, kpi.py, hints.py …
│   ├── icons.py             ← Icon-Auflösung (favicon, si-, hl-, URL), SVG-Bereinigung, Cache
│   ├── metrics/             ← Kennzahlen je Bereich
│   ├── rules/               ← Regeln je Dienst + cross.py (dienstübergreifend) + deadlines.py
│   ├── notify/              ← Apprise, Digest-Vorlagen
│   ├── templates/           ← Jinja-Seiten und Partials (HTMX)
│   └── static/
│       ├── vendor/shrippen/ ← Kopie von shrippen.css / shrippen.js / Schriften + VERSION
│       ├── dashboard.js     ← Suche, Hotkeys, Uhr, Einklappen (ohne Build)
│       └── dashboard.css    ← nur neue Komponenten (.launch, .kpi, .hint, .feed …)
├── tools/
│   ├── sync-design.sh       ← holt eine bestimmte Version des Design Systems nach vendor/
│   └── import_dashy.py      ← conf.yml → config.yml, mit Bericht
└── tests/
    ├── fixtures/            ← anonymisierte API-Antworten je Dienst, Dashy-conf.yml, RSS-Beispiele
    └── rules/               ← ein Test pro Regel
```

---

## 10. Offene Fragen

1. **Versionen:** Welche Kimai- und Invoice-Ninja-Versionen laufen (v5 self-hosted?), und bieten holiday-/abrechnung-bundle eigene API-Endpunkte?
2. **Dashy:** Welche Widgets nutzt die aktuelle `conf.yml` wirklich? Eine anonymisierte Kopie dient als Testfall für den Importer und korrigiert die Prioritäten in 4.1.
3. **Benachrichtigungen:** Welcher Kanal ist vorhanden (ntfy, Gotify, E-Mail, Matrix)?
4. **Zugriffsschutz:** Welcher Reverse Proxy / welche Authentifizierung ist im Einsatz?
5. **Steuerstatus:** Kleinunternehmer oder regelbesteuert, USt-VA monatlich oder quartalsweise? Davon hängen die Standardregeln in 5.2/5.5 ab.
6. **Dawarich:** Sind Kundenstandorte schon als Areas angelegt, und ist der Abgleich mit Kimai gewünscht?
7. **Sprache:** UI zweisprachig (DE/EN wie die Landing Pages) oder nur Deutsch?
