# Roadmap: IT- & Freelance-Dashboard

Ein selbst gehostetes, **mehrbenutzerfähiges** Dashboard. Es löst Dashy als **Startseite mit Links, Statusanzeigen und Feeds** ab, führt zugleich Daten aus **Kimai**, **Invoice Ninja**, **Snipe-IT** und **Dawarich** zusammen und leitet daraus **Hinweise, Erinnerungen und Ratschläge** ab. Konfiguriert wird im **eingebauten Editor**, gestaltet über ein **Theme-System**, von dem nur das Theme **shrippen** mitgeliefert wird. Auslieferung als **ein Docker-Container** mit eigener Anmeldung.

> Stand: v0.4 · Arbeitstitel `dashboard`
>
> **Arbeitsort:** Die gesamte Entwicklung findet in diesem Repository (`shrippen/dashboard`) statt. Das Design-System-Repo `shrippen/shrippen.github.io` ist nur Quelle, es wird von hier aus nicht verändert.

---

## Umsetzungsstand (2026-09-25)

Das Projekt ist vollständig von Python auf **Go** umgestellt (Zielplattform: Raspberry Pi, `net/http` + `html/template` + htmx, `modernc.org/sqlite` — kein cgo, kein C-Toolchain nötig). Die frühere Python-Fassung ist nur noch in der Git-Historie vorhanden. Getestet mit gemockten API-Antworten (`httptest`), **nicht gegen echte Instanzen**. Vor dem Produktivbetrieb die Verbindungstests je Dienst ausführen und die Hinweise auf Plausibilität prüfen.

| Bereich | Stand |
|---|---|
| Anmeldung (Passwort, TOTP, Passkeys, OIDC), Teams, Rechte, Freigaben, Overlays | umgesetzt |
| Editor, Bibliothek, Revisionen, YAML-/Dashy-Import, Code-Ansicht (CodeMirror) | umgesetzt |
| Themes (Editor, Import/Export, Schriften, Styleguide, WCAG-AA-Prüfung) | umgesetzt |
| Kimai, Invoice Ninja, Snipe-IT, Dawarich | Adapter, Regeln, Insight-Widgets umgesetzt |
| Homelab-Dienste (Phase 10) | 17 weitere Quellen mit Regeln; Obsidian zurückgestellt, Docker offen |
| Prüflauf | Hintergrund-Job holt alle Integrationen (Start + alle `ANALYSIS_MINUTES`); Seiten zeigen nur diesen Stand, live nur der Status-Ping |
| Benachrichtigungen | Apprise, Digest-Mail (SMTP), Wochenrückblick mit optionaler LLM-Zusammenfassung, iCal |
| Trends, Prognosen | Snapshots, Verlauf, Saisonvergleich, Jahresprognose, Liquidität |
| Betrieb | CLI `backup`, `rotate-key`, `import`; Demo-Modus; Icons-Dienst |
| Produktivbetrieb | **offen:** Parallelbetrieb neben Dashy und Umstieg (manuell, Abschnitt 7.4) |

**Abweichungen vom Plan:** Übersetzungen als YAML-Kataloge mit Schlüsseln (unverändert vom Python-Stand übernommen). Das mitgelieferte Theme liegt in `internal/web/templates/` (Builtin, eingebettet). Board-Vorlagen/Revisionen speichern den Board- bzw. Widget-eigenen Zustand, nicht die bereichsübergreifende YAML-Form aus `porting.py`.

**Bekannte Unsicherheiten:** Invoice Ninja v5 liefert IDs als Hash-Strings; Kunden-IDs werden daraus stabil in Zahlen umgerechnet, der Originalschlüssel bleibt für Schreibzugriffe erhalten. Deep-Links in Invoice Ninja (`/#/invoices/<id>/edit`), Snipe-IT meldet keine Version, Dawarich-Felder für Besuche (`area_id`, `place`) werden tolerant gelesen.

---

## 1. Ziele und Nicht-Ziele

**Ziele**

- Ein Blick am Morgen genügt: Was brennt (überfällige Rechnungen, ablaufende Garantien), was ist offen (nicht abgerechnete Stunden), wie läuft das Jahr (Umsatz, Auslastung, Stundensatz).
- Hinweise sind **regelbasiert, erklärbar und konfigurierbar**: Jede Meldung sagt, *warum* sie erscheint, welche Daten dahinterstehen und wohin man klickt, um sie zu erledigen.
- **Querverbindungen** zwischen den Diensten sind der eigentliche Mehrwert (z. B. „Du warst laut Dawarich 6 h bei Kunde X, in Kimai ist nichts gebucht“).
- **Startseite wie bisher:** Die täglich genutzten Dashy-Funktionen (Links, Icons, Status, Suche, RSS, Uhr, Wetter) sind übernommen, sodass Dashy abgeschaltet werden kann.
- **Mehrere Benutzer und Teams:** Benutzer arbeiten völlig getrennt voneinander oder teilen sich in Teams Verbindungen, Widgets und Boards. Rechte lassen sich bis auf einzelne Widgets vergeben, jeder hat sein eigenes Layout.
- **Alles in der Oberfläche einstellbar:** Konfigurationseditor mit Formularen, Drag & Drop und Code-Ansicht, Versionsverlauf und Import/Export.
- **Eigene Themes:** Themes lassen sich in der Oberfläche anlegen und bearbeiten. Mitgeliefert wird nur das shrippen-Theme (dunkel und „Leinen“).
- **Eigene Anmeldung** im Dashboard, unabhängig vom Reverse Proxy.
- Nur **lesender** Zugriff auf die Dienste. Das Dashboard verändert nichts in Kimai, Invoice Ninja usw.
- Ein Container, ein Volume.

**Nicht-Ziele (vorerst)**

- Kein Ersatz für die Fach-UIs (Rechnungen schreiben bleibt in Invoice Ninja).
- Keine Mandantentrennung auf Instanzebene (mehrere voneinander unabhängige Organisationen mit eigenen Admins). Teams und getrennte Benutzer innerhalb einer Instanz reichen.
- Keine Theme-Galerie oder Weitergabe von Themes über das Internet; Themes werden als Datei exportiert und importiert.
- Keine Steuerberatung: Steuerhinweise sind Erinnerungen mit konfigurierbaren Werten, keine verbindliche Auskunft.

---

## 2. Grundlage: Dashy erweitern oder eigene App?

| Kriterium | Dashy als Basis | Homepage / Glance | **Eigene App (empfohlen)** |
|---|---|---|---|
| Links/Startseite | sehr gut | sehr gut | wird nachgebaut (nur genutzte Funktionen, Import aus Dashy) |
| Eigene Widgets | Vue-2-Komponenten, Fork + eigener Build nötig | `customapi`/`custom-api`-Widgets, nur Anzeige | frei |
| API-Aufrufe | größtenteils im Browser (CORS-Proxy, Tokens im Frontend) | serverseitig | serverseitig, Tokens verschlüsselt im Container |
| Mehrbenutzer, Teams, Rechte je Widget | nur einfache Benutzer/Gäste | nein | ja |
| Konfigurationseditor | ja, für eine gemeinsame Datei | nein | ja, je Benutzer/Team, mit Verlauf |
| Verlauf/Trends, Regel-Engine, Push/Digest | nein | nein | ja |
| Styling nach Design System, eigene Themes | Custom-CSS-Theme, begrenzt | Custom-CSS, begrenzt | Theme-System auf Basis der Design-Tokens |

**Empfehlung:** Eigene App mit Backend. Hinweise, Mehrbenutzerbetrieb mit Rechten auf Widget-Ebene und ein Editor, der pro Benutzer oder Team speichert, lassen sich auf keinem der Startseiten-Tools sauber aufsetzen.

**Dashy wird abgelöst:** Die wahrscheinlich genutzten Dashy-Funktionen werden migriert (Abschnitt 7), die `conf.yml` wird über einen Import-Assistenten übernommen. Bis zum Umstieg laufen beide parallel; für diese Zeit lassen sich Hinweise und Kennzahlen über `/embed/*` (mit persönlichem Embed-Token) auch in Dashy einbetten.

---

## 3. Architektur

```
┌──────────────────────────────── Docker-Container ─────────────────────────────────┐
│                                                                                   │
│  Anmeldung ─► Sitzung ─► Berechtigungsprüfung (jede Anfrage, jedes Widget-Fragment) │
│                                   │                                               │
│  Web-UI (Jinja + HTMX)  ◄─────────┼──────── Boards, Widgets, Themes, Editor        │
│  JSON-API /api/*  ·  /embed/*     │                                               │
│                                   ▼                                               │
│  Scheduler ─► Quellen ─► Cache/Snapshots ─► Kennzahlen ─► Regeln ─► Hinweise       │
│               (je Verbindung    (SQLite,                        (Zustand je        │
│                und Zugangsdaten) /data)                          Benutzer/Team)    │
│                                                                      │            │
│                                                   Benachrichtigungen ◄┘ (je Benutzer)│
└───────────────────────────────────────────────────────────────────────────────────┘
```

**Grundprinzip:** Getrennte Schichten.

- **Quellen** holen Daten, nur auf dem Server, mit Cache und Timeout. Ein RSS-Feed oder eine Statusprüfung ist dabei eine Quelle wie Kimai.
- **Widgets** zeigen nur an und rufen nie selbst etwas ab.
- **Regeln** lesen dieselben Daten und erzeugen Hinweise.
- **Berechtigungen** werden zentral in der Service-Schicht geprüft, nie nur in Vorlagen. Jede Datenbankabfrage ist auf die Bereiche beschränkt, die der Benutzer sehen darf.

**Datenbank statt Konfigurationsdatei:** Weil Editor und Mehrbenutzerbetrieb schreiben, ist die **Datenbank die Quelle der Wahrheit** für Boards, Widgets, Verbindungen, Themes und Rechte. YAML bleibt als Import-/Export-Format und für eine optionale Erstbefüllung (`seed.yml`). Über Umgebungsvariablen kommen nur Betriebswerte (Basis-URL, Datenbank, Hauptschlüssel, SMTP).

**Stack-Vorschlag**

