FROM golang:1.27.4-alpine3.24@sha256:0a5f7fe796599ff487605987432e7619ae45beeb2fba4c2ce0b907879932aebf AS go-builder

WORKDIR /build

ARG VERSION=dev

COPY go.mod ./
COPY cmd/ ./cmd/
COPY internal/ ./internal/

RUN mkdir -p /out/data/webtoons /out/etc/ssl/certs /out/usr/local/bin && \
    chmod 0755 /out/etc /out/etc/ssl /out/etc/ssl/certs /out/data && \
    chmod 0700 /out/data/webtoons && \
    cp /etc/ssl/certs/ca-certificates.crt /out/etc/ssl/certs/ca-certificates.crt && \
    chmod 0444 /out/etc/ssl/certs/ca-certificates.crt && \
    CGO_ENABLED=0 go build -ldflags="-s -w -X 'main.AppVersion=${VERSION}'" -trimpath -o /out/usr/local/bin/wtd ./cmd/wtd

FROM scratch AS app

COPY --from=go-builder /out/etc /etc
COPY --from=go-builder /out/data /data
COPY --from=go-builder --chmod=0555 /out/usr/local/bin/wtd /usr/local/bin/wtd

ENTRYPOINT ["/usr/local/bin/wtd"]
