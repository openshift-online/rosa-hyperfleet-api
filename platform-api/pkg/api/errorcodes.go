package api

import (
	"encoding/json"
	"net/http"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ErrInternalMarshal is written when response serialization fails before any
// headers have been committed, ensuring the client receives a proper 500 instead
// of an empty default 200.
var ErrInternalMarshal APIError

func init() {
	ErrInternalMarshal = APIError{
		Code:       "INTERNAL-001",
		HTTPStatus: http.StatusInternalServerError,
		Message:    "internal server error",
	}
	var err error
	fallbackBody, err = json.Marshal(metav1.Status{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Status"},
		Status:   metav1.StatusFailure,
		Message:  ErrInternalMarshal.Code + ": " + ErrInternalMarshal.Message,
		Reason:   metav1.StatusReasonInternalError,
		Code:     http.StatusInternalServerError,
	})
	if err != nil {
		panic("api: failed to marshal fallback error body: " + err.Error())
	}
}
