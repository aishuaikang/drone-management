# Repository Guidelines

## Project Structure & Module Organization

This repository contains a Go backend and a React/Vite frontend.

- `cmd/api/` is the backend entry point.
- `internal/` contains backend packages such as `httpapi`, `position`, `fpv`, `interference`, `lingyun`, `settings`, `store`, and `model`.
- `frontend/src/` contains the React application, API client, shared TypeScript types, styles, components, and UI assets.
- `scripts/` contains release/build helpers.
- `docs/`, the protocol documentation directory, and `README.md` hold product and protocol documentation.
- Tests live next to Go packages as `*_test.go`.

## Build, Test, and Development Commands

- `go run ./cmd/api` starts the backend API and TCP listeners locally.
- `go test ./internal/...` runs backend package tests. On Windows permission issues, use a local cache: `GOCACHE=%CD%\tmp\gocache go test ./internal/...`.
- `cd frontend && npm install` installs frontend dependencies.
- `cd frontend && npm run dev` starts the Vite dev server on `127.0.0.1`.
- `cd frontend && npm run build` runs TypeScript build checks and creates the production frontend bundle.
- `scripts/build-release.sh` builds release packages for configured target platforms.

## Coding Style & Naming Conventions

Format Go code with `gofmt`; keep packages small and domain-focused under `internal/`. Use exported Go names only for cross-package API. Prefer table-driven tests for parser, payload, and service behavior.

Frontend code uses TypeScript, React function components, and Vite. Keep shared interfaces in `frontend/src/types.ts`, HTTP calls in `frontend/src/api.ts`, and reusable UI in `frontend/src/components/`. Use descriptive camelCase names for variables/functions and PascalCase for React components.

## Testing Guidelines

Use Go's standard `testing` package. Test files should be named `*_test.go`, with test functions named `Test<Behavior>`. Add focused tests for protocol payloads, parsers, stores, and HTTP handlers when behavior changes. For frontend changes, at minimum run `npm run build`; add component-level tests only if a test framework is introduced.

## Commit & Pull Request Guidelines

Recent history uses concise imperative subjects, sometimes with prefixes such as `feat:` or `docs:`. Keep commits scoped, for example `feat: add network status ping logs`.

Pull requests should include a short summary, test results, linked issue or context, and screenshots for UI changes. Document any configuration, protocol, or migration impact explicitly.

## Security & Configuration Tips

Do not commit real credentials, MQTT passwords, license files, or generated private keys. Use `.env.example` as the reference for runtime configuration. Treat `data/`, `tmp/`, generated bundles, and local broker credentials as environment-specific artifacts.
