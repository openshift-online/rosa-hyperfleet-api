package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// UpgradeType defines who requests the control plane upgrade requests.
// +kubebuilder:validation:Enum=ControlPlane;ControlPlaneCVE
type UpgradeType string

// ScheduleType indicates the type of schedule for the control plane upgrade.
// +kubebuilder:validation:Enum=manual;automatic
type ScheduleType string

const (
	// ControlPlaneUpgradeType represents a control plane upgrade policy defined by the user on a cluster.
	ControlPlaneUpgradeType UpgradeType = "ControlPlane"

	// CVEUpgradeType represents a control plane upgrade policy defined by Red Hat.
	CVEUpgradeType UpgradeType = "ControlPlaneCVE"

	// ManualScheduleType represents a control plane upgrade policy that happens one time for a specific version in a
	// specific time defined by the user.
	ManualScheduleType ScheduleType = "manual"

	// AutomaticScheduleType represents a recurrent control plane upgrade policy to the latest available upgrade.
	AutomaticScheduleType ScheduleType = "automatic"
)

// Conditions
const (
	// ControlPlaneUpgradeStateConditionType is a Cluster condition that represents the state of a control plane
	// upgrade policy.
	ControlPlaneUpgradeStateConditionType = "ControlPlaneUpgradeState"
)

// Reasons
const (
	// UpgradePolicyStatePending reason indicates that the upgrade policy is pending scheduling.
	UpgradePolicyStatePending = "pending"

	// UpgradePolicyStateScheduled reason indicates that the upgrade policy is scheduled.
	UpgradePolicyStateScheduled = "scheduled"

	// UpgradePolicyStateStarted reason indicates that the upgrade policy has started.
	UpgradePolicyStateStarted = "started"

	// UpgradePolicyStateCompleted reason indicates that the upgrade policy has successfully upgraded
	// to the target version.
	UpgradePolicyStateCompleted = "completed"

	// UpgradePolicyStateFailed reason indicates that the upgrade policy hasn't successfully upgraded.
	UpgradePolicyStateFailed = "failed"

	// UpgradePolicyStateCancelled reason indicates that the upgrade policy has been deleted by the user or Red Hat.
	UpgradePolicyStateCancelled = "cancelled"
)

// ControlPlaneUpgradePolicySpec defines the desired control plane upgrade policy of a Cluster.
// +kubebuilder:validation:XValidation:rule="self.scheduleType != 'manual' || (has(self.version) && has(self.nextRun))",message="version and nextRun are required when scheduleType is manual"
// +kubebuilder:validation:XValidation:rule="self.scheduleType != 'manual' || !has(self.schedule)",message="schedule must not be set when scheduleType is manual"
type ControlPlaneUpgradePolicySpec struct {

	// UpdateType indicates if it is a control plane upgrade policy defined by the user or
	// triggered by Red Hat for addressing critical CVEs.
	UpdateType UpgradeType `json:"updateType,omitempty"`

	// ScheduleType indicates if the control plane upgrade policy is "manual" and it's executed only one time or
	// whether it is "automatic" where an expression will calculate recurrent upgrades.
	ScheduleType ScheduleType `json:"scheduleType,omitempty"`

	// Schedule defines a cron expression that calculates the next automatic upgrade scheduling.
	Schedule *string `json:"schedule,omitempty"`

	// Version is the desired upgrade version on "manual" upgrade policies.
	Version *string `json:"version,omitempty"`

	// NextRun is the time the upgrade should run for "manual" upgrade policies
	NextRun *metav1.Time `json:"nextRun,omitempty"`

	// EnableMinorVersionUpgrades indicates if minor version upgrades are allowed for automatic upgrades.
	// Manual upgrades always allow it.
	// +optional
	EnableMinorVersionUpgrades *bool `json:"enableMinorVersionUpgrades,omitempty"`
}

type ControlPlaneUpgradePolicyStatus struct {
	// NextRun is the time when the control plane will upgrade.
	// When the ScheduleType is "manual" it will match with the NextRun defined by the user.
	// When the ScheduleType is "automatic" it will be calculated from the Schedule cron expression.
	NextRun *metav1.Time `json:"nextRun,omitempty"`
}