| Schicht | Wahl | Begründung |
|---|---|---|
| Sprache | Python 3.12 | gute HTTP-/Datums-Bibliotheken, die vorhandenen Tools (`preview/server.py`, `tools/*.py`) sind schon Python |
| Web | FastAPI + Jinja2 + HTMX | serverseitig gerendert, kaum JavaScript, passt zum CSS-only Design System |
| Datenbank | SQLAlchemy 2 + Alembic; SQLite im WAL-Modus | reicht für 1–10 Benutzer problemlos, ein Volume, einfaches Backup; Migrationen bei jedem Update. Durch SQLAlchemy bleibt PostgreSQL später möglich, wird aber nicht unterstützt oder getestet |
| Anmeldung | `argon2-cffi`, serverseitige Sitzungen, `pyotp` (TOTP), `authlib` (OIDC für authentik), später `webauthn` | eigene Anmeldung, dazu Single Sign-on über authentik |
| Übersetzung | Babel + Jinja2-i18n (gettext, `.po`-Dateien) | Oberfläche, Hinweise und E-Mails auf Deutsch und Englisch; Zahlen, Beträge und Datumsangaben im Format der Sprache |
| Benachrichtigungen | Apprise | ein Baustein für alle Kanäle (ntfy, Gotify, Matrix, Telegram, E-Mail …), je Benutzer eine Liste von Apprise-URLs |
| E-Mail | `aiosmtplib`, Vorlagen im Design System | Einladungen, Passwort-Reset, Sicherheitsmeldungen, Digest |
| Geheimnisse | `cryptography` (AES-GCM), Hauptschlüssel aus Docker Secret | API-Tokens verschlüsselt in der Datenbank |
| HTTP | `httpx` (async) | parallele Abrufe, Timeouts, Retries |
| Zeitplan | APScheduler | Intervall je Quelle, dazu Cron-Jobs für Digests |
| Fremdinhalte | `feedparser`, `nh3` | RSS lesen und HTML bereinigen |
| Editor | SortableJS (vorgebaut, ins Repo kopiert), Formulare aus JSON-Schema | Drag & Drop ohne Framework und ohne Build-Kette |
| Diagramme | uPlot oder Chart.js, Farben aus den Theme-Tokens | leicht, keine Build-Kette |
| Validierung | Pydantic | ein Schema je Widget-Typ für Editor, Import und API |

**Datenfluss**

1. **Quelle** holt Rohdaten über eine **Verbindung** und normalisiert sie in Pydantic-Modelle (`TimeEntry`, `Invoice`, `Asset`, `License`, `Visit` …). Der Cache-Schlüssel ist *Verbindung + Zugangsdaten*: Eine geteilte Verbindung wird einmal abgerufen, eine Verbindung mit persönlichen Zugangsdaten einmal je Benutzer.
2. **Snapshot-Store** legt pro Lauf einen Zeitstempel-Snapshot ab (für Trends und „seit gestern neu“).
3. **Kennzahlen** werden daraus berechnet (Umsatz YTD, Auslastung, offene Posten …).
4. **Regeln** laufen je Bereich über Kennzahlen und Rohdaten und erzeugen **Hinweise** mit stabilem Fingerprint (`regel-id + objekt-id`). Ein Hinweis bleibt so über Läufe hinweg derselbe, kann quittiert oder bis zu einem Datum pausiert werden und verschwindet von selbst, wenn die Bedingung wegfällt.
5. **Widgets** und **Benachrichtigungen** lesen nur Cache, Hinweise und Kennzahlen, und zwar nur für Benutzer mit Zugriff.

**Hinweis-Modell**

```yaml
id: kimai.unbilled_hours:customer-12
space: personal:alex    # Bereich, zu dem der Hinweis gehört
severity: warn          # info | warn | critical
message: kimai.unbilled_hours          # Übersetzungsschlüssel, kein fertiger Text
params: { hours: 38, customer: "Muster GmbH", days: 30, oldest: 2026-08-12 }
action: { label: open_in_kimai, url: "https://kimai.example/…" }
due: 2026-10-10         # optional, für Fristen
source: [kimai, invoiceninja]
state: open             # open | snoozed | acknowledged | resolved (je Benutzer oder für das Team)
```

Hinweise speichern **Schlüssel und Parameter statt fertiger Sätze**. So erscheint derselbe Hinweis für jeden Benutzer in seiner Sprache („38 h bei Kunde Muster GmbH noch nicht abgerechnet“ / „38 h for Muster GmbH not yet billed“), und Zahlen und Datumsangaben werden passend formatiert.

**Regeln** gibt es in zwei Formen:

- **Einstellbar** im Editor für einfache Schwellwerte (Tage, Beträge, Prozent), je Bereich.
- **Als Python-Klasse** für Regeln über mehrere Dienste hinweg (Abgleich Dawarich ↔ Kimai). Jede Regel hat ID, Standard-Schwellwerte, Beschreibung und einen Unit-Test mit Fixture-Daten.

---

## 4. Mehrbenutzer, Anmeldung und Berechtigungen

### 4.1 Datenmodell

```
Instanz
├── Benutzer ──(Mitglied mit Rolle)──► Team
├── Bereiche (Spaces)
│   ├── persönlich   — genau einer je Benutzer, nur für ihn sichtbar
│   ├── Team         — einer je Team, für alle Mitglieder gemäß Rolle
│   └── Instanz      — global, von Instanz-Admins gepflegt (z. B. gemeinsame Links, Standard-Theme)
│
│   Jeder Bereich enthält:
│   ├── Verbindungen      Dienst-URL + Zugangsdaten (verschlüsselt), z. B. „Kimai Firma“
│   ├── Widget-Bibliothek konfigurierte Widgets (Typ + Einstellungen + Verbindung)
│   ├── Boards            Seiten → Abschnitte → Platzierungen (Verweise auf Widgets)
│   ├── Regel-Einstellungen, Fristen, Ziele
│   └── Themes
│
├── Freigaben   Ressource → Benutzer oder Team, mit Recht
└── Persönliche Overlays   Layout-Anpassungen eines Benutzers an fremden Boards
```

### 4.2 Rollen und Rechte

**Instanz-Rollen**

| Rolle | Darf |
|---|---|
| Instanz-Admin | Benutzer und Teams verwalten, Einladungen, Instanz-Bereich, Instanz-Themes inkl. eigenem CSS und Schriften, Anmeldeeinstellungen, Audit-Log. **Sieht keine persönlichen Bereiche anderer** (kein Einblick in Standortdaten oder Umsätze, kein „Anmelden als“) |
| Benutzer | Eigener persönlicher Bereich, Mitgliedschaften in Teams |

**Team-Rollen**

| Rolle | Darf |
|---|---|
| Owner | Mitglieder und Rollen verwalten, Verbindungen mit Zugangsdaten anlegen, alles im Team-Bereich |
| Editor | Widgets und Boards im Team-Bereich anlegen und ändern, vorhandene Verbindungen nutzen |
| Viewer | Team-Boards ansehen, eigenes Layout-Overlay anlegen |

**Rechte auf einzelne Ressourcen** (Freigaben, zusätzlich zur Rolle; auch in einen anderen Bereich hinein):

| Recht | Bedeutung |
|---|---|
| `view` | Widget/Board sehen, seine Daten anzeigen |
| `use` | Widget auf eigenen Boards platzieren; Verbindung in eigenen Widgets verwenden (ohne die Zugangsdaten je zu sehen) |
| `edit` | Einstellungen ändern |
| `manage` | Freigeben, löschen |

Rechte lassen sich innerhalb eines Teams auch **einschränken**: Ein Team-Widget „Umsatz“ kann z. B. nur für Owner sichtbar sein, obwohl das Board allen Mitgliedern gehört. Wer ein Widget nicht sehen darf, bekommt es gar nicht erst ausgeliefert, auch nicht als leere Kachel.

### 4.3 Wiederverwendung und Layouts

- **Widget-Bibliothek:** Ein Widget wird einmal eingerichtet (z. B. „Offene Rechnungen“) und auf beliebig vielen Boards **platziert**. Platzierungen sind Verweise: Eine Änderung am Widget wirkt überall. Alternativ „Als Kopie übernehmen“ für eine unabhängige Variante.
- **Team-Widgets im persönlichen Board:** Mit `use` lässt sich ein Team-Widget auf das eigene Start-Board legen. Wird das Recht entzogen, zeigt die Platzierung „kein Zugriff mehr“ statt Daten.
- **Persönliche Layouts:** Jeder Benutzer hat eigene Boards und ein eigenes Start-Board. An Team-Boards kann er ein **Overlay** anlegen (Reihenfolge, Größe, eingeklappt, ausgeblendet), ohne das Board für andere zu ändern. „Auf Team-Layout zurücksetzen“ löscht das Overlay.
- **Persönliche Einstellungen:** Theme, hell/dunkel, Sprache, Start-Board, Benachrichtigungskanäle, Ruhezeiten, eigene Suchmaschine.
- **Vorlagen:** Boards lassen sich als Vorlage speichern (ohne Zugangsdaten) und von anderen Benutzern oder Teams übernehmen, z. B. „Freelance-Übersicht“.

### 4.4 Verbindungen: geteilte und persönliche Zugangsdaten

Eine geteilte Verbindung heißt **geteilte Daten**: Wer ein Widget auf einer Verbindung sehen darf, sieht, was deren Token sieht. Deshalb gibt es zwei Arten:

| Art | Beispiel | Wirkung |
|---|---|---|
| **Geteilte Zugangsdaten** | Snipe-IT des Teams mit einem Lese-Token | Alle mit Zugriff sehen dieselben Daten; ein Abruf für alle |
| **Persönliche Zugangsdaten** | Kimai des Teams, jeder hinterlegt sein eigenes Token | Dasselbe Team-Widget zeigt jedem Benutzer seine eigenen Daten; wer kein Token hinterlegt hat, sieht einen Hinweis „Zugang einrichten“ |

Beim Freigeben einer Verbindung mit geteilten Zugangsdaten warnt der Editor ausdrücklich. **Dawarich** ist standardmäßig nur als persönliche Verbindung erlaubt; eine Freigabe muss ein Instanz-Admin für die Instanz einschalten.

### 4.5 Szenarien

| Szenario | Umsetzung |
|---|---|
| **Autark:** mehrere Personen auf einer Instanz, nichts gemeinsam | Jeder nutzt nur seinen persönlichen Bereich mit eigenen Verbindungen. Teams bleiben leer. Der Admin sieht nur Konten, keine Inhalte |
| **Team:** gemeinsame IT-Landschaft | Team-Bereich mit geteilter Snipe-IT-Verbindung, Team-Boards „IT“ und „Links“, Rechte nach Rolle |
| **Gemischt:** Freelancer mit kleinem Team | Persönlicher Bereich für Umsatz, Steuer und Dawarich; Team-Bereich für Links, Snipe-IT und Kimai mit persönlichen Zugangsdaten; Team-Widgets auf dem eigenen Start-Board |

Hinweise gehören zu einem Bereich. Hinweise aus persönlichen Bereichen haben persönlichen Zustand. Bei Team-Hinweisen stellt der Team-Owner ein, ob „quittiert“ für das ganze Team gilt (z. B. „Audit überfällig“) oder für jeden einzeln.

### 4.6 Anmeldung

Das Dashboard hat eine **eigene Anmeldung**. Der Reverse Proxy übernimmt nur TLS, keine Authentifizierung; Header-basierte Proxy-Anmeldung wird bewusst nicht unterstützt.

