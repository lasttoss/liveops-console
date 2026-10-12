# Two stages. The build stage has the toolchain; the run stage is distroless with no
# shell, which is why the migrations are embedded in the binary instead of applied by a
# sidecar script - there is no script interpreter in the final image to run one.
FROM golang:1.25 AS build
WORKDIR /src
# go.sum is copied with go.mod, and the download happens before the source: the module
# layer is then cached across source edits, and a missing checksum file fails here
# rather than halfway through a build.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/console ./cmd/console \
 && CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags="-s -w" -o /out/demo ./cmd/demo

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/console /usr/local/bin/console
COPY --from=build /out/demo /usr/local/bin/demo
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/console"]
CMD ["--mode", "server"]
