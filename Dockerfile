# --- build stage ---
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/order ./cmd/order

# --- runtime stage ---
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && \
    addgroup -g 1000 -S app && adduser -u 1000 -S app -G app
WORKDIR /app
COPY --from=build /out/order ./order
COPY --from=build /src/migrations ./migrations
USER 1000
EXPOSE 8080
ENTRYPOINT ["./order"]
