package types

import (
	"time"

	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
)

// OidcConfig represents an OIDC config resource in the platform API response.
type OidcConfig struct {
	ID              string                `json:"id"`
	Generation      int64                 `json:"generation"`
	ResourceVersion string                `json:"resource_version"`
	Spec            public.OidcConfigSpec `json:"spec"`
	Status          *OidcConfigStatusInfo `json:"status,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

// OidcConfigStatusInfo represents the status of an OIDC config.
type OidcConfigStatusInfo struct {
	ObservedGeneration int64       `json:"observedGeneration"`
	Phase              string      `json:"phase"`
	Thumbprint         string      `json:"thumbprint,omitempty"`
	Conditions         []Condition `json:"conditions,omitempty"`
	LastUsedTimestamp  *time.Time  `json:"lastUsedTimestamp,omitempty"`
	LastUpdateTime     time.Time   `json:"lastUpdateTime"`
}

// OidcConfigCreateRequest represents a request to create an OIDC config.
type OidcConfigCreateRequest struct {
	Spec *public.OidcConfigSpec `json:"spec"`
}
