package main

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"

	seaplanev1alpha1 "github.com/openshift-online/rosa-seaplane-api/api/v1alpha1"
	"github.com/openshift-online/rosa-seaplane-api/seaplane-api/pkg/clients/seaplanedb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var accountIDPattern = regexp.MustCompile(`^\d{12}$`)

// managementClusters parses MANAGEMENT_CLUSTERS, "name:account" entries separated
// by commas (the regional Terraform format), into the clusters to register here.
func managementClusters(value, region string) ([]*seaplanev1alpha1.ManagementCluster, error) {
	var clusters []*seaplanev1alpha1.ManagementCluster
	for entry := range strings.SplitSeq(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		name, account, ok := strings.Cut(entry, ":")
		if !ok || name == "" || !accountIDPattern.MatchString(account) {
			return nil, fmt.Errorf("invalid MANAGEMENT_CLUSTERS entry %q: want name:12-digit-account", entry)
		}
		clusters = append(clusters, &seaplanev1alpha1.ManagementCluster{
			ObjectMeta: metav1.ObjectMeta{Name: name},
			Spec:       seaplanev1alpha1.ManagementClusterSpec{Region: region, AccountID: account},
		})
	}
	return clusters, nil
}

// registerManagementClusters writes each cluster as the registration endpoint did.
// Existing clusters are kept, so restarts are safe.
func registerManagementClusters(ctx context.Context, db *seaplanedb.Client, clusters []*seaplanev1alpha1.ManagementCluster, logger *slog.Logger) error {
	for _, mc := range clusters {
		if err := db.CreateManagementCluster(ctx, mc); err != nil && !seaplanedb.IsAlreadyExists(err) {
			return fmt.Errorf("failed to register management cluster %s: %w", mc.Name, err)
		}
		logger.Info("management cluster registered", "name", mc.Name, "account_id", mc.Spec.AccountID)
	}
	return nil
}
