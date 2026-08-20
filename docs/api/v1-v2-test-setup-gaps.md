# Infrastructure Gaps: Hyperfleet Test vs v1 ROSA Tests vs rosactl v2

This document analyzes the infrastructure provisioning differences between the hyperfleet sanity test (`tests/e2e/hyperfleet_sanity_test.go`), traditional v1 ROSA e2e tests, and the rosactl v2 CLI tool.

## Executive Summary

The hyperfleet sanity test validates the **Platform API v2 primitive** by manually creating the AWS infrastructure resources required by the test. In contrast, v1 ROSA tests use the **OCM v1 API**, which auto-provisions many resources on the backend. The **rosactl v2** tool bridges this gap by using CloudFormation to orchestrate AWS resource creation.

**Key architectural difference:** Platform API v2 is **BYO-everything** (bring your own VPC, IAM roles, OIDC, etc.), while OCM v1 API provides managed infrastructure creation.

---

## Summary Table: All Major Differences

| Component | Hyperfleet Test | v1 Tests | rosactl v2 | Impact Level |
|-----------|----------------|----------|-----------|--------------|
| **OIDC + Operator Roles Timing** | After cluster create | Before cluster create | IAM stack first, OIDC update after | 🔴 **CRITICAL** |
| **Account Roles (Installer/Support)** | Not created | Created via rosa CLI | Not needed | 🔴 **CRITICAL** |
| **Instance Profile** | Manual creation | OCM creates | CloudFormation | 🟡 **HIGH** |
| **Hosted Zones** | Internal only | Both internal + ingress | Internal only | 🟡 **HIGH** |
| **Cleanup Logic** | Extensive manual waits | Handler-based | Partial utilities | 🟢 **MEDIUM** |
| **VPC Resources** | Manual SDK | Helper lib | CloudFormation | 🟢 **LOW** (same result) |
| **Node Pools** | rosa CLI | rosa CLI | rosa CLI | ✅ **SAME** |

---

## 1. Instance Profile Creation Gap

### Overview

The **instance profile + role attachment** is a critical difference:
- **OCM v1 API**: Automatically creates the instance profile from the worker role ARN
- **Platform API v2**: Expects you to provide a pre-created instance profile name
- **rosactl v2**: Creates the instance profile via CloudFormation IAM stack

### Detailed Comparison

| Aspect | OCM v1 API (rosa CLI) | Platform API v2 (hyperfleet) | rosactl v2 |
|--------|----------------------|------------------------------|-----------|
| **What user provides** | Worker role ARN only | Instance profile name | Instance profile name (from stack outputs) |
| **Instance profile creation** | ✅ **Auto-created by OCM backend** | ❌ **User must create** | ✅ **Created by CloudFormation** |
| **Role-to-profile attachment** | ✅ **Auto-attached by OCM backend** | ❌ **User must attach** | ✅ **Attached in CloudFormation** |
| **API contract** | `WorkerRoleARN: "arn:aws:iam::123:role/worker"` | `workerInstanceProfile: "my-cluster-ROSA-Worker-Role"` | CloudFormation output: `WorkerInstanceProfileName` |

### Code Evidence

#### Hyperfleet Test (Manual Creation)
```go
// tests/e2e/hyperfleet_sanity_test.go lines 759-769

By("Creating worker IAM instance profile")
_, err = iamClient.CreateInstanceProfile(ctx, &iamsvc.CreateInstanceProfileInput{
    InstanceProfileName: awssdk.String(workerRoleName),
})
Expect(err).NotTo(HaveOccurred(), "creating instance profile %s", workerRoleName)

_, err = iamClient.AddRoleToInstanceProfile(ctx, &iamsvc.AddRoleToInstanceProfileInput{
    InstanceProfileName: awssdk.String(workerRoleName),
    RoleName:            awssdk.String(workerRoleName),
})
Expect(err).NotTo(HaveOccurred(), "adding worker role to instance profile")
```

