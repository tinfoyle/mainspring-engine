# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

ARG NODE_IMAGE=node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43
ARG CADDY_IMAGE=caddy:2.10.2-alpine@sha256:4c6e91c6ed0e2fa03efd5b44747b625fec79bc9cd06ac5235a779726618e530d
ARG PLAYWRIGHT_IMAGE=mcr.microsoft.com/playwright:v1.62.1-noble@sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e

FROM ${NODE_IMAGE} AS dependencies
WORKDIR /workspace
ENV NUXT_TELEMETRY_DISABLED=1
COPY package.json package-lock.json ./
COPY apps/app/package.json apps/app/package.json
COPY apps/public/package.json apps/public/package.json
COPY packages/api/package.json packages/api/package.json
COPY packages/design-system/package.json packages/design-system/package.json
RUN --mount=type=cache,target=/root/.npm npm ci

FROM dependencies AS source
COPY . .

FROM source AS app-build
RUN npm run build --workspace=@spyglass/app

FROM source AS public-build
RUN npm run build --workspace=@spyglass/public

FROM ${PLAYWRIGHT_IMAGE} AS browser-test
WORKDIR /workspace
ENV CI=1 \
    PLAYWRIGHT_HTML_OPEN=never
COPY package.json package-lock.json ./
COPY apps/app/package.json apps/app/package.json
COPY apps/public/package.json apps/public/package.json
COPY packages/api/package.json packages/api/package.json
COPY packages/design-system/package.json packages/design-system/package.json
RUN --mount=type=cache,target=/root/.npm npm ci --ignore-scripts
COPY --from=app-build /workspace/apps/app/dist apps/app/dist
COPY --from=public-build /workspace/apps/public/.output apps/public/.output
COPY playwright.config.ts playwright.config.ts
COPY tests/browser tests/browser
RUN npm run browser:app && touch /tmp/spyglass-private-browser-passed

FROM ${PLAYWRIGHT_IMAGE} AS identity-browser-test
WORKDIR /workspace
ENV CI=1 \
    PLAYWRIGHT_HTML_OPEN=never
COPY package.json package-lock.json ./
COPY apps/app/package.json apps/app/package.json
COPY apps/public/package.json apps/public/package.json
COPY packages/api/package.json packages/api/package.json
COPY packages/design-system/package.json packages/design-system/package.json
RUN --mount=type=cache,target=/root/.npm npm ci --ignore-scripts
COPY playwright.identity.config.ts playwright.identity.config.ts
COPY tests/browser/identity-entry-live.spec.ts tests/browser/identity-entry-live.spec.ts
COPY tests/browser/local-edge-port-forward.mjs tests/browser/local-edge-port-forward.mjs
CMD ["npm", "run", "browser:identity"]

FROM source AS test
COPY --from=browser-test /tmp/spyglass-private-browser-passed /tmp/spyglass-private-browser-passed
RUN test -f /tmp/spyglass-private-browser-passed && npm run verify && npm audit --audit-level=high

FROM ${CADDY_IMAGE} AS app-runtime
ARG VERSION=development
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Infinite Ocean Spyglass customer application" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.licenses="Proprietary"
RUN setcap -r /usr/bin/caddy
COPY Caddyfile.app /etc/caddy/Caddyfile
COPY --from=app-build --chown=65532:65532 /workspace/apps/app/dist /srv
USER 65532:65532
EXPOSE 8080
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD ["wget", "--spider", "--quiet", "http://127.0.0.1:8080/health/ready"]
CMD ["caddy", "run", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"]

FROM ${NODE_IMAGE} AS public-runtime
ARG VERSION=development
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Infinite Ocean public customer site" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.licenses="Proprietary"
WORKDIR /app
ENV HOST=0.0.0.0 \
    NODE_ENV=production \
    NITRO_HOST=0.0.0.0 \
    NITRO_PORT=3000 \
    NUXT_TELEMETRY_DISABLED=1
RUN apk upgrade --no-cache && rm -rf /usr/local/lib/node_modules/npm /usr/local/lib/node_modules/corepack /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack
COPY --from=public-build --chown=node:node /workspace/apps/public/.output ./
USER node
EXPOSE 3000
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:3000/').then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))"]
CMD ["node", "server/index.mjs"]