- **Erstes Konto:** Beim ersten Start gibt der Container einen einmaligen Einrichtungscode im Log aus. Nur damit lässt sich der erste Instanz-Admin anlegen, sodass niemand eine frisch gestartete Instanz übernehmen kann.
- **Konten:** Einladungen per E-Mail durch Admins (Standard), Selbstregistrierung abschaltbar (Standard: aus). Alternativ legt die erste Anmeldung über authentik das Konto an (Abschnitt 4.7).
- **Passwörter:** Argon2id, Mindestlänge 12, keine Kompositionsregeln. Zurücksetzen per E-Mail (zeitlich begrenzter Einmal-Link); ein Admin kann zusätzlich einen neuen Einladungslink erzeugen.
- **Zweiter Faktor:** TOTP mit Wiederherstellungscodes für lokale Konten; für Admins erzwingbar. Bei Anmeldung über authentik übernimmt authentik den zweiten Faktor. Später Passkeys (WebAuthn).
- **Sicherheitsmeldungen per E-Mail:** Anmeldung von neuem Gerät, geändertes Passwort, 2FA an/aus, neues API-Token.
- **Sitzungen:** serverseitig in der Datenbank, Cookie `HttpOnly; Secure; SameSite=Lax`, neue Sitzungs-ID bei Anmeldung, Leerlauf- und absolutes Zeitlimit, Liste aktiver Sitzungen mit „abmelden“.
- **Schutz:** CSRF-Token für Formulare und HTMX-Anfragen, Drosselung je IP und Konto, gleiche Fehlermeldung für falschen Benutzer und falsches Passwort.
- **API- und Embed-Tokens:** Persönliche Tokens mit Ablaufdatum und eingeschränktem Umfang (nur lesen, nur bestimmte Boards), z. B. für das Dashy-iframe während des Umstiegs oder Skripte.
- **Audit-Log:** Anmeldungen (lokal und authentik), fehlgeschlagene Versuche, Änderungen an Rechten, Verbindungen und Zugangsdaten, Board-Änderungen.

### 4.7 Single Sign-on mit authentik (OIDC)

authentik ist ein **zusätzlicher** Anmeldeweg. Konten, Teams und Rechte bleiben im Dashboard; die lokale Anmeldung bleibt als Notzugang erhalten.

**In authentik:** Provider vom Typ *OAuth2/OpenID Provider* (vertraulicher Client) und eine Application, z. B. mit Slug `dashboard`.

| Einstellung | Wert |
|---|---|
| Redirect URI | `https://dashboard.example.lan/auth/oidc/callback` |
| Scopes | `openid`, `profile`, `email` (die Standard-Zuordnung für `profile` liefert auch `groups`) |
| Issuer / Discovery | `https://auth.example.lan/application/o/dashboard/` bzw. `…/.well-known/openid-configuration` |
| Zugriff | Binding an die Gruppe `dashboard-users`, damit nur diese Benutzer sich anmelden können |

**Im Dashboard** (Admin-Einstellungen, alternativ per Env beim ersten Start):

- Issuer-URL, Client-ID, Client-Secret (verschlüsselt gespeichert). Knopf „Verbindung testen“ lädt die Discovery und prüft die Schlüssel.
- Ablauf: Authorization Code Flow mit PKCE, `state` und `nonce`; das ID-Token wird gegen die JWKS von authentik geprüft (Signatur, Issuer, Audience, Ablauf).
- **Kontozuordnung** über `sub` (stabil, ändert sich nicht bei Umbenennung). Bestehende lokale Konten werden verknüpft, indem der Benutzer angemeldet in seinem Profil „Mit authentik verknüpfen“ wählt. Eine automatische Zuordnung über die E-Mail-Adresse nur, wenn authentik `email_verified` liefert und der Admin es erlaubt.
- **Automatisches Anlegen:** Die erste Anmeldung über authentik legt ein Konto samt persönlichem Bereich an (abschaltbar).
- **Gruppen → Rollen und Teams, nur beim Anlegen:** Eine Zuordnungstabelle in den Admin-Einstellungen bestimmt die *Startwerte* des neuen Kontos, z. B. `dashboard-admins` → Instanz-Admin, `team-it` → Team „IT“ als Editor, `team-buero` → Team „Büro“ als Viewer. Danach gehören Rollen und Mitgliedschaften dem Dashboard: Admins und Team-Owner ändern sie von Hand, spätere Anmeldungen überschreiben nichts. Ändern sich die Gruppen in authentik, bleibt das Konto unverändert; ein Admin kann bei Bedarf pro Benutzer „Startwerte aus authentik neu übernehmen“ auslösen (mit Vorschau der Änderungen, im Audit-Log vermerkt).
- **Modus „nur authentik“:** blendet das Passwortfeld aus. Ausgenommen sind als Notzugang markierte lokale Admin-Konten (erreichbar über `/login?local`), damit ein Ausfall von authentik nicht aussperrt.
- **Abmelden:** beendet die Dashboard-Sitzung und leitet optional an den `end_session_endpoint` von authentik weiter.
- **Deaktivierte Benutzer:** Sitzungen über authentik haben eine kürzere absolute Laufzeit (Standard 12 h). Wer in authentik gesperrt wird, kommt danach nicht mehr hinein.

---

## 5. Konfigurationseditor

Alles, was vorher in `config.yml` stand, wird in der Oberfläche eingestellt. Bearbeiten darf, wer im jeweiligen Bereich `edit` hat.

| Teil | Funktion |
|---|---|
| **Board-Editor** | Bearbeitungsmodus direkt auf dem Board: Abschnitte anlegen, Widgets per Drag & Drop anordnen, Spalten und Größen, Widget aus der Bibliothek einfügen oder neu anlegen. Bei fremden Boards ohne `edit` bearbeitet derselbe Modus das persönliche Overlay |
| **Widget-Formulare** | Automatisch aus dem Pydantic-Schema des Widget-Typs erzeugt: Felder, Hilfetexte, Validierung. Auswahl der Verbindung zeigt nur Verbindungen mit `use`. **Vorschau** rendert das Widget mit den ungespeicherten Einstellungen |
| **Verbindungen** | Dienst, URL, Art der Zugangsdaten (geteilt/persönlich), Token-Feld nur schreibbar (gespeicherte Tokens werden nie angezeigt), Knopf „Verbindung testen“ |
| **Regeln und Ziele** | Schwellwerte, Ziele, Fristen und Steuerwerte je Bereich, mit Standardwerten und Erklärung |
| **Code-Ansicht** | Board oder Bereich als YAML bearbeiten; validiert gegen dieselben Schemas, Fehler mit Zeilennummer. Zunächst Textfeld mit serverseitiger Prüfung, später CodeMirror |
| **Verlauf** | Jede Speicherung ist eine Revision (wer, wann, Unterschiede). Wiederherstellen einer älteren Revision. Gleichzeitiges Bearbeiten wird über eine Versionsnummer erkannt („wurde inzwischen von X geändert“) |
| **Import/Export** | YAML je Board oder Bereich (ohne Zugangsdaten, dafür Platzhalter); Dashy-Import als Assistent; Theme-Import/-Export |
| **Erstbefüllung** | Optional `seed.yml` beim ersten Start, um eine Instanz reproduzierbar aufzusetzen (Konfiguration als Code) |
| **Rechte und Freigaben** | Dialog „Freigeben“ an jedem Widget, Board und jeder Verbindung; Übersicht „Wer hat Zugriff?“ |

---

## 6. Themes

### 6.1 Theme-Modell

Ein Theme ist ein **Satz von Design-Tokens** für den dunklen und den hellen Modus, dazu optional Schriften und eigenes CSS. Alle Komponenten verwenden ausschließlich Tokens (`var(--…)`), daher funktioniert jedes Theme mit jedem Widget.

- **Theme-Vertrag:** Die Token-Liste aus `tokens/variables.css` des Design Systems (Hintergründe, Text, Akzent, Semantik, Rollen wie `--field`, `--hl`, Radius, Schriften) ist die versionierte Theme-Schnittstelle. Neue Tokens bekommen Standardwerte, damit ältere Themes weiter funktionieren.
- **Mitgeliefert:** nur **shrippen** (dunkel = Standard, hell = „Leinen“). Es ist schreibgeschützt; Änderungen beginnen mit „Duplizieren“.
- **Format** für Import/Export als ZIP:

```
mein-theme.zip
├── theme.json     ← Name, Version, Autor, Theme-Vertrag-Version, Modi
├── tokens.css     ← :root { … } und :root[data-theme="light"] { … }
├── custom.css     ← optional, nur Instanz-Admins
└── fonts/         ← optional, woff2, nur Instanz-Admins
```

### 6.2 Theme-Editor

- Token-Gruppen mit Farbwählern und Zahlenfeldern, dunkel und hell nebeneinander.
- **Live-Vorschau** an einem Beispiel-Board und an der Komponenten-Übersicht (`/styleguide`).
- **Kontrastprüfung** aller Text-/Hintergrund-Paare nach WCAG AA mit Warnungen, bevor gespeichert wird.
- Schriftwahl aus den mitgelieferten und hochgeladenen Schriften.

### 6.3 Wo Themes gelten

- Themes liegen in Bereichen: Instanz-Themes für alle, Team-Themes für Mitglieder, persönliche Themes nur für den Ersteller.
- Auswahl in dieser Reihenfolge: **persönliche Wahl → Team-Standard → Instanz-Standard (shrippen)**. Team-Boards können optional ein Theme erzwingen (z. B. für einen Wandbildschirm).
- **Sicherheit:** Normale Benutzer ändern nur Token-Werte, die serverseitig geprüft werden (Farben, Längen, Schriftnamen aus der Liste). Eigenes CSS und Schriften nur für Instanz-Admins. Die Content-Security-Policy (`img-src 'self' data:`, `font-src 'self'`, `connect-src 'self'`) verhindert, dass CSS Daten nach außen lädt.
- Ein Stylelint-Check im Repo verbietet feste Farbwerte außerhalb der Token-Dateien, damit der Theme-Vertrag hält.

---

## 7. Startseite: Migration der Dashy-Funktionen

Das Dashboard übernimmt die Rolle von Dashy als Startseite. Migriert werden die Funktionen, die in einem typischen Homelab-Dashy tatsächlich genutzt werden. Alles andere ist bewusst ausgelassen oder durch etwas Einfacheres ersetzt.

### 7.1 Funktionsumfang