#### v1 Tests (OCM Creates Automatically)
```go
// tests/utils/handler/cluster_handler.go

// User only provides the WorkerRoleARN
if config.WorkerRoleARN != "" {
    instanceIAMRolesBuilder.WorkerRoleARN(config.WorkerRoleARN)
}
// OCM backend creates instance profile and attaches role automatically
```

**Evidence:** v1 e2e tests never call `CreateInstanceProfile` or `AddRoleToInstanceProfile` — confirmed by empty grep results.

#### rosactl v2 (CloudFormation)
```yaml
# internal/cloudformation/templates/cluster-iam.yaml lines 310-340

WorkerNodeRole:
  Type: AWS::IAM::Role
  Properties:
    RoleName: !Sub '${ClusterName}-ROSA-Worker-Role'
    AssumeRolePolicyDocument:
      # ... EC2 trust policy
    ManagedPolicyArns:
      - arn:aws:iam::aws:policy/service-role/ROSAWorkerInstancePolicy
      - arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore

WorkerInstanceProfile:
  Type: AWS::IAM::InstanceProfile
  Properties:
    InstanceProfileName: !Sub '${ClusterName}-ROSA-Worker-Role'
    Roles:
      - !Ref WorkerNodeRole  # <-- Automatic attachment!
```

### Why the Difference?

| Design Philosophy | OCM v1 API | Platform API v2 |
|------------------|-----------|----------------|
| **Backend Control** | OCM backend runs in Red Hat AWS account with cross-account IAM permissions | Platform API runs in Red Hat account and **cannot create IAM resources in user account** |
| **User Experience** | "Give me a worker role ARN, I'll derive the instance profile" | "Give me all resources pre-created, I'll reference them" |
| **Abstraction Level** | High-level inputs, backend fills gaps | Low-level explicit resource references |

### Impact on v1→v2 Migration

To run v1 tests in a v2 environment:

**Option 1: Use rosactl as infrastructure provider**
```bash
# rosactl creates the instance profile for you
rosactl cluster-iam create my-cluster --region us-east-1

# Extract the instance profile name from stack outputs
INSTANCE_PROFILE=$(aws cloudformation describe-stacks \
  --stack-name rosa-my-cluster-iam \
  --query 'Stacks[0].Outputs[?OutputKey==`WorkerInstanceProfileName`].OutputValue' \
  --output text)

# Pass to cluster creation
rosa create cluster --hyperfleet-url ... --operator-roles-prefix my-cluster
```

**Option 2: Manually create instance profile**
```bash
# Create the worker role
aws iam create-role --role-name my-cluster-ROSA-Worker-Role --assume-role-policy-document '{...}'

# Create instance profile
aws iam create-instance-profile --instance-profile-name my-cluster-ROSA-Worker-Role

# Attach role to instance profile
aws iam add-role-to-instance-profile \
  --instance-profile-name my-cluster-ROSA-Worker-Role \
  --role-name my-cluster-ROSA-Worker-Role
```

---

## 2. Hosted Zone Creation Gap

### Overview

There are **two types of hosted zones** with different purposes:

1. **Internal communication zone**: `{cluster-name}.hypershift.local` (private, for HCP ↔ worker PrivateLink communication)
2. **Ingress zone**: `rosa.{cluster-name}.{base-domain}` or `{cluster-name}.{base-domain}` (private for shared VPC, used for application ingress DNS)

### Detailed Comparison by Scenario

