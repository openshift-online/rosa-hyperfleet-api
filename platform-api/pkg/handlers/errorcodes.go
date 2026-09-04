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

	ErrClusterCreateInvalidBody            APIError
	ErrClusterCreateMissingFields          APIError
	ErrClusterCreateFailed                 APIError
	ErrClusterCreateNameCheck              APIError
	ErrClusterCreateOidcConfigLookupFailed APIError
	ErrClusterCreateNameConflict           APIError
	ErrClusterCreateNameTooLong            APIError
	ErrClusterCreateIDExhausted            APIError
	ErrClusterCreateInvalidSpec            APIError

	ErrClusterCreateOidcConfigRequired APIError
	ErrClusterCreateOidcConfigNotFound APIError
	ErrClusterCreateOidcConfigNotReady APIError
	ErrClusterCreateOidcConfigInUse    APIError

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

	ErrNodePoolCreateInvalidBody      APIError
	ErrNodePoolCreateMissingFields    APIError
	ErrNodePoolCreateInvalidNamespace APIError
	ErrNodePoolCreateNameConflict     APIError
	ErrNodePoolCreateClusterNotFound  APIError
	ErrNodePoolCreateClusterCheck     APIError
	ErrNodePoolCreateInvalidSpec      APIError
	ErrNodePoolCreateFailed           APIError

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

// OidcConfig error codes
var (
	ErrOidcConfigList APIError

	ErrOidcConfigCreateInvalidBody         APIError
	ErrOidcConfigCreateMissingFields       APIError
	ErrOidcConfigCreateInvalidType         APIError
	ErrOidcConfigCreateInvalidFields       APIError
	ErrOidcConfigCreateDuplicateIssuerUrl  APIError
	ErrOidcConfigCreateIssuerNotConfigured APIError
	ErrOidcConfigCreateFailed              APIError

	ErrOidcConfigGetNotFound APIError
	ErrOidcConfigGetFailed   APIError

	ErrOidcConfigDeleteNotFound APIError
	ErrOidcConfigDeleteFailed   APIError
	ErrOidcConfigDeleteInUse    APIError
)