| Dashy-Funktion | Umsetzung im Dashboard | Priorität |
|---|---|---|
| Seiten (`pages`), Abschnitte (`sections`), Einträge (`items`) | Boards → Abschnitte → Widgets | Muss |
| Eintrag: `title`, `description`, `url`, `icon`, `target` (`newtab`, `sametab`) | Widget `link`; `modal` und `workspace` entfallen (öffnen als neuer Tab) | Muss |
| Icons: `favicon`, `si-*` (Simple Icons), `hl-*` (Dashboard Icons), URL, lokale Datei | Server holt das Icon einmal, bereinigt SVGs und legt es unter `/data/icons` ab; Upload im Editor; dazu `sh-*` (selfh.st), `mdi-*` (Material Design), Font Awesome (`fas fa-*`) und Emoji; einfarbige Sätze werden im dunklen Modus invertiert | Muss |
| Statusprüfung (`statusCheck`, `statusCheckUrl`, `statusCheckAcceptCodes`, `statusCheckAllowInsecure`, `statusCheckInterval`) | Quelle `http_status` auf dem Server; Punkt auf der Kachel mit Antwortzeit im Tooltip; Status nie nur über Farbe | Muss |
| Suche/Filter durch Tippen, Tastenkürzel je Eintrag (`hotkey`) | Suchfeld (`/` fokussiert), filtert Kacheln live, `Enter` öffnet den ersten Treffer, Ziffern-Hotkeys | Muss |
| Websuche als Rückfall (`webSearch`, `searchEngine`) | Keine Treffer → Suche an konfigurierte Suchmaschine (je Benutzer einstellbar) | Muss |
| Abschnitt-Anzeige (`collapsed`, `cols`, `itemSize`, `sortBy`) | `collapsed`, `cols`, `size: small|medium|large`, `sort: manual|alphabetical`; eingeklappt/ausgeklappt im persönlichen Overlay gespeichert | Muss |
| Seitenkopf und Fuß (`pageInfo`: Titel, Beschreibung, Nav-Links, Footer) | Einstellungen des Bereichs/der Instanz, gerendert mit `.nav` und `.foot` | Muss |
| Widget `rss-feed` | Widget `rss`: Abruf und Bereinigung auf dem Server, Cache, Anzahl und Intervall einstellbar | Muss |
| Widget `clock` | Widget `clock`: rein im Browser, Zeitzonen, Datum | Muss |
| Widget `weather` / `weather-forecast` | Widget `weather` über Open-Meteo (kein API-Key); OpenWeatherMap optional | Muss |
| Themes, Theme-Wechsler, Custom CSS | Theme-System mit Editor (Abschnitt 6); mitgeliefert nur shrippen | Muss |
| Konfigurations-Editor in der UI | Konfigurationseditor (Abschnitt 5), je Bereich, mit Verlauf | Muss |
| Anmeldung, Gast-Sichtbarkeit (`hideForGuests`, `hideForUsers` …) | Eigene Anmeldung und Rechte je Widget (Abschnitt 4) | Muss |
| Widget `iframe` | Widget `iframe`; erlaubte Ziele pflegt ein Instanz-Admin, sie landen in der Content-Security-Policy (`frame-src`) | Soll |
| Widgets `gl-*` (Glances: CPU, RAM, Disk, Load) | Quelle `glances` + Widget `sysinfo` (passt zur IT-Landschaft) | Soll |
| Widget `public-ip` | Widget `public_ip` | Soll |
| Minimal-Ansicht (`/minimal`) | Board-Ansicht `?view=compact`: nur Suche und Kacheln | Soll |
| Als App installieren (PWA) | Web-App-Manifest und Icon | Soll |
| Cloud-Backup der Konfiguration | Export als YAML/ZIP, Datenbank-Backup des Volumes | Nein |
| Keycloak-Anbindung | Single Sign-on über authentik per OIDC (Abschnitt 4.7) | Muss |
| Übrige Dashy-Widgets (Krypto, GitHub-Trending, Sport …) | Nicht migriert, der Import-Assistent listet sie auf. Bei Bedarf als eigener Widget-Typ | Später |

### 7.2 Widget-Modell

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

Jedes Widget wird als eigenes HTMX-Fragment geladen und aktualisiert; jede Fragment-Anfrage prüft die Rechte erneut. Fällt eine Quelle aus, zeigt nur dieses Widget den Fehler und den letzten Stand mit Alter an.

**Link-Kachel mit Infozeile:** Die Verbindung zwischen Launcher und Auswertung. Die Kachel verlinkt auf den Dienst und zeigt zusätzlich einen Live-Wert und die Zahl offener Hinweise:

```
┌ Kimai ────────── ● online ┐  ┌ Invoice Ninja ─── ● online ┐  ┌ Snipe-IT ──────── ● online ┐
│ 3,5 h heute · Timer läuft │  │ 2 überfällig · 4.180 € off. │  │ 1 Garantie läuft ab        │
│                    ⚠ 1    │  │                      ⚠ 2    │  │                      ⓘ 1   │
└───────────────────────────┘  └─────────────────────────────┘  └────────────────────────────┘
```

### 7.3 Import-Assistent für `conf.yml`

Im Editor (oder per `dashboard import-dashy conf.yml --space <bereich>`) wird eine Dashy-Konfiguration in einen gewählten Bereich übernommen. Vor dem Speichern zeigt der Assistent eine Vorschau und einen Bericht.

| Dashy | Dashboard |
|---|---|
| `pageInfo` | Kopf-/Fußeinstellungen des Bereichs |
| `appConfig.statusCheck`, `statusCheckInterval` | Standardwerte für `link.status` |
| `appConfig.webSearch` | Suchmaschine des Bereichs |
| `appConfig.theme`, `customColors` | bekannte Themes und eigene Farben werden als Theme des Bereichs angelegt und aktiviert; unbekannte bleiben shrippen |
| `customCss`, `layout` | ignoriert (im Bericht vermerkt) |
| `appConfig.auth` (Benutzer, `hideForUsers`, `hideForGuests`) | nicht automatisch; der Bericht listet die Einschränkungen, damit sie als Rechte nachgezogen werden können |
| `sections[].items[]` | Widgets `link` in der Bibliothek + Platzierungen (inkl. Icon, Status, Hotkey, Target) |
| `sections[].widgets[]` (`rss-feed`, `clock`, `weather`, `iframe`, `gl-*`, `public-ip`) | entsprechende Widget-Typen |
| `sections[].displayData` | `collapsed`, `cols`, `size`, `sort` |
| `pages[]` (Unterseiten, eigene YAML-Dateien) | weitere Boards |
| alles andere | Bericht „nicht übernommen“ |

Getestet wird der Import mit einer anonymisierten Kopie der eigenen `conf.yml` als Fixture.

### 7.4 Umstieg

1. Dashboard läuft parallel zu Dashy (anderer Port oder Subdomain).
2. Admin-Konto einrichten, `conf.yml` in den eigenen oder einen Team-Bereich importieren, Bericht durchgehen, fehlende Icons oder Widgets nachziehen.
3. Einige Tage beide nutzen. Kriterium für den Wechsel: Alle täglich genutzten Links, Statusanzeigen und Feeds sind da, die Suche ist mindestens so schnell.
4. Weitere Benutzer einladen, Browser-Startseite umstellen, Dashy-Container stoppen, `conf.yml` archivieren.

### 7.5 Technische Leitplanken

- **Keine Aufrufe aus dem Browser** zu Diensten oder Feeds. Kein CORS-Proxy, keine Tokens im Frontend.
- **Fremde Inhalte bereinigen:** RSS-HTML mit `nh3` (erlaubte Tags: Absätze, Links, Hervorhebungen), Links mit `rel="noopener noreferrer"`. SVG-Icons ohne Skripte und externe Verweise, ausgeliefert als `<img>`.
- **Content-Security-Policy:** Skripte nur aus dem eigenen Container, `frame-src` nur für freigegebene iframe-Ziele.
- **Wenig JavaScript:** Suche, Hotkeys, Uhr und Einklappen in reinem JavaScript (wenige KB); der Editor als eigenes Modul mit SortableJS. Keine Build-Kette, alles andere per HTMX.
- **Zeitlimits:** Statusprüfungen und Feeds mit kurzen Timeouts und Backoff, damit langsame Ziele nichts blockieren.

---

## 8. Die Dienste: Daten und Hinweise

Alle Abrufe laufen read-only mit eigenen API-Tokens über die Verbindungen eines Bereichs (Abschnitt 4.4). Unterstützt wird jeweils die aktuelle Version eines Dienstes. Jede Quelle meldet die gefundene Version im Verbindungstest; ist sie älter als die getestete Mindestversion, erscheint ein Hinweis `system.version_outdated`. Die Fixtures der Tests werden bei jedem größeren Update der Dienste neu aufgenommen. Endpunkte beim Bau gegen die jeweilige Version prüfen (Kimai: `/api/doc`, Dawarich: `/api-docs`).

### 8.1 Kimai (Zeiterfassung)

**Daten:** `GET /api/timesheets` (Filter `begin`, `end`, `exported`), `/api/timesheets/active`, `/api/projects`, `/api/customers`, `/api/activities`. Ziel ist die **jeweils aktuelle Kimai-2-Version**; Authentifizierung nur per Bearer-API-Token (die alte Anmeldung über `X-AUTH-USER`/`X-AUTH-TOKEN` wird nicht unterstützt). Abwesenheiten und Feiertage kommen aus der API des **kimai-holiday-bundle** (`/api/holiday/absences?year=`, `/api/holiday/public-holidays?year=`). Offene Abrechnungsposten im Sinne des **kimai-abrechnung-bundle** (abrechenbar, nicht exportiert, beendet) liefert die Kimai-Kern-API direkt (`/api/timesheets?billable=1&exported=0&state=stopped`).

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

### 8.2 Invoice Ninja v5 (Rechnungen, Zahlungen, Ausgaben)

**Daten:** `GET /api/v1/invoices` (u. a. `client_status=unpaid|overdue`), `/payments`, `/clients`, `/quotes`, `/recurring_invoices`, `/expenses`. Header `X-API-TOKEN` und `X-Requested-With: XMLHttpRequest`. Ziel ist die **jeweils aktuelle Invoice-Ninja-v5-Version** (self-hosted).

**Kennzahlen:** Umsatz netto (Monat, YTD, Vorjahr; die Umsatzsteuer ist kein Umsatz), vereinnahmte Umsatzsteuer, Vorsteuer aus Ausgaben, voraussichtliche Zahllast, offene Posten und deren Alter, Zahlungsdauer je Kunde (Days Sales Outstanding), Umsatzanteil je Kunde, Ausgaben, grober Überschuss, effektiver Stundensatz (Umsatz / Kimai-Stunden).

| Regel | Bedingung (Standard) | Stufe |
|---|---|---|
| `in.invoice_overdue` | Rechnung überfällig; ab 14 Tagen Mahnvorschlag | warn → critical |
| `in.slow_payer` | Kunde zahlt im Mittel > 30 Tage | info |
| `in.draft_stale` | Rechnungsentwurf älter als 7 Tage | info |
| `in.quote_open` | Angebot versendet, nach 14 Tagen ohne Reaktion | info (Nachfassen) |
| `in.recurring_ending` | Wiederkehrende Rechnung endet in < 30 Tagen | info |
| `in.revenue_vs_goal` | Umsatz YTD unter anteiligem Jahresziel | info |
| `in.tax_reserve` | Empfohlene Rücklage: Umsatzsteuer-Zahllast des laufenden Zeitraums + x % des Netto-Überschusses für die Einkommensteuer, verglichen mit der erfassten Rücklage | info |
| `in.vat_liability` | Voraussichtliche USt-Zahllast des laufenden Voranmeldungszeitraums (Umsatzsteuer aus Zahlungseingängen bei Ist-Versteuerung bzw. aus Rechnungen bei Soll-Versteuerung, minus Vorsteuer aus Ausgaben) | info, vor der Frist warn |
| `in.missing_vat` | Rechnung an inländischen Kunden ohne Umsatzsteuer, oder an EU-Kunden ohne USt-IdNr. bei 0 % (Reverse Charge prüfen) | warn |
| `in.expense_no_input_vat` | Ausgabe ohne erfasste Vorsteuer, obwohl der Lieferant üblicherweise Umsatzsteuer ausweist | info |
| `in.client_concentration` | Ein Kunde > 50 % des Umsatzes (Klumpenrisiko), > 5/6 über 12 Monate (Hinweis auf Prüfung Rentenversicherungspflicht als arbeitnehmerähnlicher Selbständiger) | info / warn |