| Scenario | Zone Type | Hyperfleet Sanity Test | v1 ROSA Tests | rosactl v2 | OCM v1 API |
|----------|-----------|----------------------|---------------|-----------|------------|
| **Standard (No BYOVPC)** | Internal | ❌ N/A - test is BYOVPC only | ❌ Not created by tests | ❌ Not created | ✅ **Auto-created by OCM** |
| **Standard (No BYOVPC)** | Ingress | ❌ N/A - test is BYOVPC only | ❌ Not created by tests | ❌ Not created | ✅ **Auto-created by OCM** |
| **BYOVPC (HCP)** | Internal (`*.hypershift.local`) | ✅ **Manual SDK** (507-537) | ✅ **Manual SDK** via `PrepareHostedZone()` | ✅ **CloudFormation** | ❌ User provides zone ID |
| **BYOVPC (HCP)** | Ingress (`rosa.*.domain`) | ❌ **Not created** | ✅ **Manual SDK** via `PrepareHostedZone()` | ❌ **Not in template** | ❌ User provides zone ID |
| **BYOVPC (Classic)** | Internal | ❌ Not applicable | ❌ Not created | ❌ Not applicable | ✅ **Auto-created by OCM** |
| **BYOVPC (Classic)** | Ingress | ❌ Not created | ✅ **Manual SDK** via `PrepareHostedZone()` | ❌ Not in template | ❌ User provides zone ID |

### Zone Purposes

| Zone Type | Purpose | Who Uses It | Example FQDN |
|-----------|---------|-------------|--------------|
| **Internal communication** (`*.hypershift.local`) | HCP control plane ↔ worker communication via PrivateLink | HyperShift operators, control plane pods | `konnectivity.my-cluster.hypershift.local` |
| **Ingress** (`rosa.*.domain` or `*.domain`) | Public-facing application routes via Ingress Controller | End users accessing cluster apps | `console-openshift-console.apps.rosa.my-cluster.openshiftapps.com` |

### Code Evidence

#### Hyperfleet Test (Internal Zone Only)
```go
// tests/e2e/hyperfleet_sanity_test.go lines 506-537

By("Creating private hosted zone for PrivateLink DNS")
hzOut, err := r53Client.CreateHostedZone(ctx, &route53svc.CreateHostedZoneInput{
    Name:             awssdk.String(clusterName + ".hypershift.local"),
    HostedZoneConfig: &route53types.HostedZoneConfig{PrivateZone: true},
    VPC: &route53types.VPC{
        VPCId:     awssdk.String(vpcID),
        VPCRegion: route53types.VPCRegion(region),
    },
})
Expect(err).NotTo(HaveOccurred(), "creating private hosted zone %s.hypershift.local", clusterName)
```

**Creates:**
- ✅ `{cluster}.hypershift.local` (internal communication zone)
- ❌ **Does NOT create ingress zone** (`rosa.{cluster}.{domain}`)

#### v1 Tests (Both Zones for Shared VPC)
```go
// tests/utils/handler/cluster_handler.go lines 719-746

if ch.profile.ClusterConfig.HCP {
    // Creates TWO zones for HCP shared VPC:
    ingressHostedZoneID, err := resourcesHandler.PrepareHostedZone(
        fmt.Sprintf("rosa.%s.%s", clusterName, dnsDomain), vpc.VpcID, true)
    flags = append(flags, "--ingress-private-hosted-zone-id", ingressHostedZoneID)

    hostedCPInternalHostedZoneID, err := resourcesHandler.PrepareHostedZone(
        fmt.Sprintf("%s.hypershift.local", clusterName), vpc.VpcID, true)
    flags = append(flags, "--hcp-internal-communication-hosted-zone-id", hostedCPInternalHostedZoneID)
} else {
    // Classic only needs ingress zone:
    ingressHostedZoneID, err := resourcesHandler.PrepareHostedZone(
        fmt.Sprintf("%s.%s", clusterName, dnsDomain), vpc.VpcID, true)
    flags = append(flags, "--ingress-private-hosted-zone-id", ingressHostedZoneID)
}
```

**Creates:**
- ✅ `{cluster}.hypershift.local` (HCP only)
- ✅ `rosa.{cluster}.{base-domain}` or `{cluster}.{base-domain}` (ingress)

