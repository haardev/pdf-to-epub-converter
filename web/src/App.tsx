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

interface Assessment {
  risk_level: 'low' | 'medium' | 'high'
  impact_level: 'low' | 'medium' | 'high'
  evidence_level: 'weak' | 'moderate' | 'strong'
  risk_score: number
  confidence_score: number
  summary: string
  reasons: string[]
}

interface AnswerCardData {
  id: string
  question: string
  answer: string
  sources: Source[]
  assessment?: Assessment
}

interface CitationTarget {
  source: string
  page_number: number
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

function uniqueSources(sources: Source[]) {
  return Array.from(new Set(sources.map((source) => source.source)))
}

function normalizeCitationSource(source: string) {
  return source.trim().toLowerCase()
}

function isPlaceholderCitationSource(source: string) {
  const normalized = normalizeCitationSource(source)
  return normalized === '' || normalized === 'source.pdf' || normalized === 'policy.pdf' || normalized === 'document.pdf'
}

function resolveCitationTarget(target: CitationTarget, sources: Source[]): CitationTarget | null {
  const trimmedSource = target.source.trim()

  if (!trimmedSource || isPlaceholderCitationSource(trimmedSource)) {
    const pageMatches = sources.filter((source) => source.page_number === target.page_number)
    if (pageMatches.length === 1) {
      return { source: pageMatches[0].source, page_number: pageMatches[0].page_number }
    }
    if (pageMatches.length > 1) {
      return { source: pageMatches[0].source, page_number: pageMatches[0].page_number }
    }
    if (sources.length === 1) {
      return { source: sources[0].source, page_number: target.page_number || sources[0].page_number }
    }
    return null
  }

  const normalizedTarget = normalizeCitationSource(trimmedSource)
  const exactMatch = sources.find((source) => normalizeCitationSource(source.source) === normalizedTarget)
  if (exactMatch) {
    return { source: exactMatch.source, page_number: target.page_number || exactMatch.page_number }
  }

  const looseMatch = sources.find((source) => {
    const normalizedSource = normalizeCitationSource(source.source)
    return normalizedSource.includes(normalizedTarget) || normalizedTarget.includes(normalizedSource)
  })
  if (looseMatch) {
    return { source: looseMatch.source, page_number: target.page_number || looseMatch.page_number }
  }

  const pageMatches = sources.filter((source) => source.page_number === target.page_number)
  if (pageMatches.length > 0) {
    return { source: pageMatches[0].source, page_number: pageMatches[0].page_number }
  }

  if (sources.length === 1) {
    return { source: sources[0].source, page_number: target.page_number || sources[0].page_number }
  }

  return null
}

function createCitationMarkdown(answer: string) {
  const sourceAware = answer.replace(
    /\(((?:source:\s*)?[^()\n]+?),\s*page\s+(\d+)\)/gi,
    (_, rawSource: string, rawPage: string) => {
      const source = rawSource.replace(/^source:\s*/i, '').trim()
      const page = rawPage.trim()
      const href = `#cite:${encodeURIComponent(source)}:${page}`
      return `[(${source}, page ${page})](${href})`
    },
  )

  return sourceAware.replace(/\(page\s+(\d+)\)/gi, (_, rawPage: string) => {
    const page = rawPage.trim()
    return `[(page ${page})](#cite::${page})`
  })
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

function AssessmentCard({ assessment }: { assessment: Assessment }) {
  const [expanded, setExpanded] = useState(false)
  const riskLabel =
    assessment.risk_level === 'high'
      ? 'High-impact answer'
      : assessment.risk_level === 'medium'
        ? 'Review carefully'
        : 'Lower caution'

  const disclosureTitle =
    assessment.risk_level === 'high'
      ? 'verify this before relying on it'
      : assessment.risk_level === 'medium'
        ? 'a quick policy verification is recommended'
        : 'lower caution details'

  const evidenceLabel =
    assessment.evidence_level === 'strong'
      ? 'Strong evidence'
      : assessment.evidence_level === 'moderate'
        ? 'Moderate evidence'
        : 'Weak evidence'

  const impactLabel =
    assessment.impact_level === 'high'
      ? 'High user impact'
      : assessment.impact_level === 'medium'
        ? 'Medium user impact'
        : 'Lower user impact'

  return (
    <section className={`assessment-card assessment-card--${assessment.risk_level}`} aria-label="Answer risk assessment">
      <button
        className="assessment-toggle"
        type="button"
        onClick={() => setExpanded((current) => !current)}
        aria-expanded={expanded}
      >
        <div className="assessment-header">
          <div>
            <p className="assessment-kicker">Answer risk signal</p>
            <h3>{impactLabel}</h3>
            <p className="assessment-title-note">{disclosureTitle}</p>
          </div>
          <div className="assessment-header-right">
            <div className="assessment-badges">
              <span className={`assessment-badge assessment-badge--${assessment.risk_level}`}>{riskLabel}</span>
              <span className="assessment-badge assessment-badge--neutral">{evidenceLabel}</span>
            </div>
            <span className="assessment-toggle-icon" aria-hidden="true">
              {expanded ? '−' : '+'}
            </span>
          </div>
        </div>
      </button>

      {expanded && (
        <div className="assessment-content">
          <p className="assessment-summary">{assessment.summary}</p>

          <div className="assessment-metrics">
            <div className="assessment-metric">
              <div className="assessment-metric-top">
                <span>Risk if relied on blindly</span>
                <strong>{assessment.risk_score}%</strong>
              </div>
              <div className="assessment-meter" aria-hidden="true">
                <span className={`assessment-meter-fill assessment-meter-fill--${assessment.risk_level}`} style={{ width: `${assessment.risk_score}%` }} />
              </div>
            </div>

            <div className="assessment-metric">
              <div className="assessment-metric-top">
                <span>Evidence confidence</span>
                <strong>{assessment.confidence_score}%</strong>
              </div>
              <div className="assessment-meter" aria-hidden="true">
                <span className="assessment-meter-fill assessment-meter-fill--confidence" style={{ width: `${assessment.confidence_score}%` }} />
              </div>
            </div>
          </div>

          {assessment.reasons.length > 0 && (
            <ul className="assessment-reasons">
              {assessment.reasons.map((reason) => (
                <li key={reason}>{reason}</li>
              ))}
            </ul>
          )}
        </div>
      )}
    </section>
  )
}

function AnswerCard({ card }: { card: AnswerCardData }) {
  const [showSources, setShowSources] = useState(false)
  const [expandedSourceId, setExpandedSourceId] = useState<number | null>(null)
  const [viewerSource, setViewerSource] = useState<CitationTarget | null>(null)
  const policySources = useMemo(() => uniqueSources(card.sources), [card.sources])

  const answerWithLinks = useMemo(() => createCitationMarkdown(card.answer), [card.answer])

  function toggleSources() {
    setShowSources((current) => {
      if (current) {
        setExpandedSourceId(null)
      }
      return !current
    })
  }

  function handleCitationClick(target: CitationTarget) {
    const resolvedTarget = resolveCitationTarget(target, card.sources)
    if (!resolvedTarget) return
    setViewerSource(resolvedTarget)
  }

  return (
    <>
      <article className="answer-card">
        <h2>{card.question}</h2>
        <p className="answer-card-kicker">✦ Smart search results</p>

        {policySources.length > 0 && (
          <div className="policy-source-list" aria-label="Policies referenced">
            {policySources.map((source) => (
              <span key={source} className="policy-source-pill">
                {source}
              </span>
            ))}
          </div>
        )}

        <div className="answer-markdown">
          <ReactMarkdown
            components={{
              a: ({ href, children }) => {
                if (href?.startsWith('#cite:')) {
                  const payload = href.replace('#cite:', '')
                  const parts = payload.split(':')
                  const pageNumber = Number(parts[parts.length - 1] ?? '0')
                  const encodedSource = parts.slice(0, -1).join(':')
                  const source = encodedSource ? decodeURIComponent(encodedSource) : ''

                  return (
                    <button
                      className="citation-link"
                      type="button"
                      onClick={() => handleCitationClick({ source, page_number: pageNumber })}
                    >
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
                    onOpenPdf={(selectedSource) =>
                      setViewerSource({
                        source: selectedSource.source,
                        page_number: selectedSource.page_number,
                      })
                    }
                  />
                ))}
              </div>
            )}
          </div>
        )}

        {card.assessment && <AssessmentCard assessment={card.assessment} />}
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
  const [selectedSource, setSelectedSource] = useState('')
  const [availableSources, setAvailableSources] = useState<string[]>([])
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

  useEffect(() => {
    let cancelled = false

    async function loadSources() {
      try {
        const res = await fetch('/sources')
        if (!res.ok) {
          throw new Error(await readErrorMessage(res))
        }

        const data = await res.json()
        if (!cancelled) {
          setAvailableSources(data.sources ?? [])
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : 'Failed to load sources')
        }
      }
    }

    void loadSources()

    return () => {
      cancelled = true
    }
  }, [])

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
        body: JSON.stringify({ question, source: selectedSource || undefined }),
      })

      if (!res.ok) {
        throw new Error(await readErrorMessage(res))
      }

      const data = await res.json()

      setCurrentCard({
        id: question,
        question,
        answer: data.answer,
        sources: data.sources ?? [],
        assessment: data.assessment,
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
            <label className="source-select-wrap">
              <span className="source-select-label">Policy scope</span>
              <select
                className="source-select"
                value={selectedSource}
                onChange={(event) => setSelectedSource(event.target.value)}
                disabled={loading}
              >
                <option value="">All policies (group answers by document)</option>
                {availableSources.map((source) => (
                  <option key={source} value={source}>
                    {source}
                  </option>
                ))}
              </select>
            </label>
            {!selectedSource && <p className="source-select-label">Multi-policy answers stay grouped by source document.</p>}

            <div className="search-row">
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
            </div>
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

async function readErrorMessage(res: Response) {
  try {
    const data = (await res.json()) as { error?: string }
    if (typeof data.error === 'string' && data.error.trim()) {
      return data.error
    }
  } catch {
    // Ignore JSON parsing failures and fall back to the HTTP status.
  }

  return `Request failed: ${res.status}`
}
