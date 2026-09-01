# Triple-binary end-to-end image: builds the real homonto, onto, and to binaries
# and runs diagnosable suites against a throwaway $HOME inside the container,
# so the host system is never touched. Built and run by scripts/docker-test.sh
# (and CI). The release-packaging suite cross-compiles every target, so the image
# carries zip (windows archives) and git (onto's worktree checks).
FROM golang:1.23

RUN apt-get update && apt-get install -y --no-install-recommends zip git \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

# Cache module downloads separately from source.
COPY go.mod go.sum ./
RUN go mod download

# Build all three binaries from the full source tree.
COPY . .
RUN go build -o /usr/local/bin/homonto . \
    && go build -o /usr/local/bin/onto ./cmd/onto \
    && go build -o /usr/local/bin/to ./cmd/to

ENTRYPOINT ["sh", "/src/test/docker/run-all.sh"]
