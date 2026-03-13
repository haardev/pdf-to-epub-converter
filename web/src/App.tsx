import { useState, type FormEvent } from 'react'
import './App.css'

interface SearchResult {
  id: number
  source: string
  chunk_index: number
  page_number: number
  section_title: string
  captions: string[]
  content: string
  score: number
}

interface ChatResponse {
  answer: string
  sources: SearchResult[]
}

type Mode = 'search' | 'chat'

function App() {
  const [mode, setMode] = useState<Mode>('search')
  const [query, setQuery] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [searchResults, setSearchResults] = useState<SearchResult[] | null>(null)
  const [chatResponse, setChatResponse] = useState<ChatResponse | null>(null)

  async function handleSubmit(e: FormEvent) {
    e.preventDefault()
    if (!query.trim()) return

    setLoading(true)
    setError(null)
    setSearchResults(null)
    setChatResponse(null)

    try {
      if (mode === 'search') {
        const res = await fetch(`/search?q=${encodeURIComponent(query)}`)
        if (!res.ok) throw new Error(`Search failed: ${res.status}`)
        const data: SearchResult[] = await res.json()
        setSearchResults(data)
      } else {
        const res = await fetch('/chat', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ question: query }),
        })
        if (!res.ok) throw new Error(`Chat failed: ${res.status}`)
        const data: ChatResponse = await res.json()
        setChatResponse(data)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="app">
      <header className="header">
        <h1>📄 PDF RAG Search</h1>
        <p>Search or ask questions about your ingested documents</p>
      </header>

      <main className="main">
        <div className="mode-toggle">
          <button
            className={`mode-btn ${mode === 'search' ? 'active' : ''}`}
            onClick={() => setMode('search')}
          >
            🔍 Search
          </button>
          <button
            className={`mode-btn ${mode === 'chat' ? 'active' : ''}`}
            onClick={() => setMode('chat')}
          >
            💬 Chat
          </button>
        </div>

        <form className="search-form" onSubmit={handleSubmit}>
          <input
            className="search-input"
            type="text"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={
              mode === 'search'
                ? 'Search documents…'
                : 'Ask a question about your documents…'
            }
            disabled={loading}
            autoFocus
          />
          <button className="search-btn" type="submit" disabled={loading || !query.trim()}>
            {loading ? '…' : mode === 'search' ? 'Search' : 'Ask'}
          </button>
        </form>

        {error && <div className="error">⚠️ {error}</div>}

        {/* Search results */}
        {searchResults && (
          <section className="results">
            <p className="results-count">{searchResults.length} result{searchResults.length !== 1 ? 's' : ''}</p>
            {searchResults.length === 0 && <p className="empty">No results found.</p>}
            {searchResults.map((r) => (
              <div key={r.id} className="result-card">
                <div className="result-meta">
                  <span className="source">{r.source}</span>
                  {r.page_number > 0 && <span className="page">p.{r.page_number}</span>}
                  {r.section_title && <span className="section">{r.section_title}</span>}
                  <span className="score">{(r.score * 100).toFixed(1)}%</span>
                </div>
                <p className="result-content">{r.content}</p>
              </div>
            ))}
          </section>
        )}

        {/* Chat response */}
        {chatResponse && (
          <section className="results">
            <div className="answer-card">
              <h2>Answer</h2>
              <p className="answer-text">{chatResponse.answer}</p>
            </div>
            {chatResponse.sources && chatResponse.sources.length > 0 && (
              <>
                <p className="results-count">Sources ({chatResponse.sources.length})</p>
                {chatResponse.sources.map((r) => (
                  <div key={r.id} className="result-card">
                    <div className="result-meta">
                      <span className="source">{r.source}</span>
                      {r.page_number > 0 && <span className="page">p.{r.page_number}</span>}
                      {r.section_title && <span className="section">{r.section_title}</span>}
                    </div>
                    <p className="result-content">{r.content}</p>
                  </div>
                ))}
              </>
            )}
          </section>
        )}
      </main>
    </div>
  )
}

export default App
