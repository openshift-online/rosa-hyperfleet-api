package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
    bindingMode: exact-principal
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
	for _, name := range []string{"authz-resolver", "authz-config-file"} {
		flag := serveCmd.Flags().Lookup(name)
		if flag == nil {
			t.Fatalf("missing startup flag %s", name)
		}
		cmd.Flags().String(name, flag.DefValue, flag.Usage)
	}
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	return cmd
}

func TestStartupFailureLogs(t *testing.T) {
	t.Setenv("AUTHZ_RESOLVER", "config")
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

func TestStartupInputs(t *testing.T) {
	valid := startupFile(t, startupBundle)
	invalid := startupFile(t, "not a bundle")
	for _, tc := range []struct {
		name, resolverEnv, fileEnv, wantError string
		args                                  []string
	}{
		{"required file", "config", "", "AUTHZ_CONFIG_FILE", nil},
		{"missing file", "config", filepath.Join(t.TempDir(), "missing"), "load authorization", nil},
		{"valid environment", "config", valid, "", nil},
		{"only config resolver", "dynamodb", valid, "authz-resolver", nil},
		{"empty resolver", "", valid, "authz-resolver", nil},
		{"flag beats resolver env", "dynamodb", valid, "", []string{"--authz-resolver=config"}},
		{"flag beats file env", "config", invalid, "", []string{"--authz-config-file=" + valid}},
		{"empty file flag beats env", "config", valid, "AUTHZ_CONFIG_FILE", []string{"--authz-config-file="}},
		{"empty resolver flag beats env", "config", valid, "authz-resolver", []string{"--authz-resolver="}},
		{"invalid file flag beats env", "config", valid, "load authorization", []string{"--authz-config-file=" + invalid}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTHZ_RESOLVER", tc.resolverEnv)
			t.Setenv("AUTHZ_CONFIG_FILE", tc.fileEnv)
			cfg, authorizer, resolver, err := loadStartupConfig(startupCommand(t, tc.args...))
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || cfg != nil || authorizer != nil || resolver != nil {
					t.Fatalf("want %q and no usable startup dependencies, got cfg=%v authorizer=%v resolver=%v err=%v", tc.wantError, cfg, authorizer, resolver, err)
				}
				return
			}
			if err != nil || cfg == nil || authorizer == nil || resolver == nil {
				t.Fatalf("valid startup rejected: %v", err)
			}
			if cfg.Authz.Resolver != "config" || cfg.Authz.ConfigFile != valid || !resolver.IsAccountRegistered(t.Context(), "123456789012") {
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
		{"invalid binding", strings.Replace(startupBundle, "bindingMode: exact-principal", "bindingMode: role-membership", 1)},
		{"exact role binding", strings.Replace(startupBundle, "user/reader", "role/reader", 1)},
		{"inactive bad binding", strings.Replace(startupBundle, "scope: global", "scope: regional\n    region: not-a-region", 1)},
		{"cross account attachment", strings.Replace(startupBundle, "iam::123456789012", "iam::999999999999", 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AUTHZ_RESOLVER", "config")
			t.Setenv("AUTHZ_CONFIG_FILE", startupFile(t, tc.content))
			cfg, authorizer, resolver, err := loadStartupConfig(startupCommand(t))
			if err == nil || cfg != nil || authorizer != nil || resolver != nil {
				t.Fatalf("invalid bundle became usable: cfg=%v authorizer=%v resolver=%v err=%v", cfg, authorizer, resolver, err)
			}
		})
	}
}

func TestStartupDefaultsAndBinds(t *testing.T) {
	t.Setenv("AUTHZ_CONFIG_FILE", startupFile(t, startupBundle))
	t.Setenv("AUTHZ_RESOLVER", "config")
	if err := os.Unsetenv("AUTHZ_RESOLVER"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"", "127.0.0.1"} {
		t.Run("bind="+address, func(t *testing.T) {
			t.Setenv("API_BIND_ADDRESS", address)
			t.Setenv("HEALTH_BIND_ADDRESS", address)
			t.Setenv("METRICS_BIND_ADDRESS", address)
			cfg, _, _, err := loadStartupConfig(startupCommand(t))
			if err != nil {
				t.Fatal(err)
			}
			want := address
			if want == "" {
				want = "0.0.0.0"
			}
			if cfg.Authz.Resolver != "config" || cfg.Server.APIBindAddress != want || cfg.Server.HealthBindAddress != want || cfg.Server.MetricsBindAddress != want || cfg.Server.GRPCBindAddress != "0.0.0.0" || cfg.Server.APIPort != 8000 || cfg.Server.HealthPort != 8080 || cfg.Server.MetricsPort != 9090 {
				t.Fatalf("defaults or bind inputs changed: %+v", cfg)
			}
		})
	}
}

func TestStartupPreservesEquality(t *testing.T) {
	bundle := strings.Replace(startupBundle, "permit(principal,", `permit(principal == HyperFleet::Principal::"arn:aws:iam::123456789012:user/other",`, 1)
	t.Setenv("AUTHZ_RESOLVER", "config")
	t.Setenv("AUTHZ_CONFIG_FILE", startupFile(t, bundle))
	_, authorizer, _, err := loadStartupConfig(startupCommand(t))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := authorizer.Prepare(context.Background(), authz.Identity{AccountID: "123456789012", CallerARN: "arn:aws:iam::123456789012:user/reader", Region: "us-east-1"})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := prepared.Check(t.Context(), authz.ListClusters, authz.Resource{Kind: authz.Collection, AccountID: "123456789012", Region: "us-east-1"})
	if err != nil || decision.Allowed {
		t.Fatalf("startup binding weakened original equality: %+v %v", decision, err)
	}
}

func TestStartupSnapshot(t *testing.T) {
	path := startupFile(t, startupBundle)
	t.Setenv("AUTHZ_RESOLVER", "config")
	t.Setenv("AUTHZ_CONFIG_FILE", path)
	_, _, original, err := loadStartupConfig(startupCommand(t))
	if err != nil {
		t.Fatal(err)
	}
	replacement := strings.Replace(startupBundle, `registeredAccounts: ["123456789012"]`, "registeredAccounts: []", 1)
	if err := os.WriteFile(path, []byte(replacement), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, restarted, err := loadStartupConfig(startupCommand(t))
	if err != nil {
		t.Fatal(err)
	}
	if !original.IsAccountRegistered(t.Context(), "123456789012") || restarted.IsAccountRegistered(t.Context(), "123456789012") {
		t.Fatal("enrollment did not remain snapshot-local until restart")
	}
	if err := os.WriteFile(path, []byte("invalid replacement"), 0600); err != nil {
		t.Fatal(err)
	}
	if cfg, a, r, err := loadStartupConfig(startupCommand(t)); err == nil || cfg != nil || a != nil || r != nil {
		t.Fatal("invalid replacement used a fallback")
	}
}
