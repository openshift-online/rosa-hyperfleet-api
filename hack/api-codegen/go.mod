module github.com/openshift-online/rosa-hyperfleet-api/hack/api-codegen

go 1.27.0

require (
	github.com/openshift-online/rosa-hyperfleet-api/api v0.0.0-00010101000000-000000000000
	github.com/openshift/hypershift/api v0.0.0-20260625052409-9acec4759a16
	gopkg.in/yaml.v3 v3.0.1
	k8s.io/apiextensions-apiserver v0.37.0
	k8s.io/apimachinery v0.37.0
	sigs.k8s.io/controller-tools v0.22.0
)

replace github.com/openshift-online/rosa-hyperfleet-api/api => ../../api

require (
	github.com/fxamacker/cbor/v2 v2.9.4 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/gobuffalo/flect v1.0.3 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.3-0.20250322232337-35a7c28c31ee // indirect
	github.com/openshift/api v0.0.0-20260416105050-3c6b218b8a80 // indirect
	github.com/x448/float16 v0.8.4 // indirect
	go.yaml.in/yaml/v2 v2.4.4 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.23.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	golang.org/x/tools v0.50.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
	k8s.io/api v0.37.0 // indirect
	k8s.io/klog/v2 v2.140.0 // indirect
	k8s.io/kube-openapi v0.0.0-20260911184034-7970a1e230da // indirect
	k8s.io/utils v0.0.0-20260707023825-cf1189d6abe3 // indirect
	sigs.k8s.io/json v0.0.0-20260909141634-11ed52e25bc5 // indirect
	sigs.k8s.io/randfill v1.0.0 // indirect
	sigs.k8s.io/structured-merge-diff/v6 v6.4.2 // indirect
	sigs.k8s.io/yaml v1.6.0 // indirect
)
