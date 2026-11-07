# Real E2E image. Builds homonto, onto and to from source, then installs the
# ACTUAL OpenCode and Claude Code CLIs via their official installers, so the
# suites exercise
# homonto's projection and onto's workflow against the real tool. Credentials
# are never baked in — they are mounted read-only at run time
# by scripts/e2e-matrix.sh. Built once, run per matrix cell via entrypoint.sh.
FROM golang:1.26.10-bookworm

# Runtime deps: curl for the installer, jq/python3 for the suite assertions,
# git for onto's dirty-worktree checks, unzip for the opencode installer.
RUN apt-get update && apt-get install -y --no-install-recommends \
      curl ca-certificates git jq python3 unzip \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

# Build both binaries from the full source tree. The build context has no .git,
# so scripts/e2e-matrix.sh passes the checkout's `git describe` as
# HOMONTO_VERSION; suites that record evidence name it via `homonto version`.
COPY . .
ARG HOMONTO_VERSION=
RUN ldflags=""; [ -z "$HOMONTO_VERSION" ] || ldflags="-X github.com/Novirmor/homonto/internal/cli.Version=$HOMONTO_VERSION"; \
    go build -ldflags "$ldflags" -o /usr/local/bin/homonto . \
    && go build -o /usr/local/bin/onto ./cmd/onto \
    && go build -o /usr/local/bin/to ./cmd/to

# --- install the real tool (native installer, no npm needed) -----------------
ENV HOME=/root
# OpenCode installer -> /root/.opencode/bin/opencode
# OpenCode 2 (the only supported line, ADR 0079/0081), pinned to the verified release.
RUN curl -fsSL https://opencode.ai/v2/install | bash -s -- --version 2.2.5
# Claude Code native installer -> /root/.local/bin/claude
RUN curl -fsSL https://claude.ai/install.sh | bash
ENV PATH="/root/.opencode/bin:/root/.local/bin:${PATH}"

# Verify all three binaries resolve at build time (fail fast on a broken image).
RUN homonto version && onto version \
    && opencode --version && claude --version

# The reference extension set's published release pins. The reference suite
# installs that release over https as remote: sources, not this tree's copy.
COPY examples/reference-set/published.toml /opt/reference/published.toml

COPY test/e2e/entrypoint.sh /usr/local/bin/e2e-entrypoint
COPY test/e2e/suites /opt/e2e-suites
RUN chmod +x /usr/local/bin/e2e-entrypoint /opt/e2e-suites/*.sh

ENTRYPOINT ["/usr/local/bin/e2e-entrypoint"]
