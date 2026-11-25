# The agent-safe change API with the simulated fleet behind it (what the console talks to).
FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod ./
COPY internal ./internal
COPY cmd ./cmd
COPY config ./config
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /api ./cmd/api

FROM gcr.io/distroless/static:nonroot
COPY --from=build /api /api
COPY --from=build /src/config /config
ENV OPERATOR_ROOT=/ PORT=8080
EXPOSE 8080
USER 65532:65532
ENTRYPOINT ["/api"]
