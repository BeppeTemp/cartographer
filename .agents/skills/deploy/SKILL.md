---
name: deploy
description: Rilascia una nuova versione di Cartographer — merge della release PR di release-please, attesa della pipeline GitHub (binari, brew, ghcr), bump del manifest homelab, verifica rollout Flux e update del client locale via brew. Usare quando l'utente chiede di deployare/rilasciare una nuova versione di server e/o client.
---

# Deploy — release di server e client

Cartographer è open source su `github.com/BeppeTemp/cartographer`. Il bump semver lo calcola **release-please** dai conventional commits (`feat:` ⇒ minor, `fix:` ⇒ patch, `feat!:`/`BREAKING CHANGE` ⇒ major): niente più classificazione manuale né tag a mano. Il bot mantiene una **release PR** aperta che accumula i commit; mergiarla crea tag + GitHub Release, e il tag innesca `release.yml` (GoReleaser: 4 binari + `sha256sums.txt`, cask nel tap `BeppeTemp/homebrew-tap`, immagine `ghcr.io/beppetemp/cartographer`). Il batching resta: si rilascia mergiando la release PR, non a ogni commit.

L'homelab **non** si aggiorna da solo: il bump del manifest è manuale (passo 4).

## Procedura

1. **Precondizioni**:
   - `main` locale allineato a `origin` (GitHub), `make vet && make test` verdi;
   - doc aggiornata nella stessa sessione delle modifiche.
2. **Merge della release PR** — se non esiste, non c'è nulla di releasable dall'ultima release:
   ```bash
   gh pr list -R BeppeTemp/cartographer --author app/github-actions --search "release" --state open
   gh pr checks <n> -R BeppeTemp/cartographer        # test deve essere verde
   gh pr merge <n> -R BeppeTemp/cartographer --squash
   ```
   Se il bump calcolato è sbagliato, non taggare a mano: commit vuoto con footer `Release-As: X.Y.Z` e attendere che il bot aggiorni la PR.
3. **Segui la pipeline** (release-please crea tag+release → parte `release.yml`):
   ```bash
   gh run list -R BeppeTemp/cartographer --workflow release.yml --limit 1
   gh release view -R BeppeTemp/cartographer --json tagName,assets -q '{tag: .tagName, assets: [.assets[].name]}'
   ```
   Attesi: 4 binari + `sha256sums.txt`; commit "Brew cask update" su `BeppeTemp/homebrew-tap`; manifest `ghcr.io/v2/beppetemp/cartographer/manifests/vX.Y.Z` pullabile senza auth.
4. **Bump del manifest homelab** (repo `~/Documents/Repos/HomeLab/homelab-manifests`):
   `k8s/namespaces/ai-tools/cartographer/deployment.yaml` → `image: ghcr.io/beppetemp/cartographer:vX.Y.Z`; commit + push, poi Flux riconcilia (auto entro l'intervallo; per accelerare serve conferma dell'utente: `flux reconcile kustomization ai-tools --with-source -n flux-system`).
   ```bash
   kubectl -n ai-tools rollout status deploy/cartographer --timeout=180s
   kubectl -n ai-tools get deploy cartographer -o jsonpath='{.spec.template.spec.containers[0].image}'
   ```
5. **Aggiorna il client locale** (via brew, il cask è nel tap):
   ```bash
   brew update && brew upgrade --cask beppetemp/tap/cartographer
   cartographer version   # deve stampare vX.Y.Z
   ```
6. **Report finale**: tag = immagine sul cluster = `cartographer version` locale. Se uno dei tre non è allineato, dillo esplicitamente e indaga prima di chiudere.

## Guasti tipici

- **La release PR non si aggiorna / i check non partono**: `RELEASE_PLEASE_TOKEN` scaduto o revocato (PAT fine-grained `cartographer-release-please`, Contents+PR write su cartographer) — rigenerarlo e `gh secret set RELEASE_PLEASE_TOKEN -R BeppeTemp/cartographer`.
- **`release.yml` fallisce sul tap**: `HOMEBREW_TAP_TOKEN` scaduto (PAT `cartographer-homebrew-tap`, Contents write su homebrew-tap) — stessa cura.
- **Immagine sul cluster resta vecchia**: verificare il commit di bump su `homelab-manifests`, poi `flux get kustomizations` per errori di riconciliazione.
- **`cartographer version` resta vecchio**: più copie nel PATH (`which -a cartographer`) — deve restare solo `/opt/homebrew/bin/cartographer`.
