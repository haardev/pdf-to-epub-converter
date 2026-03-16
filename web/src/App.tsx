import { useEffect, useMemo, useRef, useState, type FormEvent, type KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'
import { Document, Page, pdfjs } from 'react-pdf'
import pdfWorker from 'pdfjs-dist/build/pdf.worker.min.mjs?url'
import ReactMarkdown from 'react-markdown'
import './App.css'

pdfjs.GlobalWorkerOptions.workerSrc = pdfWorker

interface Source {
  id: number
  source: string
  page_number: number
  section_title: string
  content: string
  score: number
}

interface AnswerCardData {
  id: string
  question: string
  answer: string
  sources: Source[]
}

function SourceCard({
  source,
  expanded,
  onToggle,
  onOpenPdf,
}: {
  source: Source
  expanded: boolean
  onToggle: () => void
  onOpenPdf: (source: Source) => void
}) {
  return (
    <div className="source-card">
      <div className="source-card-top">
        <button className="source-card-header" type="button" onClick={onToggle}>
          <span className="source-card-title">
            {source.source}
            {source.page_number > 0 && <span className="source-card-page">Page {source.page_number}</span>}
          </span>
          {source.section_title && <span className="source-card-section">{source.section_title}</span>}
          <span className="source-card-toggle">{expanded ? 'Hide excerpt' : 'Show excerpt'}</span>
        </button>

        <button className="source-open-link" type="button" onClick={() => onOpenPdf(source)}>
          Open PDF
        </button>
      </div>

      {expanded && <p className="source-card-content">{source.content}</p>}
    </div>
  )
}

function buildDocumentURL(source: string) {
  return `/documents?source=${encodeURIComponent(source)}`
}

function PDFViewerModal({
  source,
  initialPage,
  onClose,
}: {
  source: string
  initialPage: number
  onClose: () => void
}) {
  const [pageNumber, setPageNumber] = useState(initialPage)
  const [numPages, setNumPages] = useState<number | null>(null)
  const [pageWidth, setPageWidth] = useState(320)
  const containerRef = useRef<HTMLDivElement>(null)

  const documentURL = useMemo(() => buildDocumentURL(source), [source])

  useEffect(() => {
    setPageNumber(initialPage)
  }, [initialPage, source])

  useEffect(() => {
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    return () => {
      document.body.style.overflow = previousOverflow
    }
  }, [])

  useEffect(() => {
    const updateWidth = () => {
      const width = containerRef.current?.clientWidth ?? 320
      setPageWidth(Math.max(Math.floor(width) - 24, 240))
    }

    updateWidth()

    const observer = new ResizeObserver(updateWidth)
    if (containerRef.current) {
      observer.observe(containerRef.current)
    }

    return () => observer.disconnect()
  }, [])

  useEffect(() => {
    function onKeyDown(event: globalThis.KeyboardEvent) {
      if (event.key === 'Escape') {
        onClose()
      }
    }

    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [onClose])

  function handleLoadSuccess({ numPages: loadedPages }: { numPages: number }) {
    setNumPages(loadedPages)
    setPageNumber((current) => Math.min(Math.max(current, 1), loadedPages))
  }

  const modal = (
    <div className="viewer-backdrop" onClick={onClose} role="presentation">
      <div
        className="viewer-modal"
        onClick={(event) => event.stopPropagation()}
        role="dialog"
        aria-modal="true"
        aria-label={`PDF viewer for ${source}`}
      >
        <div className="viewer-header">
          <div>
            <p className="viewer-eyebrow">Source document</p>
            <h3>{source}</h3>
          </div>
          <button className="viewer-close" type="button" onClick={onClose}>
            Close
          </button>
        </div>

        <div className="viewer-toolbar">
          <button type="button" onClick={() => setPageNumber((current) => Math.max(current - 1, 1))} disabled={pageNumber <= 1}>
            Prev
          </button>
          <span>
            Page {pageNumber}
            {numPages ? ` of ${numPages}` : ''}
          </span>
          <button
            type="button"
            onClick={() => setPageNumber((current) => Math.min(current + 1, numPages ?? current + 1))}
            disabled={numPages !== null && pageNumber >= numPages}
          >
            Next
          </button>
          <a href={`${documentURL}#page=${pageNumber}`} target="_blank" rel="noreferrer">
            Open in tab
          </a>
        </div>

        <div className="viewer-document" ref={containerRef}>
          <Document
            file={documentURL}
            loading={<div className="viewer-state">Loading PDF…</div>}
            error={<div className="viewer-state">Could not load this PDF.</div>}
            onLoadSuccess={handleLoadSuccess}
          >
            <Page pageNumber={pageNumber} renderAnnotationLayer={false} renderTextLayer={false} width={pageWidth} />
          </Document>
        </div>
      </div>
    </div>
  )

  return createPortal(modal, document.body)
}

function AnswerCard({ card }: { card: AnswerCardData }) {
  const [showSources, setShowSources] = useState(false)
  const [expandedSourceId, setExpandedSourceId] = useState<number | null>(null)
  const [viewerSource, setViewerSource] = useState<Source | null>(null)

  const answerWithLinks = useMemo(
    () => card.answer.replace(/\(page\s+(\d+)\)/gi, '[(page $1)](#page-$1)'),
    [card.answer],
  )

  function toggleSources() {
    setShowSources((current) => {
      if (current) {
        setExpandedSourceId(null)
      }
      return !current
    })
  }

  function handleCitationClick(pageNumber: number) {
    const matchingSource = card.sources.find((source) => source.page_number === pageNumber)
    if (!matchingSource) return
    setViewerSource(matchingSource)
  }

  return (
    <>
      <article className="answer-card">
        <h2>{card.question}</h2>
        <p className="answer-card-kicker">✦ Smart search results</p>

        <div className="answer-markdown">
          <ReactMarkdown
            components={{
              a: ({ href, children }) => {
                if (href?.startsWith('#page-')) {
                  const pageNumber = Number(href.replace('#page-', ''))

                  return (
                    <button className="citation-link" type="button" onClick={() => handleCitationClick(pageNumber)}>
                      {children}
                    </button>
                  )
                }

                return (
                  <a className="external-link" href={href} rel="noreferrer" target="_blank">
                    {children}
                  </a>
                )
              },
            }}
          >
            {answerWithLinks}
          </ReactMarkdown>
        </div>

        {card.sources.length > 0 && (
          <div className="answer-sources">
            <button className="answer-sources-toggle" type="button" onClick={toggleSources}>
              {showSources ? 'Hide sources' : `View sources (${card.sources.length})`}
            </button>

            {showSources && (
              <div className="answer-sources-list">
                {card.sources.map((source) => (
                  <SourceCard
                    key={source.id}
                    source={source}
                    expanded={expandedSourceId === source.id}
                    onToggle={() =>
                      setExpandedSourceId((current) => (current === source.id ? null : source.id))
                    }
                    onOpenPdf={(selectedSource) => setViewerSource(selectedSource)}
                  />
                ))}
              </div>
            )}
          </div>
        )}
      </article>

      {viewerSource && (
        <PDFViewerModal
          source={viewerSource.source}
          initialPage={viewerSource.page_number || 1}
          onClose={() => setViewerSource(null)}
        />
      )}
    </>
  )
}

export default function App() {
  const [currentCard, setCurrentCard] = useState<AnswerCardData | null>(null)
  const [activeQuestion, setActiveQuestion] = useState('')
  const [input, setInput] = useState('')
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const bottomRef = useRef<HTMLDivElement>(null)

  const suggestionQuestions = [
    'Does travel insurance cover flight cancellations?',
    'Does my policy cover adventure sports?',
    'How do I make a claim while abroad?',
    'Will I be covered if I get sick before travel?',
    'What happens if my luggage is lost or stolen?',
  ]

  function goHome() {
    setCurrentCard(null)
    setActiveQuestion('')
    setError(null)
    setInput('')
  }

  async function submitQuestion(rawQuestion?: string) {
    const question = (rawQuestion ?? input).trim()
    if (!question || loading) return

    setInput('')
    setError(null)
    setActiveQuestion(question)
    setCurrentCard(null)
    setLoading(true)

    try {
      const res = await fetch('/chat', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ question }),
      })

      if (!res.ok) {
        throw new Error(`Request failed: ${res.status}`)
      }

      const data = await res.json()

      setCurrentCard({
        id: question,
        question,
        answer: data.answer,
        sources: data.sources ?? [],
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Something went wrong')
    } finally {
      setLoading(false)
      setTimeout(() => bottomRef.current?.scrollIntoView({ behavior: 'smooth', block: 'end' }), 50)
    }
  }

  function handleSubmit(event: FormEvent) {
    event.preventDefault()
    void submitQuestion()
  }

  function handleKeyDown(event: KeyboardEvent<HTMLTextAreaElement>) {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault()
      void submitQuestion()
    }
  }

  return (
    <div className="page">
      <main className="app-shell">
        <section className="phone-frame">
          <form className="search-form" onSubmit={handleSubmit}>
            <div className="search-input-wrap">
              <span className="search-icon" aria-hidden="true">
                ✦
              </span>
              <textarea
                className="search-input"
                rows={1}
                value={input}
                onChange={(event) => setInput(event.target.value)}
                onKeyDown={handleKeyDown}
                placeholder="Ask anything about travel..."
                disabled={loading}
                autoFocus
              />
            </div>
            <button className="search-button" type="submit" disabled={loading || !input.trim()}>
              Ask
            </button>
          </form>

          {(currentCard || loading || error) && (
            <div className="action-row">
              <button className="ghost-action" type="button" onClick={goHome} disabled={loading}>
                Back
              </button>
              <button
                className="ghost-action"
                type="button"
                onClick={() => setInput('')}
                disabled={loading || input.length === 0}
              >
                Clear
              </button>
            </div>
          )}

          {!currentCard && !loading && (
            <section className="suggestions-panel" aria-label="Suggested questions">
              {suggestionQuestions.map((question) => (
                <button
                  key={question}
                  className="suggestion-pill"
                  type="button"
                  onClick={() => void submitQuestion(question)}
                >
                  {question}
                </button>
              ))}
            </section>
          )}

          <section className="results-panel">
            {currentCard && <AnswerCard key={currentCard.id} card={currentCard} />}

            {loading && (
              <article className="answer-card answer-card--loading">
                <h2>{activeQuestion || 'Finding the best answer...'}</h2>
                <p className="answer-card-kicker">✦ Smart search results</p>
                <div className="loading-dots" aria-label="Loading answer">
                  <span />
                  <span />
                  <span />
                </div>
              </article>
            )}

            {error && <div className="error-banner">Something went wrong: {error}</div>}

            <div ref={bottomRef} />
          </section>
        </section>
      </main>
    </div>
  )
}
