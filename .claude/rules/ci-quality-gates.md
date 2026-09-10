# CI / quality gates

## GitHub Actions workflows (`.github/workflows/`)

### `ci.yml` — triggers on push/PR to `main`, `develop`

- **`lint`** — golangci-lint v2.13.1, `--timeout=5m`.
- **`test`** — build, vet, gofmt check, `go test ./... -race
  -coverprofile=coverage.out -coverpkg=./internal/domain/...,./internal/application/...`,
  then a 90% coverage gate on that coverprofile.
- **`integration`** — `go build/vet -tags=integration ./...` then
  `go test -tags=integration ./internal/adapters/outbound/kafka` against a
  **Testcontainers** Kafka broker (no external Kafka service in this
  workflow — do not write a `KAFKA_BROKERS`-skip-gated test, it silently
  no-ops in CI).
- **`helm-lint`** — `ct lint --charts charts/order-management` — runs ONLY
  for a `pull_request` with `base_ref == 'main'` (i.e. the develop→main
  release PR), never on develop pushes/PRs.
- **`trivy-scan`** — builds the image (no push), blocks on CRITICAL/HIGH
  with a known fix — same `base_ref == 'main'` gating as `helm-lint`.
  Always uploads SARIF regardless of pass/fail.
- **`docker-publish`** — push-to-`main` only, needs `[lint, test]`. Builds,
  pushes to GHCR, cosign-signs keylessly, generates + attests an SPDX SBOM.
- **`release`** — push-to-`main` only, needs `[docker-publish]`. Auto-
  increments `vX.Y.Z` (patch), tags the already-published image, packages
  and pushes the Helm chart to `oci://ghcr.io/claudioed`, creates the git
  tag + GitHub Release.

Note: `bdd`, `mutation`/`mutation-fast`, `vuln`/`govulncheck`, `api-lint`
(Spectral), and `arch-test` jobs are referenced by name in project history
(e.g. the Makefile header comment, `.gremlins.yaml`) as part of the CI
sensor set — **verify their exact job names and current wiring directly in
`.github/workflows/ci.yml` before relying on this list**, since job
composition has moved between commits and this file can drift.

### `docs.yml` — Docusaurus site build + GitHub Pages deploy

- **Trigger: `push: branches: [main]`, paths `["docs/**",
  ".github/workflows/docs.yml"]`, plus `workflow_dispatch`.** It does
  **NOT** trigger on `develop` pushes or on PRs at all — only a push to
  `main` (i.e. after a release PR merges) that touches `docs/**` deploys
  the live site at `https://claudioed.github.io/order-management/`.
- Job: `npm ci && npm run build` in `docs/`, uploads `docs/build` as a
  Pages artifact, then deploys via `actions/deploy-pages`.
- **This means a `docs/docs/api-reference/**` regeneration PR merged into
  `develop` alone does NOT redeploy the live site** — it only takes effect
  once `develop` promotes to `main` per the fleet's GitFlow release process.

### `codeql.yml` / `scorecard.yml`

Standard security scanning, `codeql.yml` on push/PR to `main`+`develop`
plus a weekly schedule; `scorecard.yml` on push to `main` plus weekly.

## OpenAPI docs-drift check & regeneration procedure

The Docusaurus API-reference pages (`docs/docs/api-reference/rest/*.api.mdx`)
are **generated artifacts**, not hand-edited files — each embeds a
compressed, base64-encoded copy of the relevant OpenAPI operation object in
its `api:` frontmatter field. They can go stale silently: a commit that
edits `apis/openapi.yaml` but forgets to re-run generation leaves committed
`.mdx` files whose embedded payload no longer matches the spec (verified
concretely: PR removing `security: []`/`securitySchemes.bearerAuth` from
`apis/openapi.yaml` did NOT regenerate the 5 `.api.mdx` files, leaving a
stale `"security": []` embedded in each — caught only by diffing the
decoded embedded payload, not by any file-timestamp or line-count check).

**To check for drift, don't just diff a fresh `gen-api-docs` run against
committed files** — a plain re-run without first cleaning can look
identical even when stale, because the generator sometimes preserves
untouched fields. The reliable check is:

```bash
cd docs
npm ci
npm run clean-api-docs   # removes every generated file first
npm run gen-api-docs     # docusaurus gen-api-docs order — regenerates from apis/openapi.yaml
cd ..
git status --short docs/docs/api-reference   # non-empty => real drift existed
```

If drift is found, regenerate (the steps above already did it), then:

```bash
cd docs && npm run build   # onBrokenLinks / onBrokenAnchors are both 'throw' — must pass clean
cd ..
git add docs/docs/api-reference
git commit -m 'docs: regenerate OpenAPI reference pages from apis/openapi.yaml'
git push -u origin <branch>
gh pr create --base develop --head <branch> --title '...' --body-file <file>
```

Never merge the PR yourself.

## AsyncAPI narrative-docs staleness

This repo does **not** run the AsyncAPI static-site generator (that is
reserved for the fleet aggregator repo per the fleet's docs convention).
Instead, `apis/asyncapi.yaml`'s channels/events are documented **narratively**
in plain markdown under `docs/docs/ddd/domain-events.md`,
`docs/docs/analytics/order-funnel-report.md`, and the relevant ADRs
(0005, 0006, 0008). When editing `apis/asyncapi.yaml` (new channel, renamed
event, changed payload shape), grep those narrative docs for the old
name(s) and update them by hand — there is no automated generation or CI
gate to catch this drift, unlike the OpenAPI case above.