### 8.3 Snipe-IT (Assets, Lizenzen)

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

### 8.4 Dawarich (Standortverlauf)

**Daten:** `GET /api/v1/points` (`start_at`, `end_at`), `/api/v1/visits`, `/api/v1/areas`, `/api/v1/stats`. Authentifizierung per API-Key. **Sensible Daten:** Es werden nur Aggregate gespeichert (Aufenthalte in definierten Bereichen, Tages-km), keine Rohpunkte.

**Idee:** In Dawarich werden **Areas** für Kundenstandorte, Büro und Zuhause angelegt. Im Editor wird jede Area einem Kimai-Kunden zugeordnet.

**Kennzahlen:** Tage beim Kunden je Monat, Fahrtstrecke je Tag, Abwesenheitsdauer von zu Hause, besuchte Länder/Orte (Reisen).

| Regel | Bedingung (Standard) | Stufe |
|---|---|---|
| `geo.visit_without_time` | Aufenthalt in Kunden-Area ≥ 2 h, aber keine Kimai-Buchung für diesen Kunden an dem Tag | warn |
| `geo.time_without_visit` | Kimai-Buchung mit Aktivität „vor Ort“, aber kein Aufenthalt in der Kunden-Area | info (Plausibilität) |
| `geo.travel_costs` | Fahrten zu Kunden im Monat: km × Kilometersatz (Standard 0,30 €/km), nicht als Ausgabe/Rechnungsposten erfasst | info |
| `geo.per_diem` | Abwesenheit > 8 h / 24 h an Kundentagen → Verpflegungsmehraufwand (Sätze konfigurierbar) | info |
| `geo.no_data` | Seit > 24 h keine neuen Punkte (Tracking-App aus?) | warn |

### 8.5 Übergreifend: Fristen und Wochenrückblick

| Regel | Inhalt |
|---|---|
| `tax.vat_return` | Umsatzsteuer-Voranmeldung zum 10. (Monat/Quartal, Dauerfristverlängerung konfigurierbar), mit der voraussichtlichen Zahllast aus `in.vat_liability` |
| `tax.vat_annual` | Umsatzsteuer-Jahreserklärung |
| `tax.prepayment` | ESt-Vorauszahlungen 10.03., 10.06., 10.09., 10.12. mit Betrag aus Konfiguration |
| `tax.annual` | EÜR und Einkommensteuererklärung mit eigener Frist (freiberuflich: keine Gewerbesteuer) |
| `digest.weekly` | Montag: Stunden, Umsatz, offene Posten, neue Hinweise der Woche |
| `digest.monthly` | Monatsanfang: Vormonat abschließen (Export in Kimai → Rechnung in Invoice Ninja → Fahrtkosten aus Dawarich) |

Alle Beträge, Sätze und Fristen werden im Editor je Bereich gepflegt, typischerweise im persönlichen Bereich, und pro Jahr aktualisiert.

---

## 9. Design: shrippen Design Default

Das Dashboard ist eine **App** im Sinne des Design Systems und nutzt daher die App-Komponenten. Das Design System liefert zugleich das einzige mitgelieferte Theme (Abschnitt 6).

**Einbindung**

- `shrippen.css`, `shrippen.js` und die Schriften werden als **Kopie in dieses Repo** gelegt (`app/static/vendor/shrippen/`, Quelle und Stand in einer `VERSION`-Datei vermerkt). Die Token-Werte daraus bilden `themes/shrippen/`. Das Dashboard funktioniert so auch ohne Internet und wandert nicht ungeprüft mit dem CDN mit. Aktualisiert wird bewusst per Skript (`tools/sync-design.sh`).
- Schriften (Rajdhani 500/600/700, JetBrains Mono 400/500) werden **lokal** ausgeliefert, nicht von Google Fonts (Datenschutz, offline).
- Hell/dunkel über `<html data-theme="light">`; die Wahl wird im Benutzerprofil gespeichert, nicht nur im Browser.
- Sprachumschaltung DE/EN mit dem `.lang`-Umschalter des Design Systems. Anders als auf den Landing Pages werden die Seiten aber auf dem Server in der gewählten Sprache gerendert (nicht beide Sprachen im HTML); die Wahl steht im Profil.

**Vorhandene Komponenten wiederverwenden**

| Dashboard-Element | Design-System-Komponente |
|---|---|
| Hinweis-Karte | `.callout`, `.callout-warn`, `.callout-danger`, `.callout-ok` |
| Budget, Auslastung, Umsatzziel | `.progress` mit `data-tier="green|yellow|red"` |
| Status eines Connectors (ok, lädt, Fehler) | `.pill` mit `data-state` |
| Tabellen (offene Rechnungen, Assets) | `.table-wrap` + `.table` |
| Navigation, Fuß | `.nav`, `.foot` |
| Filter, Umschalter, Editor-Formulare | `.seg`, `.switch`, `.select`, `.field`, `.input`, `.range` |
| Snooze-, Freigabe- und Editor-Dialoge | `.dialog`, `.scrim` |
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
| `.editbar`, `.dropzone` | Leiste des Bearbeitungsmodus (Speichern, Verwerfen, Verlauf), Ablagefläche beim Ziehen |
| `.share` | Freigabe-Dialog: Benutzer/Team, Recht, Warnung bei geteilten Zugangsdaten |
| `.swatch`, `.contrast` | Farbfeld und Kontrastanzeige im Theme-Editor |
| `.login` | Anmeldeseite, Einrichtung, zweiter Faktor |
| Diagramm-Palette | Reihenfolge `--blue`, `--aqua`, `--yellow`, `--orange`, `--purple`, `--green`; Achsen `--fg3`, Gitter `--bg2` |

Die Komponenten nutzen ausschließlich Theme-Tokens (`var(--…)`), keine eigenen Hex-Werte (per Stylelint geprüft). Nur so funktionieren sie mit jedem Theme. Ob sie später ins Design System wandern, ist eine eigene Entscheidung außerhalb dieses Projekts.

**Layout-Skizze: Start (Dashy-Ersatz)**

