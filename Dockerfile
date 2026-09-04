# Two stages onto distroless static.
#
# Unlike rule-110-arcade, this image is CGO-free. That repository needs cgo
# because it compiles a Runar contract at runtime through a tree-sitter parser;
# nothing here does. The SQLite driver is modernc.org/sqlite, which is pure Go,
# so the binary links static and the runtime image carries no libc at all.
FROM golang:1.26-alpine AS build

WORKDIR /src

# Dependencies first, so a source-only change does not re-download the module
# graph on every build.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/wildtag ./cmd/wildtag

# Fail here rather than in a crash loop: a dynamically linked binary will not
# start on distroless static, and the symptom there is an exec format error
# with no explanation.
RUN if ldd /out/wildtag 2>/dev/null | grep -q '=>'; then \
      echo "wildtag linked against shared libraries; distroless static will not run it"; \
      exit 1; \
    fi

# Prove the tests pass in the same environment that built the binary.
RUN go vet ./... && go test ./...

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/wildtag /usr/local/bin/wildtag

# No VOLUME instruction. It would make `docker run` invent an anonymous volume,
# which looks like persistence and is not: keys.json and both databases would
# vanish with the container, and a fresh keys.json means a different wallet with
# a different deposit address that comes up reporting a perfectly correct
# balance of zero.
ENV WILDTAG_DATA_DIR=/data \
    WILDTAG_ADDR=0.0.0.0:8120

USER 65532:65532
EXPOSE 8120

ENTRYPOINT ["/usr/local/bin/wildtag"]
CMD ["serve"]