#### rosactl v2 (Internal Zone Only)
```yaml
# internal/cloudformation/templates/cluster-vpc.yaml lines 468-481

HypershiftLocalZone:
  Type: AWS::Route53::HostedZone
  Properties:
    Name: !Sub '${ClusterName}.hypershift.local'
    VPCs:
      - VPCId: !Ref VPC
        VPCRegion: !Ref AWS::Region
    HostedZoneTags:
      - Key: Name
        Value: !Sub '${ClusterName}.hypershift.local'
      - Key: !Sub 'kubernetes.io/cluster/${ClusterName}'
        Value: 'owned'
```

**Creates:**
- ✅ `{cluster}.hypershift.local` (internal communication zone)
- ❌ **Does NOT create ingress zone**

### Why the Difference?

| Design Philosophy | OCM v1 API | Platform API v2 |
|------------------|-----------|----------------|
| **Internal zone** | Auto-created for HCP clusters | User must provide (required for PrivateLink) |
| **Ingress zone** | Auto-created for public DNS delegation | Not used (different DNS delegation model) |
| **Shared VPC ingress** | User creates and passes `--ingress-private-hosted-zone-id` | Not required by Platform API |

**Platform API v2** doesn't use the ingress zone for public DNS delegation — that's likely handled externally or by the Platform API backend using a different mechanism.

### Impact on v1→v2 Migration

**The ingress zone gap** is a key difference. If v1 tests validate DNS delegation or ingress routing:

**Option 1: Modify v1 tests to skip ingress zone validation**
```diff
- Expect(ingressHostedZoneID).NotTo(BeEmpty())
+ // Platform API v2 doesn't require ingress zone
```

**Option 2: Extend rosactl template to create ingress zone**
```yaml
# Add to cluster-vpc.yaml
IngressPrivateZone:
  Type: AWS::Route53::HostedZone
  Properties:
    Name: !Sub 'rosa.${ClusterName}.${BaseDomain}'
    VPCs:
      - VPCId: !Ref VPC
        VPCRegion: !Ref AWS::Region
```

**Option 3: Create ingress zone manually**
```bash
aws route53 create-hosted-zone \
  --name rosa.my-cluster.example.com \
  --vpc VPCRegion=us-east-1,VPCId=vpc-xxx \
  --caller-reference $(date +%s) \
  --hosted-zone-config PrivateZone=true
```

---

## 3. OIDC Provider & Operator Roles Creation Timing

### Overview

This is a **critical workflow difference** between OCM v1 API and Platform API v2:

- **OCM v1 API**: OIDC config and operator roles must be created **before** cluster creation
- **Platform API v2**: OIDC provider and operator roles are created **after** cluster creation (cluster returns the issuer URL)

### Workflow Comparison

#### Hyperfleet Test Workflow
```
1. Create VPC, IAM worker role, hosted zones
2. Create cluster via Platform API
   └─> Platform API returns OIDC issuer URL
3. Extract issuer URL from cluster describe
4. Create OIDC provider in IAM with issuer URL
5. Create operator roles with trust policies referencing OIDC provider
```

#### v1 Tests Workflow
```
1. Create VPC (if BYOVPC)
2. Create account roles (installer, support, worker, control-plane)
3. Create OIDC config (managed OIDC configuration)
4. Create OIDC provider in IAM
5. Create operator roles with trust policies
6. Create cluster with --oidc-config-id flag
   └─> OCM backend uses pre-created OIDC config
```

#### rosactl v2 Workflow
```
1. Create VPC stack
2. Create IAM stack (roles with OIDC=PENDING placeholder)
3. Create cluster via Platform API
   └─> Platform API returns OIDC issuer URL
4. Update IAM stack with actual OIDC issuer URL
   └─> CloudFormation updates trust policies
```

### Code Evidence