```
┌─────────────────────────────────────────────────────────────────────┐
│ ◆ dashboard  Start  Übersicht  Freelance  IT  Reisen  ✎  ☼  ◉ alex  │
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
│ ◆ dashboard  Start  Übersicht  Freelance  IT  Reisen  ✎  ☼  ◉ alex  │
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

**Offen: UI auf shrippen-Tokens umstellen**

Fachlich ist Phase 0–8 fast vollständig umgesetzt; die Lücke liegt allein in
der Oberfläche. `internal/web/templates/base.html` rendert bisher mit
`system-ui` und Browser-Standardstilen statt mit den Tokens aus
`internal/services/themes/builtin/shrippen/tokens.css`; die in diesem
Abschnitt vorgesehenen Komponenten (`.launch`, `.kpi`, `.hint`, `.pill`,
`.progress` …) existieren nirgends im Code. Entwürfe für vier Bildschirme
(Start, Übersicht, Editor, Anmeldung): <https://claude.ai/artifact/K1SEJhy4Pm4wyn4zDJ9vLH>.

- [x] `dashboard.css` in `internal/web/static/` angelegt: nur Tokens (`var(--…)`), keine Hex-Werte außerhalb `themes/`
- [x] `base.html`: `system-ui`-Fallback durch `.app-nav`/`.app-links`/`.app-side` und echte Formularstile ersetzt; jede Seite lädt jetzt ihr aktives Theme (`Deps.Page` setzt `ThemeURL`, vorher nur die Board-Seite)
- [x] `.launch`: Link-Kachel mit Icon-Quadrat (Monogramm als Rückfall), Status-Punkt, Infozeile und Hinweis-Zähler
- [x] `.kpi` / `.kpi-row`: Kennzahl-Kacheln (`widgets_insight.html`, bereits vor diesem Abschnitt vorhanden, jetzt mit den echten Tokens statt Fallback-Werten)
- [x] `.hint`: Hinweis-Karte mit Stufe, Quelle, aufklappbarem „Warum?“, Aktion — bestehende Struktur, jetzt mit Tokens gestylt
- [x] `.pill` mit `data-state`: Connector-/Link-Status
- [x] `.progress` mit `data-tier`: Budget- und Auslastungsbalken
- [x] `.editbar`: Bearbeitungsmodus direkt auf dem Board (`?edit`), Kacheln per Drag & Drop (SortableJS); eigenes Layout über `?layout`
- [x] `.login`: eigene Anmeldeseite (`login.html`, `totp`, `setup`)
- [x] Kontrastprüfung dunkel/hell (WCAG AA) für die neuen Komponenten *(Test `TestComponentContrastAA`; Statustexte über abgeleitete Tokens `--ok-text`/`--warn-text`/`--danger-text`, auch für eigene Themes)*

---

## 10. Phasen

Jede Phase endet mit einem lauffähigen, getaggten Image. Anmeldung und Bereichsmodell kommen bewusst ganz an den Anfang: Mehrbenutzerfähigkeit nachträglich einzubauen hieße, jede Abfrage und jedes Widget noch einmal anzufassen.

### Phase 0: Fundament (v0.1)

- [x] Repo-Struktur, `pyproject.toml`, Ruff, Stylelint, Pytest, pre-commit *(Stylecheck-Skript statt Stylelint, kein pre-commit)*
- [x] FastAPI-Grundgerüst, Jinja-Layout mit `shrippen.css`, lokale Schriften *(seither Go: `net/http`, `html/template`; Schriften unter `static/vendor/shrippen/fonts`, CSP wie in Python)*
- [x] Übersetzung von Anfang an: alle Texte über gettext (DE/EN), Formatierung mit Babel, Sprache aus Profil bzw. `Accept-Language` beim ersten Besuch; CI prüft, dass keine Übersetzung fehlt *(YAML-Kataloge mit Schlüsseln statt gettext)*
- [x] Datenbank mit SQLAlchemy + Alembic (SQLite im WAL-Modus)
- [x] Datenmodell: Benutzer, Teams, Bereiche, Verbindungen, Widgets, Boards, Platzierungen, Freigaben, Revisionen
- [x] Anmeldung: Einrichtungscode, lokale Konten (Argon2id), Sitzungen, CSRF, Drosselung, Abmelden
- [x] Zentrale Berechtigungsprüfung in der Service-Schicht, Tests als Rechte-Matrix (Rolle × Recht × Ressource)
- [x] Verschlüsselung der Zugangsdaten (AES-GCM, Hauptschlüssel aus Docker Secret)
- [x] Quellen-Schnittstelle (`fetch()`, `healthcheck()`, Cache je Verbindung + Zugangsdaten), Widget-Schnittstelle (Schema + Vorlage + HTMX-Fragment), Scheduler
- [x] Hinweis-Engine: Fingerprint, Zustände je Benutzer/Team, Snooze/Ack
- [x] Dockerfile (multi-stage, non-root, `HEALTHCHECK`), `docker-compose.example.yml`
- [x] GitHub Actions: Tests, Image-Build `linux/amd64` + `linux/arm64`, Push nach GHCR
- [x] Demo-Modus mit Fixture-Daten und Demo-Benutzern (Entwicklung, Screenshots) *(Go: `DASHBOARD_DEMO=true`, `services/seed`, `sources/demo.go`)*

### Phase 1: Startseite, Editor und Dashy-Migration (v0.2)

- [x] Widget `link` mit Icons (`favicon`, `si-*`, `hl-*`, URL, Upload, Monogramm) und Icon-Cache *(Go: `services/icons`, `sources/icons.go`, `/icons/{key}`)*
- [x] Quelle `http_status` und Statuspunkt auf den Kacheln
- [x] Boards, einklappbare Abschnitte, `cols`, Kachelgrößen, Sortierung
- [x] Suche mit Filter, `Enter`, Hotkeys, Websuche als Rückfall
- [x] Widgets `rss`, `clock`, `weather` (Open-Meteo)
- [x] Widgets `iframe`, `sysinfo` (Glances), `public_ip`; kompakte Ansicht; PWA-Manifest
- [x] Konfigurationseditor v1: Board-Editor mit Drag & Drop, Widget-Formulare aus Schema mit Vorschau, Widget-Bibliothek, Verbindungen mit „testen“, Revisionen *(Go: Felder je Typ in `widgets/fields.go`, Vorschau per htmx)*
- [x] Import/Export YAML, Dashy-Import-Assistent mit Bericht, getestet an der eigenen `conf.yml` *(Go: `services/porting`, `/import`, `/spaces/{id}/code`, CLI `dashboard import`)*
- [x] Persönliche Einstellungen: Start-Board, hell/dunkel, Sprache, Suchmaschine
- [x] Neue Komponenten `.launch`, `.launch-grid`, `.section-fold`, `.search`, `.feed`, `.clock`, `.weather`, Editor-Komponenten
- [ ] Parallelbetrieb, dann Umstieg nach Checkliste (Abschnitt 7.4)

**Ergebnis:** Dashy ist abgeschaltet, das Dashboard ist die Browser-Startseite, alles wird in der Oberfläche gepflegt.

### Phase 2: Mehrbenutzer und Teams (v0.3)

- [x] E-Mail-Versand (SMTP) mit Vorlagen im Design System; Einladungen, Selbstregistrierung (abschaltbar), Passwort-Reset per E-Mail, Sicherheitsmeldungen
- [x] Single Sign-on mit authentik (Abschnitt 4.7): Kontoverknüpfung, automatisches Anlegen mit Startwerten aus authentik-Gruppen (danach manuell pflegbar), Modus „nur authentik“ mit Notzugang *(Go: `services/oidc`, `sources/oidc.go`, PKCE + ID-Token-Prüfung mit go-jose; Einstellungen unter `/admin/settings`)*
- [x] TOTP mit Wiederherstellungscodes, für Admins erzwingbar; Sitzungsliste *(Go: `/me/security`)*
- [x] Teams mit Rollen Owner/Editor/Viewer, Team-Bereiche *(Go: `/teams`)*
- [x] Freigaben `view`/`use`/`edit`/`manage` an Widgets, Boards und Verbindungen; Dialog „Wer hat Zugriff?“ *(Go: `/shares/{kind}/{id}`, bisher nur von der Verbindungsliste verlinkt)*
- [x] Team-Widgets auf persönlichen Boards, persönliche Overlays an Team-Boards, Vorlagen *(Vorlagen über YAML-Export/-Import, keine Vorlagengalerie)*
- [x] Verbindungen mit persönlichen Zugangsdaten
- [x] Persönliche API- und Embed-Tokens *(Go: `/me/security`; `/api/summary`, `/api/hints`, `/embed/hints`, `/embed/b/{id}`)*
- [x] Audit-Log; Admin-Ansicht für Benutzer und Teams (ohne Einblick in persönliche Bereiche) *(Go: `/admin/users`, `/admin/audit`, `/admin/settings`)*

### Phase 3: Themes (v0.4)

- [x] Theme-Vertrag (Token-Liste, Version, Standardwerte) und Laden der Themes je Bereich
- [x] shrippen als einziges mitgeliefertes, schreibgeschütztes Theme (dunkel + Leinen)
- [x] Theme-Editor mit Live-Vorschau, dunkel/hell nebeneinander, Kontrastprüfung WCAG AA *(Go: `/themes/{id}`)*
- [x] Import/Export als ZIP; eigenes CSS und Schriften nur für Instanz-Admins *(Go: Schrift-Upload je Theme, Dateiname = Familie-Gewicht; Schriften reisen im ZIP mit)*
- [x] Auswahlreihenfolge persönlich → Team → Instanz, optional erzwungenes Theme je Board
- [x] Seite `/styleguide` mit allen Komponenten im aktuellen Theme

### Phase 4: Freelance-Kern: Kimai + Invoice Ninja (v0.5, MVP der Auswertung)

- [x] Kimai-Quelle und Kennzahlen, Regeln aus 8.1
- [x] Invoice-Ninja-Quelle und Kennzahlen, Regeln aus 8.2
- [x] Abgleich Kimai ↔ Invoice Ninja: nicht abgerechnete Stunden je Kunde, effektiver Stundensatz (KPI `effective_rate`, Tabelle `effective_rates`)
- [x] Boards „Übersicht“ und „Freelance“ als Vorlagen mit `.kpi`, `.hint`, `.progress`, Tabelle offener Posten
- [x] Regel-Einstellungen im Editor; Deep-Links von jedem Hinweis in die Fach-UI
- [x] Infozeilen und Hinweis-Zähler auf den Link-Kacheln von Kimai und Invoice Ninja

**Ergebnis:** Das Dashboard ersetzt den täglichen Blick in beide Tools.

### Phase 5: IT-Landschaft: Snipe-IT (v0.6)

- [x] Snipe-IT-Quelle, Regeln aus 8.3
- [x] Board-Vorlage „IT“: Assets nach Status, Garantie-/Lizenz-Zeitleiste, Audits
- [x] Abgleich Snipe-IT ↔ Invoice-Ninja-Ausgaben (`snipe.expense_missing`)

### Phase 6: Standort: Dawarich (v0.7)

- [x] Dawarich-Quelle, nur Aggregate speichern, standardmäßig nur persönliche Verbindung
- [x] Zuordnung Area → Kimai-Kunde im Editor
- [x] Regeln aus 8.4: Besuch ohne Buchung, Fahrtkosten, Verpflegungspauschalen
- [x] Board-Vorlage „Reisen“: Kundentage, km je Monat, Vorschlag für Fahrtkosten-Position

### Phase 7: Erinnerungen und Benachrichtigungen (v0.8)

- [x] Fristen-Kalender (Abschnitt 8.5), Board „Fristen“, iCal-Feed je Benutzer (mit Token) *(Go: `/calendar.ics?token=…`, `services/calendar`)*
- [x] Benachrichtigungen über Apprise: jeder Benutzer hinterlegt eigene Apprise-URLs (verschlüsselt gespeichert, mit Testknopf) und wählt Mindeststufe und Ruhezeiten; E-Mail-Benachrichtigungen nutzen den vorhandenen SMTP-Server *(Go ruft eine externe Apprise-API statt sie einzubinden)*
- [x] Texte der Benachrichtigungen in der Sprache des Empfängers
- [x] Digest als HTML-E-Mail im Design System (mit Textversion), in der Sprache des Empfängers
- [x] Morgen-Digest und Wochenrückblick; Ruhezeiten; keine Doppelmeldungen (Fingerprint)
- [x] Monatsabschluss-Checkliste (Kimai-Export → Rechnung → Fahrtkosten)

### Phase 8: Trends und Prognosen (v0.9)

- [x] Verlaufsdiagramme aus Snapshots (Umsatz, Stunden, offene Posten)
- [x] Hochrechnung Jahresumsatz, Umsatzsteuer-Zahllast und Steuerrücklage
- [x] Vergleich Vorjahr, saisonale Muster, Liquiditätsvorschau (offene Posten + wiederkehrende Rechnungen − feste Ausgaben) (Diagramm `seasonal`, KPI `liquidity_30`, Fixkosten in den Bereichseinstellungen)

### Phase 9: Ausbau (v1.0)

- [x] Passkeys (WebAuthn) für lokale Konten (Sicherheit → Passkeys, Anmeldung ohne Passwort; ersetzt TOTP)
- [x] CodeMirror in der Code-Ansicht (CodeMirror 5 vendored, YAML/CSS, Theme-Tokens)
- [x] Weitere Dashy-Widgets nach Bedarf (Liste aus dem Import-Bericht) *(Bild, Wechselkurse, Hacker News als RSS, Uptime-Kuma-Monitore; uptime-kuma/proxmox-lists mit Hinweis auf Verbindung)*
- [x] Weitere Quellen über dieselbe Schnittstelle: z. B. Uptime Kuma (Dienste down), Proxmox/Docker (Updates, Speicher), Backup-Status, Paperless-ngx (unbearbeitete Belege), Zertifikatsablauf *(Uptime Kuma, Proxmox VE mit Updates/Speicher/vzdump-Backups, Paperless-ngx, TLS-Zertifikate; Docker nicht umgesetzt)*
- [x] Optionale Wochenzusammenfassung in Fließtext per LLM (abschaltbar je Benutzer, nur Aggregate, keine Standortdaten) *(Claude über `ANTHROPIC_API_KEY`/Secret `anthropic_api_key`; nur Hinweis-Anzahlen je Regel, `geo.*` ausgenommen)*

### Phase 10: Homelab-Dienste

Jede Quelle liefert einen gecachten Datensatz (`<dienst>.data`), Regeln, eine Infozeile auf der Link-Kachel und Demodaten. Der Quell-Cache hält Ergebnisse jetzt für die TTL der Quelle im Speicher (vorher jeder Aufruf live).

- [x] Prüflauf als Hintergrund-Job: holt alle Integrationen (beim Start und alle `ANALYSIS_MINUTES`, Standard 5) und leitet Hinweise ab; Seiten lesen nur diesen Stand, live ist nur der Status-Ping der Link-Kacheln. Fehlt ein Stand (neue Verbindung), wird er einmal im Hintergrund geholt. Admin → Instanz zeigt den letzten Lauf und startet ihn auf Wunsch sofort
- [x] Datenmodus je Widget: automatisch (Vorgabe des Typs) / live beim Anzeigen / aus dem Prüflauf. Live als Vorgabe bei Home Assistant, Uptime-Kuma-Monitoren und Systemwerten; Daten anderer Verbindungen im selben Widget (z. B. Kimai beim Stundensatz) bleiben beim Prüflauf
- [x] Leichte Live-Quelle für Kimai (laufender Timer, Stunden heute), damit „live“ dort nicht den ganzen Datensatz lädt *(`kimai.live`)*

- [x] FreshRSS (Google-Reader-API): Leserückstand mit den größten Quellen, verstummte Feeds
- [x] Gitea: wartende Reviews, fällige Issues, ruhende PRs, fehlgeschlagene Actions, veraltete Spiegel
- [x] E-Mail (IMAP, nur lesend): Eingangsrechnungen erkennen (Betreff/Anhangsname, Betrag aus dem Text) und mit Invoice-Ninja-Ausgaben abgleichen (Betrag ± 1 ct im Datumsfenster, sonst Lieferantenname ~ Absender)
- [x] Sure: Kontostand/Vermögen, ausgebliebene wiederkehrende Zahlungen, niedriger Kontostand, Bank-Sync, ungewöhnliche Ausgaben, ohne Kategorie; mit Invoice Ninja: Zahlungseingang zu offener Rechnung, Geschäftsausgabe nicht erfasst; Liquidität nutzt Sures Fixkosten
- [x] Paperless-ngx: zusätzlich Rechnungsdokumente gegen Invoice-Ninja-Ausgaben
- [x] Immich: Speicher, fehlgeschlagene Jobs, Updates
- [x] Linkwarden: Lesezeichen einer Sammlung ohne Kachel, Kacheln ohne Lesezeichen
- [x] Home Assistant: Alarmsensoren, Batterien, nicht erreichbare Entitäten (ein Hinweis), Updates; Widget mit Schaltern (nur gelistete Entitäten, nur mit Recht „nutzen“ an der Verbindung)
- [x] Uptime Kuma: zusätzlich Kacheln ohne Monitor
- [x] Umami: Besuchereinbruch gegenüber Vorwoche, keine Aufrufe mehr
- [x] Scrutiny: SMART-Fehler, Temperatur, schweigender Collector
- [x] Borg Backup Server: Clients offline/Fehler, fehlgeschlagene Jobs, Alter des letzten Backups, Speicher, Updates
- [x] PG Back Web (keine Lese-API): signierte Webhook-URL je Verbindung; fehlgeschlagene/veraltete Backups, nicht erreichbare Datenbanken/Ziele, ausbleibende Webhooks
- [ ] Obsidian – zurückgestellt, bis ein konkreter Nutzen feststeht (Wege zum Lesen des Vaults unten)
- [ ] Docker

### Phase 11: Dashy-Abgleich und weitere Integrationen

Auswahl vom 25.09.2026 (Checkliste „Dashy-Abgleich“). Abgelehnt: Overlay, Arbeitsbereich- und Minimal-Ansicht, Unterseiten per URL, Bangs, Suchmaschinen-Liste, URL direkt öffnen, Ausblenden-aber-findbar, Suchziel, Prüfintervall je Link, Öffnen-Ziele beim Import melden. Offen gelassen: Schreib-API, Cloud-Sicherung, weitere Sprachen, erzeugte Icons, Healthchecks, ntfy, Synology, Linkding, Drone CI, CVE-Feed, Sport.

- [x] Import/Startseite: Tags an Links (Suche), Seitentitel/Beschreibung/Navigationslinks/Fußzeile anzeigen, mehrere Links in einer Kachel (Sub-Items), Pfeiltasten durch die Treffer *(Seitentexte in den Bereichseinstellungen, `spaces/page.go`)*
- [x] Rechtsklick-Menü je Kachel (neuer Tab, selber Tab, Adresse kopieren; Umschalt + Rechtsklick öffnet das Browser-Menü)
- [x] Layout: Abschnitte über mehrere Zeilen, eigene Farbe je Abschnitt und Link (aus Theme-Farben) *(Abschnitte zusätzlich in Vierteln der Breite; Farbe nur als Linie, Text bleibt kontrastgeprüft)*
- [x] Status-Checks mit eigenen HTTP-Headern *(verschlüsselt in der Widget-Konfiguration, nie im Formular oder Export)*
- [x] Icons: Material Design Icons, selfh.st, Emoji, Font Awesome *(Emoji als Text, ohne Download; Shortcodes wie `:rocket:` nicht)*
- [x] Themes: Auswahl der Dashy-Themes nachbauen; Dashy-Farben beim Import als Theme übernehmen *(11 Vorlagen in `themes/presets.go`: Callisto, Nord, Dracula, One Dark, Material hell/dunkel, High Contrast hell/dunkel, Oblivion, Cyberpunk, Vaporware; Dashy-eigene Paletten angenähert; beide Modi mit derselben Palette)*
- [x] Widgets ohne eigenen Dienst: Kalender (iCal), Custom API, eigene Liste, Feiertage, xkcd, NASA-Bild des Tages, Witze, Krypto, Aktien, Flüge, Nahverkehr *(Quellen in `sources/fun.go`, `media.go`, `ical.go`, `travel.go`; API-Schlüssel, Header und private Kalender-Adressen verschlüsselt in der Widget-Konfiguration, nie im Formular oder Export; Dashy-Widgets werden beim Import zugeordnet)*
- [x] Widgets/Quellen fürs Homelab: Pi-hole/AdGuard, Nextcloud, Sabnzbd, Gluetun/Mullvad, Glances im Detail, Domain-Ablauf, Blacklist-Check *(`sources/netops.go`, `rules/netops.go`; Gluetun erkennt Lecks am Vergleich der Ausgangs-IP mit der eigenen und prüft das erwartete Land – eine Mullvad-eigene Abfrage entfällt, weil das Dashboard selbst nicht im Tunnel läuft; Widget `glances_chart` für den Verlauf; Domain-Ablauf über RDAP, .de ohne Datum)*
- [x] Integrationen: TrueNAS, Komodo, Pangolin, authentik (Nutzungsstatistik) *(Go: `sources/infra.go`, `rules/infra.go`; TrueNAS über JSON-RPC per WebSocket mit REST-Fallback; Tabelle `app_usage` für Anmeldungen je Anwendung)*

### Phase 12: Ausbau

Auswahl vom 25.09.2026 (Checkliste „Dashboard-Ausbau“). Nicht gewählt: Hell/dunkel nach Uhrzeit, öffentliche Statusseite, Proxmox Backup Server, Frigate, Fragen an die eigenen Daten, Prometheus-Metriken.

**Hinweise und Analyse**
- [x] Update-Zentrale: alle verfügbaren Updates in einem Widget *(Widget `updates`: Hinweise der Update-Regeln, `rules/topics.go`)*
- [x] Backup-Übersicht: Dienst × letzte Sicherung (Borg, PG Back Web, TrueNAS-Snapshots), Dienste ohne Backup melden *(Widget `backups`; Regel `backups.gap` vergleicht Namen von Komodo-Stacks und TrueNAS-Apps mit den Backup-Einträgen)*
- [x] Ausfälle bündeln: ein Hinweis mit Ursache statt vieler Folgehinweise *(≥ 2 Fehler auf einem Host – Verbindungen und Kuma-Monitore – werden zu `system.outage`; der Analyse-Lauf ruft erst alles ab und wertet dann aus)*
- [x] Dienste ohne Kachel finden (Komodo, Pangolin, Kuma)
- [x] Zertifikate automatisch aus Link-Kacheln und Pangolin-Ressourcen prüfen *(Option `auto: true` der Zertifikats-Verbindung)*
- [x] Verlauf je Hinweis, flatternde Hinweise dämpfen *(3× wieder aufgetreten in 7 Tagen = flattert, keine Wiederholungs-Pushes)*
- [x] Eigene Regeln ohne Code (Schwellwert auf Kennzahl oder API-Feld) *(Bereichseinstellungen; neue Verbindung „Eigene API (JSON)“ für beliebige JSON-Endpunkte)*
- [x] Notiz beim Quittieren
- [x] Hinweis zuweisen (offen/in Arbeit/erledigt)

**Selbstständigkeit**
- [ ] Rechnungsentwurf aus unabgerechneten Kimai-Stunden in Invoice Ninja (Vorschau, Bestätigung)
- [x] Kimai-Timer starten/stoppen (mit der leichten Kimai-Quelle aus Phase 10) *(Widget `kimai_timer`; Schreibzugriffe nur über `outbound`, mit Nutzungsrecht auf die Verbindung)*
- [ ] Liquiditätsverlauf 90 Tage
- [ ] Zahlungsmoral je Kunde
- [ ] Verträge und Kündigungsfristen aus Paperless
- [ ] Rechnungsmail an Paperless übergeben
- [ ] Jahrespaket für die Steuer (CSV/ZIP)
- [ ] Stunden-Heatmap

**Startseite und Bedienung**
- [ ] Befehlspalette (Strg+K)
- [ ] Link per URL hinzufügen (Titel und Icon automatisch)
- [ ] Häufig genutzte Links
- [ ] Tote Links melden
- [ ] Rückgängig im Editor
- [ ] Mehrere Kacheln gleichzeitig bearbeiten
- [ ] Board duplizieren und Vorlagen
- [ ] Tastenkürzel-Übersicht („?“)
- [ ] Verfügbarkeit auf der Kachel (30 Tage, Antwortzeit)

**Anzeigen und Geräte**
- [ ] Wandanzeige (Vollbild, Boards wechseln, nachts gedimmt)
- [ ] Offline-Ansicht (letzter Stand)
- [ ] Eigenes Handy-Layout je Board

**Verbindungen**
- [ ] Verbindungs-Assistent
- [ ] Zustand je Verbindung (letzter Erfolg, Fehlerquote, Antwortzeit)
- [ ] Token-Hygiene (Alter, Ablauf)
- [ ] Eigene Integration per YAML
- [ ] Abruf-Budget für begrenzte APIs

**Integrationen**
- [ ] Tailscale / Headscale
- [ ] OPNsense / pfSense / UniFi
- [ ] Jellyfin / Plex
- [ ] Sonarr / Radarr
- [ ] Vaultwarden
- [ ] Speedtest Tracker
- [ ] Grocy
- [ ] DWD-Unwetterwarnungen
- [ ] GitHub
- [ ] Energie und Kosten (Home Assistant, Tibber)

**KI und Betrieb**
- [ ] „Was tun?“ je Hinweis
- [ ] Rechnungen in Mails per KI lesen
- [ ] Automatische Sicherung des Dashboards mit Test-Wiederherstellung
- [x] Hinweise abonnieren (je Person nach Quelle und Stufe) *(Quellen je Benachrichtigungskanal)*
- [x] Ruhezeiten für Benachrichtigungen *(Ruhezeit gab es; neu: Kritisches kommt trotzdem, wahlweise stumm; Wiederholung offener kritischer Hinweise)*

**Obsidian – Vorschlag.** Obsidian hat keinen Server; der Vault ist ein Ordner mit Markdown. Drei Wege, ihn zu lesen:

| Weg | Voraussetzung | Bewertung |
|---|---|---|
| Vault per Obsidian-Git-Plugin in ein Gitea-Repo | Plugin, privates Repo | **Empfohlen.** Gitea-Verbindung existiert schon; Lesen über die Contents-API, versioniert, kein offener Port am Rechner. |
| Vault per Syncthing auf den Server, schreibgeschützt ins Dashboard gemountet | Syncthing | Einfach, wenn Syncthing schon läuft; kein API-Token nötig. |
| Plugin „Local REST API“ | Obsidian-Desktop läuft | Nur solange der Rechner an ist – für Hinweise ungeeignet. |

Mögliche Auswertungen: offene Aufgaben `- [ ]` mit Fälligkeit (Tasks-Plugin `📅 2026-10-01`) als Hinweise und in den Fristen; Notizen mit `wiedervorlage:` im Frontmatter; fehlende Tagesnotiz; wachsender Eingangsordner; Kundennotizen, deren letzte Änderung lange zurückliegt, während in Kimai für diesen Kunden gebucht wird.

---

## 11. Betrieb und Sicherheit

- **Anmeldung:** Eigene Anmeldung (Abschnitt 4.6). Der Reverse Proxy terminiert nur TLS; das Dashboard setzt `Secure`-Cookies und erwartet HTTPS (`BASE_URL`).
- **Zugangsdaten der Dienste:** Werden im Editor eingegeben und mit AES-GCM verschlüsselt in der Datenbank gespeichert. Der Hauptschlüssel kommt aus einem Docker Secret; ohne ihn sind Datenbank-Backups für Tokens wertlos. Schlüsselwechsel per Befehl `dashboard rotate-key`. Empfohlen: pro Dienst ein eigener Benutzer mit Leserechten.
- **Trennung:** Jede Abfrage ist auf erlaubte Bereiche beschränkt; Tests prüfen für jede Route, dass fremde Bereiche nicht erreichbar sind. Instanz-Admins verwalten Konten, sehen aber keine persönlichen Inhalte.
- **Netz:** Ausgehende Verbindungen nur zu den konfigurierten Diensten, Feeds, Statuszielen und Icon-Quellen. Keine externen CDNs zur Laufzeit; Icons werden einmal geholt und lokal zwischengespeichert. Weil Benutzer selbst URLs für Statusprüfungen, Feeds und Verbindungen eintragen, kann ein Instanz-Admin festlegen, welche Netze und Hosts erreichbar sein dürfen (Positivliste). Das verhindert, dass eingeladene Benutzer das Dashboard als Scanner für das interne Netz missbrauchen.
- **Daten:** Datenbank und Icons unter `/data`, Aufbewahrung konfigurierbar (z. B. Snapshots 24 Monate, Dawarich-Aggregate 12 Monate, Audit-Log 12 Monate). Löschen eines Benutzers löscht seinen persönlichen Bereich vollständig.
- **Backup:** `dashboard backup` erzeugt ein konsistentes Abbild (SQLite-Backup-API) plus Icons und Themes.
- **Robustheit:** Ein ausgefallener Dienst lässt das Dashboard nicht ausfallen. Das Widget zeigt den letzten Stand mit Alter und `.pill[data-state="failed"]`, dazu ein Hinweis `system.connector_down` im Bereich der Verbindung.
- **Beobachtbarkeit:** `/healthz`, strukturierte Logs, optional `/metrics` (Prometheus, nur mit Token).

**Beispiel `docker-compose.yml`**

```yaml
services:
  dashboard:
    image: git.arianw.de/shrippen/dashboard:latest
    restart: unless-stopped
    volumes:
      - ./data:/data
      # optional: - ./seed.yml:/app/seed.yml:ro
    environment:
      TZ: Europe/Berlin
      BASE_URL: https://dashboard.example.lan
      SMTP_URL: smtp://dashboard@mail.example.lan:587?starttls=true
      SMTP_FROM: "dashboard <dashboard@example.lan>"
      # optional beim ersten Start, sonst in den Admin-Einstellungen:
      OIDC_ISSUER: https://auth.example.lan/application/o/dashboard/
      OIDC_CLIENT_ID: dashboard
    secrets: [master_key, smtp_password, oidc_client_secret]
    ports: ["8080:8080"]

