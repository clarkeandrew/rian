# Rian ships as a single static binary; GoReleaser builds the per-arch binary
# and drops it into this build context as `rian`, so there is no in-image
# compilation. distroless/static (not scratch) provides CA certificates for TLS
# database connections, /etc/passwd, and a nonroot user.
FROM gcr.io/distroless/static:nonroot
COPY rian /rian
ENTRYPOINT ["/rian"]