#### Hyperfleet Test (lines 603-732)
```go
By("Creating cluster via CLI")
createArgs := []string{
    "--cluster-name", clusterName,
    "--subnet-ids", subnetID,
    "--operator-roles-prefix", rolesPrefix,  // Roles don't exist yet!
}
rosa create cluster --hyperfleet-url ...

By("Fetching cluster ID and OIDC IssuerURL via CLI describe")
issuerURL := clusterDescribe["spec"]["oidc_issuer"]

// NOW create OIDC provider
By("Creating OIDC provider in IAM")
iamClient.CreateOpenIDConnectProvider(issuerURL, ...)

// NOW create operator roles
By("Creating operator IAM roles with OIDC trust policies")
for _, role := range operatorRoles {
    roleName := rolesPrefix + role.suffix
    trustPolicy := hfBuildTrustPolicy(partition, accountID, oidcProvider, role.serviceAccounts)
    iamClient.CreateRole(roleName, trustPolicy, ...)
}
```

#### v1 Tests (cluster_handler.go lines 330-370)
```go
// Create OIDC config FIRST
if ch.profile.ClusterConfig.OIDCConfig != "" {
    oidcConfigID, err = resourcesHandler.PrepareOIDCConfig(...)
    flags = append(flags, "--oidc-config-id", oidcConfigID)
    
    // Create OIDC provider FIRST
    err = resourcesHandler.PrepareOIDCProvider(oidcConfigID)
    
    // Create operator roles FIRST
    err = resourcesHandler.PrepareOperatorRolesByOIDCConfig(
        operatorRolePrefix, oidcConfigID, accRoles.InstallerRole, ...)
}

// THEN create cluster
clusterService.Create(ch.profile.ClusterConfig.Name, flags...)
```

### Impact on v1→v2 Migration

