# CLAUDE.md

Follow [AGENTS.md](AGENTS.md) — it is the canonical agent guide for this repo.

Non-negotiables, restated:

- Never commit or push unless explicitly asked.
- API/model changes start in `proto/`; run `task generate`; never edit
  generated code (`gen/`, `packages/api/src/gen/`).
- Use Taskfile targets for every build/test/lint step (`task --list`).
- Keep [docs/ROADMAP.md](docs/ROADMAP.md) current: it lists only unimplemented
  work — remove what ships, add what's deferred.
