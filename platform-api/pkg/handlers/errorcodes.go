package handlers

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/api"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
)

// APIError is an alias for api.APIError so handler code uses the short form.
type APIError = api.APIError

func writeAPIError(w http.ResponseWriter, def APIError, logger *slog.Logger) {
	if err := api.WriteError(w, def); err != nil {
		logger.Error("failed to write error response", "error", err)
	}
}

// Cluster error codes
var (
	ErrClusterList APIError

	ErrClusterCreateInvalidBody   APIError
	ErrClusterCreateMissingFields APIError
	ErrClusterCreateFailed        APIError
	ErrClusterCreateNameCheck     APIError
	ErrClusterCreateNameConflict  APIError
	ErrClusterCreateNameTooLong   APIError
	ErrClusterCreateIDExhausted   APIError
	ErrClusterCreateInvalidSpec   APIError

	ErrClusterGetNotFound APIError
	ErrClusterGetFailed   APIError

	ErrClusterUpdateInvalidBody   APIError
	ErrClusterUpdateMissingFields APIError
	ErrClusterUpdateNotFound      APIError
	ErrClusterUpdateFailed        APIError
	ErrClusterUpdateInvalidSpec   APIError

	ErrClusterDeleteNotFound APIError
	ErrClusterDeleteFailed   APIError

	ErrClusterStatusNotFound APIError
	ErrClusterStatusFailed   APIError

	ErrClusterValidation APIError
)

// NodePool error codes
var (
	ErrNodePoolList APIError

	ErrNodePoolCreateInvalidBody     APIError
	ErrNodePoolCreateMissingFields   APIError
	ErrNodePoolCreateNameConflict    APIError
	ErrNodePoolCreateClusterNotFound APIError
	ErrNodePoolCreateClusterCheck    APIError
	ErrNodePoolCreateInvalidSpec     APIError
	ErrNodePoolCreateFailed          APIError

	ErrNodePoolGetNotFound APIError
	ErrNodePoolGetFailed   APIError

	ErrNodePoolUpdateInvalidBody   APIError
	ErrNodePoolUpdateMissingFields APIError
	ErrNodePoolUpdateNotFound      APIError
	ErrNodePoolUpdateFailed        APIError
	ErrNodePoolUpdateInvalidSpec   APIError

	ErrNodePoolDeleteNotFound APIError
	ErrNodePoolDeleteFailed   APIError

	ErrNodePoolStatusNotFound APIError
	ErrNodePoolStatusFailed   APIError

	ErrNodePoolValidation APIError
)

// Accounts error codes
var (
	ErrAccountCreateInvalidBody     APIError
	ErrAccountCreateMissingID       APIError
	ErrAccountCreateMissingAdminArn APIError
	ErrAccountCreateCheckFailed     APIError
	ErrAccountCreateExists          APIError
	ErrAccountCreateFailed          APIError

	ErrAccountListFailed APIError

	ErrAccountGetFailed   APIError
	ErrAccountGetNotFound APIError

	ErrAccountDeleteFailed APIError
)

// Management cluster error codes
var (
	ErrMCCreateInvalidBody APIError
	ErrMCCreateMissingID   APIError
	ErrMCCreateMissingReg  APIError
	ErrMCCreateMissingAcct APIError
	ErrMCCreateExists      APIError
	ErrMCCreateFailed      APIError

	ErrMCListFailed APIError

	ErrMCGetNotFound APIError
	ErrMCGetFailed   APIError
)

// Authz policy error codes
var (
	ErrAuthzPolicyCreateInvalidBody APIError
	ErrAuthzPolicyCreateMissingName APIError
	ErrAuthzPolicyCreateMissingText APIError
	ErrAuthzPolicyCreateInvalid     APIError

	ErrAuthzPolicyListFailed APIError

	ErrAuthzPolicyGetFailed   APIError
	ErrAuthzPolicyGetNotFound APIError

	ErrAuthzPolicyUpdateInvalidBody APIError
	ErrAuthzPolicyUpdateInvalid     APIError

	ErrAuthzPolicyDeleteFailed APIError
	ErrAuthzPolicyDeleteInUse  APIError
)

