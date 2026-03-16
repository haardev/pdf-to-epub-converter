FROM node:22-alpine AS frontend

WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ .
# Build into internal/ui/dist (relative path from web/)
RUN npm run build

FROM golang:1.22 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Copy the built React assets produced by the frontend stage
COPY --from=frontend /internal/ui/dist ./internal/ui/dist

ARG APP_PATH=./cmd/server
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/app ${APP_PATH}

FROM gcr.io/distroless/static-debian12

WORKDIR /app
ENV DOCS_DIR=/app/documents
COPY --from=builder /out/app /app/app
COPY --from=builder /src/*.pdf /app/documents/

EXPOSE 8080

ENTRYPOINT ["/app/app"]
