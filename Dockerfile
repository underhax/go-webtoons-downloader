FROM golang:1.27.3-alpine3.24@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS go-builder

WORKDIR /build

ARG VERSION=dev

COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN mkdir -p /out/data /out/etc/ssl/certs /out/usr/local/bin && \
    chmod 0755 /out/etc /out/etc/ssl /out/etc/ssl/certs && \
    chmod 0700 /out/data && \
    cp /etc/ssl/certs/ca-certificates.crt /out/etc/ssl/certs/ca-certificates.crt && \
    chmod 0444 /out/etc/ssl/certs/ca-certificates.crt && \
    CGO_ENABLED=0 go build -ldflags="-s -w -X 'main.AppVersion=${VERSION}'" -trimpath -o /out/usr/local/bin/wtd ./cmd/wtd

FROM scratch AS app

COPY --from=go-builder /out/etc /etc
COPY --from=go-builder --chmod=0700 /out/data /data
COPY --from=go-builder --chmod=0555 /out/usr/local/bin/wtd /usr/local/bin/wtd

ENTRYPOINT ["/usr/local/bin/wtd"]
