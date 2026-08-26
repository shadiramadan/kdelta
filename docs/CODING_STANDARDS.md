# Coding Standards

These apply to all code in the repo, human- or agent-written.

## General

- **Self-describing code.** Prefer names and structure that make the code
  explain itself over comments that do it for the code. If a block needs a
  comment to be understood, first try renaming or extracting.
- **Comments document the non-obvious only**: invariants, constraints, tradeoffs,
  and "why", never "what the next line does". The exception is exported
  APIs/interfaces, which always get doc comments — they document the contract,
  not the implementation.
- **Small, focused changes.** One concern per commit, following
  [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`,
  `docs:`, `chore:`, `refactor:`, `test:`, `ci:`, `build:`). release-please
  derives versions and changelogs from these.
- **Generated code is never edited by hand.** Change `proto/` and run
  `task generate`. Generated code is committed; CI fails if it drifts.

## Go

- Follow [Effective Go](https://go.dev/doc/effective_go) and the
  [Google Go style guide](https://google.github.io/styleguide/go/).
- Package names are short, lower-case, and don't stutter (`detect.Registry`,
  not `detect.DetectRegistry`).
- Accept interfaces, return structs. Keep interfaces small and defined where
  they are consumed.
- Wrap errors with context: `fmt.Errorf("scanning namespace %q: %w", ns, err)`.
  No naked `err` returns across package boundaries when context would help.
- `context.Context` is the first parameter of anything that does I/O.
- Table-driven tests with subtests (`t.Run`). Tests live next to the code.
- Linting/formatting is golangci-lint (config in `.golangci.yml`) — run
  `task lint`. Formatting is not negotiable; run `task fmt`.

## TypeScript / React

- Strict TypeScript. No `any` (use `unknown` and narrow); no non-null assertions
  where a guard is possible.
- Function components and hooks only. Server components by default in the app
  router; add `"use client"` only where interactivity requires it.
- Colocate components with their usage until shared; promote to `packages/ui`
  only when actually used by more than one app.
- Data fetching goes through the generated contracts in `packages/api` —
  unary RPCs via connect-query hooks (`useQuery`/`useMutation` with the
  service's method descriptors). Server-streaming RPCs use TanStack's
  `experimental_streamedQuery` wrapped around the transport's stream call
  (the composition the connect-query maintainers endorse until their
  streaming API ships): keys via `createConnectQueryKey`, a `reducer`
  folding progress events into a fixed-shape value (never the default
  unbounded chunk array for progress streams), `refetchMode: "reset"`, and
  the query context's signal passed through for cancellation. Never
  hand-rolled `fetch` against RPC routes.
- Tailwind utilities over bespoke CSS; design tokens live in `@kdelta/theme`
  (CSS-first `@theme`, shadcn-compatible variables) — never hard-code palette
  colors in app code when a semantic token exists.
- Linting is [oxlint](https://oxc.rs/) (`apps/app-ui/.oxlintrc.json`) with
  type-aware rules via oxlint-tsgolint; `tsc --noEmit` still runs as the
  typecheck gate. There is no ESLint in this repo.

## Protobuf

- Follow [buf's style guide](https://buf.build/docs/best-practices/style-guide)
  (enforced by `buf lint`, `STANDARD` rules).
- All request/response fields carry
  [protovalidate](https://github.com/bufbuild/protovalidate) rules — validation
  lives in the schema, not in handlers.
- Breaking changes are checked with `buf breaking` against `main`
  (`task breaking` locally; CI runs it on every pull request).

## Testing

- `task test` runs everything and produces coverage (`coverage/`).
- New behavior comes with tests; bug fixes come with a regression test.
- Don't test generated code or trivial glue; test detectors, resolvers, and
  handlers through their public surface.
