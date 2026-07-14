package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/openshift/rosa-hyperfleet-api/pkg/config"
	"github.com/openshift/rosa-hyperfleet-api/pkg/server"
)

var (
	// Config flags
	logLevel        string
	logFormat       string
	maestroURL      string
	maestroGRPCURL  string
	hyperfleetURL   string
	allowedAccounts string
	dynamodbRegion  string
	dynamodbPrefix  string
	apiPort         int
	healthPort      int
	metricsPort     int
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "rosa-regional-platform-api",
	Short: "ROSA Regional Platform API",
	Long:  "Regional platform API for ROSA (Red Hat OpenShift Service on AWS)",
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the API server",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	serveCmd.Flags().StringVar(&logFormat, "log-format", "json", "Log format (json, text)")
	serveCmd.Flags().StringVar(&maestroURL, "maestro-url", "http://maestro:8000", "Maestro service base URL")
	serveCmd.Flags().StringVar(&allowedAccounts, "allowed-accounts", "", "Comma-separated list of allowed AWS account IDs")
	serveCmd.Flags().StringVar(&maestroGRPCURL, "maestro-grpc-url", "maestro-grpc.maestro-server:8090", "Maestro gRPC service base URL")
	serveCmd.Flags().StringVar(&hyperfleetURL, "hyperfleet-url", "http://hyperfleet-api.hyperfleet-system:8000", "Hyperfleet service base URL")
	serveCmd.Flags().StringVar(&dynamodbRegion, "dynamodb-region", "", "AWS region for DynamoDB (defaults to us-east-1)")
	serveCmd.Flags().StringVar(&dynamodbPrefix, "dynamodb-prefix", "rosa", "Prefix for DynamoDB table names (default: rosa)")
	serveCmd.Flags().IntVar(&apiPort, "api-port", 8000, "API server port")
	serveCmd.Flags().IntVar(&healthPort, "health-port", 8080, "Health check server port")
	serveCmd.Flags().IntVar(&metricsPort, "metrics-port", 9090, "Metrics server port")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	// Create logger
	logger := createLogger(logLevel, logFormat)

	logger.Info("starting rosa-regional-platform-api",
		"log_level", logLevel,
		"log_format", logFormat,
	)

	// Create config
	cfg := config.NewConfig()
	cfg.Logging.Level = logLevel
	cfg.Logging.Format = logFormat
	cfg.Maestro.BaseURL = maestroURL
	cfg.Maestro.GRPCBaseURL = maestroGRPCURL

	// Validate Hyperfleet URL
	parsedURL, err := url.ParseRequestURI(hyperfleetURL)
	if err != nil {
		logger.Error("invalid hyperfleet URL", "url", hyperfleetURL, "error", err)
		return fmt.Errorf("invalid hyperfleet URL: %w", err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		logger.Error("hyperfleet URL must have http or https scheme", "url", hyperfleetURL, "scheme", parsedURL.Scheme)
		return fmt.Errorf("hyperfleet URL must have http or https scheme, got: %s", parsedURL.Scheme)
	}
	cfg.Hyperfleet.BaseURL = hyperfleetURL

	cfg.AllowedAccounts = parseAllowedAccounts(allowedAccounts)
	cfg.Server.APIPort = apiPort
	cfg.Server.HealthPort = healthPort
	cfg.Server.MetricsPort = metricsPort

	// Set DynamoDB region from flag if provided
	if dynamodbRegion != "" {
		cfg.Authz.AWSRegion = dynamodbRegion
		logger.Info("using DynamoDB region from flag", "region", dynamodbRegion)
	}

	// Set DynamoDB table name prefix
	if dynamodbPrefix != "" {
		cfg.Authz.AccountsTableName = dynamodbPrefix + "-authz-accounts"
		cfg.Authz.AdminsTableName = dynamodbPrefix + "-authz-admins"
		cfg.Authz.GroupsTableName = dynamodbPrefix + "-authz-groups"
		cfg.Authz.MembersTableName = dynamodbPrefix + "-authz-group-members"
		logger.Info("using DynamoDB table prefix", "prefix", dynamodbPrefix)
	}

	// Authz config from environment variables (for local development)
	if endpoint := os.Getenv("DYNAMODB_ENDPOINT"); endpoint != "" {
		cfg.Authz.DynamoDBEndpoint = endpoint
		logger.Info("using custom DynamoDB endpoint", "endpoint", endpoint)
	}
	if endpoint := os.Getenv("CEDAR_AGENT_ENDPOINT"); endpoint != "" {
		cfg.Authz.CedarAgentEndpoint = endpoint
		logger.Info("using cedar-agent for local AVP", "endpoint", endpoint)
	}
	if os.Getenv("AUTHZ_DISABLED") == "true" {
		cfg.Authz.Enabled = false
		logger.Info("authz disabled via environment variable")
	}

	// ZOA configuration from environment variables
	if os.Getenv("ZOA_ENABLED") == "true" {
		cfg.Zoa.Enabled = true
		cfg.Zoa.AWSRegion = dynamodbRegion
		if table := os.Getenv("ZOA_TABLE_NAME"); table != "" {
			cfg.Zoa.TableName = table
		} else if dynamodbPrefix != "" {
			cfg.Zoa.TableName = dynamodbPrefix + "-zoa-executions"
		}
		if bucket := os.Getenv("ZOA_BUCKET_NAME"); bucket != "" {
			cfg.Zoa.BucketName = bucket
		}
		if dir := os.Getenv("ZOA_TEMPLATES_DIR"); dir != "" {
			cfg.Zoa.TemplatesDir = dir
		} else {
			cfg.Zoa.TemplatesDir = "/etc/zoa/templates"
		}
		if dir := os.Getenv("ZOA_JOB_CONFIG_DIR"); dir != "" {
			cfg.Zoa.JobConfigDir = dir
		} else {
			cfg.Zoa.JobConfigDir = "/etc/zoa/job-config"
		}
		if auditTable := os.Getenv("ZOA_AUDIT_TABLE_NAME"); auditTable != "" {
			cfg.Zoa.AuditTableName = auditTable
		}
		logger.Info("ZOA trusted actions enabled",
			"table", cfg.Zoa.TableName,
			"bucket", cfg.Zoa.BucketName,
			"region", cfg.Zoa.AWSRegion,
			"templates_dir", cfg.Zoa.TemplatesDir,
			"job_config_dir", cfg.Zoa.JobConfigDir,
			"audit_table", cfg.Zoa.AuditTableName,
		)
	}

	// Create server
	srv, err := server.New(cfg, logger)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	// Setup signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Run server
	logger.Info("server configuration",
		"api_port", cfg.Server.APIPort,
		"health_port", cfg.Server.HealthPort,
		"metrics_port", cfg.Server.MetricsPort,
		"maestro_url", cfg.Maestro.BaseURL,
		"maestro_grpc_url", cfg.Maestro.GRPCBaseURL,
		"hyperfleet_url", cfg.Hyperfleet.BaseURL,
		"allowed_accounts_count", len(cfg.AllowedAccounts),
	)

	if err := srv.Run(ctx); err != nil {
		return fmt.Errorf("server error: %w", err)
	}

	return nil
}

func createLogger(level, format string) *slog.Logger {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{
		Level: logLevel,
	}

	var handler slog.Handler
	if format == "text" {
		handler = slog.NewTextHandler(os.Stdout, opts)
	} else {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func parseAllowedAccounts(accounts string) []string {
	if accounts == "" {
		return nil
	}
	var result []string
	for _, acc := range strings.Split(accounts, ",") {
		acc = strings.TrimSpace(acc)
		if acc != "" {
			result = append(result, acc)
		}
	}
	return result
}
