# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e

ARG NODE_IMAGE=node:22.19.0-alpine@sha256:d2166de198f26e17e5a442f537754dd616ab069c47cc57b889310a717e0abbf9

FROM ${NODE_IMAGE} AS dependencies
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci

FROM dependencies AS source
COPY . .

FROM source AS test
RUN npm test && npm run lint

FROM source AS build
RUN npm run build

FROM ${NODE_IMAGE} AS runtime
WORKDIR /app
ENV HOSTNAME=0.0.0.0 \
    NEXT_TELEMETRY_DISABLED=1 \
    NODE_ENV=production \
    PORT=3000

COPY --chown=node:node --from=build /app/public ./public
COPY --chown=node:node --from=build /app/.next/standalone ./
COPY --chown=node:node --from=build /app/.next/static ./.next/static

USER node
EXPOSE 3000
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 \
  CMD ["node", "-e", "fetch('http://127.0.0.1:3000/health/ready').then(r=>{if(!r.ok)process.exit(1)}).catch(()=>process.exit(1))"]
CMD ["node", "server.js"]