// Authz group error codes
var (
	ErrAuthzGroupCreateInvalidBody APIError
	ErrAuthzGroupCreateMissingName APIError
	ErrAuthzGroupCreateFailed      APIError

	ErrAuthzGroupListFailed APIError

	ErrAuthzGroupGetFailed   APIError
	ErrAuthzGroupGetNotFound APIError

	ErrAuthzGroupDeleteFailed APIError

	ErrAuthzGroupMembersUpdateInvalidBody APIError
	ErrAuthzGroupMembersUpdateAddFailed   APIError
	ErrAuthzGroupMembersUpdateRemFailed   APIError
	ErrAuthzGroupMembersUpdateListFailed  APIError

	ErrAuthzGroupMembersListFailed APIError
)

// Authz attachment error codes
var (
	ErrAuthzAttachCreateInvalidBody   APIError
	ErrAuthzAttachCreateMissingFields APIError
	ErrAuthzAttachCreateInvalidTarget APIError
	ErrAuthzAttachCreateFailed        APIError

	ErrAuthzAttachListFailed   APIError
	ErrAuthzAttachDeleteFailed APIError
)

// Authz admin error codes
var (
	ErrAuthzAdminAddInvalidBody  APIError
	ErrAuthzAdminAddMissingPrinc APIError
	ErrAuthzAdminAddFailed       APIError

	ErrAuthzAdminListFailed   APIError
	ErrAuthzAdminDeleteFailed APIError
)

// Authz check error codes
var (
	ErrAuthzCheckInvalidBody   APIError
	ErrAuthzCheckMissingPrinc  APIError
	ErrAuthzCheckMissingAction APIError
	ErrAuthzCheckMissingRes    APIError
	ErrAuthzCheckFailed        APIError
)

// ZOA error codes
var (
	ErrZoaCreateUnknownAction   APIError
	ErrZoaCreateInvalidBody     APIError
	ErrZoaCreateMissingCluster  APIError
	ErrZoaCreateMissingJira     APIError
	ErrZoaCreateInvalidJira     APIError
	ErrZoaCreateInvalidParams   APIError
	ErrZoaCreateCooldown        APIError
	ErrZoaCreateMaxConcurrent   APIError
	ErrZoaCreateDryRunError     APIError
	ErrZoaCreateStoreFailed     APIError
	ErrZoaCreateRenderFailed    APIError
	ErrZoaCreateDispatchFailed  APIError
	ErrZoaCreateStoreSaveFailed APIError

	ErrZoaGetStoreFailed APIError
	ErrZoaGetNotFound    APIError

	ErrZoaListStoreFailed APIError

	ErrZoaAuditDisabled   APIError
	ErrZoaAuditListFailed APIError
)

// Info error codes
var ErrInfoRegionalAccountUnavailable APIError

