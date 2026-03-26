# PDF RAG Search

A Go application that ingests a PDF, generates embeddings via local Ollama or Azure AI Foundry, stores them in PostgreSQL+pgvector, and exposes an HTTP API for semantic search and RAG-style Q&A.

## Architecture

```
PDF file → text extraction → semantic chunker
         → Ollama or Azure AI Foundry embeddings → vector
         → PostgreSQL + pgvector

HTTP API:
  GET  /search?q=...  → cosine ANN → top-K chunks + scores
  POST /chat          → RAG: top-K chunks + local/hosted chat model → natural language answer
  GET  /health        → {"status":"ok"}
```

## Architecture Summary

This project is a Go-based RAG pipeline for PDFs.

### Ingestion

When you run `cmd/ingest`, the app:

1. reads the PDF locally
2. extracts text page by page
3. detects useful metadata such as page number, likely section title, and figure/table captions
4. splits the content into semantic chunks
5. sends each chunk to the configured embedding provider
6. stores chunk text, metadata, and vectors in PostgreSQL with `pgvector`

### Querying

When a user calls `/search` or `/chat`, the app:

1. embeds the user question
2. runs vector similarity search in PostgreSQL
3. reranks the retrieved chunks using content/title/caption overlap
4. for `/chat`, sends the selected chunks to the configured chat model
5. returns either ranked chunks or a grounded answer with sources

### Current working PoC setup

Your current best-working setup is:

- PDF parsing and chunking in the local Go app
- embeddings in Azure-hosted models
- answer generation in Azure-hosted models
- vectors stored in local Docker PostgreSQL + `pgvector`

Runtime flow:

```text
PDF -> Go extraction/chunking -> Azure embeddings -> local PostgreSQL
Question -> Azure embedding -> PostgreSQL retrieval -> Azure chat model -> answer
```

### Main modules

- `cmd/ingest` — PDF ingestion CLI
- `cmd/server` — HTTP API
- `internal/pdf` — extraction + chunking
- `internal/ai` — Ollama / Azure provider layer
- `internal/store` — PostgreSQL + pgvector
- `internal/rag` — retrieval, reranking, prompt building

### Why this design

- keeps the app logic simple and fully controlled in Go
- allows switching model providers with env vars
- keeps PoC storage cost low
- lets you use stronger Azure-hosted models without changing retrieval architecture

## Prerequisites

