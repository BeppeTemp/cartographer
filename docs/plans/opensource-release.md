# Piano — Pubblicazione open source: Apache-2.0, GitHub, Homebrew, ghcr.io, inglese (D79, da implementare)

> **Stato**: approvato, non implementato. WP marcati `[dev]` eseguibili da agente `dev`/Sonnet;
> WP marcati `[operatore]` richiedono il coordinatore con `gh`/credenziali (creazione repo,
> secrets, ruleset, repo esterni).
> Al termine: voce **D79** in `docs/decisions.md`, aggiornare `docs/deployment.md`,
> `docs/configurator.md`, `docs/testing.md`, `docs/index.md`, `docs/roadmap.md`,
> poi **eliminare questo file**.

## Contesto e diagnosi

Cartographer vive sul Gitea privato (`gitea.beppetemp.com`) con CI di deploy interna
(`.gitea/workflows/build-deploy.yaml`: registry privato + bump di `homelab-manifests`).
Va pubblicato come progetto open source. Decisioni **già prese con l'utente** (non rilitigare):

- **Licenza: Apache-2.0** (sostituisce la MIT attuale). Siamo pre-pubblicazione e autore unico:
  il cambio è un semplice commit. Niente NOTICE, niente header per-file: solo il testo in `LICENSE`.
  Ratio confermata con l'utente: chiunque — inclusa la sua azienda — può usare, modificare,
  chiudere e monetizzare liberamente; nessuna esclusiva commerciale, quindi **niente CLA**
  (inbound = outbound) e niente dual licensing, scartati esplicitamente.
- **Hosting: GitHub** (`github.com/BeppeTemp/cartographer`) come **unica** source of truth.
  Il repo Gitea si archivia (read-only); nessun mirror attivo.
- **History pubblica schiacciata**: il repo GitHub parte da un **singolo commit iniziale**
  (`feat: initial public release`) senza tag pregressi — la storia interna (146 commit con
  timestamp) non si espone; resta consultabile sul Gitea archiviato. Prassi comune di
  open-sourcing, scelta confermata con l'utente. Conseguenza: la versione pregressa
  (`2.1.2`) vive solo nel manifest di release-please, non in tag su GitHub.
- **Pipeline unica**: CI e release su GitHub Actions; immagine pubblica su
  `ghcr.io/beppetemp/cartographer`; l'homelab consuma ghcr. La CI Gitea si elimina.
- **Homebrew**: homebrew-core è precluso ai progetti nuovi (soglie di notorietà) → tap personale
  `BeppeTemp/homebrew-tap` automatizzato con **GoReleaser**; promozione a core in futuro.
- **Protezione main**: PR obbligatorie con status check CI verde, **senza bypass admin**,
  più blocco force-push/delete. Cambia il workflow quotidiano (niente commit diretti su main
  in questo repo).
- **Lingua: inglese ovunque** — docs/, commenti Go, script e fixture e2e, CLAUDE.md, skill.
  I contenuti futuri (piani, voci D) si scrivono in inglese.
- **Versionamento: release-please** (googleapis). I commit seguono già le conventional
  commits: il bump semver si calcola da lì (`feat:` ⇒ minor, `fix:` ⇒ patch,
  `feat!:`/`BREAKING CHANGE` ⇒ major), non più a giudizio per-release. Il bot mantiene una
  **release PR** con bump e `CHANGELOG.md` generati; il merge di quella PR crea tag e
  GitHub Release, e il tag innesca `release.yml` come già previsto. Il batching "si
  rilascia un batch, non ogni commit" resta: la release PR accumula finché non la si merge.
  Le PR si mergiano **squash-only** con titolo conventional (lintato in CI), così la
  history di main resta leggibile dal bot.

**Invarianti da preservare**: `make vet && make test` verdi a fine di ogni WP; la history
sul Gitea non si riscrive né si elimina (è l'archivio interno; verificato comunque: nessun
segreto reale, solo fixture di test); il server in produzione sull'homelab non deve avere
downtime (lo switch a ghcr avviene con la prima release post-migrazione).

Ordine dei WP: prima tutte le modifiche al repo in locale (WP1–WP6, commit diretti su main
finché si è ancora su Gitea), poi migrazione e protezione (WP7), release e homelab (WP8–WP9).

## WP1 `[dev]` — Licenza Apache-2.0

Obiettivo: sostituire MIT con Apache-2.0.

