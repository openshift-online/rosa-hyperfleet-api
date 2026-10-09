package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/spf13/cobra"
)

const startupBundle = `formatVersion: 1
registeredAccounts: ["123456789012"]
policies:
  - id: read
    ownerAccountID: "123456789012"
    content: 'permit(principal, action in HyperFleet::Action::"ReadOnly", resource);'
attachments:
  - id: user-read
    policyID: read
    principalARN: arn:aws:iam::123456789012:user/reader
    scope: global
`

func startupFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "authz.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func startupCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	flag := serveCmd.Flags().Lookup("authz-config-file")
	if flag == nil {
		t.Fatal("missing startup flag authz-config-file")
	}
	cmd.Flags().String(flag.Name, flag.DefValue, flag.Usage)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestStartupFailureLogs(t *testing.T) {
	t.Setenv("AWS_REGION", "us-east-1")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AUTHZ_CONFIG_FILE", startupFile(t, strings.Replace(startupBundle, "policyID: read", "policyID: missing", 1)))
	output, err := os.CreateTemp(t.TempDir(), "startup-log")
	if err != nil {
		t.Fatal(err)
	}
	original := os.Stdout
	os.Stdout = output
	t.Cleanup(func() {
		os.Stdout = original
		_ = output.Close()
	})
	if err := runServe(startupCommand(t), nil); err == nil {
		t.Fatal("invalid startup succeeded")
	}
	content, err := os.ReadFile(output.Name())
	if err != nil {
		t.Fatal(err)
	}
	for _, detail := range []string{"authorization startup failed", `"stage":"binding"`, "dangling policy reference", `"AttachmentID":"user-read"`} {
		if !strings.Contains(string(content), detail) {
			t.Errorf("startup log missing %q: %s", detail, content)
		}
	}
}

func TestStartupIgnoresResolverEnv(t *testing.T) {
	t.Setenv("AUTHZ_RESOLVER", "dynamodb")
	t.Setenv("AUTHZ_CONFIG_FILE", startupFile(t, startupBundle))
	_, authorizer, err := loadStartupConfig(startupCommand(t), "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := authorizer.Prepare(t.Context(), authz.Identity{
		AccountID: "123456789012", CallerARN: "arn:aws:iam::123456789012:user/reader",
	}, authz.RequestContext{RequestTime: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := prepared.Check(t.Context(), authz.ListClusters, authz.Resource{
		Kind: authz.Collection, CollectionKind: authz.Cluster, AccountID: "123456789012",
	})
	if err != nil || !decision.Allowed {
		t.Fatalf("configuration grant was not used: %+v %v", decision, err)
	}
}

func TestStartupInputs(t *testing.T) {
	valid := startupFile(t, startupBundle)
	invalid := startupFile(t, "not a bundle")
	for _, tc := range []struct {
		name, fileEnv, wantError string
		args                     []string
	}{
		{"required file", "", "AUTHZ_CONFIG_FILE", nil},
		{"missing file", filepath.Join(t.TempDir(), "missing"), "load authorization", nil},
		{"valid environment", valid, "", nil},
		{"flag beats file env", invalid, "", []string{"--authz-config-file=" + valid}},
		{"empty file flag beats env", valid, "AUTHZ_CONFIG_FILE", []string{"--authz-config-file="}},
		{"invalid file flag beats env", valid, "load authorization", []string{"--authz-config-file=" + invalid}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTHZ_CONFIG_FILE", tc.fileEnv)
			cfg, authorizer, err := loadStartupConfig(startupCommand(t, tc.args...), "us-east-1")
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || cfg != nil || authorizer != nil {
					t.Fatalf("want %q and no usable startup dependencies, got cfg=%v authorizer=%v err=%v", tc.wantError, cfg, authorizer, err)
				}
				return
			}
			if err != nil || cfg == nil || authorizer == nil {
				t.Fatalf("valid startup rejected: %v", err)
			}
			if cfg.Authz.ConfigFile != valid || cfg.Regional.AWSRegion != "us-east-1" || !authorizer.IsAccountRegistered(t.Context(), "123456789012") {
				t.Fatalf("wrong startup configuration: %+v", cfg)
			}
		})
	}
}