func init() {
	// Cluster — List
	ErrClusterList = APIError{Code: "CLUSTERS-MGMT-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list clusters"}

	// Cluster — Create
	ErrClusterCreateInvalidBody = APIError{Code: "CLUSTERS-MGMT-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrClusterCreateMissingFields = APIError{Code: "CLUSTERS-MGMT-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "Missing required fields: name and spec"}
	ErrClusterCreateFailed = APIError{Code: "CLUSTERS-MGMT-CREATE-003", HTTPStatus: http.StatusInternalServerError, Message: "Failed to create cluster"}
	ErrClusterCreateNameCheck = APIError{Code: "CLUSTERS-MGMT-CREATE-004", HTTPStatus: http.StatusInternalServerError, Message: "Failed to validate cluster name"}
	ErrClusterCreateNameConflict = APIError{Code: "CLUSTERS-MGMT-CREATE-005", HTTPStatus: http.StatusConflict, Message: "Cluster name already exists in this account", Reason: "a cluster named %q already exists in this account"}
	ErrClusterCreateNameTooLong = APIError{Code: "CLUSTERS-MGMT-CREATE-006", HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("Cluster name must be no more than %d characters", hyperfleetdb.MaxClusterNameLen)}
	ErrClusterCreateIDExhausted = APIError{Code: "CLUSTERS-MGMT-CREATE-007", HTTPStatus: http.StatusInternalServerError, Message: "Unable to generate unique DNS identifier"}
	ErrClusterCreateInvalidSpec = APIError{Code: "CLUSTERS-MGMT-CREATE-008", HTTPStatus: http.StatusBadRequest, Message: "Invalid cluster spec"}

	// Cluster — Get
	ErrClusterGetNotFound = APIError{Code: "CLUSTERS-MGMT-GET-001", HTTPStatus: http.StatusNotFound, Message: "Cluster not found"}
	ErrClusterGetFailed = APIError{Code: "CLUSTERS-MGMT-GET-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to get cluster"}

	// Cluster — Update
	ErrClusterUpdateInvalidBody = APIError{Code: "CLUSTERS-MGMT-UPDATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrClusterUpdateMissingFields = APIError{Code: "CLUSTERS-MGMT-UPDATE-002", HTTPStatus: http.StatusBadRequest, Message: "Missing required field: spec"}
	ErrClusterUpdateNotFound = APIError{Code: "CLUSTERS-MGMT-UPDATE-003", HTTPStatus: http.StatusNotFound, Message: "Cluster not found"}
	ErrClusterUpdateFailed = APIError{Code: "CLUSTERS-MGMT-UPDATE-004", HTTPStatus: http.StatusInternalServerError, Message: "Failed to update cluster"}
	ErrClusterUpdateInvalidSpec = APIError{Code: "CLUSTERS-MGMT-UPDATE-005", HTTPStatus: http.StatusBadRequest, Message: "Invalid cluster spec"}

	// Cluster — Delete
	ErrClusterDeleteNotFound = APIError{Code: "CLUSTERS-MGMT-DELETE-001", HTTPStatus: http.StatusNotFound, Message: "Cluster not found"}
	ErrClusterDeleteFailed = APIError{Code: "CLUSTERS-MGMT-DELETE-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to delete cluster"}

	// Cluster — Status
	ErrClusterStatusNotFound = APIError{Code: "CLUSTERS-MGMT-STATUS-001", HTTPStatus: http.StatusNotFound, Message: "Cluster not found"}
	ErrClusterStatusFailed = APIError{Code: "CLUSTERS-MGMT-STATUS-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to get cluster status"}

	// Cluster — Validation
	ErrClusterValidation = APIError{Code: "CLUSTERS-MGMT-VALIDATION-001", HTTPStatus: http.StatusUnprocessableEntity, Message: "A validation error has occurred, check the errors field for more information"}

	// NodePool — List
	ErrNodePoolList = APIError{Code: "NODEPOOLS-MGMT-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list nodepools"}

	// NodePool — Create
	ErrNodePoolCreateInvalidBody = APIError{Code: "NODEPOOLS-MGMT-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrNodePoolCreateMissingFields = APIError{Code: "NODEPOOLS-MGMT-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "Missing required fields: name, cluster_id, and spec"}
	ErrNodePoolCreateNameConflict = APIError{Code: "NODEPOOLS-MGMT-CREATE-003", HTTPStatus: http.StatusConflict, Message: "NodePool already exists"}
	ErrNodePoolCreateClusterNotFound = APIError{Code: "NODEPOOLS-MGMT-CREATE-004", HTTPStatus: http.StatusNotFound, Message: "Referenced cluster not found"}
	ErrNodePoolCreateClusterCheck = APIError{Code: "NODEPOOLS-MGMT-CREATE-005", HTTPStatus: http.StatusInternalServerError, Message: "Failed to validate cluster reference"}
	ErrNodePoolCreateInvalidSpec = APIError{Code: "NODEPOOLS-MGMT-CREATE-006", HTTPStatus: http.StatusBadRequest, Message: "Invalid nodepool spec"}
	ErrNodePoolCreateFailed = APIError{Code: "NODEPOOLS-MGMT-CREATE-007", HTTPStatus: http.StatusInternalServerError, Message: "Failed to create nodepool"}

	// NodePool — Get
	ErrNodePoolGetNotFound = APIError{Code: "NODEPOOLS-MGMT-GET-001", HTTPStatus: http.StatusNotFound, Message: "NodePool not found"}
	ErrNodePoolGetFailed = APIError{Code: "NODEPOOLS-MGMT-GET-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to get nodepool"}

	// NodePool — Update
	ErrNodePoolUpdateInvalidBody = APIError{Code: "NODEPOOLS-MGMT-UPDATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrNodePoolUpdateMissingFields = APIError{Code: "NODEPOOLS-MGMT-UPDATE-002", HTTPStatus: http.StatusBadRequest, Message: "Missing required field: spec"}
	ErrNodePoolUpdateNotFound = APIError{Code: "NODEPOOLS-MGMT-UPDATE-003", HTTPStatus: http.StatusNotFound, Message: "NodePool not found"}
	ErrNodePoolUpdateFailed = APIError{Code: "NODEPOOLS-MGMT-UPDATE-004", HTTPStatus: http.StatusInternalServerError, Message: "Failed to update nodepool"}
	ErrNodePoolUpdateInvalidSpec = APIError{Code: "NODEPOOLS-MGMT-UPDATE-005", HTTPStatus: http.StatusBadRequest, Message: "Invalid nodepool spec"}

	// NodePool — Delete
	ErrNodePoolDeleteNotFound = APIError{Code: "NODEPOOLS-MGMT-DELETE-001", HTTPStatus: http.StatusNotFound, Message: "NodePool not found"}
	ErrNodePoolDeleteFailed = APIError{Code: "NODEPOOLS-MGMT-DELETE-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to delete nodepool"}

	// NodePool — Status
	ErrNodePoolStatusNotFound = APIError{Code: "NODEPOOLS-MGMT-STATUS-001", HTTPStatus: http.StatusNotFound, Message: "NodePool not found"}
	ErrNodePoolStatusFailed = APIError{Code: "NODEPOOLS-MGMT-STATUS-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to get nodepool status"}

	// NodePool — Validation
	ErrNodePoolValidation = APIError{Code: "NODEPOOLS-MGMT-VALIDATION-001", HTTPStatus: http.StatusUnprocessableEntity, Message: "A validation error has occurred, check the errors field for more information"}

	// Accounts — Create
	ErrAccountCreateInvalidBody = APIError{Code: "ACCOUNTS-MGMT-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAccountCreateMissingID = APIError{Code: "ACCOUNTS-MGMT-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "accountId is required"}
	ErrAccountCreateMissingAdminArn = APIError{Code: "ACCOUNTS-MGMT-CREATE-003", HTTPStatus: http.StatusBadRequest, Message: "adminArn is required for non-privileged accounts"}
	ErrAccountCreateCheckFailed = APIError{Code: "ACCOUNTS-MGMT-CREATE-004", HTTPStatus: http.StatusInternalServerError, Message: "Failed to check account status"}
	ErrAccountCreateExists = APIError{Code: "ACCOUNTS-MGMT-CREATE-005", HTTPStatus: http.StatusConflict, Message: "Account is already enabled"}
	ErrAccountCreateFailed = APIError{Code: "ACCOUNTS-MGMT-CREATE-006", HTTPStatus: http.StatusInternalServerError, Message: "Failed to enable account"}

	// Accounts — List
	ErrAccountListFailed = APIError{Code: "ACCOUNTS-MGMT-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list accounts"}

	// Accounts — Get
	ErrAccountGetFailed = APIError{Code: "ACCOUNTS-MGMT-GET-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to get account"}
	ErrAccountGetNotFound = APIError{Code: "ACCOUNTS-MGMT-GET-002", HTTPStatus: http.StatusNotFound, Message: "Account not found"}

	// Accounts — Delete
	ErrAccountDeleteFailed = APIError{Code: "ACCOUNTS-MGMT-DELETE-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to disable account"}

	// Management clusters — Create
	ErrMCCreateInvalidBody = APIError{Code: "MC-MGMT-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrMCCreateMissingID = APIError{Code: "MC-MGMT-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "id is required"}
	ErrMCCreateMissingReg = APIError{Code: "MC-MGMT-CREATE-003", HTTPStatus: http.StatusBadRequest, Message: "region is required"}
	ErrMCCreateMissingAcct = APIError{Code: "MC-MGMT-CREATE-004", HTTPStatus: http.StatusBadRequest, Message: "accountId is required"}
	ErrMCCreateExists = APIError{Code: "MC-MGMT-CREATE-005", HTTPStatus: http.StatusConflict, Message: "Management cluster already registered", Reason: "management cluster already registered: %s"}
	ErrMCCreateFailed = APIError{Code: "MC-MGMT-CREATE-006", HTTPStatus: http.StatusInternalServerError, Message: "Failed to save management cluster config"}

	// Management clusters — List
	ErrMCListFailed = APIError{Code: "MC-MGMT-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to load management cluster config"}

	// Management clusters — Get
	ErrMCGetNotFound = APIError{Code: "MC-MGMT-GET-001", HTTPStatus: http.StatusNotFound, Message: "Management cluster not found"}
	ErrMCGetFailed = APIError{Code: "MC-MGMT-GET-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to load management cluster config"}

	// Authz — Policy — Create
	ErrAuthzPolicyCreateInvalidBody = APIError{Code: "AUTHZ-POLICY-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAuthzPolicyCreateMissingName = APIError{Code: "AUTHZ-POLICY-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "name is required"}
	ErrAuthzPolicyCreateMissingText = APIError{Code: "AUTHZ-POLICY-CREATE-003", HTTPStatus: http.StatusBadRequest, Message: "policy (Cedar text) is required"}
	ErrAuthzPolicyCreateInvalid = APIError{Code: "AUTHZ-POLICY-CREATE-004", HTTPStatus: http.StatusBadRequest, Message: "Invalid policy", Reason: "%w"}

	// Authz — Policy — List
	ErrAuthzPolicyListFailed = APIError{Code: "AUTHZ-POLICY-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list policies"}

	// Authz — Policy — Get
	ErrAuthzPolicyGetFailed = APIError{Code: "AUTHZ-POLICY-GET-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to get policy"}
	ErrAuthzPolicyGetNotFound = APIError{Code: "AUTHZ-POLICY-GET-002", HTTPStatus: http.StatusNotFound, Message: "Policy not found"}

	// Authz — Policy — Update
	ErrAuthzPolicyUpdateInvalidBody = APIError{Code: "AUTHZ-POLICY-UPDATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAuthzPolicyUpdateInvalid = APIError{Code: "AUTHZ-POLICY-UPDATE-002", HTTPStatus: http.StatusBadRequest, Message: "Invalid policy", Reason: "%w"}

	// Authz — Policy — Delete
	ErrAuthzPolicyDeleteFailed = APIError{Code: "AUTHZ-POLICY-DELETE-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to delete policy"}
	ErrAuthzPolicyDeleteInUse = APIError{Code: "AUTHZ-POLICY-DELETE-002", HTTPStatus: http.StatusConflict, Message: "Cannot delete policy with existing attachments", Reason: "%w"}

	// Authz — Group — Create
	ErrAuthzGroupCreateInvalidBody = APIError{Code: "AUTHZ-GROUP-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAuthzGroupCreateMissingName = APIError{Code: "AUTHZ-GROUP-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "name is required"}
	ErrAuthzGroupCreateFailed = APIError{Code: "AUTHZ-GROUP-CREATE-003", HTTPStatus: http.StatusInternalServerError, Message: "Failed to create group"}

	// Authz — Group — List
	ErrAuthzGroupListFailed = APIError{Code: "AUTHZ-GROUP-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list groups"}

	// Authz — Group — Get
	ErrAuthzGroupGetFailed = APIError{Code: "AUTHZ-GROUP-GET-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to get group"}
	ErrAuthzGroupGetNotFound = APIError{Code: "AUTHZ-GROUP-GET-002", HTTPStatus: http.StatusNotFound, Message: "Group not found"}

	// Authz — Group — Delete
	ErrAuthzGroupDeleteFailed = APIError{Code: "AUTHZ-GROUP-DELETE-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to delete group"}

	// Authz — Group — Members
	ErrAuthzGroupMembersUpdateInvalidBody = APIError{Code: "AUTHZ-GROUP-MEMBERS-UPDATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAuthzGroupMembersUpdateAddFailed = APIError{Code: "AUTHZ-GROUP-MEMBERS-UPDATE-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to add group member"}
	ErrAuthzGroupMembersUpdateRemFailed = APIError{Code: "AUTHZ-GROUP-MEMBERS-UPDATE-003", HTTPStatus: http.StatusInternalServerError, Message: "Failed to remove group member"}
	ErrAuthzGroupMembersUpdateListFailed = APIError{Code: "AUTHZ-GROUP-MEMBERS-UPDATE-004", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list group members"}
	ErrAuthzGroupMembersListFailed = APIError{Code: "AUTHZ-GROUP-MEMBERS-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list group members"}

	// Authz — Attachment — Create
	ErrAuthzAttachCreateInvalidBody = APIError{Code: "AUTHZ-ATTACH-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAuthzAttachCreateMissingFields = APIError{Code: "AUTHZ-ATTACH-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "policyId, targetType, and targetId are required"}
	ErrAuthzAttachCreateInvalidTarget = APIError{Code: "AUTHZ-ATTACH-CREATE-003", HTTPStatus: http.StatusBadRequest, Message: "targetType must be 'user' or 'group'"}
	ErrAuthzAttachCreateFailed = APIError{Code: "AUTHZ-ATTACH-CREATE-004", HTTPStatus: http.StatusBadRequest, Message: "Failed to attach policy", Reason: "%w"}

	// Authz — Attachment — List / Delete
	ErrAuthzAttachListFailed = APIError{Code: "AUTHZ-ATTACH-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list attachments"}
	ErrAuthzAttachDeleteFailed = APIError{Code: "AUTHZ-ATTACH-DELETE-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to detach policy"}

	// Authz — Admin — Add
	ErrAuthzAdminAddInvalidBody = APIError{Code: "AUTHZ-ADMIN-ADD-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAuthzAdminAddMissingPrinc = APIError{Code: "AUTHZ-ADMIN-ADD-002", HTTPStatus: http.StatusBadRequest, Message: "principalArn is required"}
	ErrAuthzAdminAddFailed = APIError{Code: "AUTHZ-ADMIN-ADD-003", HTTPStatus: http.StatusInternalServerError, Message: "Failed to add admin"}

	// Authz — Admin — List / Delete
	ErrAuthzAdminListFailed = APIError{Code: "AUTHZ-ADMIN-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list admins"}
	ErrAuthzAdminDeleteFailed = APIError{Code: "AUTHZ-ADMIN-DELETE-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to remove admin"}

	// Authz — Check
	ErrAuthzCheckInvalidBody = APIError{Code: "AUTHZ-CHECK-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAuthzCheckMissingPrinc = APIError{Code: "AUTHZ-CHECK-002", HTTPStatus: http.StatusBadRequest, Message: "principal is required"}
	ErrAuthzCheckMissingAction = APIError{Code: "AUTHZ-CHECK-003", HTTPStatus: http.StatusBadRequest, Message: "action is required"}
	ErrAuthzCheckMissingRes = APIError{Code: "AUTHZ-CHECK-004", HTTPStatus: http.StatusBadRequest, Message: "resource is required"}
	ErrAuthzCheckFailed = APIError{Code: "AUTHZ-CHECK-005", HTTPStatus: http.StatusInternalServerError, Message: "Authorization check failed", Reason: "%w"}

	// ZOA — Create
	ErrZoaCreateUnknownAction = APIError{Code: "ZOA-CREATE-001", HTTPStatus: http.StatusNotFound, Message: "Trusted action not found", Reason: "trusted action not found: %s"}
	ErrZoaCreateInvalidBody = APIError{Code: "ZOA-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrZoaCreateMissingCluster = APIError{Code: "ZOA-CREATE-003", HTTPStatus: http.StatusBadRequest, Message: "target_cluster is required"}
	ErrZoaCreateMissingJira = APIError{Code: "ZOA-CREATE-004", HTTPStatus: http.StatusBadRequest, Message: "jira is required for all trusted actions (e.g. ROSAENG-1234)"}
	ErrZoaCreateInvalidJira = APIError{Code: "ZOA-CREATE-005", HTTPStatus: http.StatusBadRequest, Message: "jira does not have correct format; expected PROJECT-NUMBER (e.g. ROSAENG-1234)"}
	ErrZoaCreateInvalidParams = APIError{Code: "ZOA-CREATE-006", HTTPStatus: http.StatusBadRequest, Message: "Invalid parameters", Reason: "%w"}
	ErrZoaCreateCooldown = APIError{Code: "ZOA-CREATE-007", HTTPStatus: http.StatusTooManyRequests, Message: "Write cooldown in effect", Reason: "%w"}
	ErrZoaCreateMaxConcurrent = APIError{Code: "ZOA-CREATE-008", HTTPStatus: http.StatusTooManyRequests, Message: "Too many concurrent executions on target", Reason: "%w"}
	ErrZoaCreateDryRunError = APIError{Code: "ZOA-CREATE-009", HTTPStatus: http.StatusInternalServerError, Message: "Dry run action not found", Reason: "dry_run_action '%s' not found in registry"}
	ErrZoaCreateStoreFailed = APIError{Code: "ZOA-CREATE-010", HTTPStatus: http.StatusInternalServerError, Message: "Failed to create execution"}
	ErrZoaCreateRenderFailed = APIError{Code: "ZOA-CREATE-011", HTTPStatus: http.StatusInternalServerError, Message: "Failed to build trusted action manifest"}
	ErrZoaCreateDispatchFailed = APIError{Code: "ZOA-CREATE-012", HTTPStatus: http.StatusBadGateway, Message: "Failed to dispatch trusted action"}
	ErrZoaCreateStoreSaveFailed = APIError{Code: "ZOA-CREATE-013", HTTPStatus: http.StatusInternalServerError, Message: "Failed to persist execution state"}

	// ZOA — Get
	ErrZoaGetStoreFailed = APIError{Code: "ZOA-GET-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to retrieve execution"}
	ErrZoaGetNotFound = APIError{Code: "ZOA-GET-002", HTTPStatus: http.StatusNotFound, Message: "Execution not found"}

	// ZOA — List
	ErrZoaListStoreFailed = APIError{Code: "ZOA-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list executions"}

	// ZOA — Audit
	ErrZoaAuditDisabled = APIError{Code: "ZOA-AUDIT-001", HTTPStatus: http.StatusNotFound, Message: "Audit logging is not enabled"}
	ErrZoaAuditListFailed = APIError{Code: "ZOA-AUDIT-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list audit log"}

	// Info
	ErrInfoRegionalAccountUnavailable = APIError{Code: "INFO-001", HTTPStatus: http.StatusServiceUnavailable, Message: "regional account ID is not configured"}
}