- `LICENSE`: testo integrale Apache License 2.0, copyright `2026 BeppeTemp`.
- `README.md:12`: badge `License-MIT` → `License-Apache_2.0`.
- Grep di `MIT` su tutto il repo (esclusi `bin/`, `go.sum`) per eventuali altri riferimenti.
- Licenze delle dipendenze: **già verificate** (charmbracelet MIT, yaml.v3 MIT+Apache,
  modernc.org/sqlite BSD-3 — tutte permissive, compatibili). Nessuna azione.

Accettazione: nessuna occorrenza residua di "MIT License" riferita al progetto.

## WP2 `[dev]` — Module path GitHub

Obiettivo: `go.mod:1` `module cartographer` → `module github.com/BeppeTemp/cartographer`.

- Aggiornare i ~74 file `.go` che importano `"cartographer/..."` (rename meccanico, poi `gofmt -w .`).
- `Makefile:9`, `Dockerfile:8,27`: gli ldflags `-X main.version` restano invariati
  (`main` non cambia path per il binario).

Accettazione: `make vet && make test` verdi, `go build ./...` pulito.

## WP3 `[dev]` — Bonifica riferimenti interni

Obiettivo: nessun endpoint privato nel repo pubblico.

- `install.sh` (righe 5, 15 e funzione `auth_curl`): scaricare dalle GitHub Releases
  (`https://api.github.com/repos/BeppeTemp/cartographer/releases/latest`, asset
  `cartographer-<os>-<arch>` + `sha256sums.txt` — naming garantito dal WP6);
  `GITEA_URL`/`GITEA_TOKEN` → `GITHUB_TOKEN` opzionale; header usage con l'URL
  `https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh`.
- `test/e2e/lib/sandbox.sh:35,88`: `https://headroom-proxy.beppetemp.com/v1` hardcoded →
  env `E2E_LLM_BASE_URL` obbligatoria; se assente lo scenario fallisce subito con messaggio
  chiaro ("set E2E_LLM_BASE_URL to an OpenAI-compatible endpoint"). Documentare in
  `test/e2e/README.md` (che a riga 24 cita headroom-proxy) e in `docs/testing.md`.
- `docs/deployment.md:221` (immagine) e `:320` (install URL), `docs/configurator.md:362`:
  → `ghcr.io/beppetemp/cartographer:<tag>` e URL GitHub.
- `.claude/skills/deploy/` **esce dal repo** (contiene il processo di rollout homelab):
  `git rm`, il contenuto viene riscritto fuori repo nel WP9. Aggiungere
  `.claude/skills/deploy/` a `.gitignore` così la copia locale non rientra.
- `.gitea/workflows/` si elimina (sostituito dal WP5). Il puntatore al manifest homelab
  (`k8s/namespaces/ai-tools/cartographer/deployment.yaml`, oggi a
  `.gitea/workflows/build-deploy.yaml:83`) serve al WP9: è già riportato lì.

Accettazione: `grep -ri 'beppetemp\.com\|10\.10\.0\.\|docker-registry' --exclude-dir=.git .`
restituisce solo `LICENSE`/nomi utente GitHub (`BeppeTemp` come owner va bene, i **domini** no).
Le fixture e2e (`kb-homelab-lite`) sono dati d'esempio: IP RFC1918 fittizi ammessi.

## WP4 `[dev]` — CI e release GitHub Actions + GoReleaser

Obiettivo: tre workflow in `.github/workflows/`, config GoReleaser e config release-please.

- `ci.yml`: trigger `pull_request` + `push` su `main`; due job:
  - `test`: `actions/setup-go` (Go 1.26, da `go.mod`), `make vet && make test`. Il nome
    del job (`test`) è lo status check richiesto dal ruleset del WP7.
  - `pr-title` (solo su `pull_request`): lint del titolo PR come conventional commit
    (`amannn/action-semantic-pull-request` o equivalente) — con squash-merge il titolo
    diventa il commit su main letto da release-please.
