# Hosting sandbox image: the real-tool E2E image (test/e2e/Dockerfile) plus the
# host CLIs and Node for the runtime driver. Built by scripts/hosting-sandbox.sh
# on top of the base image; credentials are passed at run time, never baked in.
ARG BASE=homonto-e2e
FROM ${BASE}

ARG GH_VERSION=2.92.0
ARG GLAB_VERSION=1.123.0
ARG NODE_VERSION=24.16.0
RUN set -eux; \
    curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_amd64.tar.gz" \
      | tar -xz -C /tmp && install /tmp/gh_${GH_VERSION}_linux_amd64/bin/gh /usr/local/bin/gh; \
    curl -fsSL "https://gitlab.com/api/v4/projects/gitlab-org%2Fcli/packages/generic/glab/${GLAB_VERSION}/glab_${GLAB_VERSION}_linux_amd64.tar.gz" \
      | tar -xz -C /tmp && install /tmp/bin/glab /usr/local/bin/glab; \
    curl -fsSL "https://nodejs.org/dist/v${NODE_VERSION}/node-v${NODE_VERSION}-linux-x64.tar.xz" \
      | tar -xJ -C /opt && ln -s /opt/node-v${NODE_VERSION}-linux-x64/bin/node /usr/local/bin/node; \
    gh --version && glab --version && node --version

COPY test/e2e/hosting /opt/e2e-hosting
COPY test/e2e/suites /opt/e2e-suites
RUN chmod +x /opt/e2e-suites/*.sh