func TestStartupInvalidBundle(t *testing.T) {
	for _, tc := range []struct{ name, content string }{
		{"malformed YAML", "formatVersion: ["},
		{"unsupported version", strings.Replace(startupBundle, "formatVersion: 1", "formatVersion: 2", 1)},
		{"unknown field", startupBundle + "allowAll: true\n"},
		{"schema invalid", strings.Replace(startupBundle, "action in HyperFleet::Action::\"ReadOnly\"", "action == HyperFleet::Action::\"Invented\"", 1)},
		{"invalid Cedar", strings.Replace(startupBundle, "permit(principal,", "permit(broken,", 1)},
		{"dangling attachment", strings.Replace(startupBundle, "policyID: read", "policyID: missing", 1)},
		{"invalid attachment", strings.Replace(startupBundle, "user/reader", "root", 1)},
		{"inactive bad binding", strings.Replace(startupBundle, "scope: global", "scope: regional\n    region: not-a-region", 1)},
		{"cross account attachment", strings.Replace(startupBundle, "iam::123456789012", "iam::999999999999", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTHZ_CONFIG_FILE", startupFile(t, tc.content))
			cfg, authorizer, err := loadStartupConfig(startupCommand(t), "us-east-1")
			if err == nil || cfg != nil || authorizer != nil {
				t.Fatalf("invalid bundle became usable: cfg=%v authorizer=%v err=%v", cfg, authorizer, err)
			}
		})
	}
}

func TestStartupDefaultsAndBinds(t *testing.T) {
	t.Setenv("AUTHZ_CONFIG_FILE", startupFile(t, startupBundle))
	for _, address := range []string{"", "127.0.0.1"} {
		t.Run("bind="+address, func(t *testing.T) {
			t.Setenv("API_BIND_ADDRESS", address)
			t.Setenv("HEALTH_BIND_ADDRESS", address)
			t.Setenv("METRICS_BIND_ADDRESS", address)
			cfg, _, err := loadStartupConfig(startupCommand(t), "us-east-1")
			if err != nil {
				t.Fatal(err)
			}
			want := address
			if want == "" {
				want = "0.0.0.0"
			}
			if cfg.Server.APIBindAddress != want || cfg.Server.HealthBindAddress != want || cfg.Server.MetricsBindAddress != want || cfg.Server.GRPCBindAddress != "0.0.0.0" || cfg.Server.APIPort != 8000 || cfg.Server.HealthPort != 8080 || cfg.Server.MetricsPort != 9090 {
				t.Fatalf("defaults or bind inputs changed: %+v", cfg)
			}
		})
	}
}

func TestStartupPreservesEquality(t *testing.T) {
	bundle := strings.Replace(startupBundle, "permit(principal,", `permit(principal == HyperFleet::Principal::"arn:aws:iam::123456789012:user/other",`, 1)
	t.Setenv("AUTHZ_CONFIG_FILE", startupFile(t, bundle))
	_, authorizer, err := loadStartupConfig(startupCommand(t), "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := authorizer.Prepare(context.Background(), authz.Identity{AccountID: "123456789012", CallerARN: "arn:aws:iam::123456789012:user/reader"}, authz.RequestContext{RequestTime: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := prepared.Check(t.Context(), authz.ListClusters, authz.Resource{Kind: authz.Collection, CollectionKind: authz.Cluster, AccountID: "123456789012"})
	if err != nil || decision.Allowed {
		t.Fatalf("startup binding weakened original equality: %+v %v", decision, err)
	}
}

func TestStartupSnapshot(t *testing.T) {
	path := startupFile(t, startupBundle)
	t.Setenv("AUTHZ_CONFIG_FILE", path)
	_, original, err := loadStartupConfig(startupCommand(t), "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	replacement := strings.Replace(startupBundle, `registeredAccounts: ["123456789012"]`, "registeredAccounts: []", 1)
	if err := os.WriteFile(path, []byte(replacement), 0600); err != nil {
		t.Fatal(err)
	}
	_, restarted, err := loadStartupConfig(startupCommand(t), "us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	if !original.IsAccountRegistered(t.Context(), "123456789012") || restarted.IsAccountRegistered(t.Context(), "123456789012") {
		t.Fatal("enrollment did not remain snapshot-local until restart")
	}
	if err := os.WriteFile(path, []byte("invalid replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if cfg, a, err := loadStartupConfig(startupCommand(t), "us-east-1"); err == nil || cfg != nil || a != nil {
		t.Fatal("invalid replacement used a fallback")
	}
}
