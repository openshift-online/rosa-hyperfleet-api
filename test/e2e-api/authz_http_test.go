package e2e_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	hyperfleetv1 "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1"
	public "github.com/openshift-online/rosa-hyperfleet-api/api/v1alpha1/public"
	fleetdb "github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db"
	"github.com/openshift-online/rosa-hyperfleet-api/hyperfleet-db/test/testinfra"
	hypershiftv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	authzAccount      = "111111111111"
	authzOtherAccount = "222222222222"
	authzRegion       = "us-east-1"
	authzAccountLabel = "hyperfleet.io/account-id"
	authzTeamLabel    = "example.com/team"
	authzFaultLabel   = "example.com/fault"
	authzFaultValue   = "fault-detail-secret"
	authzClustersPath = "/api/v0/clusters"
	authzReadyTimeout = 20 * time.Second
	authzStopTimeout  = 10 * time.Second
)

var authzStoredClusters = []struct {
	id, name, account, team string
}{
	{"00000000-0000-4000-8000-000000000001", "hidden-red", authzAccount, "red"},
	{"00000000-0000-4000-8000-000000000002", "visible-blue-one", authzAccount, "blue"},
	{"00000000-0000-4000-8000-000000000003", "hidden-missing", authzAccount, ""},
	{"00000000-0000-4000-8000-000000000004", "visible-blue-two", authzAccount, "blue"},
	{"00000000-0000-4000-8000-000000000005", "foreign-blue", authzOtherAccount, "blue"},
}

type authzProcess struct {
	cmd                                    *exec.Cmd
	done                                   chan struct{}
	err                                    error
	logPath, apiURL, healthURL, metricsURL string
	stopOnce                               sync.Once
}

type authzJUnitCase struct {
	Name    string  `xml:"name,attr"`
	Seconds string  `xml:"time,attr"`
	Failure *string `xml:"failure,omitempty"`
}

type authzJUnitSuite struct {
	XMLName  xml.Name         `xml:"testsuite"`
	Name     string           `xml:"name,attr"`
	Tests    int              `xml:"tests,attr"`
	Failures int              `xml:"failures,attr"`
	Cases    []authzJUnitCase `xml:"testcase"`
}

