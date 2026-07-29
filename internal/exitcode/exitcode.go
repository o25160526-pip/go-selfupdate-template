// Package exitcode defines the exit code contract of `app update --silent`.
//
// The CI promote step branches on these numbers, so they are a public API:
// changing a value is a breaking change for the pipeline.
package exitcode

import "errors"

const (
	OK          = 0  // updated successfully -> CI may promote draft to published
	UpToDate    = 10 // already newest, legitimate no-op -> CI passes
	NotFound    = 20 // requested version does not exist on any source -> keep draft
	VerifyError = 30 // checksum or signature mismatch -> hard fail, alert
	ApplyError  = 40 // apply failed, rollback performed -> keep draft
	Unreachable = 50 // every source failed to answer -> retry once, then fail
	Locked      = 60 // another update is already running
	Usage       = 64 // bad invocation (matches sysexits.h EX_USAGE)
)

// Error carries an exit code alongside the underlying error.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return Name(e.Code)
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Wrap tags an error with an exit code.
func Wrap(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// From extracts the exit code from an error chain. Unknown errors are treated
// as generic failures (1) rather than silently succeeding.
func From(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 1
}

func Name(code int) string {
	switch code {
	case OK:
		return "ok"
	case UpToDate:
		return "already up to date"
	case NotFound:
		return "version not found"
	case VerifyError:
		return "verification failed"
	case ApplyError:
		return "apply failed (rolled back)"
	case Unreachable:
		return "all update sources unreachable"
	case Locked:
		return "another update is in progress"
	case Usage:
		return "usage error"
	default:
		return "error"
	}
}
