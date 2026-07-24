package integration

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	dynamodbtypes "github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	hyperfleetdb "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	hyperfleetv1alpha1 "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/api/v1alpha1"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/controller"
	dynamo "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/dynamo"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-operator/internal/render"
)

const (
	ddbContainerName = "hyperfleet-test-dynamodb"
	pgContainerName  = "hyperfleet-test-postgres"
	mc               = "mc01"
)

var (
	ctx         context.Context
	cancel      context.CancelFunc
	mgr         manager.Manager
	k8sClient   client.Client
	dynamoDBCli *dynamodb.Client
	dynamoCli   *dynamo.Client
	ddbPort     string
	pgPort      string
	eventRouter *controller.EventRouter
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)
	SetDefaultEventuallyTimeout(30 * time.Second)
	SetDefaultEventuallyPollingInterval(500 * time.Millisecond)
	RunSpecs(t, "Integration Suite")
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	ctx, cancel = context.WithCancel(context.TODO())

	containerTool := os.Getenv("CONTAINER_TOOL")
	if containerTool == "" {
		containerTool = "podman"
	}
	// ── Postgres ──

	By("starting Postgres container")
	pgPort = freePort()
	cmd := exec.Command(containerTool, "run", "-d", "--rm",
		"--name", pgContainerName,
		"-e", "POSTGRES_DB=pgruntime_test",
		"-e", "POSTGRES_USER=test",
		"-e", "POSTGRES_PASSWORD=test",
		"-p", fmt.Sprintf("%s:5432", pgPort),
		"postgres:16-alpine",
	)
	out, err := cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "start postgres: %s", string(out))

	dsn := fmt.Sprintf("postgres://test:test@127.0.0.1:%s/pgruntime_test?sslmode=disable", pgPort)

	By("waiting for Postgres to become ready")
	Eventually(func() error {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%s", pgPort), time.Second)
		if err != nil {
			return err
		}
		conn.Close()
		return nil
	}, 30*time.Second, 200*time.Millisecond).Should(Succeed(), "Postgres did not become ready")

	// ── DynamoDB Local ──

	By("starting DynamoDB Local container")
	ddbPort = freePort()
	cmd = exec.Command(containerTool, "run", "-d", "--rm",
		"--name", ddbContainerName,
		"-p", fmt.Sprintf("%s:8000", ddbPort),
		"amazon/dynamodb-local",
	)
	out, err = cmd.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "start DynamoDB Local: %s", string(out))

	dynamoDBCli = dynamodb.NewFromConfig(aws.Config{
		Region:       "us-east-1",
		Credentials:  credentials.NewStaticCredentialsProvider("test", "test", "test"),
		BaseEndpoint: aws.String(fmt.Sprintf("http://127.0.0.1:%s", ddbPort)),
	})

	Eventually(func() error {
		_, err := dynamoDBCli.ListTables(ctx, &dynamodb.ListTablesInput{})
		return err
	}, 30*time.Second, 500*time.Millisecond).Should(Succeed(), "DynamoDB Local did not become ready")

	By("creating DynamoDB tables")
	createTables(dynamoDBCli)
	dynamoCli = dynamo.NewClient(dynamoDBCli)

	// ── pgruntime Manager ──

	By("creating pgruntime manager")
	scheme := runtime.NewScheme()
	Expect(hyperfleetv1alpha1.AddToScheme(scheme)).To(Succeed())

	var mgrErr error
	mgr, mgrErr = pgruntime.NewManager(pgruntime.Options{
		Scheme: scheme,
		DSN:    dsn,
		Logger: logf.Log.WithName("pgruntime"),
	})
	Expect(mgrErr).NotTo(HaveOccurred())

	k8sClient = mgr.GetClient()

	By("seeding ManagementCluster CR")
	mcCR := &hyperfleetv1alpha1.ManagementCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "mc01"},
		Spec: hyperfleetv1alpha1.ManagementClusterSpec{
			Region:    "us-east-1",
			AccountID: "111222333444",
		},
	}
	Expect(k8sClient.Create(ctx, mcCR)).To(Succeed())

	// ── Controllers ──

	By("wiring controllers")
	eventRouter = controller.NewEventRouter()
	clusterStatusEvents := make(chan event.GenericEvent, 256)
	nodePoolStatusEvents := make(chan event.GenericEvent, 256)
	manifestStatusEvents := make(chan event.GenericEvent, 256)

	Expect((&controller.PlacementReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr)).To(Succeed())

	Expect((&controller.ClusterReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
		Dynamo: dynamoCli,
		RegionalConfig: render.RegionalConfig{
			BaseDomain: "e2e.example.com",
			AWSRegion:  "us-east-1",
		},
		StatusEvents: clusterStatusEvents,
		EventRouter:  eventRouter,
	}).SetupWithManager(mgr)).To(Succeed())

	Expect((&controller.NodePoolReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Dynamo:       dynamoCli,
		StatusEvents: nodePoolStatusEvents,
		EventRouter:  eventRouter,
	}).SetupWithManager(mgr)).To(Succeed())

	Expect((&controller.ManifestReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Dynamo:       dynamoCli,
		StatusEvents: manifestStatusEvents,
		EventRouter:  eventRouter,
	}).SetupWithManager(mgr)).To(Succeed())

	// ── Start pgruntime Manager ──

	By("starting pgruntime manager")
	go func() {
		defer GinkgoRecover()
		Expect(mgr.Start(ctx)).To(Succeed())
	}()

	// Wait for cache sync before tests run.
	Eventually(func() bool {
		return mgr.GetCache().WaitForCacheSync(ctx)
	}, 10*time.Second, 100*time.Millisecond).Should(BeTrue(), "pgruntime cache did not sync")

	// ── kube-applier-aws simulators ──

	// Simulate kube-applier-aws: poll specs-applydesires and write status
	// entries with Successful=True so controllers see apply confirmations.
	//
	// We always overwrite the status entry (no ConditionExpression) so that
	// when a desire is updated to Type=Delete with a new updateTime, the next
	// poll produces a fresh ObservedDesireUpdateTime >= the desire's updateTime.
	// Without this, CheckApplyDesireStatuses rejects stale statuses and the
	// controller loops forever waiting for delete confirmation.
	go func() {
		defer GinkgoRecover()
		specsTable := mc + "-specs-applydesires"
		statusTable := mc + "-status-applydesires"

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				out, err := dynamoDBCli.Scan(ctx, &dynamodb.ScanInput{
					TableName: aws.String(specsTable),
				})
				if err != nil {
					continue
				}
				for _, item := range out.Items {
					docID, ok := item["documentID"]
					if !ok {
						continue
					}
					var observedTime time.Time
					if ut, ok := item["updateTime"]; ok {
						if sv, ok := ut.(*dynamodbtypes.AttributeValueMemberS); ok {
							observedTime, _ = time.Parse(time.RFC3339, sv.Value)
						}
					}
					completeStatus := dynamo.ApplyDesireStatus{
						ObservedDesireUpdateTime: observedTime,
						Conditions: []metav1.Condition{{
							Type:               dynamo.DesireConditionSuccessful,
							Status:             metav1.ConditionTrue,
							Reason:             "NoErrors",
							LastTransitionTime: metav1.Now(),
						}},
					}
					statusAttrs, err := attributevalue.MarshalMap(completeStatus)
					if err != nil {
						continue
					}
					docIDStr := docID.(*dynamodbtypes.AttributeValueMemberS).Value
					statusItem := map[string]dynamodbtypes.AttributeValue{
						"documentID": docID,
						"status":     &dynamodbtypes.AttributeValueMemberM{Value: statusAttrs},
					}
				// Unconditional put: overwrites stale status so delete
				// desires with a newer updateTime are confirmed promptly.
				if _, putErr := dynamoDBCli.PutItem(ctx, &dynamodb.PutItemInput{
					TableName: aws.String(statusTable),
					Item:      statusItem,
				}); putErr == nil {
					// Always dispatch so the controller is notified on
					// both first write and subsequent overwrites.
					eventRouter.Dispatch(docIDStr)
				}
				}
			}
		}
	}()

	// ── kube-applier-aws readdesires simulator ──

	// Simulate kube-applier-aws: poll specs-readdesires and write status
	// entries with fabricated KubeContent (a completed Job).
	go func() {
		defer GinkgoRecover()
		specsTable := mc + "-specs-readdesires"
		statusTable := mc + "-status-readdesires"
		completedJob := []byte(`{"apiVersion":"batch/v1","kind":"Job","metadata":{"name":"e2e-job-abc123","namespace":"e2e-actions"},"status":{"succeeded":1,"startTime":"2026-06-25T10:00:00Z","completionTime":"2026-06-25T10:00:05Z"}}`)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				out, err := dynamoDBCli.Scan(ctx, &dynamodb.ScanInput{
					TableName: aws.String(specsTable),
				})
				if err != nil {
					continue
				}
				for _, item := range out.Items {
					docID, ok := item["documentID"]
					if !ok {
						continue
					}
					docIDStr := docID.(*dynamodbtypes.AttributeValueMemberS).Value
					_, putErr := dynamoDBCli.PutItem(ctx, &dynamodb.PutItemInput{
						TableName: aws.String(statusTable),
						Item: map[string]dynamodbtypes.AttributeValue{
							"documentID":         docID,
							"status_kubeContent": &dynamodbtypes.AttributeValueMemberS{Value: string(completedJob)},
						},
					})
					if putErr == nil {
						// Notify the operator directly (replaces DynamoDB Streams watcher)
						eventRouter.Dispatch(docIDStr)
					}
				}
			}
		}
	}()
})

