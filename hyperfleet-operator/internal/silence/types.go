package silence

import "time"

const (
	// CreatedBy identifies silences managed by the hyperfleet-operator silence reconciler.
	CreatedBy = "hyperfleet-silence-reconciler"

	DefaultTTL      = 6 * time.Hour
	RenewBefore     = 1 * time.Hour
	RequeueInterval = 15 * time.Minute
)

// InstallExemptAlerts are not suppressed during cluster install.
var InstallExemptAlerts = []string{"HCPInstallTimeout15m"}

// Reason is a lifecycle-driven silence reason.
type Reason string

const (
	ReasonInstalling Reason = "installing"
	ReasonDeleting   Reason = "deleting"
)

// Matcher is an Alertmanager silence matcher.
type Matcher struct {
	Name    string `json:"name"`
	Value   string `json:"value"`
	IsRegex bool   `json:"isRegex"`
	IsEqual bool   `json:"isEqual"`
}

// PostableSilence is the Alertmanager v2 silence create payload.
type PostableSilence struct {
	Matchers  []Matcher `json:"matchers"`
	StartsAt  time.Time `json:"startsAt"`
	EndsAt    time.Time `json:"endsAt"`
	CreatedBy string    `json:"createdBy"`
	Comment   string    `json:"comment"`
}

// SilenceStatus is the active state of a silence.
type SilenceStatus struct {
	State string `json:"state"`
}

// GettableSilence is an Alertmanager v2 silence returned from the API.
type GettableSilence struct {
	ID        string        `json:"id"`
	Status    SilenceStatus `json:"status"`
	UpdatedAt time.Time     `json:"updatedAt"`
	Matchers  []Matcher     `json:"matchers"`
	StartsAt  time.Time     `json:"startsAt"`
	EndsAt    time.Time     `json:"endsAt"`
	CreatedBy string        `json:"createdBy"`
	Comment   string        `json:"comment"`
}

// ClusterIdentity labels used to scope silences to a hosted cluster.
// Namespace is unique per HCP cluster (cluster-$UUID); name is the cluster CR name.
type ClusterIdentity struct {
	Namespace string
	Name      string
}
