# Banking Assistant Readiness Design

## Purpose

This document describes the next set of capabilities required to turn the current PDF RAG prototype into a banking-app-ready in-app document assistant.

The focus areas are:

- regression evals before every release
- accessibility and mobile-native embedding
- monitoring and alerts for latency, failures, and guardrail blocks
- versioned approved-document release workflow
- audit logs for every question, answer, source set, and guardrail event
- human handoff and support escalation

## Current State

The current system already provides a strong base:

- fixed-document retrieval over approved PDFs
- document-aware chunking and retrieval metadata
- source-grounded answers with citations
- multi-document selection and scoped retrieval
- retrieval diagnostics in the frontend
- guardrail checks for user input, tool calls, tool responses, and model output
- a local evaluation harness and eval dataset
- prompt/config versioning in runtime traces

This means the next phase is less about basic RAG quality and more about operational safety, compliance, release control, and product integration.

## Target Product Shape

The target product is an embedded banking document assistant inside a mobile banking app that:

- answers questions only from approved documents
- explains where the answer came from
- behaves consistently across releases
- is observable in production
- is accessible and usable inside native mobile surfaces
- produces auditable records
- safely escalates to human support when confidence or policy rules require it

## Non-Goals

This design does not cover:

- open-web search
- customer-specific financial advice generation
- unrestricted document upload by end users
- replacing bank support agents entirely

## Architecture Principles

1. **Approved documents only**
   All retrieval must be constrained to bank-approved, versioned documents.

2. **Safe failure over confident guessing**
   When confidence is low or policy boundaries are crossed, the assistant should narrow scope, ask for clarification, or escalate.

3. **Every answer must be explainable**
   The system should retain enough evidence to reconstruct why an answer was produced.

4. **Release quality must be measurable**
   Changes to prompts, retrieval logic, documents, and models must be evaluated before reaching production.

5. **The mobile app is the product shell**
   Authentication, session identity, and handoff should align with the banking app rather than feeling like a standalone chatbot.

## 1. Regression Evals Before Every Release

### Goal

Prevent prompt, retrieval, model, or document changes from silently reducing answer quality or safety.

### Why It Matters

In a banking app, regressions are not just UX issues. They can create compliance, trust, and support risk.

### Proposed Design

Add a release gate that runs evaluation suites whenever any of these change:

- prompt templates
- retrieval/reranking logic
- model configuration
- approved document set
- guardrail configuration

The evaluation suite should include:

- direct factual document questions
- ambiguous questions
- cross-document conflict cases
- citation quality checks
- safety and prompt-injection cases
- escalation-required scenarios

### Required Enhancements

- extend the current eval dataset with gold/reference answers
- add structured scoring for:
  - correctness
  - faithfulness to sources
  - citation quality
  - escalation correctness
  - safety blocking correctness
- persist per-run metrics for comparison over time
- define release thresholds

### Suggested Release Gates

- no safety regressions allowed
- no citation regressions allowed
- correctness score must remain above an agreed threshold
- escalation-required cases must not downgrade into confident direct answers

### Output

Each release candidate should produce:

- eval run ID
- prompt version
- config version
- document bundle version
- score summary
- failed cases with categories

## 2. Accessibility and Mobile-Native Embedding

### Goal

Make the assistant usable inside a mobile banking app for all users, including assistive technology users.

### Why It Matters

The assistant will be embedded in a banking app, so it must feel native, accessible, and support mobile interaction patterns.

### Proposed Design

The current web UI can remain the functional base, but the embedded experience should be adapted for mobile-native use.

### Accessibility Requirements

- semantic headings and landmarks
- screen-reader-friendly labels for inputs, toggles, diagnostics, citations, and source cards
- keyboard accessibility for all interactive controls
- sufficient color contrast for status badges, diagnostics, and call-to-action buttons
- reduced motion support for animated loading states
- support for dynamic text scaling
- clear focus states

### Mobile Embedding Requirements

- responsive layout designed for app webview or native wrapper
- support safe-area insets
- minimize excessive nested scrolling
- native-style modal behavior for source PDFs and handoff flows
- touch-friendly hit areas
- persistent context with bank app navigation and session state

### Recommended Product Decision

Split the UI into two modes:

- **customer mode**: simple answer, citations, handoff, feedback
- **review mode**: diagnostics, retrieval trace, evaluation/debug data

Diagnostics should not be shown to normal banking users by default.

## 3. Monitoring and Alerts

### Goal

Detect production degradation early and route incidents to the correct team.

### Why It Matters

A banking assistant must surface failures before they become customer trust issues.

### What to Monitor

#### System Health

- API latency
- embedding latency
- search latency
- rerank latency
- model generation latency
- error rate
- timeout rate

#### Retrieval Quality Signals

