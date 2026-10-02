package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/config"
	"github.com/spf13/cobra"
)

func loadStartupConfig(cmd *cobra.Command) (*config.Config, *authz.Authorizer, *authz.ConfigResolver, error) {
	cfg := config.NewConfig()
	// Changed distinguishes an explicit empty flag from an absent flag.
	for _, input := range []struct {
		flag, env string
		target    *string
	}{
		{"authz-resolver", "AUTHZ_RESOLVER", &cfg.Authz.Resolver},
		{"authz-config-file", "AUTHZ_CONFIG_FILE", &cfg.Authz.ConfigFile},
	} {
		value, err := cmd.Flags().GetString(input.flag)
		if err != nil {
			return nil, nil, nil, err
		}
		if !cmd.Flags().Changed(input.flag) {
			if env, exists := os.LookupEnv(input.env); exists {
				value = env
			}
		}
		*input.target = value
	}
	if cfg.Authz.Resolver != "config" {
		return nil, nil, nil, fmt.Errorf("--authz-resolver or AUTHZ_RESOLVER must be config")
	}
	if strings.TrimSpace(cfg.Authz.ConfigFile) == "" {
		return nil, nil, nil, fmt.Errorf("--authz-config-file or AUTHZ_CONFIG_FILE is required")
	}
	resolver, err := authz.LoadConfig(cfg.Authz.ConfigFile)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to load authorization config: %w", err)
	}
	authorizer, err := authz.NewAuthorizer(resolver)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create authorizer: %w", err)
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
	return cfg, authorizer, resolver, nil
}
