FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /weekly-run ./cmd/weekly-run

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /weekly-run /weekly-run
COPY --from=build /src/watchlist.yaml /app/watchlist.yaml
USER nonroot:nonroot
ENTRYPOINT ["/weekly-run"]
