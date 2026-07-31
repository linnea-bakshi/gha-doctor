# Used by goreleaser (dockers_v2): the pre-built release binary is copied in,
# nothing is compiled here. The build context lays binaries out per platform.
# distroless/static ships CA certificates and tzdata; runs as nonroot (uid 65532).
FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/gha-doctor /usr/bin/gha-doctor
ENTRYPOINT ["/usr/bin/gha-doctor"]
