package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// APIError is a typed error response. HTTPStatus drives the HTTP status code;
// Code is the platform-specific error code surfaced in the metav1.Status message.
// Reason, when set, is the fmt template used by WithReason() to build dynamic messages.
type APIError struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"-"`
	Message    string `json:"reason"`
	Errors     any    `json:"errors,omitempty"`
	Reason     string `json:"-"`
}

// WithErrors returns a copy of e with Errors set to v for structured payloads
// (e.g. a slice of field-level validation errors).
func (e APIError) WithErrors(v any) APIError {
	e.Errors = v
	return e
}

// WithReason returns a copy of e with Errors set by applying e.Reason to args
// via fmt.Errorf. Panics if e.Reason is empty so misconfiguration is caught at
// test time.
func (e APIError) WithReason(args ...any) APIError {
	if e.Reason == "" {
		panic(fmt.Sprintf("api: WithReason() called on %q which has no Reason template", e.Code))
	}
	e.Errors = fmt.Errorf(e.Reason, args...)
	return e
}

// WriteError serializes def as a metav1.Status JSON response understood natively
// by client-go and kubectl. The platform error code is included in Message as
// "<code>: <reason>". Structured Errors are serialized in status.details.causes.
func WriteError(w http.ResponseWriter, def APIError) error {
	msg := def.Message

	if err, ok := def.Errors.(error); ok {
		b, merr := json.Marshal(def.Errors)
		if merr != nil {
			writeFallback(w)
			return merr
		}
		if len(b) == 0 || string(b) == "{}" || string(b) == "null" {
			// Plain error: use the error string as the message.
			msg = err.Error()
			def.Errors = nil
		}
	}

	if def.Code != "" {
		msg = def.Code + ": " + msg
	}

	httpStatus := def.HTTPStatus
	if httpStatus < 100 || httpStatus > 599 {
		httpStatus = http.StatusInternalServerError
	}

	status := metav1.Status{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Status",
		},
		Status:  metav1.StatusFailure,
		Message: msg,
		Reason:  httpStatusToReason(httpStatus),
		Code:    int32(httpStatus),
	}

	if def.Errors != nil {
		status.Details = errorsToStatusDetails(def.Errors)
	}

	b, err := json.Marshal(status)
	if err != nil {
		writeFallback(w)
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(httpStatus)
	_, err = w.Write(b)
	return err
}

// errorsToStatusDetails converts structured Errors into metav1.StatusDetails.
// Slices of objects with "field"/"detail" keys are mapped to StatusCause entries;
// anything else is embedded as a single cause with the JSON representation.
func errorsToStatusDetails(errs any) *metav1.StatusDetails {
	b, err := json.Marshal(errs)
	if err != nil {
		return nil
	}

	// Try slice of {field, detail, reason} objects → StatusCause list.
	var causes []struct {
		Field   string `json:"field"`
		Detail  string `json:"detail"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &causes) == nil && len(causes) > 0 {
		// Only accept the slice if at least one entry carries meaningful content.
		// An array of zero-value structs (e.g. decoded from non-field-error JSON)
		// must fall through to the fallback rather than emit empty StatusCauses.
		hasContent := false
		for _, c := range causes {
			if c.Field != "" || c.Detail != "" || c.Reason != "" || c.Message != "" {
				hasContent = true
				break
			}
		}
		if hasContent {
			sc := make([]metav1.StatusCause, 0, len(causes))
			for _, c := range causes {
				msg := c.Detail
				if msg == "" {
					msg = c.Message
				}
				reason := metav1.CauseType(c.Reason)
				if reason == "" {
					reason = metav1.CauseTypeFieldValueInvalid
				}
				sc = append(sc, metav1.StatusCause{
					Type:    reason,
					Message: msg,
					Field:   c.Field,
				})
			}
			return &metav1.StatusDetails{Causes: sc}
		}
	}

	// Fallback: the errors payload is not a recognized field-error slice.
	// Emit a fixed safe message rather than the raw JSON to avoid leaking
	// internal details in the client-visible response.
	return &metav1.StatusDetails{
		Causes: []metav1.StatusCause{{
			Type:    metav1.CauseTypeUnexpectedServerResponse,
			Message: "unexpected error format",
		}},
	}
}

// httpStatusToReason maps an HTTP status code to the metav1.StatusReason expected
// by client-go's error classification (apierrors.IsNotFound, IsConflict, etc.).
func httpStatusToReason(code int) metav1.StatusReason {
	switch code {
	case http.StatusBadRequest:
		return metav1.StatusReasonBadRequest
	case http.StatusUnauthorized:
		return metav1.StatusReasonUnauthorized
	case http.StatusForbidden:
		return metav1.StatusReasonForbidden
	case http.StatusNotFound:
		return metav1.StatusReasonNotFound
	case http.StatusMethodNotAllowed:
		return metav1.StatusReasonMethodNotAllowed
	case http.StatusConflict:
		return metav1.StatusReasonConflict
	case http.StatusUnprocessableEntity:
		return metav1.StatusReasonInvalid
	case http.StatusTooManyRequests:
		return metav1.StatusReasonTooManyRequests
	case http.StatusInternalServerError:
		return metav1.StatusReasonInternalError
	case http.StatusServiceUnavailable:
		return metav1.StatusReasonServiceUnavailable
	default:
		return metav1.StatusReasonUnknown
	}
}

// fallbackBody is the pre-marshaled metav1.Status for ErrInternalMarshal, used
// when normal serialization fails before headers are committed.
var fallbackBody []byte

// writeFallback writes a 500 metav1.Status body when normal serialization has failed.
// It must not call Write or WriteError to avoid circular/recursive calls.
func writeFallback(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write(fallbackBody)
}
