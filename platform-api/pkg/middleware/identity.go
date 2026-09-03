package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"regexp"
)

type contextKey string

const (
	// ContextKeyAccountID is the context key for AWS account ID
	ContextKeyAccountID contextKey = "account_id"
	// ContextKeyCallerARN is the context key for AWS caller ARN
	ContextKeyCallerARN contextKey = "caller_arn"
	// ContextKeyUserID is the context key for AWS user ID
	ContextKeyUserID contextKey = "user_id"
	// ContextKeySourceIP is the context key for source IP
	ContextKeySourceIP contextKey = "source_ip"
	// ContextKeyRequestID is the context key for request ID
	ContextKeyRequestID contextKey = "request_id"
)

// AWS identity headers from API Gateway
const (
	HeaderAccountID = "X-Amz-Account-Id"
	HeaderCallerARN = "X-Amz-Caller-Arn"
	HeaderUserID    = "X-Amz-User-Id"
	HeaderSourceIP  = "X-Amz-Source-Ip"
	HeaderRequestID = "X-Amz-Request-Id"
)

var awsAccountIDRegexp = regexp.MustCompile(`^[0-9]{12}$`)

// Identity returns a middleware that extracts AWS identity headers and adds them
// to the request context. It rejects requests with a malformed account ID.
func Identity(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if accountID := r.Header.Get(HeaderAccountID); accountID != "" {
				if !awsAccountIDRegexp.MatchString(accountID) {
					writeError(w, ErrInvalidAccountID, logger)
					return
				}
				ctx = context.WithValue(ctx, ContextKeyAccountID, accountID)
			}

			if callerARN := r.Header.Get(HeaderCallerARN); callerARN != "" {
				ctx = context.WithValue(ctx, ContextKeyCallerARN, callerARN)
			}

			if userID := r.Header.Get(HeaderUserID); userID != "" {
				ctx = context.WithValue(ctx, ContextKeyUserID, userID)
			}

			if sourceIP := r.Header.Get(HeaderSourceIP); sourceIP != "" {
				ctx = context.WithValue(ctx, ContextKeySourceIP, sourceIP)
			}

			if requestID := r.Header.Get(HeaderRequestID); requestID != "" {
				ctx = context.WithValue(ctx, ContextKeyRequestID, requestID)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetAccountID retrieves the AWS account ID from context
func GetAccountID(ctx context.Context) string {
	if v := ctx.Value(ContextKeyAccountID); v != nil {
		return v.(string)
	}
	return ""
}

// GetCallerARN retrieves the AWS caller ARN from context
func GetCallerARN(ctx context.Context) string {
	if v := ctx.Value(ContextKeyCallerARN); v != nil {
		return v.(string)
	}
	return ""
}

// GetUserID retrieves the AWS user ID from context
func GetUserID(ctx context.Context) string {
	if v := ctx.Value(ContextKeyUserID); v != nil {
		return v.(string)
	}
	return ""
}

// GetRequestID retrieves the request ID from context
func GetRequestID(ctx context.Context) string {
	if v := ctx.Value(ContextKeyRequestID); v != nil {
		return v.(string)
	}
	return ""
}
