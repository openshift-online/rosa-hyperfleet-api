# Manual Review Guide - Unmatched Fields
**Total unmatched fields:** 90
**Grouped by prefix:** 67 groups

---
## Summary by Prefix
| Prefix | Count | Suggested JIRA |
|--------|-------|----------------|
| `spec.hostedCluster.configuration` | 23 | ROSAENG-65162 (Version Gates) |
| `spec.displayName` | 2 | ROSAENG-65625 (Namespace Ownership Policies) |
| `spec.creatorARN` | 1 | No suggestion |
| `spec.expirationTimestamp` | 1 | No suggestion |
| `spec.hostedCluster.auditWebhook` | 1 | No suggestion |
| `spec.hostedCluster.autoscaling` | 1 | ROSAENG-65202 (Cluster Autoscaler) |
| `spec.hostedCluster.channel` | 1 | ROSAENG-65103 (Product Minimal Versions) |
| `spec.hostedCluster.controllerAvailabilityPolicy` | 1 | ROSAENG-65133 (Multiple Ingress Controllers) |
| `spec.hostedCluster.etcd` | 1 | ROSAENG-65101 (FIPS Mode) |
| `spec.hostedCluster.issuerURL` | 1 | No suggestion |
| `spec.hostedCluster.networking` | 1 | No suggestion |
| `spec.hostedCluster.olmCatalogPlacement` | 1 | No suggestion |
| `spec.hostedCluster.pausedUntil` | 1 | No suggestion |
| `spec.hostedCluster.release` | 1 | No suggestion |
| `spec.hostedCluster.serviceAccountSigningKey` | 1 | ROSAENG-65220 (AWS STS Account Roles Inquiry) |
| `spec.hostedCluster.services` | 1 | No suggestion |
| `spec.hostedCluster.sshKey` | 1 | No suggestion |
| `spec.hostedCluster.tolerations` | 1 | No suggestion |
| `spec.hostedCluster.updateService` | 1 | ROSAENG-65191 (Global Pull Secret Updates) |
| `spec.internalId` | 1 | No suggestion |
| `spec.properties` | 1 | No suggestion |
| `featureGate` | 1 | ROSAENG-65162 (Version Gates) |
| `kubelet.allowedUnsafeSysctls` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.containerLogMaxFiles` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.containerLogMaxSize` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.cpuManagerPolicy` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.cpuManagerPolicyOptions` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.cpuManagerReconcilePeriod` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.imageGCHighThresholdPercent` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.imageGCLowThresholdPercent` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.memoryThrottlingFactor` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.podPidsLimit` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.registryPullQPS` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.serializeImagePulls` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.streamingConnectionIdleTimeout` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.topologyManagerPolicy` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `kubelet.topologyManagerScope` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `machineConfig.allowedKernelArguments` | 1 | ROSAENG-65119 (Blocked / Allowed / Insecure Registries) |
| `machineConfig.kernelArguments` | 1 | ROSAENG-65204 (GPU Machine Pools) |
| `machineConfig.kernelType` | 1 | ROSAENG-65204 (GPU Machine Pools) |
| `machineConfig.systemdUnits` | 1 | ROSAENG-65204 (GPU Machine Pools) |
| `oauth` | 1 | No suggestion |
| `proxy` | 1 | ROSAENG-65178 (Transparent Forward Proxies) |
| `scheduler` | 1 | No suggestion |
| `containerLogMaxFiles` | 1 | ROSAENG-65192 (maxUnavailable configurable) |
| `containerLogMaxSize` | 1 | ROSAENG-65164 (Custom Worker Disk Size) |
| `cpuManagerPolicy` | 1 | ROSAENG-65633 (AWS STS Credential Requests) |
| `cpuManagerPolicyOptions` | 1 | ROSAENG-65633 (AWS STS Credential Requests) |
| `cpuManagerReconcilePeriod` | 1 | ROSAENG-65107 (Node Drain Grace Period) |
| `evictionHard` | 1 | No suggestion |
| `evictionSoft` | 1 | No suggestion |
| `imageGCHighThresholdPercent` | 1 | ROSAENG-65153 (Image Digest Mirror Sets (IDMS)) |
| `imageGCLowThresholdPercent` | 1 | ROSAENG-65153 (Image Digest Mirror Sets (IDMS)) |
| `kubeReserved` | 1 | ROSAENG-65216 (Kubelet Configs) |
| `maxPods` | 1 | ROSAENG-65192 (maxUnavailable configurable) |
| `memoryThrottlingFactor` | 1 | No suggestion |
| `podPidsLimit` | 1 | ROSAENG-65223 (Limited Support Reasons) |
| `streamingConnectionIdleTimeout` | 1 | No suggestion |
| `systemReserved` | 1 | No suggestion |
| `topologyManagerPolicy` | 1 | ROSAENG-65633 (AWS STS Credential Requests) |
| `topologyManagerScope` | 1 | No suggestion |
| `extensions` | 1 | No suggestion |
| `files` | 1 | No suggestion |
| `kernelArguments` | 1 | No suggestion |
| `systemdUnits` | 1 | No suggestion |
| `spec.autoRepair` | 1 | ROSAENG-65202 (Cluster Autoscaler) |
| `spec.issuerUrl` | 1 | No suggestion |

---
## Detailed Suggestions

### spec.hostedCluster.configuration (23 fields)

#### `spec.hostedCluster.configuration.featureGate`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** feature, gate

**Suggested JIRA tickets:**

1. **ROSAENG-65162** - Version Gates
   - Confidence: 0.45
   - Complexity: `complex`
   - Tests: 0 (0 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: gate
   - Description: No feature-gates CLI FVT in tests/e2e

---

#### `spec.hostedCluster.configuration.kubelet.allowedUnsafeSysctls`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, allowed, unsafe, sysctls

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65206** - Additional Allowed Principals
   - Confidence: 0.25
   - Complexity: `complex`
   - Tests: 3 (3 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: allowed
   - Description: Additional allowed principals create/edit/negative

3. **ROSAENG-65119** - Blocked / Allowed / Insecure Registries
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: allowed
   - Description: Blocked/allowed/insecure registries via registry-config

---

#### `spec.hostedCluster.configuration.kubelet.containerLogMaxFiles`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, container, log, max, files

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65192** - maxUnavailable configurable
   - Confidence: 0.14
   - Complexity: `unknown`
   - Tests: 2 (2 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: max
   - Description: HCP nodepool maxUnavailable/maxSurge

3. **ROSAENG-65168** - Scaling to Zero
   - Confidence: 0.04
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP scale/autoscaling/max-nodes; confirm replicas=0 is actually asserted

---

#### `spec.hostedCluster.configuration.kubelet.containerLogMaxSize`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, container, log, max, size

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65164** - Custom Worker Disk Size
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: size
   - Description: Worker disk / root volume size on cluster and machinepool

3. **ROSAENG-65192** - maxUnavailable configurable
   - Confidence: 0.14
   - Complexity: `unknown`
   - Tests: 2 (2 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: max
   - Description: HCP nodepool maxUnavailable/maxSurge

---

#### `spec.hostedCluster.configuration.kubelet.cpuManagerPolicy`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, cpu, manager, policy

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

3. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

---

#### `spec.hostedCluster.configuration.kubelet.cpuManagerPolicyOptions`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, cpu, manager, policy, options

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.04
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

3. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.04
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

---

#### `spec.hostedCluster.configuration.kubelet.cpuManagerReconcilePeriod`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, cpu, manager, reconcile, period

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65107** - Node Drain Grace Period
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 1 (1 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: period
   - Description: HCP nodepool node_drain_grace_period

3. **ROSAENG-65127** - End-of-Life Grace Period
   - Confidence: 0.16
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: period
   - Description: No CLI FVT match found

---

#### `spec.hostedCluster.configuration.kubelet.imageGCHighThresholdPercent`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, image, high, threshold, percent

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65153** - Image Digest Mirror Sets (IDMS)
   - Confidence: 0.20
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: image
   - Description: Image mirror (IDMS) CRUD

3. **ROSAENG-65148** - IDMS & Zero-Egress Mirrors
   - Confidence: 0.04
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: IDMS image-mirror only; no zero-egress-specific CLI FVT found

---

#### `spec.hostedCluster.configuration.kubelet.imageGCLowThresholdPercent`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, image, low, threshold, percent

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65153** - Image Digest Mirror Sets (IDMS)
   - Confidence: 0.20
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: image
   - Description: Image mirror (IDMS) CRUD

3. **ROSAENG-65207** - Registry Allowlists
   - Confidence: 0.14
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: low
   - Description: Registry allowlists via registry-config flags

---

#### `spec.hostedCluster.configuration.kubelet.memoryThrottlingFactor`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, memory, throttling, factor

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

#### `spec.hostedCluster.configuration.kubelet.podPidsLimit`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, pod, pids, limit

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65223** - Limited Support Reasons
   - Confidence: 0.12
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: limit
   - Description: No CLI FVT match found

3. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

---

#### `spec.hostedCluster.configuration.kubelet.registryPullQPS`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, registry, pull, qps

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65207** - Registry Allowlists
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: registry
   - Description: Registry allowlists via registry-config flags

3. **ROSAENG-65191** - Global Pull Secret Updates
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: pull
   - Description: No global pull-secret CLI FVT match found

---

#### `spec.hostedCluster.configuration.kubelet.serializeImagePulls`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, serialize, image, pulls

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65153** - Image Digest Mirror Sets (IDMS)
   - Confidence: 0.25
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: image
   - Description: Image mirror (IDMS) CRUD

3. **ROSAENG-65148** - IDMS & Zero-Egress Mirrors
   - Confidence: 0.05
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: IDMS image-mirror only; no zero-egress-specific CLI FVT found

---

#### `spec.hostedCluster.configuration.kubelet.streamingConnectionIdleTimeout`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, streaming, connection, idle, timeout

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

#### `spec.hostedCluster.configuration.kubelet.topologyManagerPolicy`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, topology, manager, policy

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

3. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

---

#### `spec.hostedCluster.configuration.kubelet.topologyManagerScope`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, topology, manager, scope

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

#### `spec.hostedCluster.configuration.machineConfig.allowedKernelArguments`

- **Owner:** Cluster
- **Write mode:** immutable
- **Hidden:** False
- **Keywords:** machine, config, allowed, kernel, arguments

**Suggested JIRA tickets:**

1. **ROSAENG-65119** - Blocked / Allowed / Insecure Registries
   - Confidence: 0.24
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: allowed
   - Description: Blocked/allowed/insecure registries via registry-config

2. **ROSAENG-65206** - Additional Allowed Principals
   - Confidence: 0.20
   - Complexity: `complex`
   - Tests: 3 (3 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: allowed
   - Description: Additional allowed principals create/edit/negative

3. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.16
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: machine
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

---

#### `spec.hostedCluster.configuration.machineConfig.kernelArguments`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** machine, config, kernel, arguments

**Suggested JIRA tickets:**

1. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: machine
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

2. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.17
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: config
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

3. **ROSAENG-65166** - Tuning Configs
   - Confidence: 0.17
   - Complexity: `complex`
   - Tests: 6 (5 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: config
   - Description: HCP tuning configs + nodepool attach; Classic file is N/A

---

#### `spec.hostedCluster.configuration.machineConfig.kernelType`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** machine, config, kernel, type

**Suggested JIRA tickets:**

1. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: machine
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

2. **ROSAENG-65142** - Network Type Selection
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: type
   - Description: HCP network type create/validate

3. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.17
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: config
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

#### `spec.hostedCluster.configuration.machineConfig.systemdUnits`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** machine, config, systemd, units

**Suggested JIRA tickets:**

1. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: machine
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

2. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.17
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: config
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

3. **ROSAENG-65166** - Tuning Configs
   - Confidence: 0.17
   - Complexity: `complex`
   - Tests: 6 (5 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: config
   - Description: HCP tuning configs + nodepool attach; Classic file is N/A

---

#### `spec.hostedCluster.configuration.oauth`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** oauth

**No JIRA suggestions found** - May need new ticket or is out of scope

---

#### `spec.hostedCluster.configuration.proxy`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** proxy

**Suggested JIRA tickets:**

1. **ROSAENG-65178** - Transparent Forward Proxies
   - Confidence: 0.20
   - Complexity: `complex`
   - Tests: 4 (4 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: Cluster proxy day1-post / day2 / negative

2. **ROSAENG-65150** - allow zero nodepools/machinepools
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP machinepool deletion / max nodes — weak proxy for 'zero nodepools'

3. **ROSAENG-65104** - CIDR Block Access Control
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: Machine CIDR validation — weak proxy for CIDR access-control / trusted IPs

---

#### `spec.hostedCluster.configuration.scheduler`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** scheduler

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.displayName (2 fields)

#### `spec.displayName`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** display, name

**Suggested JIRA tickets:**

1. **ROSAENG-65625** - Namespace Ownership Policies
   - Confidence: 0.35
   - Complexity: `passthrough`
   - Tests: 2 (0 TBD)
   - Suggested status: `passthrough-clean`
   - Matched keywords: name
   - Description: Ingress namespace-ownership-policy (Classic → N/A on HCP profile)

2. **ROSAENG-65205** - Excluded Namespaces / Selectors
   - Confidence: 0.35
   - Complexity: `passthrough`
   - Tests: 2 (0 TBD)
   - Suggested status: `passthrough-clean`
   - Matched keywords: name
   - Description: Ingress excluded-namespaces / route selectors (Classic → N/A on HCP profile)

3. **ROSAENG-65628** - Ingress Namespace Selectors
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 2 (0 TBD)
   - Suggested status: `passthrough-clean`
   - Matched keywords: name
   - Description: Ingress route selectors (Classic SkipNotClassic → N/A on HCP profile)

---

#### `spec.displayName`

- **Owner:** NodePool
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** display, name

**Suggested JIRA tickets:**

1. **ROSAENG-65625** - Namespace Ownership Policies
   - Confidence: 0.35
   - Complexity: `passthrough`
   - Tests: 2 (0 TBD)
   - Suggested status: `passthrough-clean`
   - Matched keywords: name
   - Description: Ingress namespace-ownership-policy (Classic → N/A on HCP profile)

2. **ROSAENG-65205** - Excluded Namespaces / Selectors
   - Confidence: 0.35
   - Complexity: `passthrough`
   - Tests: 2 (0 TBD)
   - Suggested status: `passthrough-clean`
   - Matched keywords: name
   - Description: Ingress excluded-namespaces / route selectors (Classic → N/A on HCP profile)

3. **ROSAENG-65628** - Ingress Namespace Selectors
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 2 (0 TBD)
   - Suggested status: `passthrough-clean`
   - Matched keywords: name
   - Description: Ingress route selectors (Classic SkipNotClassic → N/A on HCP profile)

---

### spec.creatorARN (1 fields)

#### `spec.creatorARN`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** creator, arn

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.expirationTimestamp (1 fields)

#### `spec.expirationTimestamp`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** expiration, timestamp

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.auditWebhook (1 fields)

#### `spec.hostedCluster.auditWebhook`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** audit, webhook

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.autoscaling (1 fields)

#### `spec.hostedCluster.autoscaling`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** autoscaling

**Suggested JIRA tickets:**

1. **ROSAENG-65202** - Cluster Autoscaler
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: Cluster autoscaler HCP + Classic; nodepool autoscaling; Classic Its N/A on HCP profile

2. **ROSAENG-65168** - Scaling to Zero
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP scale/autoscaling/max-nodes; confirm replicas=0 is actually asserted

---

### spec.hostedCluster.channel (1 fields)

#### `spec.hostedCluster.channel`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** False
- **Keywords:** channel

**Suggested JIRA tickets:**

1. **ROSAENG-65103** - Product Minimal Versions
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 4 (3 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: Version list (hosted-cp) + nodepool version + channel-group negative

---

### spec.hostedCluster.controllerAvailabilityPolicy (1 fields)

#### `spec.hostedCluster.controllerAvailabilityPolicy`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** controller, availability, policy

**Suggested JIRA tickets:**

1. **ROSAENG-65133** - Multiple Ingress Controllers
   - Confidence: 0.17
   - Complexity: `complex`
   - Tests: 3 (3 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: controller
   - Description: HCP default ingress update/describe; 71174 asserts default-ingress edit limits on HCP

2. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.07
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

3. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.07
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

---

### spec.hostedCluster.etcd (1 fields)

#### `spec.hostedCluster.etcd`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** etcd

**Suggested JIRA tickets:**

1. **ROSAENG-65101** - FIPS Mode
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 2 (1 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: FIPS cluster create + FIPS/etcd-encryption negative

---

### spec.hostedCluster.issuerURL (1 fields)

#### `spec.hostedCluster.issuerURL`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** False
- **Keywords:** issuer, url

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.networking (1 fields)

#### `spec.hostedCluster.networking`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** networking

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.olmCatalogPlacement (1 fields)

#### `spec.hostedCluster.olmCatalogPlacement`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** olm, catalog, placement

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.pausedUntil (1 fields)

#### `spec.hostedCluster.pausedUntil`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** False
- **Keywords:** paused, until

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.release (1 fields)

#### `spec.hostedCluster.release`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** release

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.serviceAccountSigningKey (1 fields)

#### `spec.hostedCluster.serviceAccountSigningKey`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** service, account, signing, key

**Suggested JIRA tickets:**

1. **ROSAENG-65220** - AWS STS Account Roles Inquiry
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: account
   - Description: Account-roles list/create including hosted-cp

2. **ROSAENG-65184** - Permission Boundaries
   - Confidence: 0.10
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: --permissions-boundary on account-roles, operator-roles, IAM service account

3. **ROSAENG-65140** - IRSA
   - Confidence: 0.10
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: IAM service account roles (IRSA)

---

### spec.hostedCluster.services (1 fields)

#### `spec.hostedCluster.services`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** services

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.sshKey (1 fields)

#### `spec.hostedCluster.sshKey`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** ssh, key

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.tolerations (1 fields)

#### `spec.hostedCluster.tolerations`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** tolerations

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.hostedCluster.updateService (1 fields)

#### `spec.hostedCluster.updateService`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** update, service

**Suggested JIRA tickets:**

1. **ROSAENG-65191** - Global Pull Secret Updates
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: update
   - Description: No global pull-secret CLI FVT match found

2. **ROSAENG-65184** - Permission Boundaries
   - Confidence: 0.10
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: --permissions-boundary on account-roles, operator-roles, IAM service account

3. **ROSAENG-65140** - IRSA
   - Confidence: 0.10
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: IAM service account roles (IRSA)

---

### spec.internalId (1 fields)

#### `spec.internalId`

- **Owner:** Cluster
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** internal

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.properties (1 fields)

#### `spec.properties`

- **Owner:** Cluster
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** properties

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### featureGate (1 fields)

#### `featureGate`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** feature, gate

**Suggested JIRA tickets:**

1. **ROSAENG-65162** - Version Gates
   - Confidence: 0.45
   - Complexity: `complex`
   - Tests: 0 (0 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: gate
   - Description: No feature-gates CLI FVT in tests/e2e

---

### kubelet.allowedUnsafeSysctls (1 fields)

#### `kubelet.allowedUnsafeSysctls`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, allowed, unsafe, sysctls

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65206** - Additional Allowed Principals
   - Confidence: 0.25
   - Complexity: `complex`
   - Tests: 3 (3 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: allowed
   - Description: Additional allowed principals create/edit/negative

3. **ROSAENG-65119** - Blocked / Allowed / Insecure Registries
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: allowed
   - Description: Blocked/allowed/insecure registries via registry-config

---

### kubelet.containerLogMaxFiles (1 fields)

#### `kubelet.containerLogMaxFiles`

- **Owner:** ClusterConfiguration
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, container, log, max, files

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65192** - maxUnavailable configurable
   - Confidence: 0.14
   - Complexity: `unknown`
   - Tests: 2 (2 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: max
   - Description: HCP nodepool maxUnavailable/maxSurge

3. **ROSAENG-65168** - Scaling to Zero
   - Confidence: 0.04
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP scale/autoscaling/max-nodes; confirm replicas=0 is actually asserted

---

### kubelet.containerLogMaxSize (1 fields)

#### `kubelet.containerLogMaxSize`

- **Owner:** ClusterConfiguration
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, container, log, max, size

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65164** - Custom Worker Disk Size
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: size
   - Description: Worker disk / root volume size on cluster and machinepool

3. **ROSAENG-65192** - maxUnavailable configurable
   - Confidence: 0.14
   - Complexity: `unknown`
   - Tests: 2 (2 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: max
   - Description: HCP nodepool maxUnavailable/maxSurge

---

### kubelet.cpuManagerPolicy (1 fields)

#### `kubelet.cpuManagerPolicy`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, cpu, manager, policy

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

3. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

---

### kubelet.cpuManagerPolicyOptions (1 fields)

#### `kubelet.cpuManagerPolicyOptions`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, cpu, manager, policy, options

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.04
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

3. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.04
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

---

### kubelet.cpuManagerReconcilePeriod (1 fields)

#### `kubelet.cpuManagerReconcilePeriod`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, cpu, manager, reconcile, period

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65107** - Node Drain Grace Period
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 1 (1 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: period
   - Description: HCP nodepool node_drain_grace_period

3. **ROSAENG-65127** - End-of-Life Grace Period
   - Confidence: 0.16
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: period
   - Description: No CLI FVT match found

---

### kubelet.imageGCHighThresholdPercent (1 fields)

#### `kubelet.imageGCHighThresholdPercent`

- **Owner:** ClusterConfiguration
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, image, high, threshold, percent

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65153** - Image Digest Mirror Sets (IDMS)
   - Confidence: 0.20
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: image
   - Description: Image mirror (IDMS) CRUD

3. **ROSAENG-65148** - IDMS & Zero-Egress Mirrors
   - Confidence: 0.04
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: IDMS image-mirror only; no zero-egress-specific CLI FVT found

---

### kubelet.imageGCLowThresholdPercent (1 fields)

#### `kubelet.imageGCLowThresholdPercent`

- **Owner:** ClusterConfiguration
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, image, low, threshold, percent

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65153** - Image Digest Mirror Sets (IDMS)
   - Confidence: 0.20
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: image
   - Description: Image mirror (IDMS) CRUD

3. **ROSAENG-65207** - Registry Allowlists
   - Confidence: 0.14
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: low
   - Description: Registry allowlists via registry-config flags

---

### kubelet.memoryThrottlingFactor (1 fields)

#### `kubelet.memoryThrottlingFactor`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, memory, throttling, factor

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

### kubelet.podPidsLimit (1 fields)

#### `kubelet.podPidsLimit`

- **Owner:** ClusterConfiguration
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, pod, pids, limit

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65223** - Limited Support Reasons
   - Confidence: 0.12
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: limit
   - Description: No CLI FVT match found

3. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

---

### kubelet.registryPullQPS (1 fields)

#### `kubelet.registryPullQPS`

- **Owner:** ClusterConfiguration
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, registry, pull, qps

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65207** - Registry Allowlists
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: registry
   - Description: Registry allowlists via registry-config flags

3. **ROSAENG-65191** - Global Pull Secret Updates
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: pull
   - Description: No global pull-secret CLI FVT match found

---

### kubelet.serializeImagePulls (1 fields)

#### `kubelet.serializeImagePulls`

- **Owner:** ClusterConfiguration
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, serialize, image, pulls

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65153** - Image Digest Mirror Sets (IDMS)
   - Confidence: 0.25
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: image
   - Description: Image mirror (IDMS) CRUD

3. **ROSAENG-65148** - IDMS & Zero-Egress Mirrors
   - Confidence: 0.05
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: IDMS image-mirror only; no zero-egress-specific CLI FVT found

---

### kubelet.streamingConnectionIdleTimeout (1 fields)

#### `kubelet.streamingConnectionIdleTimeout`

- **Owner:** ClusterConfiguration
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** kubelet, streaming, connection, idle, timeout

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

### kubelet.topologyManagerPolicy (1 fields)

#### `kubelet.topologyManagerPolicy`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, topology, manager, policy

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

2. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

3. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

---

### kubelet.topologyManagerScope (1 fields)

#### `kubelet.topologyManagerScope`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kubelet, topology, manager, scope

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kubelet
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

### machineConfig.allowedKernelArguments (1 fields)

#### `machineConfig.allowedKernelArguments`

- **Owner:** ClusterConfiguration
- **Write mode:** immutable
- **Hidden:** False
- **Keywords:** machine, config, allowed, kernel, arguments

**Suggested JIRA tickets:**

1. **ROSAENG-65119** - Blocked / Allowed / Insecure Registries
   - Confidence: 0.24
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: allowed
   - Description: Blocked/allowed/insecure registries via registry-config

2. **ROSAENG-65206** - Additional Allowed Principals
   - Confidence: 0.20
   - Complexity: `complex`
   - Tests: 3 (3 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: allowed
   - Description: Additional allowed principals create/edit/negative

3. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.16
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: machine
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

---

### machineConfig.kernelArguments (1 fields)

#### `machineConfig.kernelArguments`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** machine, config, kernel, arguments

**Suggested JIRA tickets:**

1. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: machine
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

2. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.17
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: config
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

3. **ROSAENG-65166** - Tuning Configs
   - Confidence: 0.17
   - Complexity: `complex`
   - Tests: 6 (5 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: config
   - Description: HCP tuning configs + nodepool attach; Classic file is N/A

---

### machineConfig.kernelType (1 fields)

#### `machineConfig.kernelType`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** machine, config, kernel, type

**Suggested JIRA tickets:**

1. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: machine
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

2. **ROSAENG-65142** - Network Type Selection
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: type
   - Description: HCP network type create/validate

3. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.17
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: config
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

### machineConfig.systemdUnits (1 fields)

#### `machineConfig.systemdUnits`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** machine, config, systemd, units

**Suggested JIRA tickets:**

1. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: machine
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

2. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.17
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: config
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

3. **ROSAENG-65166** - Tuning Configs
   - Confidence: 0.17
   - Complexity: `complex`
   - Tests: 6 (5 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: config
   - Description: HCP tuning configs + nodepool attach; Classic file is N/A

---

### oauth (1 fields)

#### `oauth`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** oauth

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### proxy (1 fields)

#### `proxy`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** proxy

**Suggested JIRA tickets:**

1. **ROSAENG-65178** - Transparent Forward Proxies
   - Confidence: 0.20
   - Complexity: `complex`
   - Tests: 4 (4 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: Cluster proxy day1-post / day2 / negative

2. **ROSAENG-65150** - allow zero nodepools/machinepools
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP machinepool deletion / max nodes — weak proxy for 'zero nodepools'

3. **ROSAENG-65104** - CIDR Block Access Control
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: Machine CIDR validation — weak proxy for CIDR access-control / trusted IPs

---

### scheduler (1 fields)

#### `scheduler`

- **Owner:** ClusterConfiguration
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** scheduler

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### containerLogMaxFiles (1 fields)

#### `containerLogMaxFiles`

- **Owner:** KubeletConfig
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** container, log, max, files

**Suggested JIRA tickets:**

1. **ROSAENG-65192** - maxUnavailable configurable
   - Confidence: 0.17
   - Complexity: `unknown`
   - Tests: 2 (2 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: max
   - Description: HCP nodepool maxUnavailable/maxSurge

2. **ROSAENG-65168** - Scaling to Zero
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP scale/autoscaling/max-nodes; confirm replicas=0 is actually asserted

3. **ROSAENG-65150** - allow zero nodepools/machinepools
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP machinepool deletion / max nodes — weak proxy for 'zero nodepools'

---

### containerLogMaxSize (1 fields)

#### `containerLogMaxSize`

- **Owner:** KubeletConfig
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** container, log, max, size

**Suggested JIRA tickets:**

1. **ROSAENG-65164** - Custom Worker Disk Size
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: size
   - Description: Worker disk / root volume size on cluster and machinepool

2. **ROSAENG-65192** - maxUnavailable configurable
   - Confidence: 0.17
   - Complexity: `unknown`
   - Tests: 2 (2 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: max
   - Description: HCP nodepool maxUnavailable/maxSurge

3. **ROSAENG-65168** - Scaling to Zero
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP scale/autoscaling/max-nodes; confirm replicas=0 is actually asserted

---

### cpuManagerPolicy (1 fields)

#### `cpuManagerPolicy`

- **Owner:** KubeletConfig
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** cpu, manager, policy

**Suggested JIRA tickets:**

1. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.07
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

2. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.07
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

3. **ROSAENG-65631** - Control Plane Upgrade Policies
   - Confidence: 0.07
   - Complexity: `complex`
   - Tests: 6 (5 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: Control-plane upgrade policy list/create/delete (HCP + classic)

---

### cpuManagerPolicyOptions (1 fields)

#### `cpuManagerPolicyOptions`

- **Owner:** KubeletConfig
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** cpu, manager, policy, options

**Suggested JIRA tickets:**

1. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

2. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.05
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

3. **ROSAENG-65631** - Control Plane Upgrade Policies
   - Confidence: 0.05
   - Complexity: `complex`
   - Tests: 6 (5 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: Control-plane upgrade policy list/create/delete (HCP + classic)

---

### cpuManagerReconcilePeriod (1 fields)

#### `cpuManagerReconcilePeriod`

- **Owner:** KubeletConfig
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** cpu, manager, reconcile, period

**Suggested JIRA tickets:**

1. **ROSAENG-65107** - Node Drain Grace Period
   - Confidence: 0.25
   - Complexity: `passthrough`
   - Tests: 1 (1 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: period
   - Description: HCP nodepool node_drain_grace_period

2. **ROSAENG-65127** - End-of-Life Grace Period
   - Confidence: 0.20
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: period
   - Description: No CLI FVT match found

3. **ROSAENG-65132** - Node Pool Upgrade Strategy & Drain
   - Confidence: 0.05
   - Complexity: `complex`
   - Tests: 6 (6 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: Node drain grace period + maxUnavailable/maxSurge + nodepool upgrade

---

### evictionHard (1 fields)

#### `evictionHard`

- **Owner:** KubeletConfig
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** eviction, hard

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### evictionSoft (1 fields)

#### `evictionSoft`

- **Owner:** KubeletConfig
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** eviction, soft

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### imageGCHighThresholdPercent (1 fields)

#### `imageGCHighThresholdPercent`

- **Owner:** KubeletConfig
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** image, high, threshold, percent

**Suggested JIRA tickets:**

1. **ROSAENG-65153** - Image Digest Mirror Sets (IDMS)
   - Confidence: 0.25
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: image
   - Description: Image mirror (IDMS) CRUD

2. **ROSAENG-65148** - IDMS & Zero-Egress Mirrors
   - Confidence: 0.05
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: IDMS image-mirror only; no zero-egress-specific CLI FVT found

---

### imageGCLowThresholdPercent (1 fields)

#### `imageGCLowThresholdPercent`

- **Owner:** KubeletConfig
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** image, low, threshold, percent

**Suggested JIRA tickets:**

1. **ROSAENG-65153** - Image Digest Mirror Sets (IDMS)
   - Confidence: 0.25
   - Complexity: `complex`
   - Tests: 1 (1 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: image
   - Description: Image mirror (IDMS) CRUD

2. **ROSAENG-65207** - Registry Allowlists
   - Confidence: 0.17
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: low
   - Description: Registry allowlists via registry-config flags

3. **ROSAENG-65206** - Additional Allowed Principals
   - Confidence: 0.17
   - Complexity: `complex`
   - Tests: 3 (3 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: low
   - Description: Additional allowed principals create/edit/negative

---

### kubeReserved (1 fields)

#### `kubeReserved`

- **Owner:** KubeletConfig
- **Write mode:** immutable
- **Hidden:** False
- **Keywords:** kube, reserved

**Suggested JIRA tickets:**

1. **ROSAENG-65216** - Kubelet Configs
   - Confidence: 0.35
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: kube
   - Description: KubeletConfig HCP CRUD/attach; Classic Its are N/A on HyperFleet profile

---

### maxPods (1 fields)

#### `maxPods`

- **Owner:** KubeletConfig
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** max, pods

**Suggested JIRA tickets:**

1. **ROSAENG-65192** - maxUnavailable configurable
   - Confidence: 0.35
   - Complexity: `unknown`
   - Tests: 2 (2 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: max
   - Description: HCP nodepool maxUnavailable/maxSurge

2. **ROSAENG-65168** - Scaling to Zero
   - Confidence: 0.10
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP scale/autoscaling/max-nodes; confirm replicas=0 is actually asserted

3. **ROSAENG-65150** - allow zero nodepools/machinepools
   - Confidence: 0.10
   - Complexity: `passthrough`
   - Tests: 2 (2 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP machinepool deletion / max nodes — weak proxy for 'zero nodepools'

---

### memoryThrottlingFactor (1 fields)

#### `memoryThrottlingFactor`

- **Owner:** KubeletConfig
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** memory, throttling, factor

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### podPidsLimit (1 fields)

#### `podPidsLimit`

- **Owner:** KubeletConfig
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** pod, pids, limit

**Suggested JIRA tickets:**

1. **ROSAENG-65223** - Limited Support Reasons
   - Confidence: 0.17
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: limit
   - Description: No CLI FVT match found

2. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.07
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

3. **ROSAENG-65133** - Multiple Ingress Controllers
   - Confidence: 0.07
   - Complexity: `complex`
   - Tests: 3 (3 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: HCP default ingress update/describe; 71174 asserts default-ingress edit limits on HCP

---

### streamingConnectionIdleTimeout (1 fields)

#### `streamingConnectionIdleTimeout`

- **Owner:** KubeletConfig
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** streaming, connection, idle, timeout

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### systemReserved (1 fields)

#### `systemReserved`

- **Owner:** KubeletConfig
- **Write mode:** immutable
- **Hidden:** False
- **Keywords:** system, reserved

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### topologyManagerPolicy (1 fields)

#### `topologyManagerPolicy`

- **Owner:** KubeletConfig
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** topology, manager, policy

**Suggested JIRA tickets:**

1. **ROSAENG-65633** - AWS STS Credential Requests
   - Confidence: 0.07
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS credential/trust-policy coverage on account-roles and hosted-cp create

2. **ROSAENG-65632** - AWS STS Policies Inquiry
   - Confidence: 0.07
   - Complexity: `passthrough`
   - Tests: 6 (6 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: STS policy attach/upgrade and managed-policy operator-role checks

3. **ROSAENG-65631** - Control Plane Upgrade Policies
   - Confidence: 0.07
   - Complexity: `complex`
   - Tests: 6 (5 TBD)
   - Suggested status: `not-passthrough`
   - Matched keywords: 
   - Description: Control-plane upgrade policy list/create/delete (HCP + classic)

---

### topologyManagerScope (1 fields)

#### `topologyManagerScope`

- **Owner:** KubeletConfig
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** topology, manager, scope

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### extensions (1 fields)

#### `extensions`

- **Owner:** MachineConfigSpec
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** extensions

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### files (1 fields)

#### `files`

- **Owner:** MachineConfigSpec
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** files

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### kernelArguments (1 fields)

#### `kernelArguments`

- **Owner:** MachineConfigSpec
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** kernel, arguments

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### systemdUnits (1 fields)

#### `systemdUnits`

- **Owner:** MachineConfigSpec
- **Write mode:** service-set
- **Hidden:** True
- **Keywords:** systemd, units

**No JIRA suggestions found** - May need new ticket or is out of scope

---

### spec.autoRepair (1 fields)

#### `spec.autoRepair`

- **Owner:** NodePool
- **Write mode:** mutable
- **Hidden:** False
- **Keywords:** auto, repair

**Suggested JIRA tickets:**

1. **ROSAENG-65202** - Cluster Autoscaler
   - Confidence: 0.35
   - Complexity: `passthrough`
   - Tests: 7 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: auto
   - Description: Cluster autoscaler HCP + Classic; nodepool autoscaling; Classic Its N/A on HCP profile

2. **ROSAENG-65204** - GPU Machine Pools
   - Confidence: 0.10
   - Complexity: `passthrough`
   - Tests: 0 (0 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: No GPU instance-type node pool CLI FVT (autoscaler --gpu-limit is a different capability)

3. **ROSAENG-65168** - Scaling to Zero
   - Confidence: 0.10
   - Complexity: `passthrough`
   - Tests: 4 (4 TBD)
   - Suggested status: `needs-test`
   - Matched keywords: 
   - Description: HCP scale/autoscaling/max-nodes; confirm replicas=0 is actually asserted

---

### spec.issuerUrl (1 fields)

#### `spec.issuerUrl`

- **Owner:** OidcConfig
- **Write mode:** immutable
- **Hidden:** False
- **Keywords:** issuer, url

**No JIRA suggestions found** - May need new ticket or is out of scope

---