secrets:
  master_key:         { file: ./secrets/master_key }   # z. B. `openssl rand -base64 32`
  smtp_password:      { file: ./secrets/smtp_password }
  oidc_client_secret: { file: ./secrets/oidc_client_secret }
```

**Beispiel YAML-Export eines Bereichs (Ausschnitt)**

Dasselbe Format dient für Export, Import, Code-Ansicht und `seed.yml`. Zugangsdaten werden nie exportiert.

```yaml
space: personal:alex
settings:
  locale: de             # de | en; Standard für neue Benutzer im Bereich
  search: { engine: "https://searx.example.lan/search?q={query}" }
  goals: { revenue_year: 90000, billable_ratio: 0.7, hours_week_max: 45 }
  tax:
    vat: { method: ist, return_interval: monthly, extension: true }   # ist | soll; monthly | quarterly
    prepayments: { amount: 1200 }

connections:
  - id: kimai
    type: kimai
    url: https://kimai.example.lan
    credentials: personal          # jeder Benutzer hinterlegt sein eigenes Token
  - id: dawarich
    type: dawarich
    url: https://dawarich.example.lan
    credentials: shared            # Token wird im Editor eingegeben, nicht hier
    areas:
      "Muster GmbH Büro": { kimai_customer: 12 }
      "Home": { home: true }
    km_rate: 0.30
    per_diem: { over_8h: 14, full_day: 28 }

