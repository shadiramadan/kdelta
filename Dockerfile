# syntax=docker/dockerfile:1
# Multi-stage build on Chainguard images. The free tier only publishes
# :latest/:latest-dev tags - pin by digest via automation (digestabot/Renovate)
# rather than version tags. Generated code (gen/, packages/api/src/gen/) is
# committed, so no buf/codegen step runs here.

FROM cgr.dev/chainguard/node:latest-dev AS ui-build
USER root
RUN npm install -g pnpm@10.28.0
USER node
WORKDIR /app
COPY --chown=node:node package.json pnpm-lock.yaml pnpm-workspace.yaml turbo.json ./
COPY --chown=node:node apps/app-ui/package.json apps/app-ui/
COPY --chown=node:node packages/api/package.json packages/api/
COPY --chown=node:node packages/theme/package.json packages/theme/
COPY --chown=node:node packages/ui/package.json packages/ui/
RUN pnpm install --frozen-lockfile
COPY --chown=node:node apps/app-ui/ apps/app-ui/
COPY --chown=node:node packages/api/ packages/api/
COPY --chown=node:node packages/theme/ packages/theme/
COPY --chown=node:node packages/ui/ packages/ui/
RUN pnpm --filter @kdelta/app-ui build

FROM cgr.dev/chainguard/go:latest AS go-build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
# .git is included in the context (see .dockerignore) so Go's built-in VCS
# stamping versions the binary; -buildvcs=true fails loudly if that breaks.
COPY . .
COPY --from=ui-build /app/apps/app-ui/out internal/ui/dist
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=true -ldflags "-s -w" -o /out/kdelta .

# The Claude Code CLI powers the agent runner (Claude Agent SDK harness);
# installed in its own stage and copied into the runtime image.
FROM cgr.dev/chainguard/wolfi-base:latest AS claude-cli
RUN apk add --no-cache curl bash ca-certificates && adduser -D claude
USER claude
WORKDIR /home/claude
RUN curl -fsSL https://claude.ai/install.sh | bash

# Runtime: wolfi-base (glibc) because the claude binary needs it; kdelta
# itself stays a static binary. HOME must be writable at runtime (the CLI
# keeps state in ~/.claude) - the deployment mounts an emptyDir there.
FROM cgr.dev/chainguard/wolfi-base:latest
RUN apk add --no-cache ca-certificates && mkdir -p /home/nonroot && chown 65532:65532 /home/nonroot
COPY --from=go-build /out/kdelta /usr/bin/kdelta
# Same path as the install stage: the installer's launcher symlink is absolute.
COPY --from=claude-cli --chown=65532:65532 /home/claude/.local /home/claude/.local
ENV PATH="/home/claude/.local/bin:${PATH}" \
    HOME=/home/nonroot \
    DISABLE_AUTOUPDATER=1
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/bin/kdelta"]
CMD ["serve", "--bind", ":8080"]
