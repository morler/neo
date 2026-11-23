package retry

import (
	"context"
	"time"

	"github.com/owainlewis/neo/internal/llm"
	"github.com/owainlewis/neo/internal/logx"
)

const defaultBaseDelay = 500 * time.Millisecond

type AttemptResult struct {
	Body       []byte
	Status     int
	RetryAfter RetryAfter
}

type AttemptFunc func(context.Context) (AttemptResult, error)

type RetryAfterBodyFunc func([]byte) RetryAfter

type Options struct {
	Provider       string
	MaxRetries     int
	BaseDelay      time.Duration
	Retryable      func(status int) bool
	RetryAfterBody RetryAfterBodyFunc
	ErrorLabel     string
	// CredentialHint overrides the "check <ENV>" advice attached to 401/403
	// errors raised after retries are exhausted.
	CredentialHint string
}

func Do(ctx context.Context, opts Options, attemptFn AttemptFunc) (AttemptResult, error) {
	policy := normalizeOptions(opts)

	var lastErr error
	for attempt := 0; attempt <= policy.maxRetries; attempt++ {
		logx.Debug("provider attempt", "provider", opts.Provider, "attempt", attempt+1, "max_attempts", policy.maxRetries+1)
		result, err := attemptFn(ctx)
		if err != nil {
			lastErr = err
			delay, retry, err := policy.transportFailure(ctx, err, attempt)
			if !retry {
				return AttemptResult{}, err
			}
			if err := Sleep(ctx, delay); err != nil {
				return AttemptResult{}, err
			}
			continue
		}

		if policy.retryable(result.Status) {
			httpErr := llm.NewHTTPError(policy.label, result.Status, result.Body)
			httpErr.CredentialHint = opts.CredentialHint
			lastErr = httpErr
			if attempt == policy.maxRetries {
				logx.Debug("provider retries exhausted",
					"provider", opts.Provider,
					"status", result.Status,
					"body", logx.PayloadValue(string(result.Body)),
				)
				return AttemptResult{}, lastErr
			}
			delay := policy.httpDelay(result, attempt)
			if err := Sleep(ctx, delay); err != nil {
				return AttemptResult{}, err
			}
			continue
		}
		return result, nil
	}
	return AttemptResult{}, lastErr
}

// policy is Options with every unset value replaced by its default, so the
// retry loop reads one field per decision.
type policy struct {
	provider       string
	maxRetries     int
	baseDelay      time.Duration
	retryable      func(status int) bool
	retryAfterBody RetryAfterBodyFunc
	label          string
}

func normalizeOptions(opts Options) policy {
	p := policy{
		provider:       opts.Provider,
		maxRetries:     opts.MaxRetries,
		baseDelay:      opts.BaseDelay,
		retryable:      opts.Retryable,
		retryAfterBody: opts.RetryAfterBody,
		label:          opts.ErrorLabel,
	}
	if p.maxRetries < 0 {
		p.maxRetries = 0
	}
	if p.baseDelay <= 0 {
		p.baseDelay = defaultBaseDelay
	}
	if p.retryable == nil {
		p.retryable = DefaultRetryableStatus
	}
	if p.label == "" {
		p.label = opts.Provider
	}
	return p
}

// transportFailure decides what a failed attempt means: give up with the
// unwrapped error, or retry after a delay. retry reports whether to keep going
// and delay is how long to wait first.
func (p policy) transportFailure(ctx context.Context, err error, attempt int) (delay time.Duration, retry bool, out error) {
	if ctx.Err() != nil {
		logx.Debug("provider request canceled", "provider", p.provider, "error", ctx.Err().Error())
		return 0, false, ctx.Err()
	}
	if attempt == p.maxRetries || !retryableTransportError(err) {
		logx.Debug("provider transport failed", "provider", p.provider, "attempt", attempt+1, "error", err.Error())
		return 0, false, unwrapPermanent(err)
	}
	delay = Delay(p.baseDelay, attempt, Absent())
	logx.Debug("provider retry scheduled",
		"provider", p.provider,
		"attempt", attempt+1,
		"reason", "transport_error",
		"delay", delay.String(),
		"error", err.Error(),
	)
	return delay, true, nil
}

// httpDelay computes how long to wait before retrying a retryable status,
// preferring the server's Retry-After over the caller's body-derived value.
func (p policy) httpDelay(result AttemptResult, attempt int) time.Duration {
	retryAfter := result.RetryAfter
	if !retryAfter.Present && p.retryAfterBody != nil {
		retryAfter = p.retryAfterBody(result.Body)
	}
	delay := Delay(p.baseDelay, attempt, retryAfter)
	logx.Debug("provider retry scheduled",
		"provider", p.provider,
		"attempt", attempt+1,
		"reason", "http_retryable",
		"status", result.Status,
		"delay", delay.String(),
		"retry_after_present", retryAfter.Present,
		"body", logx.PayloadValue(string(result.Body)),
	)
	return delay
}

func DefaultRetryableStatus(status int) bool {
	return status == 429 || status >= 500
}

func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
