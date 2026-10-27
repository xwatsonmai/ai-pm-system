# Build from the repository root or an unpacked release bundle.
ARG NODE_IMAGE=node:22.23.2-trixie-slim
FROM ${NODE_IMAGE}
ARG NPM_REGISTRY=https://registry.npmjs.org/
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates ripgrep fd-find \
    && ln -s /usr/bin/fdfind /usr/local/bin/fd && rm -rf /var/lib/apt/lists/*
WORKDIR /app/runtime
COPY runtime/package.json runtime/package-lock.json ./
RUN npm ci --registry="${NPM_REGISTRY}" --omit=dev --ignore-scripts --no-audit --no-fund && npm cache clean --force
COPY runtime/*.mjs ./
RUN node --check worker.mjs && node --test *.test.mjs
USER node
ENV HOME=/state NODE_ENV=production
WORKDIR /workspace
ENTRYPOINT ["node", "/app/runtime/worker.mjs"]
