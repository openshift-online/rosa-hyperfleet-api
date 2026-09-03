package pagination

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	hyperfleetdb "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	defaultLimit = 50
	maxLimit     = 100
)

// Options is the standard pagination input for list operations.
type Options struct {
	Limit    int
	Continue string // opaque cursor token; empty = start from beginning
}

// ParseOptions extracts limit and continue from the HTTP request query string.
// Invalid or out-of-range limit values fall back to the default.
func ParseOptions(r *http.Request) Options {
	opts := Options{Limit: defaultLimit}

	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 && n <= maxLimit {
			opts.Limit = n
		}
	}

	opts.Continue = r.URL.Query().Get("continue")
	return opts
}

// Response is the standard envelope for all paginated list endpoints.
// The continue token is placed in metadata.continue following K8s conventions
// so the generated typed clientset reads it from ListMeta.Continue.
// Callers determine whether more pages exist by checking metadata.continue != "".
type Response[T any] struct {
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []T `json:"items"`
	Limit           int `json:"limit"`
}

// platformToken wraps the hyperfleet-db continue token with the query scope so
// that tokens cannot be replayed against a different collection or cluster.
// ClusterID is empty for non-cluster-scoped collections (clusters, oidcconfigs).
type platformToken struct {
	Cursor     string `json:"cursor"`
	AccountID  string `json:"account_id"`
	Collection string `json:"collection"`
	ClusterID  string `json:"cluster_id,omitempty"`
}

// TokenScope identifies the collection and optional cluster scope that a
// continue token was issued for. Passing a token to a mismatched scope is
// rejected with ErrInvalidContinueToken.
type TokenScope struct {
	AccountID  string
	Collection string
	ClusterID  string // empty for collections not scoped to a cluster
}

// ErrInvalidContinueToken is returned when the continue token provided by a
// caller is malformed or does not match the current query scope.
var ErrInvalidContinueToken = errors.New("invalid continue token")

// DecodeContinue validates the platform-level continue token against the
// expected scope, then returns the inner hyperfleet-db cursor.
// An empty token is valid and signals the first page.
func DecodeContinue(token string, scope TokenScope) (string, error) {
	if token == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidContinueToken, err)
	}
	var pt platformToken
	if err := json.Unmarshal(data, &pt); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidContinueToken, err)
	}
	if pt.AccountID != scope.AccountID {
		return "", fmt.Errorf("%w: token account mismatch", ErrInvalidContinueToken)
	}
	if pt.Collection != scope.Collection {
		return "", fmt.Errorf("%w: token collection mismatch", ErrInvalidContinueToken)
	}
	if pt.ClusterID != scope.ClusterID {
		return "", fmt.Errorf("%w: token scope mismatch", ErrInvalidContinueToken)
	}
	return pt.Cursor, nil
}

// EncodeContinue wraps a hyperfleet-db cursor with the query scope to produce
// the platform-level continue token returned to callers.
func EncodeContinue(cursor string, scope TokenScope) string {
	if cursor == "" {
		return ""
	}
	data, _ := json.Marshal(platformToken{
		Cursor:     cursor,
		AccountID:  scope.AccountID,
		Collection: scope.Collection,
		ClusterID:  scope.ClusterID,
	})
	return base64.StdEncoding.EncodeToString(data)
}

// IsInvalidCursor reports whether err is a continue token error (hyperfleet-db
// or platform-level), suitable for mapping to HTTP 400.
func IsInvalidCursor(err error) bool {
	return errors.Is(err, ErrInvalidContinueToken) ||
		errors.Is(err, hyperfleetdb.ErrInvalidContinueToken)
}