func TestAuthzHTTP(t *testing.T) {
	binary := os.Getenv("AUTHZ_HTTP_API_BINARY")
	if binary == "" {
		if os.Getenv("AUTHZ_HTTP_REQUIRED") == "1" {
			t.Fatal("AUTHZ_HTTP_API_BINARY is required for the local authorization target")
		}
		t.Skip("local authorization binary absent; run make test-e2e-authz")
	}
	output := os.Getenv("AUTHZ_HTTP_OUTPUT_DIR")
	if output == "" {
		output = filepath.Join("..", "..", "test-results", fmt.Sprintf("authz-http-%d", time.Now().UnixNano()))
	}
	output, err := filepath.Abs(output)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	t.Logf("local HTTP artifacts: %s", output)
	suite := authzJUnitSuite{Name: "TestAuthzHTTP"}
	t.Cleanup(func() {
		if t.Failed() && suite.Failures == 0 {
			message := "Infrastructure failed; see authz-http.log and API process logs"
			suite.Cases = append(suite.Cases, authzJUnitCase{Name: "infrastructure", Failure: &message})
			suite.Failures++
		}
		suite.Tests = len(suite.Cases)
		content, err := xml.MarshalIndent(suite, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(output, "junit-authz-http.xml"), append([]byte(xml.Header), content...), 0600)
		}
		if err != nil {
			t.Errorf("write JUnit: %v", err)
		}
	})
	run := func(name string, check func(*testing.T)) bool {
		started := time.Now()
		passed := t.Run(name, check)
		record := authzJUnitCase{Name: name, Seconds: strconv.FormatFloat(time.Since(started).Seconds(), 'f', 3, 64)}
		if !passed {
			message := "See authz-http.log and API process logs"
			record.Failure = &message
			suite.Failures++
		}
		suite.Cases = append(suite.Cases, record)
		return passed
	}
	if os.Getenv("PGCTL_DSN") != "" {
		t.Fatal("refusing external PGCTL_DSN; only a runner-owned disposable database is safe")
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(binary); err != nil || info.IsDir() || info.Mode()&0111 == 0 {
		t.Fatalf("API binary is not executable: %s (%v)", binary, err)
	}
	if _, err := exec.LookPath("podman"); err != nil {
		t.Fatal("Podman is required by FleetDB testinfra: ", err)
	}
	authzClearAWS(t)
	bundle, err := os.ReadFile("testdata/authz-http.json")
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(output, "authz-config.json")
	authzWriteFile(t, configPath, bundle)

	db := testinfra.StartPostgres(t)
	// The shell runner also records container ownership before readiness for interrupted setup.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	var processes []*authzProcess
	var processMu sync.Mutex
	stopped := make(chan struct{})
	go func() {
		select {
		case <-stopped:
			return
		case <-ctx.Done():
			select {
			case <-stopped:
				return
			default:
			}
			processMu.Lock()
			for _, process := range processes {
				process.stop()
			}
			processMu.Unlock()
			db.Stop()
			os.Exit(1)
		}
	}()
	t.Cleanup(func() { close(stopped); cancel() })
	start := func(t *testing.T, name string) *authzProcess {
		t.Helper()
		processMu.Lock()
		defer processMu.Unlock()
		process := authzStartProcess(t, binary, configPath, db.ConnStr, output, name)
		processes = append(processes, process)
		return process
	}
	scheme := runtime.NewScheme()
	if err := hyperfleetv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	store, closeStore, err := fleetdb.NewClient(fleetdb.Options{Scheme: scheme, DSN: db.ConnStr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(closeStore)
	authzSeedClusters(t, store)
	earlierID, faultID := authzMarkLateFault(t, store)
	process := start(t, "initial")
	t.Cleanup(process.stop)
	if !run("infrastructure", func(t *testing.T) {
		authzWaitReady(t, process)
		log, err := os.ReadFile(process.logPath)
		if err != nil {
			t.Fatal(err)
		}
		for _, endpoint := range []string{process.apiURL, process.healthURL, process.metricsURL} {
			address := strings.TrimPrefix(endpoint, "http://")
			if !bytes.Contains(log, []byte(`"addr":"`+address+`"`)) {
				t.Errorf("listener must honor loopback bind-address environment input: %s; log %s", address, log)
			}
		}
	}) {
		return
	}

	blueOne := authzStoredClusters[1].id
	blueTwo := authzStoredClusters[3].id
	foreign := authzStoredClusters[4].id
	alice := authzUser("alice")
	broad := authzUser("broad")
	for _, tc := range []struct {
		name, arn, account, path, outcome string
		status                            int
	}{
		{"ARN-A matching stored label", alice, authzAccount, "/" + blueOne, "allow", http.StatusOK},
		{"ARN-A different stored label", alice, authzAccount, "/" + authzStoredClusters[0].id, "deny", http.StatusForbidden},
		{"ARN-A missing stored label", alice, authzAccount, "/" + authzStoredClusters[2].id, "deny", http.StatusForbidden},
		{"ARN-B enrolled no grants list", "arn:aws:iam::222222222222:user/bob", authzOtherAccount, "", "deny", http.StatusForbidden},
		{"ARN-B enrolled no grants describe", "arn:aws:iam::222222222222:user/bob", authzOtherAccount, "/" + foreign, "deny", http.StatusForbidden},
		{"same account no grants", authzUser("bob"), authzAccount, "", "deny", http.StatusForbidden},
		{"unregistered broad policy", "arn:aws:iam::333333333333:user/unregistered", "333333333333", "", "", http.StatusForbidden},
		{"missing account", alice, "", "", "", http.StatusForbidden},
		{"missing ARN", "", authzAccount, "", "", http.StatusForbidden},
		{"missing identity", "", "", "", "", http.StatusForbidden},
		{"identity account mismatch", alice, authzOtherAccount, "", "", http.StatusForbidden},
		{"role session forbid", "arn:aws:sts::111111111111:assumed-role/readers/blocked", authzAccount, "/" + blueOne, "deny", http.StatusForbidden},
		{"role session forbid list", "arn:aws:sts::111111111111:assumed-role/readers/blocked", authzAccount, "", "deny", http.StatusForbidden},
		{"path role other session permit", "arn:aws:sts::111111111111:assumed-role/readers/allowed", authzAccount, "/" + blueOne, "allow", http.StatusOK},
		{"global forbid local permit", authzUser("regional"), authzAccount, "/" + blueOne, "deny", http.StatusForbidden},
		{"global forbid local permit list", authzUser("regional"), authzAccount, "", "deny", http.StatusForbidden},
		{"list-only cannot describe", authzUser("list-only"), authzAccount, "/" + blueOne, "deny", http.StatusForbidden},
		{"describe-only cannot list", authzUser("describe-only"), authzAccount, "", "deny", http.StatusForbidden},
		{"describe-only permit", authzUser("describe-only"), authzAccount, "/" + blueOne, "allow", http.StatusOK},
		{"broad foreign get", broad, authzAccount, "/" + foreign, "deny", http.StatusNotFound},
		{"broad missing get", broad, authzAccount, "/00000000-0000-4000-8000-000000000099", "deny", http.StatusNotFound},
	} {
		run(tc.name, func(t *testing.T) {
			operation := "DescribeCluster"
			if tc.path == "" {
				operation = "ListClusters"
			}
			response := authzMeasuredGet(t, process, authzClustersPath+tc.path, tc.arn, tc.account, operation, tc.outcome)
			authzAssertStatus(t, response, tc.status)
			if tc.status == http.StatusOK {
				var cluster public.Cluster
				if err := json.Unmarshal(response.Body, &cluster); err != nil || string(cluster.UID) != blueOne {
					t.Errorf("describe UID = %q, want %q (%v)", cluster.UID, blueOne, err)
				}
			}
		})
	}

	run("runtime policy earlier item allow", func(t *testing.T) {
		response := authzMeasuredGet(t, process, authzClustersPath+"/"+earlierID, authzUser("error-fixture"), authzAccount, "DescribeCluster", "allow")
		authzAssertStatus(t, response, http.StatusOK)
		var cluster public.Cluster
		if err := json.Unmarshal(response.Body, &cluster); err != nil || string(cluster.UID) != earlierID {
			t.Errorf("earlier describe UID = %q, want %q (%v)", cluster.UID, earlierID, err)
		}
		response = authzMeasuredGet(t, process, authzClustersPath+"?limit=1", broad, authzAccount, "ListClusters", "allow")
		page := authzReadList(t, response, 4, 1, 0)
		if got := authzClusterIDs(page); !slices.Equal(got, []string{earlierID}) || earlierID == faultID {
			t.Fatalf("first HTTP page = %v, want earlier allowed candidate %s, not fault %s", got, earlierID, faultID)
		}
	})
	for _, tc := range []struct{ name, path, operation string }{
		{"runtime policy describe failure", "/" + faultID, "DescribeCluster"},
		{"runtime policy late list failure", "?limit=1", "ListClusters"},
	} {
		run(tc.name, func(t *testing.T) {
			beforeLog, err := os.ReadFile(process.logPath)
			if err != nil {
				t.Fatal(err)
			}
			before := authzMetrics(t, process)
			apiClient := NewAPIClient(process.apiURL)
			apiClient.CallerARN = authzUser("error-fixture")
			response, err := apiClient.Get(authzClustersPath+tc.path, authzAccount)
			if err != nil {
				t.Fatal("unsigned local request: ", err)
			}
			authzMetricDelta(t, before, authzMetrics(t, process), tc.operation, "error", "evaluation")
			authzAssertStatus(t, response, http.StatusInternalServerError)
			var status metav1.Status
			if err := json.Unmarshal(response.Body, &status); err != nil || status.Reason != metav1.StatusReasonInternalError || status.Message != "AUTHZ-FAILED-001: Authorization failed" || status.Details != nil {
				t.Errorf("runtime failure must contain only safe typed Status details: %s (%v)", response.Body, err)
			}
			if response.Headers.Get("Content-Type") != "application/json" {
				t.Errorf("failure Content-Type = %q", response.Headers.Get("Content-Type"))
			}
			log, err := os.ReadFile(process.logPath)
			if err != nil || len(log) < len(beforeLog) {
				t.Fatalf("read request failure log: %v", err)
			}
			revision := fmt.Sprintf("%x", sha256.Sum256(bundle))
			authzAssertFailureLog(t, log[len(beforeLog):], tc.operation, revision)
			if bytes.Contains(response.Body, []byte(revision)) {
				t.Error("failure response exposed configuration revision")
			}
			t.Logf("%s HTTP %d body %s", tc.operation, response.StatusCode, response.Body)
		})
	}

	run("visibility total and pagination", func(t *testing.T) {
		response := authzMeasuredGet(t, process, authzClustersPath+"?limit=100", alice, authzAccount, "ListClusters", "allow")
		full := authzReadList(t, response, 2, 100, 0)
		want := []string{blueOne, blueTwo}
		if got := authzClusterIDs(full); !slices.Equal(got, want) {
			t.Errorf("visible membership = %v, want %v", got, want)
		}
		for offset := 0; offset <= 3; offset++ {
			response := authzMeasuredGet(t, process, fmt.Sprintf("%s?limit=1&offset=%d", authzClustersPath, offset), alice, authzAccount, "ListClusters", "allow")
			page := authzReadList(t, response, 2, 1, offset)
			var expected []public.Cluster
			if offset < len(full) {
				expected = full[offset : offset+1]
			}
			if !slices.Equal(authzClusterIDs(page), authzClusterIDs(expected)) {
				t.Errorf("page %d = %v, want observed visible subsequence %v", offset, authzClusterIDs(page), authzClusterIDs(expected))
			}
		}
	})
	run("list-only empty visible set", func(t *testing.T) {
		response := authzMeasuredGet(t, process, authzClustersPath, authzUser("list-only"), authzAccount, "ListClusters", "allow")
		if items := authzReadList(t, response, 0, 50, 0); len(items) != 0 {
			t.Errorf("list-only leaked %d objects", len(items))
		}
	})
	run("broad list still excludes foreign account", func(t *testing.T) {
		response := authzMeasuredGet(t, process, authzClustersPath, broad, authzAccount, "ListClusters", "allow")
		items := authzReadList(t, response, 4, 50, 0)
		want := []string{authzStoredClusters[0].id, blueOne, authzStoredClusters[2].id, blueTwo}
		if got := authzClusterIDs(items); !slices.Equal(got, want) {
			t.Errorf("account-scoped list = %v, want %v", got, want)
		}
	})
	run("request claims cannot replace stored labels or owner", func(t *testing.T) {
		before := authzMetrics(t, process)
		path := authzClustersPath + "/" + authzStoredClusters[0].id + "?accountId=111111111111&example.com%2Fteam=blue&labels=blue"
		request, err := http.NewRequest(http.MethodGet, process.apiURL+path, strings.NewReader(`{"metadata":{"labels":{"example.com/team":"blue","hyperfleet.io/account-id":"111111111111"}},"spec":{"accountId":"111111111111"}}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("X-Amz-Account-Id", authzAccount)
		request.Header.Set("X-Amz-Caller-Arn", alice)
		request.Header.Set("X-Hyperfleet-Labels", `{"example.com/team":"blue"}`)
		request.Header.Set("X-Hyperfleet-Account-Id", authzAccount)
		response := authzRawRequest(t, request)
		authzAssertStatus(t, response, http.StatusForbidden)
		authzMetricDelta(t, before, authzMetrics(t, process), "DescribeCluster", "deny", "")
		response = authzMeasuredGet(t, process, authzClustersPath+"/"+foreign+"?accountId="+authzAccount+"&example.com%2Fteam=blue", broad, authzAccount, "DescribeCluster", "deny")
		authzAssertStatus(t, response, http.StatusNotFound)
		var stored hyperfleetv1.Cluster
		if err := store.Get(context.Background(), client.ObjectKey{Namespace: "cluster-" + authzStoredClusters[0].id, Name: authzStoredClusters[0].name}, &stored); err != nil || stored.Labels[authzTeamLabel] != "red" {
			t.Errorf("request changed stored labels: %+v (%v)", stored.Labels, err)
		}
	})
	run("public requests leave authorization metrics unchanged", func(t *testing.T) {
		before := authzMetrics(t, process)
		for _, endpoint := range []string{
			process.apiURL + "/api/v0/live", process.apiURL + "/api/v0/ready", process.apiURL + "/api/v0/info",
			process.healthURL + "/healthz", process.healthURL + "/readyz", process.metricsURL + "/metrics",
		} {
			request, err := http.NewRequest(http.MethodGet, endpoint, nil)
			if err != nil {
				t.Fatal(err)
			}
			response := authzRawRequest(t, request)
			if response.StatusCode != http.StatusOK {
				t.Errorf("public %s returned %d", endpoint, response.StatusCode)
			}
		}
		authzMetricDelta(t, before, authzMetrics(t, process), "", "", "")
	})

	run("startup snapshot and restart enrollment", func(t *testing.T) {
		var replacement struct {
			FormatVersion      int               `json:"formatVersion"`
			RegisteredAccounts []string          `json:"registeredAccounts"`
			Policies           []json.RawMessage `json:"policies"`
			Attachments        []json.RawMessage `json:"attachments"`
		}
		if err := json.Unmarshal(bundle, &replacement); err != nil {
			t.Fatal(err)
		}
		replacement.RegisteredAccounts = []string{authzOtherAccount}
		content, err := json.Marshal(replacement)
		if err != nil {
			t.Fatal(err)
		}
		authzWriteFile(t, configPath, content)
		response := authzMeasuredGet(t, process, authzClustersPath+"/"+blueOne, alice, authzAccount, "DescribeCluster", "allow")
		authzAssertStatus(t, response, http.StatusOK)
		process.stop()
		restarted := start(t, "restarted")
		defer restarted.stop()
		authzWaitReady(t, restarted)
		response = authzMeasuredGet(t, restarted, authzClustersPath+"/"+blueOne, alice, authzAccount, "", "")
		authzAssertStatus(t, response, http.StatusForbidden)
		authzWriteFile(t, configPath, []byte(`{"formatVersion":1,"registeredAccounts":["111111111111"],"policies":[],"attachments":[`))
		response = authzMeasuredGet(t, restarted, authzClustersPath+"/"+blueOne, alice, authzAccount, "", "")
		authzAssertStatus(t, response, http.StatusForbidden)
	})
	for _, invalid := range []struct{ name, content string }{
		{"malformed", `{"formatVersion":1,"attachments":[`},
		{"unsupported-version", `{"formatVersion":999,"registeredAccounts":[],"policies":[],"attachments":[]}`},
	} {
		run("invalid startup "+invalid.name, func(t *testing.T) {
			authzWriteFile(t, configPath, []byte(invalid.content))
			invalidProcess := start(t, invalid.name)
			defer invalidProcess.stop()
			authzAssertRejected(t, invalidProcess)
		})
	}
}

func TestAuthzErrorStatus(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
		wantFailure        bool
	}{
		{"typed Status", "", "", false},
		{"partial items", "items", `[]`, true},
		{"partial spec", "spec", `{}`, true},
		{"partial total", "total", `1`, true},
		{"partial limit", "limit", `1`, true},
		{"partial offset", "offset", `0`, true},
		{"resource labels", "metadata", `{"labels":{"example.com/fault":"fault-detail-secret"}}`, true},
		{"success Status", "status", `"Success"`, true},
		{"resource kind", "kind", `"Cluster"`, true},
		{"wrong version", "apiVersion", `"other/v1"`, true},
		{"wrong code", "code", `200`, true},
		{"empty message", "message", `""`, true},
		{"fault label", "message", `"example.com/fault"`, true},
		{"fault value", "message", `"fault-detail-secret"`, true},
		{"Cedar diagnostics", "message", `"integer overflow"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]json.RawMessage{
				"apiVersion": json.RawMessage(`"v1"`), "kind": json.RawMessage(`"Status"`),
				"metadata": json.RawMessage(`{}`), "status": json.RawMessage(`"Failure"`),
				"code": json.RawMessage(`500`), "reason": json.RawMessage(`"InternalError"`),
				"message": json.RawMessage(`"AUTHZ-FAILED-001: Authorization failed"`),
			}
			if tc.field != "" {
				body[tc.field] = json.RawMessage(tc.value)
			}
			content, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			var failures authzFailures
			authzAssertStatus(&failures, &APIResponse{StatusCode: http.StatusInternalServerError, Body: content}, http.StatusInternalServerError)
			if hasFailure := len(failures) != 0; hasFailure != tc.wantFailure {
				t.Fatalf("failure = %t, want %t; body %s; diagnostics %v", hasFailure, tc.wantFailure, content, failures)
			}
		})
	}
}

func authzUser(name string) string {
	return "arn:aws:iam::" + authzAccount + ":user/" + name
}

func authzWriteFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
}

func authzClearAWS(t *testing.T) {
	t.Helper()
	// Remove every credential source, including inherited profiles and container credentials.
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(key, "AWS_") {
			t.Setenv(key, "")
		}
	}
	empty := t.TempDir()
	for _, name := range []string{"config", "credentials"} {
		path := filepath.Join(empty, name)
		authzWriteFile(t, path, nil)
		key := "AWS_CONFIG_FILE"
		if name == "credentials" {
			key = "AWS_SHARED_CREDENTIALS_FILE"
		}
		t.Setenv(key, path)
	}
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REGION", authzRegion)
	t.Setenv("RATE_LIMIT_ENABLED", "false")
}

func authzSeedClusters(t *testing.T, store client.Client) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, fixture := range authzStoredClusters {
		labels := map[string]string{authzAccountLabel: fixture.account}
		if fixture.team != "" {
			labels[authzTeamLabel] = fixture.team
		}
		cluster := &hyperfleetv1.Cluster{
			ObjectMeta: metav1.ObjectMeta{Namespace: "cluster-" + fixture.id, Name: fixture.name, Labels: labels},
			Spec:       hyperfleetv1.ClusterSpec{AccountID: fixture.account, InternalID: fixture.id},
		}
		cluster.Spec.HostedCluster.Platform = hypershiftv1.PlatformSpec{
			Type: hypershiftv1.AWSPlatform,
			AWS:  &hypershiftv1.AWSPlatformSpec{Region: authzRegion},
		}
		// A spec tag must not substitute for the missing or different stored metadata label.
		cluster.Spec.Tags = map[string]string{authzTeamLabel: "blue"}
		if err := store.Create(ctx, cluster); err != nil {
			t.Fatalf("seed %s: %v", fixture.name, err)
		}
		var persisted hyperfleetv1.Cluster
		if err := store.Get(ctx, client.ObjectKeyFromObject(cluster), &persisted); err != nil || !maps.Equal(persisted.Labels, labels) || persisted.Spec.InternalID != fixture.id || persisted.Spec.AccountID != fixture.account {
			t.Fatalf("stored fixture %s did not round trip: %+v (%v)", fixture.name, persisted, err)
		}
	}
}

func authzMarkLateFault(t *testing.T, store client.Client) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var candidates hyperfleetv1.ClusterList
	selector := client.MatchingLabels{authzAccountLabel: authzAccount}
	if err := store.List(ctx, &candidates, selector); err != nil {
		t.Fatal(err)
	}
	if len(candidates.Items) < 2 {
		t.Fatal("late failure needs an earlier account-scoped candidate")
	}
	// Select by observed database order, which a metadata update may also change.
	last := candidates.Items[len(candidates.Items)-1].DeepCopy()
	last.Labels[authzFaultLabel] = authzFaultValue
	if err := store.Update(ctx, last); err != nil {
		t.Fatal(err)
	}
	var persisted hyperfleetv1.ClusterList
	if err := store.List(ctx, &persisted, selector); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Items) != len(candidates.Items) {
		t.Fatal("fault marker changed account-scoped membership")
	}
	fault := persisted.Items[len(persisted.Items)-1]
	if client.ObjectKeyFromObject(&fault) != client.ObjectKeyFromObject(last) || !maps.Equal(fault.Labels, last.Labels) || !reflect.DeepEqual(fault.Spec, last.Spec) {
		t.Fatal("fault marker must round trip on the last candidate without changing its original fields")
	}
	for _, earlier := range persisted.Items[:len(persisted.Items)-1] {
		if _, exists := earlier.Labels[authzFaultLabel]; exists {
			t.Fatal("fault marker must occur only on the last account-scoped candidate")
		}
	}
	earlierID := persisted.Items[0].Spec.InternalID
	t.Logf("observed account-scoped candidates=%d earlier=%s fault=%s index=%d, beyond limit=1", len(persisted.Items), earlierID, fault.Spec.InternalID, len(persisted.Items)-1)
	return earlierID, fault.Spec.InternalID
}

func authzStartProcess(t *testing.T, binary, config, dsn, output, name string) *authzProcess {
	t.Helper()
	// Hold all three reservations until port selection is complete to prevent duplicate picks.
	var listeners []net.Listener
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	var ports []string
	for range 3 {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
		ports = append(ports, strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	}
	process := &authzProcess{
		done: make(chan struct{}), logPath: filepath.Join(output, "api-"+name+".log"),
		apiURL: "http://127.0.0.1:" + ports[0], healthURL: "http://127.0.0.1:" + ports[1], metricsURL: "http://127.0.0.1:" + ports[2],
	}
	log, err := os.Create(process.logPath)
	if err != nil {
		t.Fatal(err)
	}
	process.cmd = exec.Command(binary, "serve", "--api-port", ports[0], "--health-port", ports[1], "--metrics-port", ports[2])
	process.cmd.Env = append(os.Environ(),
		"POSTGRES_DSN="+dsn, "AUTHZ_RESOLVER=config", "AUTHZ_CONFIG_FILE="+config,
		"API_BIND_ADDRESS=127.0.0.1", "HEALTH_BIND_ADDRESS=127.0.0.1", "METRICS_BIND_ADDRESS=127.0.0.1",
		"TARGET_GROUP_ARN=arn:aws:elasticloadbalancing:"+authzRegion+":"+authzAccount+":targetgroup/authz-http/fixture",
	)
	process.cmd.Stdout, process.cmd.Stderr = log, log
	for _, listener := range listeners {
		_ = listener.Close()
	}
	if err := process.cmd.Start(); err != nil {
		_ = log.Close()
		t.Fatal(err)
	}
	go func() {
		process.err = process.cmd.Wait()
		_ = log.Close()
		close(process.done)
	}()
	return process
}

func (process *authzProcess) stop() {
	process.stopOnce.Do(func() {
		select {
		case <-process.done:
			return
		default:
		}
		_ = process.cmd.Process.Signal(syscall.SIGTERM)
		select {
		case <-process.done:
		case <-time.After(authzStopTimeout):
			_ = process.cmd.Process.Kill()
			<-process.done
		}
	})
}

func authzWaitReady(t *testing.T, process *authzProcess) {
	t.Helper()
	deadline := time.Now().Add(authzReadyTimeout)
	httpClient := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		select {
		case <-process.done:
			log, _ := os.ReadFile(process.logPath)
			t.Fatalf("API exited before readiness: %v\n%s", process.err, log)
		default:
		}
		isReady := true
		for _, endpoint := range []string{process.apiURL + "/api/v0/ready", process.healthURL + "/readyz", process.metricsURL + "/metrics"} {
			response, err := httpClient.Get(endpoint)
			if err != nil {
				isReady = false
				continue
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode != http.StatusOK {
				isReady = false
			}
		}
		if isReady {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	log, _ := os.ReadFile(process.logPath)
	t.Fatalf("API readiness exceeded %s\n%s", authzReadyTimeout, log)
}

func authzAssertRejected(t *testing.T, process *authzProcess) {
	t.Helper()
	deadline := time.After(authzReadyTimeout)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-process.done:
			if process.err == nil {
				t.Error("invalid configuration exited successfully")
			}
			log, _ := os.ReadFile(process.logPath)
			if bytes.Contains(log, []byte("starting API server")) || bytes.Contains(log, []byte("starting health server")) || bytes.Contains(log, []byte("starting metrics server")) {
				t.Errorf("invalid configuration opened a serving listener\n%s", log)
			}
			return
		case <-deadline:
			t.Fatal("invalid configuration did not exit before serving")
		case <-ticker.C:
			for _, endpoint := range []string{process.apiURL, process.healthURL, process.metricsURL} {
				connection, err := net.DialTimeout("tcp", strings.TrimPrefix(endpoint, "http://"), 20*time.Millisecond)
				if err == nil {
					_ = connection.Close()
					t.Fatalf("invalid configuration opened %s", endpoint)
				}
			}
		}
	}
}

func authzAssertFailureLog(t *testing.T, log []byte, operation, revision string) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(log))
	failures := 0
	for decoder.More() {
		var entry struct {
			Message    string `json:"msg"`
			Operation  string `json:"operation"`
			Stage      string `json:"stage"`
			Cause      string `json:"cause"`
			Provenance []struct {
				DiagnosticID, PolicyID, PolicyRevision, AttachmentID, AttachmentRevision, PrincipalARN string
			}
			Diagnostics []struct {
				Policy   string `json:"policy"`
				Message  string `json:"message"`
				Position struct {
					Line int `json:"line"`
				} `json:"position"`
			}
		}
		if err := decoder.Decode(&entry); err != nil {
			t.Fatal("invalid request log JSON: ", err)
		}
		if entry.Message != "cluster authorization failed" {
			continue
		}
		failures++
		if entry.Operation != operation || entry.Stage != "evaluation" || entry.Cause != "cedar evaluation diagnostics" || len(entry.Provenance) != 1 || len(entry.Diagnostics) != 1 {
			t.Fatalf("failure log lost evaluation cause or unique provenance: %s", log)
		}
		provenance := entry.Provenance[0]
		diagnostic := entry.Diagnostics[0]
		if provenance.DiagnosticID != "attachment/error-forbid" || provenance.PolicyID != "runtime-fault" || provenance.AttachmentID != "error-forbid" || provenance.PolicyRevision != revision || provenance.AttachmentRevision != revision || provenance.PrincipalARN != authzUser("error-fixture") || diagnostic.Policy != provenance.DiagnosticID || !strings.Contains(diagnostic.Message, "integer overflow") || diagnostic.Position.Line < 1 {
			t.Fatalf("failure log lost actual attachment revision or native overflow diagnostic: %s", log)
		}
	}
	if failures != 1 {
		t.Errorf("request failure logs = %d, want 1; log %s", failures, log)
	}
}

