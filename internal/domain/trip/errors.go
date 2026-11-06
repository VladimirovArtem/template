package trip

import "errors"

var (
	ErrNotFound     = errors.New("trip not found")
	ErrDriverBusy   = errors.New("driver busy")
	ErrCompleted    = errors.New("trip already completed")
	ErrInvalidInput = errors.New("invalid trip input")
)

type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Reason
}

func (e *ValidationError) Unwrap() error {
	return ErrInvalidInput
}
