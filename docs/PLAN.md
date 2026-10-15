# Projektplan: audiobook-generator

Stand: 2026-10-15 · Status: **Entwurf zur Abstimmung**

Ziel: Ein selbst gehosteter Dienst, der sehr große englische EPUBs in
mehrstimmige Hörbücher verwandelt. Vorab werden alle Figuren des Buchs
analysiert, jede (wichtige) Figur bekommt eine eigene Stimme, und über eine
Weboberfläche lassen sich Kapitel, Figuren und Stimmen ansehen und anpassen.
Vertont wird kapitelweise. Ausgeliefert wird alles als **ein Helm-Chart, das
alles mitbringt, was es braucht**.

- Backend: **Go**
- Frontend: **React** (TypeScript)
- Auslieferung: **Helm-Chart** + Container-Image auf ghcr.io

---

## 1. Rahmenbedingungen

| Thema | Vorgabe / Annahme |
|---|---|
| Bücher | Englisch, DRM-frei, sehr groß: Beispiel-EPUB 12 MB ohne Bilder ≈ **4–6 Mio. Wörter**, vermutlich > 1000 Kapitel, Hunderte bis Tausende Figuren |
| Hörbuchlänge | bei ~155 Wörtern/min ≈ **450–650 h Audio** pro Buch |
| Hardware | Kubernetes (Talos) auf Proxmox, **keine GPU** (iGPU UHD 770 vorerst ungenutzt), Nodes ~16 GB RAM |
| LLM | Cloud erlaubt, aber **kostenlos**: Free-Tiers OpenAI-kompatibler Anbieter (primär Gemini Flash), optional lokales Modell als Fallback |
| TTS | **Kokoro-82M** über Kokoro-FastAPI (CPU-Image, OpenAI-kompatibel, Stimm-Mischung) |
| Nutzung | Ein Haushalt, LAN-only, wenige Nutzer, kein Multi-Tenant |

Abgeleitete Grundsätze:

