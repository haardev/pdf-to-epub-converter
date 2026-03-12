# PDF RAG Search

A Go application that ingests a PDF, generates embeddings via [Ollama](https://ollama.com), stores them in PostgreSQL+pgvector, and exposes an HTTP API for semantic search and RAG-style Q&A.

## Architecture

```
PDF file → text extraction → sliding-window chunker (512 words, 64 overlap)
         → Ollama mxbai-embed-large → vector(1024)
         → PostgreSQL + pgvector

HTTP API:
  GET  /search?q=...  → cosine ANN → top-K chunks + scores
  POST /chat          → RAG: top-K chunks + llama3 → natural language answer
  GET  /health        → {"status":"ok"}
```

## Prerequisites

| Tool | Purpose | Install |
|---|---|---|
| [Go 1.22+](https://go.dev/dl/) | Build & run the app | `go.dev/dl` |
| [Docker](https://docs.docker.com/get-docker/) | PostgreSQL + pgvector | `docker.com` |
| [Ollama](https://ollama.com) | Local LLM inference | `ollama.com` |

## Setup

### 1 — Start the database

```bash
docker compose up -d
```

### 2 — Pull Ollama models

```bash
ollama pull mxbai-embed-large
ollama pull llama3
```

### 3 — Configure environment

```bash
cp .env.example .env
# edit .env if your ports differ
```

If you're using the included `docker-compose.yml` and local Ollama defaults, this step is optional: the app falls back to `postgres://rag:rag@localhost:5432/ragdb` and `http://localhost:11434`.

### 4 — Install Go dependencies

```bash
go mod tidy
```

### 5 — Ingest a PDF

Drop your PDF into `docs/` then run:

```bash
go run ./cmd/ingest docs/my-paper.pdf
```

Progress is logged per-chunk. Large PDFs may take a few minutes (embedding calls are sequential). The default ingestion chunking is intentionally conservative for Ollama embeddings: `CHUNK_SIZE=120` and `CHUNK_OVERLAP=24`. If Ollama still rejects a chunk as too long, ingestion automatically splits that chunk into smaller pieces and retries.

### 6 — Start the query server

```bash
go run ./cmd/server
# listening on :8080
```

## API Usage

### Semantic search

```bash
curl "http://localhost:8080/search?q=what+is+the+main+contribution&k=3"
```

Response:
```json
[
  { "ID": 42, "Source": "my-paper.pdf", "ChunkIndex": 17, "Content": "...", "Score": 0.91 },
  ...
]
```

### RAG — ask a question

```bash
curl -X POST http://localhost:8080/chat \
     -H "Content-Type: application/json" \
     -d '{"question": "What methodology did the authors use?"}'
```

Response:
```json
{
  "answer": "The authors used a mixed-methods approach ...",
  "sources": [
    { "ID": 12, "Source": "my-paper.pdf", "ChunkIndex": 5, "Content": "...", "Score": 0.94 }
  ]
}
```

### Health check

```bash
curl http://localhost:8080/health
# {"status":"ok"}
```

## Configuration (`.env`)

| Variable | Default | Description |
|---|---|---|
| `DB_URL` | `postgres://rag:rag@localhost:5432/ragdb` | PostgreSQL DSN |
| `OLLAMA_URL` | `http://localhost:11434` | Ollama base URL |
| `EMBED_MODEL` | `mxbai-embed-large` | Embedding model name |
| `GEN_MODEL` | `llama3` | Generative model for RAG |
| `TOP_K` | `5` | Number of chunks to retrieve |
| `PORT` | `8080` | HTTP server port |

## Project Structure

```
.
├── cmd/
│   ├── ingest/main.go     CLI — ingest a PDF
│   └── server/main.go     HTTP server
├── internal/
│   ├── pdf/
│   │   ├── reader.go      PDF text extraction
│   │   └── chunker.go     Sliding-window chunker
│   ├── ollama/
│   │   └── client.go      Ollama REST client
│   ├── store/
│   │   ├── db.go          pgx pool + schema migration
│   │   └── vectors.go     Upsert & ANN cosine search
│   └── rag/
│       ├── search.go      Semantic search
│       └── answer.go      RAG answer generation
├── docs/                  Place your PDF files here
├── docker-compose.yml
└── .env.example
```
