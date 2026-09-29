// Package platformtask submits an optional reception phrase task.
// A missing client is text degradation, never a billing pass.
package platformtask

import "context"

// PhraseInput is the only payload a reception task may carry.
// Visitor text, phone numbers, persona, and other-tenant knowledge stay out.
type PhraseInput struct {
	IdempotencyKey string
	Language       string
	Body           string
	CitationIDs    []string
}

// PhraseResult is the local receipt. BillingVerdict is recorded or not_completed.
type PhraseResult struct {
	TaskID         string
	CostCents      int
	BillingVerdict string
}

// Phraser meters one grounded answer.
type Phraser interface {
	Phrase(ctx context.Context, in PhraseInput) PhraseResult
}
