// SPDX-License-Identifier: AGPL-3.0-only

package proxy

const (
	OutcomeCommitted = "committed"
	OutcomeCancelled = "cancelled"
	OutcomeFailed    = "failed"
)

// classifyOutcome returns the terminal state of a settled call. It returns "" when call.EndedAt is zero (still in flight).
func classifyOutcome(call Call) string {
	if call.EndedAt.IsZero() {
		return ""
	}
	err := call.Normalized.Error
	clientErr := err != nil && err.Normalized == "client_aborted" && call.ClientAborted &&
		(err.Source == "" || err.Source == "client")
	if err != nil && !clientErr {
		return OutcomeFailed
	}
	if call.HTTPStatus >= 400 {
		return OutcomeFailed
	}
	if call.ClientAborted {
		return OutcomeCancelled
	}
	if call.Normalized.StreamTruncated {
		return OutcomeFailed
	}
	if call.HTTPStatus == 0 {
		return OutcomeFailed
	}
	return OutcomeCommitted
}
