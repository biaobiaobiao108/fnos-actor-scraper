FROM oven/bun:alpine AS build
WORKDIR /app
COPY package.json bun.lock ./
RUN bun install --frozen-lockfile
COPY src ./src
RUN bun build src/index.ts --target=bun --outfile=/tmp/dist/index.js

FROM oven/bun:alpine AS runtime
WORKDIR /app
COPY --from=build /tmp/dist/index.js ./index.js
ENV MEDIA_ROOT=/media
ENV CACHE_DIR=/config
ENTRYPOINT ["bun", "index.js"]
