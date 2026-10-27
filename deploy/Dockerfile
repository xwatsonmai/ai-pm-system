# Build context is the release bundle produced by deploy/package.sh.
ARG NODE_IMAGE=node:22.23.2-bookworm-slim
FROM ${NODE_IMAGE}
ARG APT_MIRROR=http://deb.debian.org/debian
ARG APT_SECURITY_MIRROR=http://deb.debian.org/debian-security
RUN sed -i "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g; s|http://deb.debian.org/debian$|${APT_MIRROR}|g" /etc/apt/sources.list.d/debian.sources \
    && apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=20 update \
    && apt-get install -y --no-install-recommends ca-certificates git openssh-client \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app/runtime
COPY runtime/package.json runtime/package-lock.json ./
ARG NPM_REGISTRY=https://registry.npmjs.org/
RUN npm ci --registry="${NPM_REGISTRY}" --omit=dev --ignore-scripts --no-audit --no-fund --fetch-retries=2 --fetch-timeout=30000 && npm cache clean --force
COPY runtime/*.mjs ./
RUN npm run check && npm test
WORKDIR /app
COPY ai-pm ./ai-pm
USER node
ENV HOME=/app/data/home NODE_ENV=production
EXPOSE 8090
ENTRYPOINT ["/app/ai-pm"]
CMD ["-addr", "0.0.0.0:8090", "-data", "/app/data", "-pi-worker", "/app/runtime/worker.mjs", "-auth-config", "/run/ai-pm/auth.json"]