var _ = AfterSuite(func() {
	By("stopping pgruntime manager")
	cancel()

	containerTool := os.Getenv("CONTAINER_TOOL")
	if containerTool == "" {
		containerTool = "podman"
	}

	By("stopping Postgres container")
	_ = exec.Command(containerTool, "rm", "-f", pgContainerName).Run()

	By("stopping DynamoDB Local container")
	_ = exec.Command(containerTool, "rm", "-f", ddbContainerName).Run()
})

func freePort() string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return fmt.Sprintf("%d", port)
}

func createTables(db *dynamodb.Client) {
	suffixes := []string{"-applydesires", "-readdesires"}
	prefixes := []string{mc + "-specs", mc + "-status"}

	for _, prefix := range prefixes {
		for _, suffix := range suffixes {
			tableName := prefix + suffix
			input := &dynamodb.CreateTableInput{
				TableName: aws.String(tableName),
				AttributeDefinitions: []dynamodbtypes.AttributeDefinition{
					{
						AttributeName: aws.String("documentID"),
						AttributeType: dynamodbtypes.ScalarAttributeTypeS,
					},
				},
				KeySchema: []dynamodbtypes.KeySchemaElement{
					{
						AttributeName: aws.String("documentID"),
						KeyType:       dynamodbtypes.KeyTypeHash,
					},
				},
				BillingMode: dynamodbtypes.BillingModePayPerRequest,
			}
			_, err := db.CreateTable(context.Background(), input)
			Expect(err).NotTo(HaveOccurred(), "create table %s", tableName)
		}
	}
}
