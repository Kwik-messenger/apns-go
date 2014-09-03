package apns

import (
	"errors"
)

var (
	// Client.Send returns this error when caller specified wrong priority.
	ErrInvalidPriority = errors.New("invalid priority")
	// Client.Start returns this error when worker counter is not applicable.
	ErrInvalidWorkerCount = errors.New("worker count must be greater than zero")
	// Feedback.GetBadTokens return this error when it have been stopped.
	ErrFeedbackClientStopped = errors.New("feedback client have been stopped")
	// Client.Send returns this error when payload misses 'aps' section.
	ErrNoAPS              = errors.New("no 'aps' section in payload")
	// Client.Send returns this error when payload is too large.
	ErrPayloadIsTooLarge  = errors.New("payload exceed 256 bytes")
	// Client.Send returns this error when token is not well-formed.
	ErrInvalidTokenLength = errors.New("invalid token length")
	// worker returns this error when apple returns an unknown error code (should not happen).
	ErrInvalidAppleResponse = errors.New("invalid apple response")
	// worker returns this error when idle timeout is reached. 
	ErrIdlingTimeout        = errors.New("idle timeout")

	errMessageAlreadyPushedBack = errors.New("message already pushed back")
)