- answer count
- no-answer rate
- low-citation rate
- average selected chunk count
- unusual guardrail block spikes
- source distribution drift

#### Product Health

- handoff rate
- user feedback rate
- negative feedback rate
- abandonment rate

### Alerts

Add alerts for:

- sustained p95 latency increase
- spikes in 5xx responses
- guardrail block spikes above baseline
- no-answer rate above threshold
- model/provider outage
- missing approved document bundle in runtime

### Recommended Implementation

- structured JSON logs
- metrics export to a central monitoring system
- dashboards for latency, quality, and safety
- alert routing to engineering/on-call and, where relevant, support operations

## 4. Versioned Approved-Document Release Workflow

### Goal

Ensure the assistant only answers from approved banking documents and that document changes are controlled, reviewable, and reversible.

### Why It Matters

Fixed documents are one of the system's biggest strengths, but only if release control is strict.

### Proposed Design

Introduce a document release workflow based on immutable bundles.

Each approved release should define:

- document bundle ID
- document names
- document version/date
- checksum/hash per document
- ingestion job ID
- prompt/config versions used for validation
- approval metadata

### Lifecycle

1. Documents are prepared in a staging area.
2. A staging ingestion job creates embeddings and metadata.
3. Evaluation runs against the staged bundle.
4. Compliance/business owner approves the bundle.
5. The bundle is promoted to production.
6. Runtime answers only use the currently active approved bundle.

### Required Controls

- separate staging and production bundles
- immutable bundle records
- explicit promotion action
- rollback to previous bundle
- UI/API visibility into the active bundle version

### Recommended Data Model

- `document_bundles`
- `bundle_documents`
- `bundle_approvals`
- `bundle_promotions`

## 5. Audit Logs

### Goal

Retain a reliable record of how each answer was produced and what controls fired.

### Why It Matters

Auditability is critical in banking for compliance, incident review, and customer support investigation.

### What to Log Per Interaction

- request ID / run ID
- timestamp
- authenticated user/session identifier from the banking app
- app/client version
- question text or approved redacted form
- selected source scope
- retrieved source set
- answer text or approved redacted form
- citations returned
- prompt version
- config version
- document bundle version
- guardrail events and block reasons
- escalation decision
- user feedback if provided

### Logging Principles

- redact or minimize personal/sensitive data where possible
- separate operational logs from long-term audit records
- define retention and access policy
- ensure logs are queryable by support/compliance workflows

### Recommended Output Streams

- real-time structured application logs
- durable audit event store
- analytics event stream for product metrics

## 6. Human Handoff and Support Escalation

### Goal

Move the user smoothly to human support when the assistant should not answer directly.

### Why It Matters

Some banking questions will be too sensitive, too ambiguous, outside approved scope, or require account-specific handling.

### Escalation Triggers

- low confidence
- missing evidence
- conflicting document evidence
- policy says “contact support”
- user asks for account-specific action
- guardrail or policy block
- repeated reformulations without resolution

### Proposed Design

Add an escalation policy layer that can return one of:

- answer normally
- answer with caution and suggest support
- do not answer directly and route to support

### Handoff UX

The handoff should include:

- a clear explanation of why the assistant is escalating
- a direct path to chat, call, secure message, or help center
- optional transfer of conversation context
- visible disclaimer that the assistant did not complete the task

### Support Payload

When escalation happens, send a support-safe payload containing:

- run ID
- user question
- answer status
- retrieved sources
- active document bundle version
- guardrail or confidence reason

This reduces repetition for the customer and speeds up support handling.

## Cross-Cutting Decisions

### Confidence Policy

The system should define explicit rules for:

- when to answer
- when to ask a clarifying question
- when to escalate

This policy should be testable in the eval set.

### Review vs Production Surfaces

The current diagnostics UI is useful for internal review. For banking production:

- keep diagnostics for internal/admin/debug users
- keep the customer UI simple and low-risk

### Data Governance

Confirm:

- what user text is stored
- what is redacted
- where logs are stored
- how long data is retained
- which teams can access which records

## Suggested Delivery Sequence

1. Audit logging foundation
2. Document bundle/version workflow
3. Regression eval release gates
4. Monitoring and alerting
5. Human handoff/escalation policy
6. Customer-mode accessibility and mobile embedding pass

## Definition of Ready for Banking Integration

The assistant is ready for banking-app integration when:

- only approved document bundles can be served
- every release passes regression evaluation gates
- every interaction is auditable
- alerts exist for major latency, failure, and safety issues
- customer-visible escalation is implemented
- the embedded UI meets accessibility and mobile requirements

## Summary

The current prototype already solves the core RAG problem well for fixed documents. To make it banking-ready, the highest-value additions are not more retrieval complexity, but stronger release control, auditability, operational monitoring, accessibility, and safe support escalation.