| Tool | Purpose | Install |
|---|---|---|
| [Go 1.22+](https://go.dev/dl/) | Build & run the app | `go.dev/dl` |
| [Docker](https://docs.docker.com/get-docker/) | PostgreSQL + pgvector | `docker.com` |
| [Ollama](https://ollama.com) | Local LLM inference (optional) | `ollama.com` |
| Azure AI Foundry | Hosted embeddings + chat (optional) | `ai.azure.com` |

## Setup

### 1 — Start the database

```bash
docker compose up -d
```

This starts both:

- `postgres` — pgvector-enabled PostgreSQL
- `api` — the Go HTTP API on port `8080`

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

Progress is logged per-chunk. Large PDFs may take a few minutes (embedding calls are sequential). The ingestion pipeline stores page-aware chunks with section-title and caption metadata and now defaults to larger semantic windows (`CHUNK_SIZE=400`, `CHUNK_OVERLAP=100`).

### 6 — Start the query server

```bash
go run ./cmd/server
# listening on :8080
```

If you're using Docker Compose, the API is already started for you. The manual command above is only for local non-container development.

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
| `AI_PROVIDER` | `ollama` | `ollama`, `azure-openai`, or `azure-foundry` |
| `DB_URL` | `postgres://rag:rag@localhost:5432/ragdb` | PostgreSQL DSN |
| `OLLAMA_URL` | `http://localhost:11434` | Ollama base URL |
| `EMBED_MODEL` | `mxbai-embed-large` | Embedding model name |
| `GEN_MODEL` | `llama3` | Generative model for RAG |
| `AZURE_AI_ENDPOINT` | empty | Azure endpoint base URL |
| `AZURE_AI_API_KEY` | empty | Azure API key |
| `AZURE_AI_API_VERSION` | provider default | Azure REST API version |
| `AZURE_EMBED_DEPLOYMENT` | empty | Azure OpenAI embedding deployment |
| `AZURE_CHAT_DEPLOYMENT` | empty | Azure OpenAI chat deployment |
| `RECALL_K` | `20` | Number of chunks to pull from vector search before reranking |
| `TOP_K` | `3` | Final number of chunks returned to search/RAG after reranking |
| `PROMPT_VERSION` | `prompt-v1` | Prompt version label attached to answer/search runs |
| `CONFIG_VERSION` | `retrieval-v1` | Retrieval/config version label attached to answer/search runs |
| `GUARDRAILS_ENABLED` | `false` | Enables guardrail checks for user input, retrieval flow, and model output |
| `CONTENT_SAFETY_ENDPOINT` | empty | Optional Azure AI Content Safety endpoint |
| `CONTENT_SAFETY_API_KEY` | empty | Optional Azure AI Content Safety API key |
| `CONTENT_SAFETY_API_VERSION` | `2024-09-01` | Azure AI Content Safety REST API version |
| `PORT` | `8080` | HTTP server port |

### Guardrails

When `GUARDRAILS_ENABLED=true`, the server can:

- screen user input before retrieval
- screen retrieval flow/tool-call metadata
- screen retrieved document excerpts for prompt-injection patterns
- screen model output before returning it

If `CONTENT_SAFETY_ENDPOINT` and `CONTENT_SAFETY_API_KEY` are set, the app calls Azure AI Content Safety for:

- harmful content thresholds (`Hate`, `SelfHarm`, `Sexual`, `Violence`)
- Prompt Shields on user prompts
- Prompt Shields on retrieved document text
- optional protected-material detection on model output

Without Azure Content Safety configured, the app still applies lightweight local prompt-injection heuristics.

## Evaluation harness

The repo now includes a JSONL evaluation set and a local runner:

```bash
go run ./cmd/eval
```

By default it reads `evals/policy_eval_set.jsonl`, runs the current retrieval + generation pipeline, and checks:

- source scoping
- citation presence
- forbidden-source leakage
- guardrail blocking for safety cases (or skips them when guardrails are disabled)

## Retrieval observability

Both `/search` and `/chat` now emit lightweight run metadata in successful responses:

- `run.run_id`
- `run.prompt_version`
- `run.config_version`
- `run.source_scope`
- `run.total_latency_ms`

You can also request full retrieval traces for debugging:

```bash
curl "http://localhost:8080/search?q=breakdown+recovery&debug=true"
```

```bash
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{"question":"Does breakdown assistance include roadside recovery?","source":"breakdown.pdf","debug":true}'
```

The trace includes:

- candidate chunks returned from vector recall
- base and reranked scores
- matched-token coverage
- which chunks were discarded, filtered, or selected
- embedding, search, rerank, and total latency

### Azure AI Foundry / Azure OpenAI

You can switch off Ollama and use hosted models by changing only env vars.

#### Option A: Azure OpenAI deployments in Azure AI Foundry

```env
AI_PROVIDER=azure-openai
AZURE_AI_ENDPOINT=https://YOUR_RESOURCE.openai.azure.com
AZURE_AI_API_KEY=YOUR_KEY
AZURE_AI_API_VERSION=2024-06-01
AZURE_EMBED_DEPLOYMENT=text-embedding-3-large
AZURE_CHAT_DEPLOYMENT=gpt-4o-mini
EMBED_MODEL=text-embedding-3-large
GEN_MODEL=gpt-4o-mini
```

#### Option B: Azure AI Foundry model inference endpoint

```env
AI_PROVIDER=azure-foundry
AZURE_AI_ENDPOINT=https://YOUR_RESOURCE.services.ai.azure.com
AZURE_AI_API_KEY=YOUR_KEY
AZURE_AI_API_VERSION=2024-05-01-preview
EMBED_MODEL=text-embedding-3-large
GEN_MODEL=gpt-4o-mini
```

If you're using a **project endpoint** from Foundry, it usually looks like:

```env
AI_PROVIDER=azure-foundry
AZURE_AI_ENDPOINT=https://YOUR_RESOURCE.services.ai.azure.com/api/projects/YOUR_PROJECT
AZURE_AI_API_KEY=YOUR_KEY
EMBED_MODEL=text-embedding-3-large
GEN_MODEL=gpt-4o-mini
```

For project endpoints, the app uses the OpenAI-compatible `v1` routes automatically and does not require `AZURE_AI_API_VERSION`.

For both modes, your Go API still performs retrieval itself; the hosted model does not talk to PostgreSQL directly.

## Deploying to Azure

Recommended production stack:

- `Azure Container Apps` for the Go HTTP API
- `Azure Database for PostgreSQL Flexible Server` with `pgvector`
- `Azure AI Foundry` for embeddings and chat

Build a container:

```bash
docker build -t pdf-rag-api .
```

The included `Dockerfile` builds `cmd/server` by default. To build the ingest CLI image instead:

```bash
docker build --build-arg APP_PATH=./cmd/ingest -t pdf-rag-ingest .
```

Typical deployment flow:

1. create Azure Database for PostgreSQL Flexible Server
2. allow the `vector` extension
3. deploy chat + embedding models in Azure AI Foundry
4. deploy this container to Azure Container Apps with env vars/secrets
5. run the ingest job against the Azure database

## Cheapest Azure PoC: one Linux VM + Docker Compose

If you want the simplest low-cost deployment, use one small Ubuntu VM and run the included Compose stack there.

Recommended VM:

- `B1ms` or similar small burstable VM for a PoC
- open inbound ports `22` and `8080`

### VM deployment steps

SSH into the VM and install Docker + Compose plugin, then:

```bash
git clone <your-repo-url>
cd pdf-to-epub-converter
cp .env.example .env
```

For Azure-hosted models, edit `.env` like:

```env
AI_PROVIDER=azure-openai
AZURE_AI_ENDPOINT=https://YOUR_RESOURCE.openai.azure.com
AZURE_AI_API_KEY=YOUR_KEY
AZURE_AI_API_VERSION=2024-06-01
AZURE_EMBED_DEPLOYMENT=text-embedding-3-large
AZURE_CHAT_DEPLOYMENT=gpt-4o-mini
EMBED_MODEL=text-embedding-3-large
GEN_MODEL=gpt-4o-mini
DB_URL=postgres://rag:rag@postgres:5432/ragdb
PORT=8080
```

Then start everything:

```bash
docker compose up -d --build
```

Check health:

```bash
curl http://localhost:8080/health
```

From your own machine:

```bash
curl http://YOUR_VM_PUBLIC_IP:8080/health
```

### Ingesting a PDF on the VM

Because the API container only runs the server, you can run the ingest CLI as a one-off container:

```bash
docker build --build-arg APP_PATH=./cmd/ingest -t pdf-rag-ingest .
docker run --rm --env-file .env pdf-rag-ingest /app/app /data/flex-plus.pdf
```

For the simplest workflow, copy the PDF onto the VM and use host networking for the ingest container:

```bash
docker run --rm --network host \
  --env-file .env \
  -e DB_URL=postgres://rag:rag@localhost:5432/ragdb \
  -v $(pwd):/data \
  pdf-rag-ingest /app/app /data/flex-plus.pdf
```

That setup keeps cost low and avoids paying for managed PostgreSQL during the PoC.

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
│   ├── ai/
│   │   └── client.go      Ollama + Azure AI provider client
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
