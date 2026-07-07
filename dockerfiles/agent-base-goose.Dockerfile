# agent-base-goose — extends agent-base with the goose runtime and sets
# the entrypoint. This is the image the POC actually deploys.
FROM localhost/agent-base:latest

RUN microdnf install -y bzip2 && microdnf clean all \
    && ARCH=$(uname -m) \
    && if [ "$ARCH" = "x86_64" ]; then GOOSE_ARCH="x86_64-unknown-linux-gnu"; \
       elif [ "$ARCH" = "aarch64" ]; then GOOSE_ARCH="aarch64-unknown-linux-gnu"; \
       else echo "Unsupported architecture: $ARCH" && exit 1; fi \
    && curl -fsSL "https://github.com/block/goose/releases/download/stable/goose-${GOOSE_ARCH}.tar.bz2" -o /tmp/goose.tar.bz2 \
    && tar -xjf /tmp/goose.tar.bz2 -C /usr/local/bin \
    && chmod +x /usr/local/bin/goose \
    && rm -f /tmp/goose.tar.bz2

ENTRYPOINT ["konveyor-harness"]
CMD ["run"]
