package e2e_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAuthzStatusMetadata(t *testing.T) {
	body, err := json.Marshal(metav1.Status{
		TypeMeta: metav1.TypeMeta{Kind: "Status", APIVersion: "v1"},
		Status:   metav1.StatusFailure, Message: "Access denied", Code: http.StatusForbidden,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"metadata":{}`) {
		t.Fatalf("Kubernetes Status fixture lost empty metadata: %s", body)
	}
	authzAssertStatus(t, &APIResponse{StatusCode: http.StatusForbidden, Body: body}, http.StatusForbidden)
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, key, value string
		wantFailure      bool
	}{
		{"absent metadata", "metadata", "", false},
		{"empty metadata with whitespace", "metadata", "{ }", false},
		{"null metadata", "metadata", "null", true},
		{"array metadata", "metadata", "[]", true},
		{"scalar metadata", "metadata", `""`, true},
		{"number metadata", "metadata", "0", true},
		{"resource version", "metadata", `{"resourceVersion":""}`, true},
		{"resource labels", "metadata", `{"labels":{"team":"blue"}}`, true},
		{"resource items", "items", "[]", true},
		{"resource spec", "spec", "{}", true},
		{"success status", "status", `"Success"`, true},
		{"success resource", "kind", `"Cluster"`, true},
		{"wrong code", "code", "404", true},
		{"empty message", "message", `""`, true},
		{"policy permit", "message", `"permit(principal)"`, true},
		{"policy forbid", "message", `"forbid(principal)"`, true},
		{"principal ARN", "message", `"principalARN"`, true},
		{"binding attachment", "message", `"attachment"`, true},
		{"sensitive label", "message", `"example.com/team"`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modified := maps.Clone(envelope)
			if tc.value == "" {
				delete(modified, tc.key)
			} else {
				modified[tc.key] = json.RawMessage(tc.value)
			}
			body, err := json.Marshal(modified)
			if err != nil {
				t.Fatal(err)
			}
			var failures authzFailures
			authzAssertStatus(&failures, &APIResponse{StatusCode: http.StatusForbidden, Body: body}, http.StatusForbidden)
			if hasFailure := len(failures) != 0; hasFailure != tc.wantFailure {
				t.Fatalf("failure = %t, want %t; body %s; diagnostics %v", hasFailure, tc.wantFailure, body, failures)
			}
		})
	}
}

type authzFailures []string

func (*authzFailures) Helper() {}

func (failures *authzFailures) Errorf(format string, args ...any) {
	*failures = append(*failures, fmt.Sprintf(format, args...))
}

func TestAuthzChildTargetGroup(t *testing.T) {
	for _, inherited := range []string{"", "not-an-arn", "arn:aws:elasticloadbalancing:eu-west-1:999999999999:targetgroup/inherited/id"} {
		t.Run(inherited, func(t *testing.T) {
			t.Setenv("TARGET_GROUP_ARN", inherited)
			output := t.TempDir()
			binary := filepath.Join(output, "child")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s' \"$TARGET_GROUP_ARN\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			process := authzStartProcess(t, binary, "unused", "unused", output, "fixture")
			t.Cleanup(process.stop)
			<-process.done
			body, err := os.ReadFile(process.logPath)
			want := "arn:aws:elasticloadbalancing:us-east-1:111111111111:targetgroup/authz-http/fixture"
			if err != nil || string(body) != want {
				t.Fatalf("child TARGET_GROUP_ARN = %q, want %q (%v)", body, want, err)
			}
		})
	}
}

func TestAuthzStopDrain(t *testing.T) {
	output := t.TempDir()
	binary := filepath.Join(output, "child")
	// The real API drains for five seconds before shutting down its listeners.
	content := "#!/bin/bash\ntrap 'sleep 5.1; exit 0' TERM\necho ready\nwhile :; do sleep 0.05; done\n"
	if err := os.WriteFile(binary, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	process := authzStartProcess(t, binary, "unused", "unused", output, "drain")
	t.Cleanup(process.stop)
	deadline := time.Now().Add(time.Second)
	for {
		body, err := os.ReadFile(process.logPath)
		if err == nil && strings.Contains(string(body), "ready") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("drain fixture did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	process.stop()
	if process.err != nil {
		t.Fatalf("five-second drain did not exit cleanly: %v", process.err)
	}
}
