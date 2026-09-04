// --- FILE service.https ---

package xhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"sprout/pkg/xlog"
)

// Err implements the error interface, wrapping the underlying error along with a status code and message safe for HTTP responses.
type Err struct {
	Code int
	Msg  string
	Err  error // underlying error
}

func (e *Err) Error() string {
	return fmt.Sprintf(`HTTP error: status: %d message: "%s" underlying: %s`, e.Code, e.Msg, e.Err)
}

func (e *Err) Unwrap() error { return e.Err }

type unwrapMany interface {
	Unwrap() []error
}

func logError(ctx context.Context, err error) {
	// get logger from context
	logger := xlog.FromContext(ctx)
	if logger == nil { // fallback to console
		fmt.Println(err.Error())
		return
	}
	logger.Error(err.Error())
}

func walkErrs(err error, visit func(*Err) bool) bool {
	if err == nil {
		return false
	}

	if e, ok := err.(*Err); ok {
		if !visit(e) {
			return false
		}
	}

	switch u := err.(type) {
	case unwrapMany:
		for _, child := range u.Unwrap() {
			if !walkErrs(child, visit) {
				return false
			}
		}
	case interface{ Unwrap() error }:
		return walkErrs(u.Unwrap(), visit)
	}

	return true
}

// Error logs the error and sends an http response. If the error is an [Err], it sends the given
// message and status code. Otherwise, it sends a generic "Internal server error" and 500 status code.
func Error(ctx context.Context, w http.ResponseWriter, err error) {
	logError(ctx, err)
	var e *Err
	if errors.As(err, &e) {
		http.Error(w, e.Msg, e.Code)
	} else {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// ErrorJoined logs the error and sends an http response using the first matching [Err] status code
// and a "; "-joined list of all matching [Err] messages found in the error tree. If there is no
// [Err], it sends a generic "Internal server error" and 500 status code.
func ErrorJoined(ctx context.Context, w http.ResponseWriter, err error) {
	logError(ctx, err)

	var first *Err
	var msgs []string
	walkErrs(err, func(e *Err) bool {
		if first == nil {
			first = e
		}
		if e.Msg != "" {
			msgs = append(msgs, e.Msg)
		}
		return true
	})

	if first == nil || len(msgs) == 0 {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.Error(w, strings.Join(msgs, "; "), first.Code)
}
