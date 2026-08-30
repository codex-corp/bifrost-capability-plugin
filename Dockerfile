# syntax=docker/dockerfile:1

FROM debian:trixie-slim

ARG RELEASE_TAG=dev
ARG BIFROST_VERSION=v2.0.0
ARG VCS_REF=unknown

LABEL org.opencontainers.image.title="Bifrost Capability Router"
LABEL org.opencontainers.image.description="ABI-matched Bifrost runtime and capability-router plugin"
LABEL org.opencontainers.image.source="https://github.com/codex-corp/bifrost-capability-plugin"
LABEL org.opencontainers.image.version="${RELEASE_TAG}"
LABEL org.opencontainers.image.revision="${VCS_REF}"
LABEL io.codex-corp.bifrost.version="${BIFROST_VERSION}"

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 bifrost \
    && useradd --uid 10001 --gid bifrost --create-home --home-dir /home/bifrost bifrost \
    && install -d -o bifrost -g bifrost /app/data /opt/bifrost/plugins

COPY --chmod=0755 .build/matched/bifrost-http /usr/local/bin/bifrost-http
COPY --chmod=0644 .build/matched/agent-capability-router.so /opt/bifrost/plugins/agent-capability-router.so
COPY --chmod=0644 .build/matched/SHA256SUMS /opt/bifrost/SHA256SUMS
COPY --chmod=0644 .build/matched/compatible-runtime.json /opt/bifrost/compatible-runtime.json
COPY --chmod=0644 .build/matched/isolated-test-result.json /opt/bifrost/isolated-test-result.json

USER bifrost
WORKDIR /app/data

ENV BIFROST_HOST=0.0.0.0

VOLUME ["/app/data"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD curl -fsS http://127.0.0.1:8080/health >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/bifrost-http"]
CMD ["-host", "0.0.0.0", "-port", "8080", "-app-dir", "/app/data"]
