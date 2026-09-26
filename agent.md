- When writing something intended for human consumption, (comment, commit message, reply to prompt) use as few words as possible. Pick every word meticulously to reduce the volume to a strict minimum. Be down to the point. Less is more.

- Avoid superlatives and praise. Stop telling me I am absolutely right. Give me the cold hard truth.

- Avoid magic numbers and strings by extracting recurring or meaningful values into descriptive constants (const) or enums. Keep self-explanatory, one-off values inline to avoid clutter. If a value comes from a spec (e.g. HTTP 200 OK), use a constant regardless.

- Reduce code indentation. Avoid Arrow Anti-Pattern. Leverage early return and continue.

- Keep function names short. Less than 30 characters.

- Use enums instead of booleans for function parameters.

- Let the reader of the code breathe. Add empty lines between logical blocks of code.

- Add a small, to the point, comment to explain *what* the block does and *why*. Use examples when possible. Propose ASCII drawings to explain complete systems.

- Treat member visibility changes as a breaking design shift. Keep all fields and functions private unless external access is strictly required by the design. Prompt the user for explicit approval before changing any access modifier from private to internal or public.

- Program to levels of abstraction. Lower-level mechanics (e.g., raw hardware I/O, sector parsing, direct socket streams) must be encapsulated in a dedicated driver/abstraction layer. Expose clean, high-level APIs to the rest of the application so calling code works with domain concepts, not raw implementation details.

- Don't touch blocks of code unrelated to the feature you implement. e.g. Don't add comments to a block of code if you did not create it or modify it. As much as possible try to minimize the number of changed lines when implementing a feature.

- Strictly adhere to the layered boundary hierarchy: each layer may only communicate with its immediate neighbor directly below it. Never "punch holes" through layers (e.g., controllers or UI components must never directly call database queries, raw hardware drivers, or low-level network clients; always route through the intermediate service/abstraction layer).

- Always use {}, even on a one-line "if" statement.

When you write a commit message, follow these 7 rules:
Rule 1: Separate the subject line from the body with a single blank line.
Rule 2: Limit the subject line to 50 characters (72 is the absolute hard limit).
Rule 3: Capitalize the first letter of the subject line.
Rule 4: Do not end the subject line with a period.
Rule 5: Use the imperative mood in the subject line (e.g., "Fix bug," "Add feature," 
        not "Fixed" or "Adds"). Test formula: It must complete the sentence: "If applied,
        this commit will [your subject line here]".
Rule 6: Wrap the body text manually at 72 characters to prevent Git formatting issues.
Rule 7: Use the body to explain what and why vs. how. Assume the code explains the how;
        the message must explain the context and reasoning. 

- If the prompt indicates that a bug is being fixed, don't write the fix right away. First write the test. Observe it failing. Then write the fix. And observe the test passing.

## Andon – Projekt-Wissen

## Zweck
Selbst gehostetes Mehrbenutzer-Dashboard Andon: Startseite (Dashy-Ersatz) plus
Auswertung von Kimai, Invoice Ninja, Snipe-IT, Dawarich mit Hinweisen.
Plan und Entscheidungen: `ROADMAP.md`.

Implementierung: Go (`net/http`, `html/template`, htmx für Fragment-Lazy-Load).
Vollständig auf Go umgestellt; die frühere Python-Fassung ist nur noch in
der Git-Historie vorhanden.

## Schichten (nur zum direkten Nachbarn darunter)
```
web/        Routen, Templates, Formulare        (net/http, html/template, htmx)
  ↓
services/   Anwendungslogik, Rechteprüfung      (einzige Stelle für Right-Prüfung)
  ↓
repos/      Datenbankzugriff     sources/  Dienst-Adapter (lesen)   outbound/  Apprise (senden)
  ↓                                 ↓                                  ↓
db/         database/sql, Tx     drivers/  rohes HTTP je Dienst
```
- Routen rufen nie Repos, Sources, Outbound oder Drivers direkt auf.
- Widgets (`internal/widgets/`) rendern nur Daten, die ein Service liefert.
- Unexportierte (kleingeschriebene) Namen; Export nach außen nur mit Rückfrage.

## Konventionen
- Go (aktuelle Stable-Version), gofmt, go vet. `make check` vor jedem Commit.
- Texte nur über `t(key)` (Kataloge `internal/i18n/catalogs/*.yml`); Hinweise speichern Schlüssel + Parameter.
- CSS nur mit Theme-Tokens, keine Hex-Werte außerhalb `themes/`.
- Zugangsdaten nur verschlüsselt (`internal/crypto`), nie im Log, nie im Export.

## Bausteine
```
internal/sources/*.go           <service>.data: ein gecachter Datensatz je Verbindung (+ .test)
internal/metrics/*.go           reine Funktionen: Datensatz → Kennzahlen (kein I/O)
internal/rules/*.go             Register(id, scope, defaults, run): Datensatz → Finding (kein I/O)
internal/widgets/*.go           WidgetType: Decode, Queries, View (rein), Template
internal/services/analysis/     Job: Datensätze laden, Regeln anwenden, hints.Sync()
internal/services/scheduler/    Background-Jobs (Ticker je Job, panic-/error-isoliert)
```
- Neue Regel: Funktion in `internal/rules/`, Texte `hint.<message>.title|why` in beiden Katalogen, Test in `internal/rules/*_test.go`.
- Neues Widget: Typ in `internal/widgets/`, Template-Define `widgets/<key>` in `internal/web/templates/widgets_*.html`, `wtype.<key>` in den Katalogen.
- Hinweis-Parameter typisiert übergeben (`money()`, `day()`, `num()` aus `internal/i18n`).

## Gelernte Fehler
- Go 1.22+ Mux: Pfadmuster wie `/theme/{id}.css` (Wildcard + fester Suffix in einem Segment) werden nicht unterstützt ("bad wildcard segment"). Ganzes Segment als Wildcard registrieren, Suffix im Handler abschneiden.
- `html/template` kann kein Template mit zur Laufzeit berechnetem Namen einbinden (`{{template}}` braucht einen String-Literal) — anders als Jinjas `include`. Für pro-Typ-Fragmente (Widgets) daher `ExecuteTemplate` mit dynamischem Namen aus einer eigenen Route aufrufen (siehe `/widget-fragments/{id}`), nicht versuchen, es inline im Template zu lösen.
- Der SQLite-Treiber liefert TEXT-Spalten als `string`, nicht als `time.Time` — auch wenn der Wert wie ein Zeitstempel aussieht. Erst in `string` scannen, dann `db.ParseTime`.
- String-Enum mit explizitem Zero-Value versehen, wenn die Go-Zero-Value (`""`) semantisch "Standard"/"keiner" bedeuten soll (z. B. `ConnUse`s `ConnNone`); sonst weicht ein Feld, das nie explizit gesetzt wird, unbemerkt vom Default ab.
- DB-Datei ist verschlüsselt (Adiantum-VFS); Kopien nur über `db.Snapshot`/`db.OpenReadOnly`, nie per Dateikopie oder `sql.Open`. Reine Lesepfade über `db.WithRead`, `db.WithTx` nimmt die Schreibsperre sofort.
- Board-Freigabe muss Widgets aus dem Bereich des Boards sichtbar machen (`boards.seenRight`).
