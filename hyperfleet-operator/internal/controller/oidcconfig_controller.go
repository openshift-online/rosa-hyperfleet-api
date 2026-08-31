/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/oidc"
)

const (
	oidcConfigFinalizer    = "hyperfleet.io/oidcconfig"
	thumbprintRefreshDelay = 24 * time.Hour
)

// OidcConfigReconciler reconciles OidcConfig objects by copying the
// customer-provided signing key into the regional Secrets Manager and
// computing the issuer's TLS thumbprint.
type OidcConfigReconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	OIDC                    oidc.InfraClient
	MaxConcurrentReconciles int
}

// +kubebuilder:rbac:groups=hyperfleet.io,resources=oidcconfigs,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=hyperfleet.io,resources=oidcconfigs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=hyperfleet.io,resources=oidcconfigs/finalizers,verbs=update

func (r *OidcConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var oc hyperfleetv1alpha1.OidcConfig
	if err := r.Get(ctx, req.NamespacedName, &oc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !oc.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, &oc)
	}

	if !controllerutil.ContainsFinalizer(&oc, oidcConfigFinalizer) {
		controllerutil.AddFinalizer(&oc, oidcConfigFinalizer)
		if err := r.Update(ctx, &oc); err != nil {
			if apierrors.IsNotFound(err) {
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, fmt.Errorf("add finalizer: %w", err)
		}
		// The Update above triggers a new reconcile via the watch on this
		// object, so no explicit requeue is needed here.
		return ctrl.Result{}, nil
	}

	return r.reconcile(ctx, &oc)
}

func (r *OidcConfigReconciler) reconcile(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig) (ctrl.Result, error) {
	log := logf.FromContext(ctx)
	configID := oc.Name

	if oc.Status.Phase == "" {
		r.setPhase(ctx, oc, hyperfleetv1alpha1.OidcConfigPhasePending)
	}

	exists, err := r.OIDC.PrivateKeyExists(ctx, configID)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("check private key: %w", err)
	}

	if !exists {
		log.Info("Copying customer private key to local Secrets Manager", "config", configID)

		keyData, err := r.OIDC.ReadCrossAccountSecret(ctx, oc.Spec.SecretArn, oc.Spec.InstallerRoleArn)
		if err != nil {
			r.setReadyCondition(ctx, oc, "CrossAccountReadFailed", err.Error())
			return ctrl.Result{}, fmt.Errorf("read cross-account secret: %w", err)
		}

		if err := oidc.ValidateRSAPrivateKey(keyData); err != nil {
			r.setReadyConditionAndPhase(ctx, oc, "InvalidPrivateKey", err.Error(), hyperfleetv1alpha1.OidcConfigPhaseError)
			return ctrl.Result{}, nil
		}

		if err := r.OIDC.StorePrivateKey(ctx, configID, keyData); err != nil {
			r.setReadyCondition(ctx, oc, "SecretStoreFailed", err.Error())
			return ctrl.Result{}, fmt.Errorf("store private key: %w", err)
		}
	}

	return r.finalizeReady(ctx, oc)
}

// finalizeReady computes the thumbprint and sets the status to Ready.
func (r *OidcConfigReconciler) finalizeReady(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig) (ctrl.Result, error) {
	thumbprint, err := r.OIDC.ComputeThumbprint(ctx, oc.Spec.IssuerUrl)
	if err != nil {
		// Returning the error lets controller-runtime's rate limiter apply exponential backoff
		r.setReadyConditionAndPhase(ctx, oc, "ThumbprintFailed", err.Error(), hyperfleetv1alpha1.OidcConfigPhaseError)
		return ctrl.Result{}, fmt.Errorf("compute thumbprint: %w", err)
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.OidcConfig
		if err := r.Get(ctx, client.ObjectKeyFromObject(oc), &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "OIDCConfigured",
			Message: "OIDC infrastructure is configured and ready",
		})
		latest.Status.Phase = hyperfleetv1alpha1.OidcConfigPhaseReady
		latest.Status.Thumbprint = thumbprint
		latest.Status.ObservedGeneration = latest.Generation
		return r.Status().Update(ctx, &latest)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("update status: %w", err)
	}

	return ctrl.Result{RequeueAfter: thumbprintRefreshDelay}, nil
}

func (r *OidcConfigReconciler) reconcileDelete(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(oc, oidcConfigFinalizer) {
		return ctrl.Result{}, nil
	}

	configID := oc.Name
	log.Info("OidcConfig deleting", "config", configID)

	if err := r.OIDC.DeletePrivateKey(ctx, configID); err != nil {
		return ctrl.Result{}, fmt.Errorf("delete private key: %w", err)
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.OidcConfig
		if err := r.Get(ctx, client.ObjectKeyFromObject(oc), &latest); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !controllerutil.ContainsFinalizer(&latest, oidcConfigFinalizer) {
			return nil
		}
		controllerutil.RemoveFinalizer(&latest, oidcConfigFinalizer)
		return r.Update(ctx, &latest)
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("remove finalizer: %w", err)
	}

	return ctrl.Result{}, nil
}

// --- status helpers ---

func (r *OidcConfigReconciler) setPhase(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig, phase hyperfleetv1alpha1.OidcConfigPhase) {
	if oc.Status.Phase == phase {
		return
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.OidcConfig
		if err := r.Get(ctx, client.ObjectKeyFromObject(oc), &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if latest.Status.Phase == phase {
			return nil
		}
		latest.Status.Phase = phase
		latest.Status.ObservedGeneration = latest.Generation
		return r.Status().Update(ctx, &latest)
	}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update oidcconfig phase", "phase", phase)
	}
}

func (r *OidcConfigReconciler) setReadyCondition(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig, reason, message string) {
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.OidcConfig
		if err := r.Get(ctx, client.ObjectKeyFromObject(oc), &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
		return r.Status().Update(ctx, &latest)
	}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update Ready condition")
	}
}

// setReadyConditionAndPhase updates the Ready condition and the phase together in a single Get + Status().Update retry loop
func (r *OidcConfigReconciler) setReadyConditionAndPhase(ctx context.Context, oc *hyperfleetv1alpha1.OidcConfig, reason, message string, phase hyperfleetv1alpha1.OidcConfigPhase) {
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var latest hyperfleetv1alpha1.OidcConfig
		if err := r.Get(ctx, client.ObjectKeyFromObject(oc), &latest); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		meta.SetStatusCondition(&latest.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  reason,
			Message: message,
		})
		latest.Status.Phase = phase
		latest.Status.ObservedGeneration = latest.Generation
		return r.Status().Update(ctx, &latest)
	}); err != nil {
		logf.FromContext(ctx).Error(err, "Failed to update Ready condition and phase", "phase", phase)
	}
}

func (r *OidcConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		For(&hyperfleetv1alpha1.OidcConfig{}).
		Named("oidcconfig").
		Complete(r)
}