widgets:
  - id: kimai-link
    type: link
    title: Kimai
    url: https://kimai.example.lan
    icon: hl-kimai
    status: http
    info: { connection: kimai, metric: today }
    hotkey: 1
  - id: heise
    type: rss
    url: https://www.heise.de/rss/heise-atom.xml
    limit: 8
    refresh: 30m
  - id: open-invoices
    type: table
    connection: invoiceninja
    query: open_invoices

rules:
  kimai.unbilled_hours: { warn_days: 30, critical_days: 60 }
  in.invoice_overdue:   { dunning_after_days: 14 }

boards:
  - name: Start
    theme: null                    # persönliche Wahl bzw. Standard
    sections:
      - title: Freelance
        cols: 3
        widgets: [kimai-link, team:it/snipeit-link]   # Verweis auf ein Team-Widget
      - title: News
        widgets: [heise]

shares:
  - { resource: widget:open-invoices, to: team:buero, right: view }
```

---

## 12. Vorgeschlagene Repo-Struktur

```
dashboard/
├── ROADMAP.md
├── README.md
├── Dockerfile
├── docker-compose.example.yml
├── seed.example.yml
├── pyproject.toml
├── alembic/                 ← Datenbank-Migrationen
├── app/
│   ├── main.py              ← FastAPI, Routen, Scheduler-Start
│   ├── settings.py          ← Betriebswerte aus Env/Secrets
│   ├── db/                  ← SQLAlchemy-Modelle, Sitzung, Revisionen
│   ├── auth/                ← Konten, Passwörter, Sitzungen, CSRF, TOTP, Tokens, Einrichtungscode, oidc.py (authentik)
│   ├── mail/                ← SMTP-Versand, Vorlagen (Einladung, Reset, Sicherheit, Digest)
│   ├── access/              ← Bereiche, Rollen, Freigaben, zentrale Prüfung (`can(user, right, resource)`)
│   ├── crypto.py            ← Verschlüsselung der Zugangsdaten
│   ├── sources/             ← base.py, kimai.py, invoiceninja.py, snipeit.py, dawarich.py,
│   │                          rss.py, http_status.py, open_meteo.py, glances.py, public_ip.py
│   ├── widgets/             ← base.py, link.py, rss.py, clock.py, weather.py, iframe.py,
│   │                          sysinfo.py, kpi.py, hints.py, table.py …
│   ├── editor/              ← Board-Editor, Formulare aus Schema, YAML-Import/-Export, Dashy-Import
│   ├── themes/              ← Theme-Vertrag, Laden, Prüfen, Import/Export
│   ├── icons.py             ← Icon-Auflösung (favicon, si-, hl-, URL, Upload), SVG-Bereinigung, Cache
│   ├── metrics/             ← Kennzahlen je Bereich
│   ├── rules/               ← Regeln je Dienst + cross.py (dienstübergreifend) + deadlines.py
│   ├── notify/              ← Apprise, Digest-Vorlagen
│   ├── i18n/                ← de/ und en/ (.po/.mo), Babel-Konfiguration
│   ├── cli.py               ← import-dashy, backup, rotate-key, create-admin
│   ├── templates/           ← Jinja-Seiten und Partials (HTMX)
│   └── static/
│       ├── vendor/shrippen/ ← Kopie von shrippen.css / shrippen.js / Schriften + VERSION
│       ├── vendor/sortable/ ← SortableJS (vorgebaut)
│       ├── dashboard.js     ← Suche, Hotkeys, Uhr, Einklappen
│       ├── editor.js        ← Drag & Drop, Vorschau (nur im Bearbeitungsmodus geladen)
│       └── dashboard.css    ← nur neue Komponenten, ausschließlich mit Tokens
├── themes/
│   └── shrippen/            ← einziges mitgeliefertes Theme (theme.json, tokens.css)
├── tools/
│   └── sync-design.sh       ← holt eine bestimmte Version des Design Systems nach vendor/ und themes/shrippen/
└── tests/
    ├── fixtures/            ← anonymisierte API-Antworten je Dienst, Dashy-conf.yml, RSS-Beispiele
    ├── access/              ← Rechte-Matrix, Bereichstrennung je Route
    ├── auth/                ← Anmeldung, Sitzungen, CSRF, Drosselung, OIDC gegen einen Test-Provider
    └── rules/               ← ein Test pro Regel
```

---

## 13. Entscheidungen

| Thema | Entscheidung |
|---|---|
| Benutzerzahl | 1–10 Benutzer → SQLite im WAL-Modus, kein PostgreSQL |
| E-Mail | Vorhandener SMTP-Server für Einladungen, Passwort-Reset, Sicherheitsmeldungen und Digest |
| Single Sign-on | authentik per OIDC als zusätzlicher Anmeldeweg in Phase 2, lokale Anmeldung als Notzugang |
| Kimai, Invoice Ninja | Unterstützt wird jeweils die aktuelle Version (Kimai 2, Invoice Ninja v5 self-hosted) |
| Kimai-Bundles | Holiday-Bundle über `/api/holiday/*`; Abrechnungsstatus über die Kimai-Kern-API |
| Benachrichtigungen | Alle Kanäle über Apprise, je Benutzer konfigurierbar |
| Steuerstatus | Freiberuflich, umsatzsteuerpflichtig (keine Kleinunternehmerregelung, keine Gewerbesteuer). Die Kleinunternehmer-Regel entfällt; dazu kommen Regeln zu USt-Zahllast, Vorsteuer und fehlender Umsatzsteuer |
| Sprache | Deutsch und Englisch, je Benutzer wählbar; Hinweise und E-Mails in der Sprache des Empfängers |
| authentik-Gruppen | Bestimmen beim automatischen Anlegen eines Kontos die Start-Rolle und Start-Teams; danach werden Rollen und Teams nur im Dashboard gepflegt, kein Abgleich bei späteren Anmeldungen |

## 14. Offene Fragen

1. **Dashy:** Welche Widgets nutzt die aktuelle `conf.yml` wirklich? Eine anonymisierte Kopie dient als Testfall für den Import und korrigiert die Prioritäten in 7.1.
2. **Umsatzsteuer im Detail:** Ist-Versteuerung (bei Freiberuflern üblich) oder Soll-Versteuerung? Voranmeldung monatlich oder quartalsweise, mit Dauerfristverlängerung? Das sind nur Standardwerte, sie sind im Editor änderbar.
3. **Dawarich:** Sind Kundenstandorte schon als Areas angelegt, und ist der Abgleich mit Kimai gewünscht?
