# Client-Go for rosa-hyperfleet-api: Why It Works and How to Do It

> **Generation prompt:** `/tmp/prompt-lib/v2.p1.md` — explains how to build the
> rosa-hyperfleet-api with client-go support instead of a custom SDK like
> ocm-sdk-go, using the hypershift project as a reference.

---

## The Problem

External Go programs need to interact with rosa-hyperfleet-api resources
(Cluster, NodePool, ManagementCluster, Manifest, Placement). The traditional
OCM approach is to build a bespoke SDK (`ocm-sdk-go`) with hand-written
request/response builders, connection management, authentication helpers, and
serialization logic — hundreds of generated files that must be versioned and
maintained in lockstep with the API.

**The question:** Can we skip all that and let consumers use standard
Kubernetes client-go tooling instead?

**The answer:** Yes — and the project is already 80% of the way there.

---

## How HyperShift Does It

The [openshift/hypershift](https://github.com/openshift/hypershift) project
publishes a **standalone API types module** (`github.com/openshift/hypershift/api`)
and a **generated typed clientset** (`github.com/openshift/hypershift/client`).

External consumers have two choices:

### Option A: controller-runtime client (lightweight)

```go
import (
    hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/scheme"
)

// Register the scheme once
s := runtime.NewScheme()
hyperv1.AddToScheme(s)

// Use any controller-runtime client
c, _ := client.New(cfg, client.Options{Scheme: s})
c.List(ctx, &hyperv1.HostedClusterList{}, client.InNamespace("clusters"))
```

### Option B: native typed clientset (familiar kubectl-style API)

```go
import (
    hyperclient "github.com/openshift/hypershift/client/clientset/clientset"
)

cs, _ := hyperclient.NewForConfig(cfg)
hcs, _ := cs.HypershiftV1beta1().HostedClusters("clusters").List(ctx, metav1.ListOptions{})
```

Both work because hypershift runs `k8s.io/code-generator` (`client-gen`,
`lister-gen`, `informer-gen`, `applyconfiguration-gen`) via a
`hack/update-codegen.sh` script.

---

## What rosa-hyperfleet-api Already Has

| Piece | Status | Where |
|-------|--------|-------|
| Standalone API types module | **Done** | `hyperfleet-operator/api/go.mod` |
| Minimal dependencies on API module | **Done** | Only `k8s.io/apimachinery` + `openshift/hypershift/api` |
| `SchemeBuilder` + `AddToScheme()` | **Done** | `api/v1alpha1/groupversion_info.go` |
| `+kubebuilder:object:root=true` markers | **Done** | All 5 CRD types |
| Deep-copy generation | **Done** | `zz_generated.deepcopy.go` |
| CRD YAML manifests | **Done** | `config/crd/bases/` |
| Generated typed clientset | **Not done** | — |
| Generated informers / listers | **Not done** | — |

**The API types module is already structured correctly.** Any Go program can
import `github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api`
today with zero extra work and use it with controller-runtime's `client.Client`.

---

## What It Takes to Add Full client-go Support

### Step 1: controller-runtime path (works today, zero changes)

An external Go consumer can already do this:

```go
import (
    hyperfleetv1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
    "k8s.io/apimachinery/pkg/runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/client/config"
)

func main() {
    s := runtime.NewScheme()
    hyperfleetv1.AddToScheme(s)

    cfg := config.GetConfigOrDie()
    c, err := client.New(cfg, client.Options{Scheme: s})

    // List clusters
    clusters := &hyperfleetv1.ClusterList{}
    c.List(ctx, clusters, client.InNamespace("my-namespace"))

    // Create a node pool
    np := &hyperfleetv1.NodePool{
        ObjectMeta: metav1.ObjectMeta{Name: "pool-1", Namespace: "cluster-uuid"},
        Spec: hyperfleetv1.NodePoolSpec{...},
    }
    c.Create(ctx, np)

    // Watch for changes
    c.Watch(ctx, &hyperfleetv1.ClusterList{})
}
```

This is the **recommended starting point** — it requires no code generation,
no new packages, and no maintenance burden.

### Step 2: native typed clientset (optional, for kubectl-like ergonomics)

If consumers want typed methods like
`Clusters("ns").Get(ctx, "name", metav1.GetOptions{})` instead of the
generic `client.Get(ctx, key, &obj)`, add code-generator:

```bash
# 1. Add code-generator to hack/tools
go get k8s.io/code-generator@v0.36.0

# 2. Add generation tags to api/v1alpha1/doc.go
// +k8s:deepcopy-gen=package,register
// +k8s:defaulter-gen=TypeMeta
// +k8s:openapi-gen=true
// +groupName=hyperfleet.io

# 3. Create hack/update-codegen.sh (mirror hypershift's script)
# Generates: client/clientset/, client/informers/, client/listers/

# 4. Add Makefile target
make clients
```

After generation, external usage becomes:

```go
import (
    hfclient "github.com/openshift-online/rosa-hyperfleet-api/client/clientset/clientset"
)

cs, _ := hfclient.NewForConfig(cfg)

// Typed, discoverable API
cluster, _ := cs.HyperfleetV1alpha1().Clusters("ns").Get(ctx, "my-cluster", metav1.GetOptions{})
pools, _   := cs.HyperfleetV1alpha1().NodePools("ns").List(ctx, metav1.ListOptions{})
```

---

## Comparison: client-go vs ocm-sdk-go Style

| Dimension | client-go (hypershift model) | Custom SDK (ocm-sdk-go model) |
|-----------|------------------------------|-------------------------------|
| **Client code complexity** | 5–10 lines to get a working client | Connection builders, token refresh, pagination helpers, custom types |
| **Server-side complexity** | Kubernetes API server handles serialization, validation, watch, RBAC | Must build all REST handlers, pagination, filtering, auth middleware |
| **Type sharing** | Import the API module directly — one source of truth | Duplicate types in SDK, keep in sync manually |
| **Authentication** | kubeconfig, service accounts, OIDC — all built-in | Custom token flow, SigV4, or bespoke auth |
| **Authorization** | Kubernetes RBAC, built-in | Custom (Cedar/AVP in this project) |
| **Watch / real-time** | Built-in (`Watch`, informers, work queues) | Must build SSE/websocket/polling |
| **Generated client maintenance** | `make clients` reruns code-gen | Maintain SDK generator + SDK repo |
| **Ecosystem compatibility** | Works with kubectl, kustomize, ArgoCD, Flux, any K8s tooling | Only works with the custom SDK |
| **Learning curve for consumers** | Standard Kubernetes patterns — most Go developers already know this | Must learn the SDK's API surface |
| **Go dependency weight** | `k8s.io/client-go` (~well-known, already in most K8s projects) | Custom SDK dependency |

---

## The Critical Architectural Question

There is one important distinction to understand:

**HyperShift's CRDs run on a real Kubernetes API server.** Consumers point
their kubeconfig at a management cluster and talk to it using standard
Kubernetes API machinery (etcd-backed, with RBAC, watches, etc.).

**rosa-hyperfleet-api's platform-api is a REST gateway** that does NOT expose
a Kubernetes API server endpoint. It uses `hyperfleet-db` (PostgreSQL) as its
backing store and speaks REST/JSON over an API Gateway with SigV4 auth and
Cedar/AVP authorization.

This means:

| Scenario | client-go works? | Notes |
|----------|-------------------|-------|
| **Operator-to-operator** (inside the K8s cluster) | **Yes, today** | The hyperfleet-operator already uses controller-runtime against the API server where CRDs are installed |
| **External tool → K8s API server** (if CRDs are exposed) | **Yes, today** | Any kubeconfig-holding client can CRUD the CRDs directly |
| **External tool → platform-api REST gateway** | **No** | platform-api speaks REST, not the Kubernetes API protocol — client-go cannot target it directly |

### What This Means In Practice

If the goal is "external Go clients can manage hyperfleet resources," there are
two clean paths:

#### Path 1: Expose CRDs on a Kubernetes API Server (simplest)

Let external clients talk directly to the management cluster's Kubernetes API
where the CRDs are installed. This is what hypershift does.

- **Pros:** Zero SDK work. client-go just works. kubectl just works.
  Full watch/informer support. RBAC for free.
- **Cons:** Requires the client to have a kubeconfig for the management
  cluster. Authorization is Kubernetes RBAC, not Cedar/AVP.

#### Path 2: Keep the REST Gateway, Use the Types Module Only

Keep platform-api as the entry point, but let Go clients import the API types
module for struct definitions and serialization:

```go
import (
    hyperfleetv1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
    "encoding/json"
    "net/http"
)

cluster := &hyperfleetv1.Cluster{
    Spec: hyperfleetv1.ClusterSpec{...},
}
body, _ := json.Marshal(cluster)
req, _ := http.NewRequest("POST", "https://api.example.com/v1/clusters", bytes.NewReader(body))
// Add SigV4 signing ...
```

This is simpler than a full SDK because the types are already published —
consumers just marshal/unmarshal them. But they don't get the rich client-go
experience (typed methods, watches, informers).

#### Path 3: API Server Aggregation or Virtual API Server (advanced)

Implement a Kubernetes API server facade in front of hyperfleet-db so that the
platform-api speaks the Kubernetes API protocol. Then client-go works end to
end. Projects like `kcp` and `crossplane` do this.

- **Pros:** Full client-go ecosystem support against the managed API.
- **Cons:** Significant engineering effort to implement the API server
  protocol (discovery, OpenAPI, watch, pagination, field selectors).

---

## Recommendation

**Start with Path 1 + the controller-runtime pattern.** The project already
has everything needed:

1. The `hyperfleet-operator/api` module is standalone and lightweight
2. `AddToScheme()` is implemented
3. CRDs are generated and installable
4. Any Go client can `import` the types and use `controller-runtime/pkg/client`

For clients that need to go through the REST gateway (SigV4/Cedar auth), use
**Path 2** — publish the types module and let consumers use it for
serialization with a thin HTTP client. This is dramatically simpler than
building an ocm-sdk-go-style SDK.

Only invest in **generated typed clientsets** (code-generator) if there is
strong demand from consumers who prefer the `Clientset.Resource().Verb()`
pattern over controller-runtime's generic client.

---

## Summary

| Question | Answer |
|----------|--------|
| Can we use client-go instead of a custom SDK? | **Yes** — for direct K8s API access, it works today with zero changes |
| Is it simpler? | **Significantly** — no SDK to build, version, or maintain |
| Is the client code simpler? | **Yes** — 5–10 lines vs. connection builders + pagination + auth helpers |
| Does it work for the REST gateway? | **Partially** — types can be shared, but client-go cannot target a non-K8s REST API directly |
| What should we do first? | Publish the API types module, document the controller-runtime usage pattern, and optionally run code-generator for typed clientsets |
