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

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/sns"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	hyperfleetdb "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	v1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/controller"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo/snspublisher"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo/statussqsconsumer"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/render"
)

var setupLog = ctrl.Log.WithName("setup")

func main() {
	var metricsAddr string
	var probeAddr string
	var awsRegion string
	var baseDomain string
	var sqsStatusQueueURL string
	var maxConcurrentReconciles int

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&awsRegion, "aws-region", "", "AWS region for DynamoDB, SNS, SQS, and EKS (required).")
	flag.StringVar(&baseDomain, "base-domain", "", "DNS base domain for hosted clusters (required).")
	flag.StringVar(&sqsStatusQueueURL, "sqs-status-queue-url", "", "SQS queue URL for receiving status change notifications from kube-applier (required).")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 10, "Maximum number of concurrent reconciles per controller.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if awsRegion == "" {
		setupLog.Error(nil, "--aws-region is required")
		os.Exit(1)
	}
	if baseDomain == "" {
		setupLog.Error(nil, "--base-domain is required")
		os.Exit(1)
	}
	if sqsStatusQueueURL == "" {
		setupLog.Error(nil, "--sqs-status-queue-url is required")
		os.Exit(1)
	}

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		setupLog.Error(nil, "POSTGRES_DSN environment variable is required")
		os.Exit(1)
	}

	replicaCount := envInt("REPLICA_COUNT", 1)
	ordinal, err := podOrdinal()
	if err != nil {
		setupLog.Error(err, "Failed to parse pod ordinal from hostname")
		os.Exit(1)
	}

	setupLog.Info("shard config",
		"replicaCount", replicaCount,
		"ordinal", ordinal,
	)

	signalCtx := ctrl.SetupSignalHandler()
	ctx := context.Background()

	// Load AWS config — credentials come from Pod Identity / IRSA automatically.
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(awsRegion))
	if err != nil {
		setupLog.Error(err, "Failed to load AWS config")
		os.Exit(1)
	}

	// Discover the AWS account ID so we can construct deterministic SNS topic
	// ARNs (arn:aws:sns:<region>:<accountID>:mc-<mcName>-specs) without
	// requiring an explicit CLI flag.
	stsClient := sts.NewFromConfig(awsCfg)
	identity, err := stsClient.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		setupLog.Error(err, "Failed to get AWS caller identity for SNS ARN construction")
		os.Exit(1)
	}
	awsAccountID := *identity.Account
	setupLog.Info("Resolved AWS account ID for SNS topic ARNs", "accountID", awsAccountID)

	scheme := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(scheme); err != nil {
		setupLog.Error(err, "Failed to add v1alpha1 to scheme")
		os.Exit(1)
	}

	log := ctrl.Log.WithName("pgruntime")

	mgr, err := pgruntime.NewManager(pgruntime.Options{
		Scheme: scheme,
		DSN:    dsn,
		Shard: &pgruntime.ShardConfig{
			Mod:   replicaCount,
			Owned: []int{ordinal},
			UnshardedGVKs: []schema.GroupVersionKind{
				v1alpha1.SchemeGroupVersion.WithKind("ManagementCluster"),
			},
		},
		Logger:                 log,
		HealthProbeBindAddress: probeAddr,
	})
	if err != nil {
		setupLog.Error(err, "Failed to create pgruntime manager")
		os.Exit(1)
	}

	dynamoDBClient := dynamodb.NewFromConfig(awsCfg)
	snsClient := sns.NewFromConfig(awsCfg)
	sqsClient := sqs.NewFromConfig(awsCfg)
	publisher := snspublisher.New(snsClient, awsRegion, awsAccountID)
	dynamoClient := dynamo.NewClientWithSNS(dynamoDBClient, publisher)

	rcfg := render.RegionalConfig{
		BaseDomain: baseDomain,
		AWSRegion:  awsRegion,
	}

	eventRouter := controller.NewEventRouter()
	clusterStatusEvents := make(chan event.GenericEvent, 256)
	nodePoolStatusEvents := make(chan event.GenericEvent, 256)
	manifestStatusEvents := make(chan event.GenericEvent, 256)

	if err := (&controller.ClusterReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Dynamo:                  dynamoClient,
		RegionalConfig:          rcfg,
		StatusEvents:            clusterStatusEvents,
		EventRouter:             eventRouter,
		MaxConcurrentReconciles: maxConcurrentReconciles,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "cluster")
		os.Exit(1)
	}
	if err := (&controller.NodePoolReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Dynamo:                  dynamoClient,
		StatusEvents:            nodePoolStatusEvents,
		EventRouter:             eventRouter,
		MaxConcurrentReconciles: maxConcurrentReconciles,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "nodepool")
		os.Exit(1)
	}
	if err := (&controller.PlacementReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		MaxConcurrentReconciles: maxConcurrentReconciles,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "placement")
		os.Exit(1)
	}
	if err := (&controller.ManifestReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		Dynamo:                  dynamoClient,
		StatusEvents:            manifestStatusEvents,
		EventRouter:             eventRouter,
		MaxConcurrentReconciles: maxConcurrentReconciles,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "Failed to create controller", "controller", "manifest")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	// Replace DynamoDB Streams-based statusstream.Manager with a single
	// pre-provisioned SQS queue per operator replica. kube-applier publishes
	// a status notification to SNS after each status write; SNS delivers to
	// all per-replica queues; the operator drains its own queue only.
	// EventRouter.Dispatch silently drops document IDs it does not own,
	// so no per-MC filtering is required here.
	statusConsumer := statussqsconsumer.New(
		sqsClient,
		sqsStatusQueueURL,
		func(documentID string) { eventRouter.Dispatch(documentID) },
	)
	watchCtx, watchCancel := context.WithCancel(context.Background())
	defer watchCancel()
	go statusConsumer.Run(watchCtx)

	setupLog.Info("Starting pgruntime manager",
		"sqsStatusQueueURL", sqsStatusQueueURL,
	)
	if err := mgr.Start(signalCtx); err != nil {
		setupLog.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}

func envInt(key string, fallback int) int {
	s := os.Getenv(key)
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		setupLog.Error(err, "Invalid integer env var", "key", key, "value", s)
		os.Exit(1)
	}
	return v
}

// podOrdinal extracts the StatefulSet ordinal from the hostname.
// e.g. "hyperfleet-operator-2" → 2. Returns 0 if no trailing number.
func podOrdinal() (int, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return 0, fmt.Errorf("get hostname: %w", err)
	}
	parts := strings.Split(hostname, "-")
	last := parts[len(parts)-1]
	ordinal, err := strconv.Atoi(last)
	if err != nil {
		return 0, nil
	}
	return ordinal, nil
}
