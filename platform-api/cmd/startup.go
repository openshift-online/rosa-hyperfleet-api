package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/config"
	"github.com/spf13/cobra"
)

// loadStartupConfig loads service configuration and fixes authorization to the
// deployment region for policy attachment selection, Cedar context, and resource identities.
func loadStartupConfig(cmd *cobra.Command, region string) (*config.Config, *authz.Authorizer, error) {
	cfg := config.NewConfig()
	cfg.Regional.AWSRegion = region
	value, err := cmd.Flags().GetString("authz-config-file")
	if err != nil {
		return nil, nil, err
	}
	// Changed distinguishes an explicit empty flag from an absent flag.
	if !cmd.Flags().Changed("authz-config-file") {
		if env, exists := os.LookupEnv("AUTHZ_CONFIG_FILE"); exists {
			value = env
		}
	}
	cfg.Authz.ConfigFile = value
	if strings.TrimSpace(cfg.Authz.ConfigFile) == "" {
		return nil, nil, fmt.Errorf("--authz-config-file or AUTHZ_CONFIG_FILE is required")
	}
	authorizer, err := authz.LoadConfig(cfg.Authz.ConfigFile, region)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load authorization config: %w", err)
	}

	for _, input := range []struct {
		env    string
		target *string
	}{
		{"API_BIND_ADDRESS", &cfg.Server.APIBindAddress},
		{"HEALTH_BIND_ADDRESS", &cfg.Server.HealthBindAddress},
		{"METRICS_BIND_ADDRESS", &cfg.Server.MetricsBindAddress},
	} {
		if value := os.Getenv(input.env); value != "" {
			*input.target = value
		}
	}
	return cfg, authorizer, nil
}
