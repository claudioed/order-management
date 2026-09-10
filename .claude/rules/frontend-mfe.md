# Frontend micro-frontend remote (`web/`)

This repo also owns `web/`: **`order-mgmt-mfe`**, a Vite + React Module
Federation **remote** consumed by the separate `warehouse-console` shell
repo (ADR-0007). It is a plain browser client of this service's own REST
API — nothing in `web/` talks to any other bounded context, and nothing in
`internal/` knows `web/` exists.

- Screens here (order search/lookup) are this repo's own responsibility to
  design and maintain, same as the REST API itself.
- Own `package.json`, build, and dev server (`:5181`).
- Does **not** participate in this repo's Go quality gate (`make
  check`/`check-all`) and is **not** part of the Go module.
- See ADR-0002 in `warehouse-ops-agent`'s docs (the fleet-wide canonical
  MFE-architecture record — note this is a DIFFERENT "ADR-0002" than this
  repo's own inventory-storage/wes-work-planning boundary ADR, see
  `adrs.md`) and this repo's own adoption record, ADR-0007.
- CORS: `CORS_ALLOWED_ORIGINS` (env, default
  `http://localhost:5173,http://localhost:5181`) is the ONLY behavior
  change in `internal/` for this adoption — see `api-contracts.md`.
- Order Management is the **first hop** in the Order Lifecycle fan-out:
  `warehouse-console`'s BFF calls `GET /orders/{id}` directly — no new
  lookup-by-reference endpoint was needed here (unlike some sibling
  services), since `GET /orders/{id}` already returns everything the BFF
  needs.