**v1 tests will fail in v2 environment because:**
- v1 tests try to create OIDC provider before cluster exists
- v1 tests reference `--oidc-config-id` flag (Platform API doesn't use OIDC configs)
- v1 tests create operator roles before cluster (Platform API workflow is reversed)

**Solution: Modify v1 test workflow**
```diff
- PrepareOIDCConfig()
- PrepareOIDCProvider()
- PrepareOperatorRolesByOIDCConfig()
  CreateCluster()
+ GetClusterIssuerURL()
+ CreateOIDCProvider(issuerURL)
+ CreateOperatorRoles(issuerURL)
```

---

## 4. Account-Level IAM Roles Gap

### Overview

**OCM v1 API** requires account-level roles (Installer, Support, ControlPlane) for cluster lifecycle management. **Platform API v2** does not use these roles.

### Role Comparison

| Role Type | Purpose | OCM v1 API | Platform API v2 |
|-----------|---------|-----------|----------------|
| **Installer Role** | OCM backend assumes this role to create/manage cluster resources | ✅ Required | ❌ Not used |
| **Support Role** | Red Hat SRE support access | ✅ Required | ❌ Not used |
| **ControlPlane Role** (Classic) | Control plane node EC2 instances | ✅ Required (Classic) | ❌ Not used |
| **Worker Role** | Worker node EC2 instances | ✅ Required | ✅ Required |
| **Operator Roles** (7 roles) | HCP control plane components | ✅ Required | ✅ Required |

### Code Evidence

#### v1 Tests Create Account Roles
```go
// tests/utils/handler/cluster_handler.go lines 300-350

accRoles, err := resourcesHandler.PrepareAccountRoles(
    accountRolePrefix,
    ch.profile.ClusterConfig.HCP,
    ch.clusterConfig.Version.RawID,
    ch.profile.ChannelGroup,
    ch.profile.AccountRoleConfig.Path,
    ch.profile.AccountRoleConfig.PermissionBoundary,
    "", "",
)

flags = append(flags,
    "--role-arn", accRoles.InstallerRole,        // OCM-specific
    "--support-role-arn", accRoles.SupportRole,  // OCM-specific
    "--worker-iam-role", accRoles.WorkerRole,
)

if !ch.profile.ClusterConfig.HCP {
    flags = append(flags,
        "--controlplane-iam-role", accRoles.ControlPlaneRole,  // Classic only
    )
}
```

**This calls:** `rosa create account-roles` which creates 4 roles (3 OCM-specific + worker).

#### Hyperfleet Test Does NOT Create Account Roles
```bash
# Search results:
grep -r "InstallerRole\|SupportRole\|ControlPlaneRole\|account.*role" \
  tests/e2e/hyperfleet_sanity_test.go

# Result: No matches
```

Hyperfleet test only creates:
- ✅ Worker role (for EC2 instances)
- ✅ Operator roles (7 HCP component roles)
- ❌ No installer/support/control-plane roles

### Why the Difference?

| Aspect | OCM v1 API | Platform API v2 |
|--------|-----------|----------------|
| **Cluster Management** | OCM backend assumes installer role to manage AWS resources in user account | Platform API has different auth model (doesn't assume roles in user account) |
| **Support Access** | Red Hat SRE uses support role for troubleshooting | Platform API uses different support mechanism |
| **Architecture** | Installer/support roles are OCM-specific concepts | Platform API doesn't use these abstractions |

### Impact on v1→v2 Migration

**v1 tests expect account roles to exist** but Platform API v2 doesn't use them:

```diff
  # v1 test preparation
- rosa create account-roles --prefix my-cluster
- # Creates: Installer, Support, Worker, ControlPlane (Classic)
  
  # v2 environment
+ # Only create worker role (via rosactl cluster-iam create)
+ # No installer/support/control-plane roles needed
```

**Solution:** Skip account role creation in v1 tests when running against Platform API v2, or stub them out as no-ops.

---

## 5. Cleanup & Teardown Logic Differences

### Overview

The hyperfleet test has **extensive manual cleanup logic** to handle orphaned resources left by cluster controllers. v1 tests rely on OCM backend cleanup. rosactl has **partial cleanup utilities**.

### Cleanup Comparison

| Cleanup Task | Hyperfleet Test | v1 Tests | rosactl v2 |
|--------------|----------------|----------|-----------|
| **Load Balancers (Classic ELB)** | ✅ Manual deletion + wait loops (1024-1116) | ❌ Not in test code (OCM cleanup) | ✅ Utility: `elb.CleanVPCLoadBalancers()` |
| **Load Balancers (ALB/NLB)** | ✅ Manual deletion + wait loops (1056-1144) | ❌ Not in test code (OCM cleanup) | ✅ Utility: `elb.CleanVPCLoadBalancers()` |
| **Orphaned ENIs** | ✅ Manual deletion (1146-1172) | ❌ Not in test code | ❌ Not in cleanup utilities |
| **VPC Endpoints** | ❌ Not handled in test | ❌ Not in test code | ✅ Utility: `ec2.CleanVPCForDeletion()` |
| **Security Groups** | ✅ Manual deletion (1174-1201) | ✅ Handler cleanup | ✅ Utility: `ec2.CleanVPCForDeletion()` |
| **EC2 Instance Termination Wait** | ✅ Manual wait loop (1229-1259) | ❌ Not in test code | ❌ Not in utilities |
| **Route53 Record Purge** | ✅ Manual record deletion (985-1020) | ✅ Handler cleanup | ✅ Utility: `route53.PurgeHostedZoneRecords()` |

### Hyperfleet Test Cleanup Order

```go
// tests/e2e/hyperfleet_sanity_test.go lines 623-664

1. Delete cluster via rosa CLI
2. Wait for cluster to be fully deleted (Platform API confirms deletion)
3. Wait for worker EC2 instances to terminate (15 min timeout)
4. Delete classic ELBs created by ingress controller
5. Wait for classic ELBs to be deleted (5 min timeout)
6. Delete ALBs/NLBs created by ingress controller  
7. Wait for ALBs/NLBs to be deleted (5 min timeout)
8. Delete orphaned ENIs left by cluster controllers
9. Delete non-default security groups
10. (VPC stack cleanup via DeferCleanup LIFO order)
```

### Why the Extensive Cleanup?

**Kubernetes cloud controllers create resources outside of CloudFormation:**
- Ingress controller creates load balancers for `Service` resources
- CSI driver creates volumes and ENIs
- Cloud controller manager creates security groups

**These resources block VPC deletion** if not cleaned up first because they have dependencies on:
- Security groups
- Subnets
- VPC itself

### rosactl Cleanup Utilities

```go
// internal/aws/elb/cleanup.go
elb.CleanVPCLoadBalancers(ctx, cfg, vpcID)
// Deletes all classic ELBs and ALBs/NLBs in the VPC

// internal/aws/ec2/cleanup.go  
ec2.CleanVPCForDeletion(ctx, cfg, vpcID)
// Deletes VPC endpoints and non-default security groups
```

**Usage pattern:**
```bash
rosactl cluster delete my-cluster
# Then before VPC delete:
# Call cleanup utilities if cluster left orphaned resources
rosactl cluster-vpc delete my-cluster
```

### Impact on v1→v2 Migration

**v1 tests may not clean up orphaned resources** because they rely on OCM backend cleanup. When running against Platform API v2:

1. Cluster deletion doesn't trigger OCM cleanup
2. Load balancers remain in the VPC
3. ENIs remain attached
4. Security groups can't be deleted due to dependencies
5. VPC stack deletion fails

**Solution:** Add cleanup logic from hyperfleet test to v1 test teardown:

```go
// After cluster delete
hfWaitVPCInstancesTerminated(ctx, ec2Client, vpcID, 15*time.Minute)
hfDeleteVPCClassicLoadBalancers(ctx, elbClient, vpcID)
hfDeleteVPCLoadBalancers(ctx, elbv2Client, vpcID)
hfDeleteAvailableENIs(ctx, ec2Client, vpcID)
hfDeleteVPCSecurityGroups(ctx, ec2Client, vpcID)
```

Or use rosactl cleanup utilities before stack deletion.

---

## Migration Recommendations

### To Run v1 Tests Against Platform API v2 Environment

**Cannot directly run v1 tests unchanged because:**

1. 🔴 **v1 tests create OIDC before cluster** — Platform API requires cluster-first workflow
2. 🔴 **v1 tests expect account roles** (installer, support, control plane) — Platform API doesn't use them
3. 🟡 **v1 tests may expect ingress hosted zone** — Platform API doesn't need it
4. 🟡 **v1 cleanup expects OCM to handle orphaned resources** — Platform API leaves them

### Recommended Approach

**Option 1: Use rosactl + Modified v1 Tests**
```bash
# Setup infrastructure with rosactl
rosactl cluster-vpc create my-cluster
rosactl cluster-iam create my-cluster  # Creates roles with OIDC=PENDING

# Create cluster (gets issuer URL)
rosa create cluster --hyperfleet-url ... --subnet-ids ... --operator-roles-prefix my-cluster
ISSUER=$(rosa describe cluster -c my-cluster -o json | jq -r .spec.oidc_issuer)

# Update OIDC
rosactl cluster-oidc create my-cluster --oidc-issuer-url $ISSUER

# Now run v1 feature tests (skip infrastructure setup)
```

**Modify v1 tests:**
```diff
- PrepareAccountRoles()  // Skip - Platform API doesn't use these
- PrepareOIDCConfig()    // Skip - cluster-first workflow
- PrepareOIDCProvider()  // Skip - do after cluster
- PrepareOperatorRoles() // Skip - do after cluster
- PrepareHostedZone(ingress) // Skip - Platform API doesn't need it
+ PrepareVPC() or use rosactl cluster-vpc
+ Create cluster
+ Get issuer URL
+ Create OIDC + operator roles
+ Add cleanup for orphaned LBs/ENIs/SGs
```

**Option 2: Wrap Hyperfleet Test as Infrastructure Provider**

Use hyperfleet test setup code as a reusable library:
```go
// Setup phase: run hyperfleet test setup
hfSetup := NewHyperfleetInfraSetup()
clusterID, vpcID := hfSetup.Provision(ctx, clusterName)

// Test phase: run v1 feature tests
RunV1FeatureTests(clusterID)

// Teardown phase: run hyperfleet test cleanup
hfSetup.Cleanup(ctx, clusterID, vpcID)
```

**Option 3: Create v2-Specific Test Suite**

Build new tests from scratch that assume Platform API v2 workflow:
- Use hyperfleet test as template
- Focus on Platform API contract validation
- Include comprehensive cleanup logic
- Don't try to adapt v1 tests

---

## Appendix: Resource Creation Matrix

| Resource | Hyperfleet Test | v1 Tests | rosactl v2 | Created By |
|----------|----------------|----------|-----------|------------|
| VPC | Manual EC2 SDK | `vpc_client.PrepareVPC()` | CloudFormation | User |
| Subnets | Manual EC2 SDK | `vpc_client` helper | CloudFormation | User |
| Internet Gateway | Manual EC2 SDK | `vpc_client` helper | CloudFormation | User |
| NAT Gateway | Manual EC2 SDK | `vpc_client` helper | CloudFormation | User |
| Route Tables | Manual EC2 SDK | `vpc_client` helper | CloudFormation | User |
| Worker Security Group | Manual EC2 SDK | `PrepareSecurityGroups()` | CloudFormation | User |
| Internal Hosted Zone | Manual Route53 SDK | `PrepareHostedZone()` | CloudFormation | User |
| Ingress Hosted Zone | ❌ Not created | `PrepareHostedZone()` (SharedVPC only) | ❌ Not created | User (v1 SharedVPC) |
| Worker IAM Role | Manual IAM SDK | `rosa create account-roles` | CloudFormation | User |
| Worker Instance Profile | Manual IAM SDK | ❌ OCM creates | CloudFormation | User (v2) / OCM (v1) |
| Installer Role | ❌ Not needed | `rosa create account-roles` | ❌ Not needed | OCM (v1 only) |
| Support Role | ❌ Not needed | `rosa create account-roles` | ❌ Not needed | OCM (v1 only) |
| ControlPlane Role | ❌ Not needed | `rosa create account-roles` (Classic) | ❌ Not needed | OCM (v1 Classic) |
| OIDC Provider | Manual IAM SDK (after cluster) | `rosa create oidc-provider` (before cluster) | CloudFormation update (after cluster) | User |
| Operator Roles (7) | Manual IAM SDK (after cluster) | `rosa create operator-roles` (before cluster) | CloudFormation (before cluster, trust updated after) | User |
| Cluster | `rosa create cluster --hyperfleet-url` | `rosa create cluster` | `rosactl cluster create` | Platform API (v2) / OCM (v1) |
| Node Pools | `rosa create machinepool` | `rosa create machinepool` | `rosa create machinepool` | Platform API (v2) / OCM (v1) |

---

## References

### Source Files
- **Hyperfleet sanity test**: `tests/e2e/hyperfleet_sanity_test.go`
- **v1 test cluster handler**: `tests/utils/handler/cluster_handler.go`
- **v1 test resources handler**: `tests/utils/handler/resources_handler_prepare.go`
- **rosactl VPC template**: `internal/cloudformation/templates/cluster-vpc.yaml` (rosa-hyperfleet-cli repo)
- **rosactl IAM template**: `internal/cloudformation/templates/cluster-iam.yaml` (rosa-hyperfleet-cli repo)
- **rosactl OIDC template**: `internal/cloudformation/templates/cluster-oidc.yaml` (rosa-hyperfleet-cli repo)
- **rosactl cleanup utilities**: `internal/aws/{ec2,elb,route53}/cleanup.go` (rosa-hyperfleet-cli repo)

### Related Documentation
- Platform API architecture: `guidelines/hyperfleet-architecture.md`
- ROSA CLI command guidelines: `guidelines/command-guidelines.md`
- AWS guidelines: `guidelines/aws-guidelines.md`

---

*Last updated: 2026-08-20*