- `release-please.yml`: trigger `push` su `main`; `googleapis/release-please-action`,
  `release-type: go`. Config in root: `release-please-config.json` +
  `.release-please-manifest.json` **bootstrappato all'ultima versione taggata su Gitea**
  (oggi `2.1.2`, verificare con `git describe --tags --abbrev=0` al momento
  dell'implementazione). Su GitHub non esisteranno tag pregressi (history schiacciata):
  il manifest è l'unica fonte della versione corrente e release-please riparte da lì.
  Il bot apre/aggiorna la release PR (bump + `CHANGELOG.md`); al merge crea tag `vX.Y.Z`
  e GitHub Release con le note.
- `release.yml`: trigger tag `v*` (creato da release-please; resta valido anche per un
  tag manuale d'emergenza). Due job:
  1. **GoReleaser** (`goreleaser/goreleaser-action`): build `darwin/linux × amd64/arm64`,
     `CGO_ENABLED=0`, ldflags `-s -w -X main.version={{.Version}}` (coerente con
     `cmd/cartographer/main.go:14`), **formato `binary`** con naming
     `cartographer-<os>-<arch>` + `checksums.txt` chiamato `sha256sums.txt` (compatibilità
     con `install.sh` del WP3); sezione `brews` che pubblica la formula `cartographer` su
     `BeppeTemp/homebrew-tap` (directory `Formula/`), token `HOMEBREW_TAP_TOKEN` da secret.
     La GitHub Release la crea **release-please** con le note dal changelog: GoReleaser
     deve solo allegarci i binari (`release: mode: keep-existing`), non sovrascriverla.
  2. **Docker**: buildx multi-arch `linux/amd64,linux/arm64` dal `Dockerfile` esistente
     (già `CGO_ENABLED=0`), push su `ghcr.io/beppetemp/cartographer:<tag>` + `:latest`,
     login con `GITHUB_TOKEN` (permessi `packages: write`).
- File `.goreleaser.yaml` in root. Il target `test` del Dockerfile resta (usato in locale),
  ma la CI usa il toolchain nativo.
- `.github/dependabot.yml`: ecosistemi `gomod` e `github-actions`, cadenza settimanale,
  PR raggruppate (`groups`) per non inondare la release PR di release-please di voci
  `chore(deps)` singole.
- `README.md`: badge CI GitHub, sezione install aggiornata (`brew install beppetemp/tap/cartographer`
  come via primaria, `install.sh` da raw.githubusercontent come alternativa Linux/server).

Accettazione: `goreleaser check` pulito (o `goreleaser release --snapshot --clean` locale);
i workflow passano il lint di `actionlint` se disponibile, altrimenti review manuale.

## WP5 `[dev]` — File community e README da progetto pubblico

Obiettivo: superficie minima per contributor esterni e un README che comunichi a colpo
d'occhio cosa fa Cartographer.

README — rifare la parte alta (il resto — config, testing, structure — è già buono):
- **Pitch prima delle feature**: 3–4 righe problema → soluzione (l'agente perde il contesto
  tra sessioni / RAG stateless vs conoscenza che si accumula; il server garantisce le
  invarianti così l'agente non corrompe la KB). L'attuale "What is it" parte dal *cosa*,
  deve partire dal *perché*.
- **Demo visiva subito dopo il pitch**: GIF registrata con `vhs` (charmbracelet, coerente
  con lo stack): tape committata in `.github/vhs/demo.tape` + GIF generata committata
  (`docs/assets/demo.gif`). Contenuto: `cartographer` TUI dashboard + un giro di
  `connect`/`status`. La registrazione va rifatta a ogni cambio di UX rilevante — basta
  rilanciare la tape.
- **Quickstart install-first**: `brew install beppetemp/tap/cartographer` (poi install.sh
  e `go install` come alternative), `serve --init`, `connect` — l'attuale quickstart parte
  da `make build`, che diventa la sezione "Building from source".
- Diagramma architettura: convertire l'ASCII art in **Mermaid** (rende bene su GitHub,
  dark-mode aware); l'ASCII resta nei doc se serve.
- Badge: CI, release (versione corrente), Go Report Card, licenza (già previsto WP1).
- Rimuovere la nota "Most documentation is currently in Italian" (vero solo fino al WP6).

File community:

- `CONTRIBUTING.md`: prerequisiti (Go 1.26), `make build/test/vet/fmt`, com'è organizzato il
  codice (rimando a CLAUDE.md §Mappa e `docs/index.md`), flusso PR (fork → PR verso `main`,
  CI verde richiesta), convenzioni da `docs/conventions.md`. Niente CLA/DCO.
- `SECURITY.md`: segnalazioni private via GitHub Security Advisories, no issue pubbliche
  per vulnerabilità; scope: server HTTP con auth a token.
- In README o CONTRIBUTING una riga di **posture di manutenzione**: progetto personale
  mantenuto best-effort, issue e PR benvenute senza SLA di risposta — gestisce le
  aspettative dei futuri utenti.
- Issue template e CODE_OF_CONDUCT: **non** in questo piano (si aggiungono se/quando servono).

## WP6 `[dev]` — Traduzione in inglese e riorientamento della doc per utenti

Obiettivo: repo interamente in inglese **e** doc leggibile da un utente esterno, non solo
dal maintainer. È il WP più voluminoso: delegabile a più run `dev` in batch,
**un batch = un commit**, review del coordinatore su ciascuno.

La doc attuale è scritta per chi mantiene il progetto; un utente nuovo non ha un percorso
"da zero a prima sessione". Le pagine user-facing si **traducono e riorientano nello
stesso passaggio** (mai due riscritture della stessa pagina):

- **Nuova pagina `docs/getting-started.md`** (scritta direttamente in inglese): tutorial
  end-to-end del profilo Local Core — install, `serve --kb ~/my-kb --init`, `connect` di
  Claude Code, prima sessione con l'agente (search → concept_write → log_append), cosa
  guardare nella KB dopo. È la pagina linkata per prima dal README.
- **Riorientamento durante la traduzione**: `deployment.md`, `configurator.md`,
  `control-plane.md` (§API come reference consultabile), `data-plane.md` (spiegare
  atlas/map/journal a chi non conosce OKF) — audience: utente/operatore, non maintainer.
- **`docs/index.md` con due percorsi di lettura**: "Using Cartographer" (getting-started,
  deployment, configurator, control-plane, data-plane, skills) e "Internals &
  contributing" (conventions, testing, concurrency, decisions, roadmap). Le pagine
  interne si traducono senza riorientarle.

Perimetro e batch suggeriti:
1. `docs/` esclusa `decisions.md` (~2.000 righe: `configurator.md` 368, `deployment.md` 329,
   `sync.md` 184, `control-plane.md` 140, `testing.md` 133, `data-plane.md` 130 + 10 minori).
2. `docs/decisions.md` (1.352 righe, 78 voci D): tradurre integralmente — è storia pubblica
   del progetto, non può restare l'unico file in italiano.
3. Commenti nei `.go` (~30–50 file su 123 contengono italiano; le stringhe user-facing e i
   messaggi d'errore sono già in inglese — verificare con grep di parole italiane comuni:
   `\b(della|degli|nella|questo|già|perché|così)\b`).
4. `test/e2e/`: commenti degli script, `README.md`, e le fixture `kb-homelab-lite`
   (contenuti e mandato agente in `scenarios/02_read_write.sh:53`).
5. `CLAUDE.md` di progetto e `.claude/skills/plan/SKILL.md` (aggiornare anche la regola di
   lingua della skill: "piani in inglese"); `config.example.yaml` e `.env.example`
   (commenti già misti).

Vincoli: la traduzione **non cambia semantica né identificatori**; i termini OKF
(concept, map, atlas, journal) restano invariati; le voci D mantengono numerazione e titoli-àncora
(`## D<n>` — i riferimenti `Grep D<n>` nel CLAUDE.md devono continuare a funzionare).

Accettazione: grep delle parole italiane comuni (lista sopra) su `docs/`, `*.go`, `test/`
senza hit; `make test` verde (le fixture e2e tradotte sono usate dagli scenari —
`02_read_write.sh:80` asserisce su `10.10.0.1`, che non cambia).

## WP7 `[operatore]` — Migrazione GitHub e protezione main

Obiettivo: repo pubblico su GitHub, Gitea archiviato, main protetto. Da fare **dopo** che
WP1–WP6 sono commitati su main.

1. `gh repo create BeppeTemp/cartographer --public` (description dal README, topics:
   `mcp`, `go`, `knowledge-base`, `llm-agents`).
2. **Squash della history**: dalla working copy allineata all'ultimo commit su Gitea,
   creare un branch orfano (`git checkout --orphan <tmp>`) con l'intero albero in un
   **unico commit** `feat: initial public release` (messaggio conventional: è il primo
   commit che release-please leggerà). `git remote set-url origin
   git@github.com:BeppeTemp/cartographer.git` e push del branch orfano come `main`.
   **Nessun tag** viene pushato. Il main locale si resetta sul nuovo main GitHub; la
   history completa resta raggiungibile via remote Gitea (archiviato al passo 7).
3. Secret di repo: `HOMEBREW_TAP_TOKEN` (PAT fine-grained con write su
   `BeppeTemp/homebrew-tap`) e `RELEASE_PLEASE_TOKEN` (PAT fine-grained con
   contents+pull-requests write su cartographer, usato da `release-please.yml`:
   le PR aperte col `GITHUB_TOKEN` di default **non innescano** i workflow
   `pull_request`, e la release PR resterebbe bloccata senza il check `test`).
   Creare prima il repo tap: pubblico, vuoto, con directory `Formula/` e un README
   di una riga.
4. Impostazioni merge del repo: **solo squash-merge** (titolo PR come messaggio del
   commit), disabilitare merge commit e rebase-merge.
5. Verificare che `ci.yml` giri verde su main.
6. Ruleset su `main`: require pull request (0 approvazioni: maintainer unico, la review
   la fa la CI), require status check `test`, block force pushes, restrict deletions,
   **nessun bypass** per gli admin.
6-bis. Security del repo (Settings → Code security): abilitare **secret scanning con
   push protection** e **Dependabot alerts** (gli update automatici arrivano dal
   `dependabot.yml` del WP4).
7. Gitea: repo `BeppeTemp/cartographer` → Settings → Archive (read-only, la history resta
   consultabile internamente).
8. Fuori repo (coordinatore): annotare nel CLAUDE.md **globale** (`~/.claude/CLAUDE.md`,
   regola "Git: si lavora su main") l'eccezione: *cartographer richiede PR — branch
   `feat/<slug>` + `gh pr create` + merge a CI verde*.

Accettazione: `gh repo view` mostra il repo pubblico; un push diretto su main viene
rifiutato; una PR di prova con CI verde è mergiabile.

## WP8 `[operatore]` — Prima release pubblica

Obiettivo: validare l'intera pipeline di release.

- La release parte **mergiando la release PR** aperta da release-please: con la history
  schiacciata l'unico commit visibile è `feat: initial public release`, quindi il bump
  atteso dal manifest `2.1.2` è minor ⇒ **`v2.2.0`**. Se il calcolo non tornasse, forzare
  con un commit vuoto con footer `Release-As: 2.2.0` — non taggare a mano.
- Verificare a valle: release GitHub con i 4 binari + `sha256sums.txt`; formula aggiornata
  nel tap (`brew install beppetemp/tap/cartographer` da una macchina pulita, o
  `brew reinstall`); immagine `ghcr.io/beppetemp/cartographer:<tag>` pullabile **senza
  auth** (il primo push crea il package come privato: renderlo pubblico da
  Settings → Packages).
- `curl -fsSL https://raw.githubusercontent.com/BeppeTemp/cartographer/main/install.sh | sh -s -- update`
  funziona.

## WP9 `[operatore]` — Switch homelab a ghcr e nuova skill deploy

Obiettivo: l'homelab consuma l'immagine pubblica; il flusso di rilascio interno è documentato
fuori dal repo pubblico.

- `homelab-manifests` → `k8s/namespaces/ai-tools/cartographer/deployment.yaml`: image
  `docker-registry.beppetemp.com/cartographer:<old>` → `ghcr.io/beppetemp/cartographer:<tag WP8>`;
  commit, riconciliazione Flux (`flux-system/ai-tools`), verifica rollout con `kubectl`.
- Riscrivere la skill deploy **in locale, non versionata** (`.claude/skills/deploy/`,
  ora ignorata dal WP3): nuovo flusso = merge della release PR di release-please →
  attesa release.yml (binari, brew, ghcr) → bump manifest in homelab-manifests →
  verifica Flux/rollout → update client locale via brew. La tabella di classificazione
  semver sparisce dalla skill: il bump lo calcola release-please dai conventional commits.
- Ritirare l'immagine cartographer dal registry privato (cleanup, non bloccante).

## Chiusura

- [ ] `docs/decisions.md`: voce **D79** (licenza, hosting, pipeline, brew, protezione main,
      lingua — le decisioni della sezione Contesto, in inglese come da WP6).
- [ ] `docs/deployment.md`: stato finale (ghcr, GitHub Releases, niente registry privato);
      `docs/configurator.md` §install; `docs/testing.md` (env `E2E_LLM_BASE_URL`);
      `docs/index.md` se cambia la mappa; `docs/roadmap.md` (milestone pubblicazione).
- [ ] CLAUDE.md di progetto: riga workflow release (skill deploy ora locale) e lingua.
- [ ] Wiki homelab (`concept_write` su `ai-tools/` + `log_append`): migrazione hosting e
      nuovo flusso di deploy di Cartographer.
- [ ] **Eliminare questo file** nel commit di chiusura docs (via PR, ormai).
- [ ] (Opzionale, post-pubblicazione) Distribuzione nell'ecosistema MCP: submission al
      registry ufficiale MCP e alle liste `awesome-mcp-servers` — è il canale principale
      con cui gli utenti target scoprono un server MCP.
