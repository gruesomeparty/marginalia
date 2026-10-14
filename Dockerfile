# The hosted image (PRD §5.9): the released binary on a distroless base,
# running as a non-root user, nothing baked in. goreleaser builds it from the
# same binaries it releases as archives — there is no second build path —
# which is why there is no `go build` here: the context holds one file,
# `marginalia`, already built for the target platform.
#
#   docker run --rm -p 8080:8080 \
#     -e MARGINALIA_STORE=file:///tmp/marginalia \
#     -e MARGINALIA_MCP_TOKEN=... \
#     -e MARGINALIA_HUMAN_JWT_ISSUER=... -e MARGINALIA_HUMAN_JWT_AUDIENCE=... \
#     ghcr.io/gruesomeparty/marginalia:<version>
#
# Store and secrets come from the environment only. With an r2:// store the
# image needs no writable volume; with file:// it needs one for anything that
# should outlive the container. Diagrams are off: there is no mermaid-cli
# here, and a hosted instance should not shell out for an agent's input.
#
# Health: GET /healthz answers 200 with no credential. There is no
# HEALTHCHECK instruction because a distroless image has no shell or curl to
# run one with; the platform probes the port instead.
FROM gcr.io/distroless/static-debian12:nonroot
COPY marginalia /marginalia
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/marginalia"]
CMD ["host", "--addr", ":8080"]
