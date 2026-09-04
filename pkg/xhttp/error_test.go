// --- FILE service.https ---

package xhttp

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestErrUnwrap(t *testing.T) {
	inner := errors.New("boom")
	e := &Err{Code: 400, Msg: "bad", Err: inner}

	if !errors.Is(e, inner) { // exercises Unwrap
		t.Fatalf("expected errors.Is to match underlying error")
	}
}

func TestErrErrorFormatting(t *testing.T) {
	inner := errors.New("boom")
	e := &Err{Code: 418, Msg: "teapot", Err: inner}

	got := e.Error()
	if !strings.Contains(got, "418") || !strings.Contains(got, "teapot") || !strings.Contains(got, "boom") {
		t.Fatalf("unexpected Error() output: %q", got)
	}
}

func TestErrorHandlerWithTypedErr(t *testing.T) {
	rec := httptest.NewRecorder()

	err := &Err{Code: 418, Msg: "teapot", Err: errors.New("boom")}
	Error(context.Background(), rec, err)

	if rec.Code != 418 {
		t.Fatalf("want status 418, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "teapot" {
		t.Fatalf("want body %q, got %q", "teapot", body)
	}
}

func TestErrorHandlerWithPlainErr(t *testing.T) {
	rec := httptest.NewRecorder()

	Error(context.Background(), rec, errors.New("something bad"))

	if rec.Code != 500 {
		t.Fatalf("want status 500, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, "Internal server error") {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestErrorJoinedWithTypedErr(t *testing.T) {
	rec := httptest.NewRecorder()

	err := &Err{Code: 418, Msg: "teapot", Err: errors.New("boom")}
	ErrorJoined(context.Background(), rec, err)

	if rec.Code != 418 {
		t.Fatalf("want status 418, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "teapot" {
		t.Fatalf("want body %q, got %q", "teapot", body)
	}
}

func TestErrorJoinedWithJoinedErrs(t *testing.T) {
	rec := httptest.NewRecorder()

	err := errors.Join(
		&Err{Code: 400, Msg: "name is required", Err: errors.New("missing name")},
		&Err{Code: 400, Msg: "email is invalid", Err: errors.New("bad email")},
	)
	ErrorJoined(context.Background(), rec, err)

	if rec.Code != 400 {
		t.Fatalf("want status 400, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "name is required; email is invalid" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestErrorJoinedWithNestedErrs(t *testing.T) {
	rec := httptest.NewRecorder()

	err := &Err{
		Code: 400,
		Msg:  "request invalid",
		Err: &Err{
			Code: 422,
			Msg:  "email is invalid",
			Err:  errors.New("validator: bad email"),
		},
	}
	ErrorJoined(context.Background(), rec, err)

	if rec.Code != 400 {
		t.Fatalf("want status 400, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "request invalid; email is invalid" {
		t.Fatalf("unexpected body: %q", body)
	}
}

func TestErrorJoinedWithPlainErr(t *testing.T) {
	rec := httptest.NewRecorder()

	ErrorJoined(context.Background(), rec, errors.New("something bad"))

	if rec.Code != 500 {
		t.Fatalf("want status 500, got %d", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); !strings.Contains(body, "Internal server error") {
		t.Fatalf("unexpected body: %q", body)
	}
}