1. **Just-in-time statt alles vorab.** Die Figurenanalyse läuft einmal über
   das ganze Buch; Sprecherzuordnung und Vertonung passieren kapitelweise kurz
   bevor sie gebraucht werden ("immer die nächsten N Kapitel nach der
   Hörposition fertig halten"). Erzeugung (~3–5× Echtzeit) ist schneller als
   Hören.
2. **Das LLM schreibt nie den Buchtext neu.** Text wird deterministisch im
   Code segmentiert; das LLM liefert nur kompakte Zuordnungen
   (`q17 → char_42`). Spart Free-Tier-Kontingent und verhindert, dass das
   Modell Text verfälscht.
3. **Alles ist fortsetzbar.** Jeder Arbeitsschritt ist ein persistenter Job;
   Neustarts, 429-Rate-Limits und Tageskontingente unterbrechen nur, sie
   verlieren nichts.
4. **Stimmen sind deterministisch.** Eine Figurenstimme ist ein "Rezept"
   (Mischung aus Kokoro-Grundstimmen + Tempo). Gleiches Rezept ⇒ gleiche
   Stimme in Kapitel 3 und Kapitel 1300.
5. **Self-contained.** Ein `helm install` reicht: App, TTS-Server,
   Datenhaltung. Externe Abhängigkeiten (OIDC, S3-Backup, externer TTS) sind
   optional.

---

## 2. Architektur

```
                       ┌──────────────────── Helm-Release ─────────────────────┐
 Browser (React SPA) ──┼─▶ Ingress ─▶ app (Go, 1 Pod)                          │
 Podcast-App (RSS) ────┤               ├─ HTTP-API (OpenAPI) + SPA (embed.FS)  │
                       │               ├─ Job-Runner                           │
                       │               │    ├─ LLM-Worker (rate-limited) ──────┼──▶ Gemini / Groq / OpenRouter / lokal
                       │               │    ├─ TTS-Worker (N parallel) ───┐    │      (OpenAI-kompatibel)
                       │               │    └─ Audio-Assembler (ffmpeg)   │    │
                       │               ├─ SQLite (WAL) ─┐                 │    │
                       │               └─ /data (PVC) ◀─┘ EPUBs, Audio    │    │
                       │                    └─ optional Litestream ───────┼────┼──▶ S3 (z. B. Garage) Backup
                       │                                                  ▼    │
                       │              kokoro (Kokoro-FastAPI CPU, 1..n Pods)   │
                       └───────────────────────────────────────────────────────┘
```

### 2.1 Komponenten

| Komponente | Aufgabe |
|---|---|
| **app** (Go-Binary) | API, ausgelieferte SPA, Job-Runner, EPUB-Import, Segmentierung, LLM-Orchestrierung, Audio-Zusammenbau, Player-Backend, RSS-Feed |
| **kokoro** | Kokoro-FastAPI (CPU), nur clusterintern erreichbar; skalierbar über Replicas |
| **SQLite** auf PVC | Bücher, Kapitel, Segmente, Figuren, Stimmen, Jobs, Kontingente, Hörfortschritt |
| **/data** auf PVC | Original-EPUBs, fertige Kapitel-Audios, temporäre Render-Dateien |
| **ffmpeg** (im app-Image) | Zusammenfügen, Lautheitsnormalisierung, AAC/M4B mit Kapitelmarken |

### 2.2 Entscheidung Datenbank: SQLite statt Postgres

Empfehlung **SQLite** (pure Go, `modernc.org/sqlite`, kein CGO):

- Das Chart bleibt wirklich eigenständig (kein Postgres-Operator, kein
  zweites StatefulSet).
- Ein Prozess, ein Writer: passt zum Single-Pod-Design; WAL-Modus reicht
  für parallele TTS-Worker locker (Schreiblast ist gering).
- Mengengerüst: ~1 Mio. Segmente pro Buch, ~30 MB Text — problemlos.
- Backup: optionaler **Litestream**-Sidecar repliziert kontinuierlich nach S3
  (im Homelab: Garage, das ohnehin Teil der Backup-Kette ist). Zusätzlich
  JSON-Export/-Import der Analyse-Ergebnisse (Figuren, Stimmen, Zuordnungen),
  denn die kosten Tage an LLM-Kontingent.
- Die Store-Schicht kapselt die DB (sqlc-generierter Code hinter
  Interfaces); ein späterer Wechsel auf Postgres bleibt möglich.

Konsequenz: `replicas: 1`, Strategy `Recreate`, PVC `ReadWriteOnce`.

### 2.3 Entscheidung Player: eingebaut statt Audiobookshelf-Pflicht

Der Dienst bringt einen **eigenen Web-Player** mit (Fortschritt pro Nutzer,
Kapitelnavigation, Geschwindigkeit, Lockscreen-Steuerung über die Media
Session API) und einen **Podcast-RSS-Feed** pro Buch/Band, damit jede
Podcast-App auf dem Handy funktioniert. Der eingebaute Player liefert direkt
die Hörposition für die Just-in-time-Vertonung.

Audiobookshelf-Export (Upload per API oder gemeinsames Volume) ist ein
optionales späteres Feature, keine Voraussetzung.

---

## 3. Verarbeitungs-Pipeline

```
 EPUB ─▶ 1 Import ─▶ 2 Segmentierung ─▶ 3 Figurenanalyse ─▶ 4 Zusammenführen ─▶ 5 Besetzung
         (einmalig)   (einmalig, Code)    (einmalig, LLM,      (Code + LLM)        (Code + LLM)
                                           ~150–250 Requests)
                                                                     │
          ┌──────────────────────── pro Kapitel, just in time ───────┘
          ▼
   6 Sprecherzuordnung (LLM) ─▶ 7 Vertonung (Kokoro) ─▶ 8 Zusammenbau (ffmpeg) ─▶ Player / RSS / Download
```

### 3.1 Import (EPUB → Bände/Kapitel/Absätze)

- EPUB selbst parsen: `archive/zip` + `encoding/xml` (OPF, NCX) +
  `golang.org/x/net/html` (XHTML, nav). Keine schwere Abhängigkeit.
- Struktur aus dem Inhaltsverzeichnis (nav.xhtml bzw. NCX) **inklusive
  Fragment-Anker** — bei großen Web-Serials stecken oft viele Kapitel in
  einer XHTML-Datei. Fallbacks: Überschriften (`h1`–`h3`), dann
  Spine-Dokumente.
- Hierarchie **Band → Kapitel**, abgeleitet aus der TOC-Verschachtelung.
- Normalisierung: typografische Zeichen, Fußnoten/Endnoten entfernen,
  Szenenwechsel (`***`, `<hr>`) als Pausenmarker, Kursivschrift als Markup
  erhalten (späteres Feature: Gedanken).
- Streaming-fähig: auch eine einzelne 30-MB-XHTML-Datei darf den Speicher
  nicht sprengen.
- Ergebnis prüfbar in der UI: Kapitel zusammenführen/teilen/umbenennen,
  Front-/Backmatter ausschließen.

### 3.2 Segmentierung (deterministisch, im Code)

- Absätze in **Erzählung** und **wörtliche Rede** zerlegen. Zustandsautomat
  über Anführungszeichen; Anführungsstil wird pro Buch statistisch erkannt
  (`“…”` vs. `"…"` vs. britisch `‘…’`). Mehrabsätzige Zitate (Absatz ohne
  schließendes Zeichen) werden korrekt fortgeführt.
- Jedes Zitat bekommt eine stabile ID (`q<n>` pro Kapitel).
- Lange Erzählabschnitte werden satzweise in TTS-taugliche Stücke geteilt
  (Kokoro arbeitet am besten mit ≤ ~400 Zeichen pro Aufruf).
- Begleitsätze ("…, she said.") bleiben Erzählung.

### 3.3 Figurenanalyse (einmalig, ganzes Buch)

- Kapitel werden zu Batches von ~30–50k Tokens gebündelt
  (≈ 150–250 Requests für ein Buch mit 5 Mio. Wörtern).
- Batches laufen **sequenziell**; jeder Request bekommt den kompakten
  Index der bereits bekannten Figuren (nur kanonischer Name + Aliase) mit,
  damit das Modell vorhandene Namen wiederverwendet statt Dubletten zu
  erzeugen.
- Antwort als JSON-Schema (strukturierte Ausgabe), pro Figur: kanonischer
  Name, Aliase/Titel, Geschlecht, Altersgruppe, Rolle, Kurzbeschreibung,
  Sprechweise, geschätzte Wichtigkeit, Kapitel mit Auftritten.
- Pro Kapitel zusätzlich: **Erzählperspektive** (Ich-Erzähler? Wer?) —
  wichtig für Web-Serials mit wechselnden POV-Kapiteln.
- Ergebnis: Figurenbibel + Index "Figur → Kapitel" (+ Erwähnungszähler).
- Dauer im Free-Tier: grob **1–3 Tage** inkl. automatischer Pausen. Die UI
  zeigt Fortschritt und verbrauchtes Tageskontingent.

### 3.4 Zusammenführen (Entity Resolution)

1. Deterministisch: Normalisierung (Groß/Klein, Titel wie *Mr./Lady/Captain*),
   Alias-Überschneidung ⇒ Union-Find-Cluster.
2. Nur unklare Kandidaten ("Erin" vs. "Erin Solstice" vs. "the [Innkeeper]")
   gehen als kompakte Profile in einen LLM-Merge-Request.
3. Manuelle Korrektur in der UI: zusammenführen, aufteilen, Alias
   verschieben.

### 3.5 Besetzung (Voice Casting)

- **Stimmkatalog**: Kokoro-Stimmen mit Metadaten (Geschlecht, Akzent US/UK,
  Qualitätsstufe, Klangcharakter) als eingebettete Katalogdatei, abgeglichen
  mit `/v1/audio/voices` des TTS-Servers.
- **Erzähler**: feste Stimme pro Buch (wählbar).
- **Hauptfiguren** (Top ~40–60 nach Wichtigkeit/Redeanteil): das LLM
  bekommt Figurenprofil + Katalog und schlägt ein Rezept vor
  (z. B. `bm_george(0.7)+am_michael(0.3)`, Tempo 0.95). Der Code erzwingt:
  Geschlecht passt, Rezepte unterscheiden sich ausreichend (Mindestabstand im
  Gewichtsraum), keine Kollision mit dem Erzähler.
- **Nebenfiguren**: Pool-Stimmen nach (Geschlecht, Altersgruppe),
  deterministisch per Hash zugeteilt — dieselbe Nebenfigur klingt immer
  gleich.
- Jede Figur lässt sich in der UI hochstufen/herabstufen und das Rezept frei
  bearbeiten; Hörprobe mit einem **echten Zitat der Figur**.
- Unbekannte Sprecher ("a guard") → generische Pool-Stimme nach Geschlecht.

### 3.6 Sprecherzuordnung (pro Kapitel, just in time)

- Eingabe: Kapiteltext mit Zitat-Markern (`[q12]“…”`), die im Kapitel
  vorkommenden Figuren (aus dem Index) + die Sprecher der letzten Zitate des
  Vorkapitels als Kontext.
- Ausgabe: `{ "q12": { "speaker": "char_42" | "unknown" | "new:Name",
  "emotion": "angry", "confidence": 0.8 } }`.
- Validierung: alle Zitat-IDs abgedeckt, nur bekannte IDs; sonst
  Reparatur-Request. `new:`-Figuren landen als Nebenfigur in der Bibel.
- Zu lange Kapitel werden an Szenenwechseln geteilt, mit Überlappung.
- Niedrige Konfidenz wird in der UI markiert.
- Optional später: Ich-Erzähl-Kapitel mit der Stimme der POV-Figur erzählen.

### 3.7 Vertonung und Zusammenbau

- Jedes Segment → `POST /v1/audio/speech` (Rezept, Tempo, Format PCM/WAV).
  Parallelität konfigurierbar (≈ Anzahl Kokoro-Replicas × 1–2).
- Ausspracheliste pro Buch (Fantasy-Namen) wird vor dem TTS-Aufruf
  angewendet (Ersetzung bzw. Kokoro-Phonem-Syntax).
- Zusammenbau mit ffmpeg: Pausen (Erzählung↔Dialog ~150 ms, Absatz ~400 ms,
  Szenenwechsel ~1,2 s, Kapiteltitel + 1 s), `loudnorm`, **AAC 64 kbit/s
  mono** pro Kapitel (`.m4a`) mit Metadaten; auf Wunsch ein M4B pro Band mit
  Kapitelmarken.
- Temporäre Dateien liegen nur während des Jobs auf der Platte. In v1 wird
  nach einer Korrektur das ganze Kapitel neu erzeugt (wenige CPU-Minuten);
  ein Segment-Cache (Opus) ist eine spätere Optimierung.
- Speicherbedarf fertige Audios: ~22–29 MB/h ⇒ **~10–19 GB** pro Riesenbuch.

### 3.8 Scheduler und Jobs

- Job-Tabelle in SQLite; Typen: `import`, `analyze_batch`, `merge`,
  `cast`, `attribute_chapter`, `render_chapter`, `assemble_volume`.
- Eigene Worker-Pools: **LLM** (Concurrency 1, Rate-Limiter) und **TTS**
  (Concurrency N), damit die eine Ressource die andere nicht blockiert.
- **Lookahead-Policy**: hält die nächsten *N* Kapitel nach der Hörposition
  fertig (attributiert + vertont). Zusätzlich manuell: einzelne Kapitel oder
  Bereiche einreihen, priorisieren, pausieren.
- Abhängigkeiten: `render_chapter` setzt `attribute_chapter` voraus, das
  wiederum eine abgeschlossene Analyse + Besetzung.
- Live-Fortschritt an die UI per **Server-Sent Events**.

### 3.9 LLM-Schicht

- Ein schlanker OpenAI-kompatibler Client (Chat Completions + JSON-Schema),
  mehrere **Provider** konfigurierbar mit Basis-URL, Modell, API-Key und
  Limits (RPM, TPM, RPD).
- Token-Bucket pro Provider, Tageszähler persistent, `429`/`Retry-After`
  wird respektiert, danach Fallback auf den nächsten Provider der Liste.
- Kontingent-Anzeige in der UI ("heute 312/1000 Requests bei Gemini").
- Prompts als versionierte, eingebettete Templates; Prompt-Version wird mit
  dem Ergebnis gespeichert (Nachvollziehbarkeit, gezieltes Neu-Rechnen).
- Hinweis in der Doku: Free-Tiers dürfen Eingaben oft zum Training nutzen;
  Limits ändern sich häufig ⇒ alles konfigurierbar, nichts hart kodiert.

---

## 4. Datenmodell (Skizze)

```
book            id, title, author, language, cover_path, epub_path, quote_style,
                narrator_voice_id, status, created_at
volume          id, book_id, idx, title
chapter         id, book_id, volume_id, idx, title, word_count, pov_character_id,
                excluded, status (imported|attributed|rendered|failed), audio_path,
                audio_duration_ms, prompt_version
paragraph       id, chapter_id, idx, kind (text|scene_break|heading), text
segment         id, chapter_id, paragraph_id, idx, kind (narration|quote), quote_ref,
                text, speaker_character_id, emotion, confidence, manual_override
character       id, book_id, name, aliases (json), gender, age_group, role,
                description, speech_style, importance, mention_count, line_count,
                tier (main|minor|pool), merged_into_id
character_chapter  character_id, chapter_id, mentions
voice           id, book_id, recipe (z. B. "bm_george(0.7)+am_michael(0.3)"),
                speed, label
character_voice character_id, voice_id
lexicon_entry   id, book_id, term, replacement, is_phonemes
job             id, type, book_id, chapter_id, status, priority, attempts,
                not_before, error, payload (json), created_at, updated_at
llm_usage       provider, day, requests, input_tokens, output_tokens
listen_progress user_id, book_id, chapter_id, position_ms, updated_at
user            id, oidc_subject, name                (nur wenn OIDC aktiv)
```

Migrationen mit **goose**, Queries mit **sqlc**.

---

## 5. Backend (Go)

| Bereich | Wahl |
|---|---|
| Sprache | Go (aktuelle stabile Version, ≥ 1.25) |
| HTTP | `net/http` (ServeMux mit Patterns) + **oapi-codegen** (strict server) |
| API-Vertrag | **OpenAPI 3.1** in `api/openapi.yaml` als Single Source of Truth |
| DB | `modernc.org/sqlite`, **sqlc**, **goose** |
| OIDC | `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2`, Session-Cookie; abschaltbar |
| Logging / Metriken | `log/slog` (JSON), Prometheus `/metrics` (Jobs, Kontingente, Renderzeit, Echtzeitfaktor) |
| Konfiguration | Env-Variablen + optionale YAML-Datei (Provider-Liste, Limits) |
| Audio | `ffmpeg`/`ffprobe` als Prozess (im Image enthalten) |
| Frontend-Auslieferung | `embed.FS`, SPA-Fallback auf `index.html` |
| Tests | `testing` + testify, Golden-Files, Fake-LLM- und Fake-TTS-Server (httptest) |
| Lint | golangci-lint |

### Paketstruktur

```
cmd/audiobook-generator/     main: config, wiring, server + runner
internal/config/
internal/store/              sqlc-Code, Migrationen, Transaktionen
internal/epub/               OPF/NCX/nav-Parser, XHTML → Absätze, Kapitel-Split
internal/segment/            Anführungszeichen-Automat, Satz-Chunking
internal/llm/                OpenAI-kompatibler Client, Provider, Rate-Limiter, Usage
internal/analysis/           Figurenanalyse-Batches, Merge, POV
internal/casting/            Stimmkatalog, Rezept-Vergabe, Pool
internal/attribution/        Sprecherzuordnung pro Kapitel + Validierung
internal/tts/                Kokoro-Client, Ausspracheliste
internal/audio/              ffmpeg-Pipeline, Pausen, loudnorm, M4A/M4B
internal/jobs/               Queue, Worker-Pools, Lookahead-Policy, SSE-Events
internal/api/                oapi-codegen-Handler
internal/auth/               OIDC, Sessions
internal/feed/               Podcast-RSS
internal/prompts/            eingebettete Prompt-Templates (versioniert)
web/                         React-App (eigenes package.json)
api/openapi.yaml
charts/audiobook-generator/
```

### API (Auszug)

```
POST   /api/books                         EPUB hochladen (multipart, Limit konfigurierbar)
GET    /api/books | /api/books/{id}       Liste / Details inkl. Pipeline-Status
POST   /api/books/{id}/analysis           Figurenanalyse starten/fortsetzen
GET    /api/books/{id}/chapters           paginiert, Filter (Status, Band, Figur)
PATCH  /api/chapters/{id}                 Titel, ausschließen
POST   /api/chapters/{id}/merge|split
GET    /api/chapters/{id}/segments
PATCH  /api/segments/{id}                 Sprecher/Emotion/Text korrigieren
POST   /api/chapters/render               Bereich/Auswahl einreihen
GET    /api/books/{id}/characters         paginiert, sortierbar, Suche
PATCH  /api/characters/{id}               Profil, Tier, Stimme
POST   /api/characters/merge | /{id}/split
GET    /api/voices/catalog                Kokoro-Grundstimmen
POST   /api/voices/preview                Rezept + Text → Audio (gecacht)
GET    /api/books/{id}/lexicon  (+ CRUD)
GET    /api/jobs  · POST /api/jobs/{id}/retry|cancel · POST /api/queue/pause
GET    /api/llm/usage
GET    /api/events                        SSE: Job- und Fortschritts-Events
GET    /api/chapters/{id}/audio           Range-Requests für den Player
PUT    /api/books/{id}/progress           Hörposition
GET    /feed/{token}/{bookId}.xml         Podcast-RSS (Token statt Session)
GET    /api/export/{bookId} · POST /api/import
GET    /healthz · /readyz · /metrics
```

---

## 6. Frontend (React)

| Bereich | Wahl |
|---|---|
| Build | **Vite** + React + TypeScript (strict) |
| Routing / Daten | **TanStack Router** + **TanStack Query** |
| API-Client | **openapi-typescript** + **openapi-fetch** (Typen aus `api/openapi.yaml`) |
| Tabellen | **TanStack Table** + **TanStack Virtual** (1000+ Kapitel, 1000+ Figuren) |
| UI-Bibliothek | **Mantine** (Slider, Tabellen, Notifications, Modals out of the box; hell/dunkel) |
| Tests | Vitest + Testing Library; Playwright für einen E2E-Smoke-Test |
| Lint/Format | ESLint + Prettier |

### Seiten

```
┌ Bibliothek ───────────────────────────────────────────────────────────────┐
│ [EPUB hochladen]                                                          │
│ ▣ The Wandering Inn   Import ✔  Analyse 63 % (121/190)  Besetzung –       │
│                       LLM heute: 312/1000 Req.          ▶ weiterhören     │
└───────────────────────────────────────────────────────────────────────────┘
┌ Buch ▸ Kapitel ───────────────────────────────────────────────────────────┐
│ Filter: [Status ▾] [Band ▾] [Figur: Erin ▾]   Lookahead: [10] Kapitel     │
│ ▾ Band 1                                                                  │
│   1.00  Chapter 1.00          6.120 W  Skript ✔  Audio ✔ 41:12  ▶         │
│   1.01  Chapter 1.01          5.870 W  Skript ✔  Audio ⏳ 63 %   ✕         │
│   1.02  Interlude             7.010 W  Skript –  Audio –        [▸ rendern]│
│ ☐ Auswahl: 20 Kapitel → [einreihen] [ausschließen]                        │
└───────────────────────────────────────────────────────────────────────────┘
┌ Buch ▸ Figuren ───────────────────────────────────────────────────────────┐
│ Suche [      ]  Tier [alle ▾]                                             │
│ Name     Aliase                Zeilen  Kap.  Stimme                   Test│
│ Erin     the Innkeeper          8.204   812  bf_emma(.8)+af_bella(.2)   ▶ │
│ Ryoka    the Runner             3.112   402  af_nicole · 1.05×          ▶ │
│ Guard #  —                         41    12  Pool (m, adult)            ▶ │
│ [zusammenführen] [aufteilen] [als Hauptfigur]                             │
└───────────────────────────────────────────────────────────────────────────┘
┌ Stimm-Editor (Erin) ──────────────────────────────────────────────────────┐
│ bf_emma     ███████████░░░  0.8      Tempo  ◀━━━━●━━━▶ 1.00              │
│ af_bella    ███░░░░░░░░░░░  0.2      [+ Grundstimme]                     │
│ Probetext: (zufälliges echtes Zitat von Erin)  [↻]   ▶ Hörprobe          │
└───────────────────────────────────────────────────────────────────────────┘
┌ Kapitel-Editor (1.01) ────────────────────────────────────────────────────┐
│ ▶ Kapitel  [neu rendern]                                                  │
│ 🟦 Erzähler  The door creaked open …                                      │
│ 🟥 Erin      “Welcome to the inn!”        [Erin ▾] [neutral ▾] ⚠ 0.55      │
│ 🟩 Pisces    “Hardly a welcome.”          [Pisces ▾] [dry ▾]               │
└───────────────────────────────────────────────────────────────────────────┘
```

Weitere Seiten: **Player** (Kapitelliste, Fortschritt, Tempo, Sleep-Timer,
Media Session), **Aussprache** (Lexikon mit Hörprobe), **Stimmkatalog**
(alle Kokoro-Stimmen anhören), **Jobs** (Queue, Fehler, Retry),
**Einstellungen** (Provider, Limits, Kontingente, Erzählerstimme, Lookahead).

---

## 7. Helm-Chart

`charts/audiobook-generator/` — das Chart bringt alles mit; abschaltbar ist
nur, was man extern ersetzen will.

| Ressource | Bedingung |
|---|---|
| Deployment `app` (1 Replica, `Recreate`) | immer |
| Service, ServiceAccount | immer |
| PVC `data` (SQLite + Audio, Default 50Gi) | `persistence.enabled` (sonst `existingClaim`) |
| ConfigMap (Provider-Liste, Limits, Lookahead, Pausen) | immer |
| Secret (LLM-Keys, OIDC-Client-Secret, Session-Key, Feed-Token-Secret) | wenn kein `existingSecret` gesetzt |
| Deployment + Service `kokoro` | `tts.kokoro.enabled` (Default `true`), sonst `tts.externalUrl` |
| Ingress | `ingress.enabled` (className, hosts, tls, annotations frei) |
| Litestream-Sidecar + Config | `backup.litestream.enabled` (S3-Endpoint, Bucket, `existingSecret`) |
| ServiceMonitor | `metrics.serviceMonitor.enabled` |
| NetworkPolicy | `networkPolicy.enabled` (kokoro nur von app erreichbar) |

Weitere Werte: `resources` getrennt für app und kokoro, `priorityClassName`,
`nodeSelector`/`tolerations`/`affinity`, `podSecurityContext`
(non-root, `readOnlyRootFilesystem`, `seccompProfile: RuntimeDefault`,
`/tmp` als emptyDir mit `sizeLimit`), Image-Tags gepinnt
(Kokoro-FastAPI-Version explizit), `values.schema.json` zur Validierung.

Probes: `startupProbe` großzügig (Migrationen), `readinessProbe` auf
`/readyz` (DB offen, Kokoro erreichbar), `livenessProbe` auf `/healthz`
(unabhängig von laufenden Jobs, damit lange Renderings keinen Neustart
auslösen). Kokoro: großzügige Startup-Probe (Modell-Load).

Auslieferung: Chart als **OCI-Artefakt** nach
`oci://ghcr.io/yoadey/charts/audiobook-generator`, Image nach
`ghcr.io/yoadey/audiobook-generator`, beide versioniert per Git-Tag.
Damit kann das Cluster-Repo (`fluxcd-talos-cluster`) es später per
`HelmRelease` + OCI-Source einbinden; Renovate übernimmt die Updates.

---

## 8. Container-Image

- Multi-Stage-Build: Node (Vite-Build) → Go (Build mit eingebettetem
  `web/dist`, `CGO_ENABLED=0`) → Laufzeit-Image mit **ffmpeg**
  (`debian:stable-slim` + ffmpeg oder ein schlankes statisches ffmpeg),
  non-root User.
- Zielplattform `linux/amd64` (Homelab); `arm64` optional.
- Das Kokoro-Image wird **nicht** selbst gebaut, sondern das offizielle
  Kokoro-FastAPI-CPU-Image gepinnt verwendet.

---

## 9. CI/CD (GitHub Actions)

| Workflow | Inhalt |
|---|---|
| `ci.yaml` (PR + main) | `go vet`, golangci-lint, `go test -race`; sqlc/oapi-codegen-Drift-Check (generierter Code aktuell?); Frontend lint, typecheck, Vitest, Build; `helm lint`, **helm-unittest**, `kubeconform` auf gerenderte Manifeste; Docker-Build (ohne Push) |
| `e2e.yaml` (PR, optional nightly) | kind-Cluster, Chart installieren (mit Fake-LLM), Mini-EPUB hochladen, ein Kapitel rendern, Audio-Datei prüfen |
| `release.yaml` (Tag `v*`) | Image + Chart nach ghcr.io pushen, GitHub Release |
| Renovate | Go-Module, npm, Actions, Kokoro-Image-Tag im Chart |

---

## 10. Teststrategie & Qualitätsmessung

- **Fixtures**: gemeinfreie EPUBs (Project Gutenberg, z. B. *Pride and
  Prejudice* — dialoglastig) + synthetische Problemfälle (alles in einer
  XHTML-Datei, verschachtelte TOC, britische Anführungszeichen,
  mehrabsätzige Zitate).
- **Golden-Tests** für Kapitel-Split und Segmentierung.
- **LLM-Schicht** mit aufgezeichneten Antworten (Fake-Provider) testen;
  Rate-Limiter/Fallback mit simulierten 429ern.
- **Qualitätsmessung der Sprecherzuordnung**: Auswertungsskript gegen ein
  annotiertes Korpus (z. B. *Project Dialogism Novel Corpus*, gemeinfreie
  Romane mit annotierten Sprechern) — um Prompts und Provider objektiv zu
  vergleichen, bevor Kontingent am echten Buch verbraucht wird.
- **Benchmark** Kokoro auf den Cluster-Nodes (Echtzeitfaktor) als
  Prometheus-Metrik.

---

## 11. Meilensteine

Jeder Meilenstein endet mit einem installierbaren Chart-Stand.

| # | Inhalt | Ergebnis |
|---|---|---|
| **M0** | Repo-Grundgerüst: Go-Modul, OpenAPI + Codegen, React-Skelett, embed, Dockerfile, Chart-Skelett (app + kokoro + PVC), CI | `helm install` startet App + Kokoro, leere UI |
| **M1** | EPUB-Import, Kapitel-Split, Segmentierung, Bibliothek + Kapitelliste + Textansicht | Echtes 12-MB-EPUB korrekt in Bände/Kapitel zerlegt |
| **M2** | Kokoro-Anbindung, Jobs/Queue, Render mit **einer Erzählerstimme**, eingebauter Player, Lookahead | **Schon nutzbares Hörbuch** (einstimmig); Echtzeitfaktor gemessen |
| **M3** | LLM-Schicht: Provider, Rate-Limiter, Kontingente, strukturierte Ausgabe, Einstellungsseite | Provider-Test-Button, Kontingentanzeige |
| **M4** | Figurenanalyse + Merge + Figuren-UI (Liste, Details, zusammenführen/aufteilen) | Vollständige Figurenbibel des Buchs |
| **M5** | Besetzung + Stimmkatalog + Stimm-Editor + Hörproben | Jede Hauptfigur hat ein Rezept |
| **M6** | Sprecherzuordnung + mehrstimmiges Rendern + Kapitel-Editor | **Mehrstimmiges Hörbuch** |
| **M7** | Ausspracheliste, Podcast-RSS, OIDC, Metriken/ServiceMonitor, Litestream, Export/Import, M4B pro Band | Feature-komplett v1.0 |
| **M8** (optional) | Segment-Cache, Emotionen/Gedanken, POV-Erzählstimme, Audiobookshelf-Export, lokaler LLM-Fallback | Verfeinerungen |

Die Einbindung ins Cluster-Repo (`HelmRelease`, Zitadel-Client per
Terraform, Secrets per SOPS, Garage-Bucket für Litestream) passiert separat
dort, sobald M2 steht.

---

## 12. Risiken

| Risiko | Gegenmaßnahme |
|---|---|
| Free-Tier-Limits ändern sich / Modelle verschwinden | Provider-Liste + Limits nur Konfiguration, Fallback-Kette, persistente Jobs |
| Free-Tier nutzt Eingaben zum Training | Bewusste Entscheidung, in Doku/UI benannt; lokaler Fallback möglich |
| Qualität der Sprecherzuordnung bei Web-Serial-Stil | Messung gegen annotiertes Korpus, Konfidenz-Markierung, schneller manueller Editor |
| Kokoro auf CPU langsamer als erwartet | M2 misst früh; Kokoro-Replicas skalieren; Lookahead puffert |
| Nur ~25 englische Grundstimmen | Gewichtete Mischung + Tempo; Pool für Nebenfiguren; Mindestabstand im Code |
| Pathologische EPUBs (eine Riesendatei, kaputte TOC) | Streaming-Parser, mehrere Fallbacks, manuelle Kapitel-Korrektur |
| SQLite-Datenverlust | PVC + Litestream nach S3 + JSON-Export der Analyse |
| Urheberrecht | Nur private Nutzung eigener DRM-freier Bücher, LAN-only, keine öffentliche Bereitstellung |

---

## 13. Offene Entscheidungen

1. **SQLite + Litestream** (Empfehlung) oder Postgres (extern/CNPG)?
2. **Eingebauter Player + RSS** (Empfehlung) — oder doch Audiobookshelf als
   Pflicht-Player?
3. UI-Bibliothek **Mantine** (Empfehlung) oder shadcn/ui + Tailwind?
4. Sprache von Code-Doku/README: Englisch (wie das Cluster-Repo)?
5. Git-Workflow: Feature-Branches + PRs gegen `main`, Releases per Tag?
