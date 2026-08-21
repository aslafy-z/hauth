FROM golang:1.26-alpine AS build

ARG VERSION=dev
ARG COMMIT=none
ARG DATE=unknown

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build \
  -ldflags "-s -w \
    -X github.com/aslafy-z/hauth/internal/version.Version=${VERSION} \
    -X github.com/aslafy-z/hauth/internal/version.Commit=${COMMIT} \
    -X github.com/aslafy-z/hauth/internal/version.Date=${DATE}" \
  -o /out/hauth ./cmd/hauth

FROM alpine:3.21

RUN apk add --no-cache ca-certificates \
  && adduser -D -u 65532 nonroot

COPY --from=build /out/hauth /usr/local/bin/hauth

USER nonroot

ENTRYPOINT ["hauth"]