// Accounts error codes
var (
	ErrAccountCreateInvalidBody APIError
	ErrAccountCreateMissingID   APIError
	ErrAccountCreateCheckFailed APIError
	ErrAccountCreateExists      APIError
	ErrAccountCreateFailed      APIError

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

// Info error codes
var ErrInfoRegionalAccountUnavailable APIError

// Router fallback error codes — returned when gorilla/mux finds no matching
// route or method so that the response format matches api.WriteError output.
var (
	ErrRouteNotFound         APIError
	ErrRouteMethodNotAllowed APIError
)

func init() {
	// Cluster — List
	ErrClusterList = APIError{Code: "CLUSTERS-MGMT-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list clusters"}

	// Cluster — Create
	ErrClusterCreateInvalidBody = APIError{Code: "CLUSTERS-MGMT-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrClusterCreateMissingFields = APIError{Code: "CLUSTERS-MGMT-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "Missing required fields: name and spec"}
	ErrClusterCreateFailed = APIError{Code: "CLUSTERS-MGMT-CREATE-003", HTTPStatus: http.StatusInternalServerError, Message: "Failed to create cluster"}
	ErrClusterCreateNameCheck = APIError{Code: "CLUSTERS-MGMT-CREATE-004", HTTPStatus: http.StatusInternalServerError, Message: "Failed to validate cluster name"}
	ErrClusterCreateOidcConfigLookupFailed = APIError{Code: "CLUSTERS-MGMT-CREATE-013", HTTPStatus: http.StatusInternalServerError, Message: "Failed to look up referenced OIDC config"}
	ErrClusterCreateNameConflict = APIError{Code: "CLUSTERS-MGMT-CREATE-005", HTTPStatus: http.StatusConflict, Message: "Cluster name already exists in this account", Reason: "a cluster named %q already exists in this account"}
	ErrClusterCreateNameTooLong = APIError{Code: "CLUSTERS-MGMT-CREATE-006", HTTPStatus: http.StatusBadRequest, Message: fmt.Sprintf("Cluster name must be no more than %d characters", hyperfleetdb.MaxClusterNameLen)}
	ErrClusterCreateIDExhausted = APIError{Code: "CLUSTERS-MGMT-CREATE-007", HTTPStatus: http.StatusInternalServerError, Message: "Unable to generate unique DNS identifier"}
	ErrClusterCreateInvalidSpec = APIError{Code: "CLUSTERS-MGMT-CREATE-008", HTTPStatus: http.StatusBadRequest, Message: "Invalid cluster spec"}
	ErrClusterCreateOidcConfigRequired = APIError{Code: "CLUSTERS-MGMT-CREATE-009", HTTPStatus: http.StatusBadRequest, Message: "spec.oidcConfigId is required"}
	ErrClusterCreateOidcConfigNotFound = APIError{Code: "CLUSTERS-MGMT-CREATE-010", HTTPStatus: http.StatusNotFound, Message: "Referenced OIDC config not found"}
	ErrClusterCreateOidcConfigNotReady = APIError{Code: "CLUSTERS-MGMT-CREATE-011", HTTPStatus: http.StatusUnprocessableEntity, Message: "Referenced OIDC config is not ready"}
	ErrClusterCreateOidcConfigInUse = APIError{Code: "CLUSTERS-MGMT-CREATE-012", HTTPStatus: http.StatusConflict, Message: "Referenced OIDC config is already associated with another cluster", Reason: "an OidcConfig backs at most one cluster; %q is already in use"}

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
	ErrNodePoolCreateMissingFields = APIError{Code: "NODEPOOLS-MGMT-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "Missing required fields: metadata.name and metadata.namespace"}
	ErrNodePoolCreateNameConflict = APIError{Code: "NODEPOOLS-MGMT-CREATE-003", HTTPStatus: http.StatusConflict, Message: "NodePool already exists"}
	ErrNodePoolCreateClusterNotFound = APIError{Code: "NODEPOOLS-MGMT-CREATE-004", HTTPStatus: http.StatusNotFound, Message: "Referenced cluster not found"}
	ErrNodePoolCreateClusterCheck = APIError{Code: "NODEPOOLS-MGMT-CREATE-005", HTTPStatus: http.StatusInternalServerError, Message: "Failed to validate cluster reference"}
	ErrNodePoolCreateInvalidSpec = APIError{Code: "NODEPOOLS-MGMT-CREATE-006", HTTPStatus: http.StatusBadRequest, Message: "Invalid nodepool spec"}
	ErrNodePoolCreateFailed = APIError{Code: "NODEPOOLS-MGMT-CREATE-007", HTTPStatus: http.StatusInternalServerError, Message: "Failed to create nodepool"}
	ErrNodePoolCreateInvalidNamespace = APIError{Code: "NODEPOOLS-MGMT-CREATE-008", HTTPStatus: http.StatusBadRequest, Message: "metadata.namespace must be a valid cluster namespace (cluster-<uuid>)"}

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

	// OidcConfig — List
	ErrOidcConfigList = APIError{Code: "OIDCCONFIGS-MGMT-LIST-001", HTTPStatus: http.StatusInternalServerError, Message: "Failed to list OIDC configs"}

	// OidcConfig — Create
	ErrOidcConfigCreateInvalidBody = APIError{Code: "OIDCCONFIGS-MGMT-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrOidcConfigCreateMissingFields = APIError{Code: "OIDCCONFIGS-MGMT-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "Missing required fields: spec with type"}
	ErrOidcConfigCreateFailed = APIError{Code: "OIDCCONFIGS-MGMT-CREATE-003", HTTPStatus: http.StatusInternalServerError, Message: "Failed to create OIDC config"}
	ErrOidcConfigCreateInvalidType = APIError{Code: "OIDCCONFIGS-MGMT-CREATE-004", HTTPStatus: http.StatusBadRequest, Message: "spec.type must be 'managed' or 'unmanaged'"}
	ErrOidcConfigCreateInvalidFields = APIError{Code: "OIDCCONFIGS-MGMT-CREATE-005", HTTPStatus: http.StatusBadRequest, Message: "unmanaged type requires secretArn and installerRoleArn; managed type must not set them"}
	ErrOidcConfigCreateDuplicateIssuerUrl = APIError{Code: "OIDCCONFIGS-MGMT-CREATE-006", HTTPStatus: http.StatusConflict, Message: "An OIDC config with this issuerUrl already exists"}
	ErrOidcConfigCreateIssuerNotConfigured = APIError{Code: "OIDCCONFIGS-MGMT-CREATE-007", HTTPStatus: http.StatusInternalServerError, Message: "Server is not configured with an OIDC issuer base URL"}

	// OidcConfig — Get
	ErrOidcConfigGetNotFound = APIError{Code: "OIDCCONFIGS-MGMT-GET-001", HTTPStatus: http.StatusNotFound, Message: "OIDC config not found"}
	ErrOidcConfigGetFailed = APIError{Code: "OIDCCONFIGS-MGMT-GET-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to get OIDC config"}

	// OidcConfig — Delete
	ErrOidcConfigDeleteNotFound = APIError{Code: "OIDCCONFIGS-MGMT-DELETE-001", HTTPStatus: http.StatusNotFound, Message: "OIDC config not found"}
	ErrOidcConfigDeleteFailed = APIError{Code: "OIDCCONFIGS-MGMT-DELETE-002", HTTPStatus: http.StatusInternalServerError, Message: "Failed to delete OIDC config"}
	ErrOidcConfigDeleteInUse = APIError{Code: "OIDCCONFIGS-MGMT-DELETE-003", HTTPStatus: http.StatusConflict, Message: "Cannot delete OIDC config referenced by clusters"}

	// Accounts — Create
	ErrAccountCreateInvalidBody = APIError{Code: "ACCOUNTS-MGMT-CREATE-001", HTTPStatus: http.StatusBadRequest, Message: "Invalid request body"}
	ErrAccountCreateMissingID = APIError{Code: "ACCOUNTS-MGMT-CREATE-002", HTTPStatus: http.StatusBadRequest, Message: "accountId is required"}
	ErrAccountCreateCheckFailed = APIError{Code: "ACCOUNTS-MGMT-CREATE-003", HTTPStatus: http.StatusInternalServerError, Message: "Failed to check account status"}
	ErrAccountCreateExists = APIError{Code: "ACCOUNTS-MGMT-CREATE-004", HTTPStatus: http.StatusConflict, Message: "Account is already enabled"}
	ErrAccountCreateFailed = APIError{Code: "ACCOUNTS-MGMT-CREATE-005", HTTPStatus: http.StatusInternalServerError, Message: "Failed to enable account"}

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

	// Info
	ErrInfoRegionalAccountUnavailable = APIError{Code: "INFO-001", HTTPStatus: http.StatusServiceUnavailable, Message: "regional account ID is not configured"}

	// Router fallbacks
	ErrRouteNotFound = APIError{Code: "ROUTE-001", HTTPStatus: http.StatusNotFound, Message: "route not found"}
	ErrRouteMethodNotAllowed = APIError{Code: "ROUTE-002", HTTPStatus: http.StatusMethodNotAllowed, Message: "method not allowed"}
}