func authzAssertStatus(t interface {
	Helper()
	Errorf(string, ...any)
}, response *APIResponse, expected int) {
	t.Helper()
	if response.StatusCode != expected {
		t.Errorf("HTTP status = %d, want %d; body %s", response.StatusCode, expected, response.Body)
	}
	if expected == http.StatusOK {
		return
	}
	var status metav1.Status
	if err := json.Unmarshal(response.Body, &status); err != nil || status.APIVersion != "v1" || status.Kind != "Status" || status.Status != metav1.StatusFailure || int(status.Code) != expected || status.Message == "" {
		t.Errorf("expected structured failure code %d, got %s (%v)", expected, response.Body, err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(response.Body, &envelope); err == nil {
		for key := range envelope {
			if !slices.Contains([]string{"apiVersion", "kind", "metadata", "status", "message", "reason", "details", "code"}, key) {
				t.Errorf("failure response contains non-Status field %q", key)
			}
		}
		// Kubernetes Status emits metadata {}, but resource metadata must never leak.
		if metadata, exists := envelope["metadata"]; exists {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(metadata, &fields); err != nil || fields == nil || len(fields) != 0 {
				t.Errorf("failure response contains nonempty or invalid Status metadata")
			}
		}
	}
	for _, secret := range []string{"permit(", "forbid(", "principalARN", "attachment", authzTeamLabel, authzFaultLabel, authzFaultValue, "runtime-fault", "error-forbid", "integer overflow"} {
		if bytes.Contains(response.Body, []byte(secret)) {
			t.Errorf("failure exposed policy or binding detail %q", secret)
		}
	}
}

func authzReadList(t *testing.T, response *APIResponse, total, limit, offset int) []public.Cluster {
	t.Helper()
	authzAssertStatus(t, response, http.StatusOK)
	var list struct {
		Items  []public.Cluster `json:"items"`
		Total  int              `json:"total"`
		Limit  int              `json:"limit"`
		Offset int              `json:"offset"`
	}
	if err := json.Unmarshal(response.Body, &list); err != nil {
		t.Fatal(err)
	}
	if list.Items == nil || list.Total != total || list.Limit != limit || list.Offset != offset {
		t.Errorf("list envelope = %+v, want total=%d limit=%d offset=%d non-null items", list, total, limit, offset)
	}
	return list.Items
}

func authzClusterIDs(clusters []public.Cluster) []string {
	ids := make([]string, 0, len(clusters))
	for _, cluster := range clusters {
		ids = append(ids, string(cluster.UID))
	}
	slices.Sort(ids)
	return ids
}

func authzRawRequest(t *testing.T, request *http.Request) *APIResponse {
	t.Helper()
	response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return &APIResponse{StatusCode: response.StatusCode, Body: body, Headers: response.Header}
}

func authzMeasuredGet(t *testing.T, process *authzProcess, path, arn, account, operation, outcome string) *APIResponse {
	t.Helper()
	before := authzMetrics(t, process)
	apiClient := NewAPIClient(process.apiURL)
	apiClient.CallerARN = arn
	response, err := apiClient.Get(path, account)
	if err != nil {
		t.Fatal("unsigned local request: ", err)
	}
	authzMetricDelta(t, before, authzMetrics(t, process), operation, outcome, "")
	return response
}

func authzMetrics(t *testing.T, process *authzProcess) map[string]float64 {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, process.metricsURL+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	response := authzRawRequest(t, request)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("metrics HTTP status = %d", response.StatusCode)
	}
	parser := expfmt.NewTextParser(model.UTF8Validation)
	families, err := parser.TextToMetricFamilies(bytes.NewReader(response.Body))
	if err != nil {
		t.Fatal("invalid Prometheus exposition: ", err)
	}
	samples := map[string]float64{}
	for name, family := range families {
		if !strings.HasPrefix(name, "authz_") {
			continue
		}
		if !slices.Contains([]string{"authz_requests_total", "authz_duration_seconds", "authz_failures_total"}, name) {
			t.Errorf("unexpected authorization collector %s", name)
		}
		for _, metric := range family.Metric {
			labels := map[string]string{}
			for _, label := range metric.Label {
				labels[label.GetName()] = label.GetValue()
			}
			if len(labels) != 2 || !slices.Contains([]string{"ListClusters", "DescribeCluster"}, labels["operation"]) {
				t.Errorf("unbounded metric labels %s %v", name, labels)
			}
			classification := labels["outcome"]
			if name == "authz_failures_total" {
				classification = labels["stage"]
				if !slices.Contains([]string{"resolution", "parsing", "binding", "entity_validation", "evaluation", "resource_loading"}, classification) {
					t.Errorf("unbounded failure stage %q", classification)
				}
			} else if !slices.Contains([]string{"allow", "deny", "error"}, classification) {
				t.Errorf("unbounded outcome %q", classification)
			}
			key := name + "/" + labels["operation"] + "/" + classification
			if name == "authz_duration_seconds" {
				if metric.Histogram == nil {
					t.Errorf("duration collector is not a histogram")
				}
				histogram := metric.GetHistogram()
				samples[key] = float64(histogram.GetSampleCount())
				bounds := []float64{}
				for _, bucket := range histogram.Bucket {
					bounds = append(bounds, bucket.GetUpperBound())
				}
				want := []float64{0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1}
				// The text parser may preserve the explicit +Inf bucket.
				if len(bounds) == len(want)+1 && math.IsInf(bounds[len(want)], 1) {
					bounds = bounds[:len(want)]
				}
				if !slices.Equal(bounds, want) || !bytes.Contains(response.Body, []byte(`le="+Inf"`)) {
					t.Errorf("histogram buckets = %v, want %v and +Inf", bounds, want)
				}
			} else {
				if metric.Counter == nil {
					t.Errorf("%s is not a counter", name)
				}
				samples[key] = metric.GetCounter().GetValue()
			}
		}
	}
	return samples
}

func authzMetricDelta(t *testing.T, before, after map[string]float64, operation, outcome, stage string) {
	t.Helper()
	deltas := map[string]float64{}
	for key, value := range after {
		if delta := value - before[key]; delta != 0 {
			deltas[key] = delta
		}
	}
	for key, value := range before {
		if _, exists := after[key]; !exists && value != 0 {
			deltas[key] = -value
		}
	}
	want := map[string]float64{}
	if outcome != "" {
		want["authz_requests_total/"+operation+"/"+outcome] = 1
		want["authz_duration_seconds/"+operation+"/"+outcome] = 1
	}
	if stage != "" {
		want["authz_failures_total/"+operation+"/"+stage] = 1
		t.Logf("request-level authorization metric deltas = %v", deltas)
	}
	if !reflect.DeepEqual(deltas, want) {
		t.Errorf("request-level authorization metric deltas = %v, want %v", deltas, want)
	}
}
