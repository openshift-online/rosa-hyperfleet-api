# Marker Validation Report - Stage 2

**Date:** 2026-09-09 16:16:04
**Total fields analyzed:** 177
**Issues found:** 66
**Suggestions:** 1

---

## Summary

- ⚠️ **Warnings:** 14 (should review)
- ℹ️ **Info:** 52 (informational)

### Issues by Category

- **write-mode:** 62
- **immutability:** 3
- **consistency:** 1

---

## Validation Issues

### ⚠️ `spec.displayName`

**Category:** immutability  
**Issue:** Field name suggests immutability but marked mutable  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=immutable or service-set  
**Confidence:** 70%

---

### ⚠️ `spec.displayName`

**Category:** immutability  
**Issue:** Field name suggests immutability but marked mutable  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=immutable or service-set  
**Confidence:** 70%

---

### ⚠️ `spec.nodePool.clusterName`

**Category:** immutability  
**Issue:** Field name suggests immutability but marked mutable  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=immutable or service-set  
**Confidence:** 70%

---

### ⚠️ `spec.hostedCluster.configuration.kubelet.imageMinimumGCAge`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.configuration.kubelet.maxPods`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.configuration.kubelet.podPidsLimit`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.configuration.kubelet.registryBurst`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.configuration.kubelet.registryPullQPS`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.configuration.kubelet.serializeImagePulls`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.configuration.kubelet.streamingConnectionIdleTimeout`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.imageContentSources`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.networking`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.platform.aws`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ⚠️ `spec.hostedCluster.release`

**Category:** write-mode  
**Issue:** Mutable field in hostedCluster (usually service-set)  
**Current:** write_mode=mutable  
**Suggestion:** Consider write_mode=service-set unless field is user-mutable  
**Confidence:** 60%

---

### ℹ️ `Group: *labels`

**Category:** consistency  
**Issue:** Similar fields have different write_modes: {'service-set', 'mutable'}  
**Current:** Fields: spec.hostedCluster.labels, spec.labels...  
**Suggestion:** Review marker consistency across similar fields  
**Confidence:** 60%

---

### ℹ️ `apiServer`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `authentication`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `featureGate`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `image`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `ingress`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.allowedUnsafeSysctls`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.cpuManagerPolicy`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.cpuManagerPolicyOptions`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.cpuManagerReconcilePeriod`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.evictionHard`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.evictionSoft`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.evictionSoftGracePeriod`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.memoryThrottlingFactor`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.topologyManagerPolicy`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kubelet.topologyManagerScope`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `machineConfig.extensions`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `machineConfig.files`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `machineConfig.kernelArguments`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `machineConfig.kernelType`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `machineConfig.systemdUnits`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `network`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `oauth`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `proxy`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `scheduler`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `allowedUnsafeSysctls`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `cpuManagerPolicy`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `cpuManagerPolicyOptions`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `cpuManagerReconcilePeriod`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `evictionHard`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `evictionSoft`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `evictionSoftGracePeriod`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `memoryThrottlingFactor`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `topologyManagerPolicy`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `topologyManagerScope`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `extensions`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `files`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kernelArguments`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `kernelType`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `systemdUnits`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.internalPoolId`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.arch`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.autoScaling`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.config`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.management`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.nodeDrainTimeout`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.nodeLabels`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.nodeVolumeDetachTimeout`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.osImageStream`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.pausedUntil`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.taints`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

### ℹ️ `spec.nodePool.tuningConfig`

**Category:** write-mode  
**Issue:** service-set field outside typical locations  
**Current:** write_mode=service-set  
**Suggestion:** Verify this field is actually service-managed  
**Confidence:** 50%

---

## Marker Improvement Suggestions

### Consistency

**Observation:** Kubelet fields split between mutable (22) and service-set (20)  
**Recommended action:** Consider standardizing kubelet configuration field markers  
**Confidence:** 70%

---

## Conclusion

⚠️ **14 warnings found** - review recommended.

### Next Steps

1. Review warnings and update markers in source code if needed
2. Re-run `make codegen-registry` to regenerate field_metadata.json
3. Re-run `make accel-validate-markers` to verify fixes
4. Proceed to Stage 3 (Test Mapper) once markers are validated

