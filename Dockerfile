FROM --platform=$BUILDPLATFORM golang:alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
RUN apk add --no-cache ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/fnactor .

FROM alpine:latest
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build /out/fnactor /usr/local/bin/fnactor
ENV CACHE_DIR=/config \
    FNOS_DB_PATH=/fnos-db/trimmedia.db \
    GOMEMLIMIT=640MiB \
    GOGC=75
ENTRYPOINT ["fnactor"]
