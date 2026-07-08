# agent-base — building block, NO entrypoint, NO runtime.
# The entrypoint lives on agent-base-goose (or future agent-base-<runtime>
# images) because konveyor-harness requires a runtime to launch.
FROM registry.access.redhat.com/ubi9/ubi-minimal:latest

RUN microdnf install -y git jq curl python3 python3-pip && microdnf clean all

# graphify (required by konveyor-detect)
RUN pip3 install --no-cache-dir graphifyy==0.7.17

# skillctl (skill discovery, from the skillimage project)
RUN curl -fsSL <skillctl-release-url> -o /usr/local/bin/skillctl \
    && chmod +x /usr/local/bin/skillctl

# Go binaries (built in CI via `go build ./cmd/...`, copied in from bin/)
COPY bin/konveyor-clone      /usr/local/bin/
COPY bin/konveyor-push       /usr/local/bin/
COPY bin/konveyor-configure  /usr/local/bin/
COPY bin/konveyor-detect     /usr/local/bin/
COPY bin/konveyor-results    /usr/local/bin/
COPY bin/konveyor-harness    /usr/local/bin/

# GIT_ASKPASS helper — enables konveyor-clone/konveyor-push to
# authenticate without embedding credentials in the git remote URL or
# passing them via argv. This is part of the credential-isolation
# design: the agent runtime (goose, installed in agent-base-goose) must
# never receive git push credentials in its own environment — see
# internal/git.FilterCredentials, used by cmd/harness/main.go, and the
# design spec's credential handling section.
COPY scripts/git-askpass.sh /usr/local/bin/git-askpass.sh
RUN chmod +x /usr/local/bin/git-askpass.sh

# Pipeline skills — baked in (POC deviation from PR #296's "never baked
# in" model, see design spec's Key Decisions and Open Questions)
COPY skills/ /opt/skills/

# Writable dirs for non-root (OpenShift restricted SCC compatible)
RUN mkdir -p /workspace /.konveyor \
    && chmod 775 /workspace /.konveyor \
    && chgrp -R 0 /workspace /.konveyor \
    && chmod -R g=u /workspace /.konveyor

WORKDIR /workspace
# No ENTRYPOINT here — this image has no runtime to launch.
