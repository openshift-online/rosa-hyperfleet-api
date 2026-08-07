package install

import (
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	"k8s.io/apimachinery/pkg/runtime"
)

func Install(scheme *runtime.Scheme) {
	if err := public.AddToScheme(scheme); err != nil {
		panic(err)
	}
}
