package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/spf13/cobra"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/clients/hyperfleetdb"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/config"
	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/server"
)

var (
	// Config flags
	logLevel                 string
	logFormat                string
	allowedAccounts          string
	postgresDSN              string
	dynamodbRegion           string
	dynamodbPrefix           string
	oidcIssuerBaseURL        string
	defaultClusterExpiration time.Duration
	apiPort                  int
	healthPort               int
	metricsPort              int
)

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "rosa-hyperfleet-api",
	Short: "Hyperfleet Platform API",
	Long:  "Hyperfleet platform API for ROSA HCP regional cluster management",
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the API server",
	RunE:  runServe,
}

func init() {
	serveCmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	serveCmd.Flags().StringVar(&logFormat, "log-format", "json", "Log format (json, text)")
	serveCmd.Flags().StringVar(&allowedAccounts, "allowed-accounts", "", "Comma-separated list of allowed AWS account IDs")
	serveCmd.Flags().StringVar(&postgresDSN, "postgres-dsn", "", "PostgreSQL connection string (required)")
	serveCmd.Flags().StringVar(&dynamodbRegion, "dynamodb-region", "", "AWS region for DynamoDB (defaults to auto-detected region)")
	serveCmd.Flags().StringVar(&dynamodbPrefix, "dynamodb-prefix", "rosa", "Prefix for DynamoDB table names")
	serveCmd.Flags().StringVar(&oidcIssuerBaseURL, "oidc-issuer-base-url", "", "Base URL for OIDC issuer (e.g. https://<cloudfront-domain>)")
	serveCmd.Flags().DurationVar(&defaultClusterExpiration, "default-cluster-expiration", 0, "Default cluster lifetime (e.g. 24h). Clusters created without an explicit expirationTimestamp get one stamped at creation. Zero means no default.")
	serveCmd.Flags().IntVar(&apiPort, "api-port", 8000, "API server port")
	serveCmd.Flags().IntVar(&healthPort, "health-port", 8080, "Health check server port")
	serveCmd.Flags().IntVar(&metricsPort, "metrics-port", 9090, "Metrics server port")

	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, args []string) error {
	// Create logger
	logger := createLogger(logLevel, logFormat)

	logger.Info("starting rosa-hyperfleet-api",
		"log_level", logLevel,
		"log_format", logFormat,
	)

	// Detect AWS region from SDK default chain (IMDS, AWS_REGION env var, etc.)
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		return fmt.Errorf("failed to detect AWS region: %w", err)
	}
	if awsCfg.Region == "" {
		return fmt.Errorf("AWS region could not be detected from environment; set AWS_REGION")
	}
	logger.Info("detected AWS region", "region", awsCfg.Region)

	// Create config
	cfg := config.NewConfig()
	cfg.Logging.Level = logLevel
	cfg.Logging.Format = logFormat
	if postgresDSN == "" {
		postgresDSN = os.Getenv("POSTGRES_DSN")
	}
	if postgresDSN == "" {
		return fmt.Errorf("--postgres-dsn or POSTGRES_DSN is required")
	}
	cfg.DB.DSN = postgresDSN

	cfg.Regional.OIDCIssuerBaseURL = oidcIssuerBaseURL
	cfg.Regional.DefaultClusterExpiration = defaultClusterExpiration
	cfg.AllowedAccounts = parseAllowedAccounts(allowedAccounts)
	cfg.Server.APIPort = apiPort
	cfg.Server.HealthPort = healthPort
	cfg.Server.MetricsPort = metricsPort

	// Authz DynamoDB config
	if dynamodbRegion != "" {
		cfg.Authz.AWSRegion = dynamodbRegion
	} else {
		cfg.Authz.AWSRegion = awsCfg.Region
	}
	if dynamodbPrefix != "" {
		cfg.Authz.AccountsTableName = dynamodbPrefix + "-authz-accounts"
		cfg.Authz.AdminsTableName = dynamodbPrefix + "-authz-admins"
		cfg.Authz.GroupsTableName = dynamodbPrefix + "-authz-groups"
		cfg.Authz.MembersTableName = dynamodbPrefix + "-authz-group-members"
	}
	if endpoint := os.Getenv("DYNAMODB_ENDPOINT"); endpoint != "" {
		cfg.Authz.DynamoDBEndpoint = endpoint
		logger.Info("using custom DynamoDB endpoint for authz", "endpoint", endpoint)
	}
	if endpoint := os.Getenv("CEDAR_AGENT_ENDPOINT"); endpoint != "" {
		cfg.Authz.CedarAgentEndpoint = endpoint
		logger.Info("using cedar-agent for local AVP", "endpoint", endpoint)
	}
	if os.Getenv("AUTHZ_DISABLED") == "true" {
		cfg.Authz.Enabled = false
		logger.Info("authz disabled via environment variable")
	}

	// Rate limiting configuration from environment variables
	if os.Getenv("RATE_LIMIT_ENABLED") == "true" {
		cfg.RateLimit.Enabled = true
		cfg.RateLimit.RedisAddr = os.Getenv("REDIS_ENDPOINT")
		cfg.RateLimit.InMemory = os.Getenv("RATE_LIMIT_IN_MEMORY") == "true"
		cfg.RateLimit.ConfigFile = os.Getenv("RATE_LIMIT_CONFIG_FILE")
		if v := os.Getenv("RATE_LIMIT_DEFAULT_RATE"); v != "" {
			rate, err := strconv.Atoi(v)
			if err != nil {
				logger.Warn("ignoring malformed RATE_LIMIT_DEFAULT_RATE", "value", v, "error", err)
			} else {
				cfg.RateLimit.DefaultRate = rate
			}
		}
		if v := os.Getenv("RATE_LIMIT_DEFAULT_BURST"); v != "" {
			burst, err := strconv.Atoi(v)
			if err != nil {
				logger.Warn("ignoring malformed RATE_LIMIT_DEFAULT_BURST", "value", v, "error", err)
			} else {
				cfg.RateLimit.DefaultBurst = burst
			}
		}
		if v := os.Getenv("RATE_LIMIT_DEFAULT_WINDOW"); v != "" {
			window, err := strconv.Atoi(v)
			if err != nil {
				logger.Warn("ignoring malformed RATE_LIMIT_DEFAULT_WINDOW", "value", v, "error", err)
			} else {
				cfg.RateLimit.DefaultWindow = window
			}
		}
		if os.Getenv("RATE_LIMIT_TEST_MODE") == "true" {
			cfg.RateLimit.DefaultRate = 3
			cfg.RateLimit.DefaultBurst = 6
			cfg.RateLimit.DefaultWindow = 1
			cfg.RateLimit.InMemory = true
			logger.Info("rate limiting in TEST MODE (rate=3, burst=6, window=1s, in-memory)")
		} else {
			logger.Info("rate limiting enabled",
				"in_memory", cfg.RateLimit.InMemory,
				"config_file", cfg.RateLimit.ConfigFile,
			)
		}
	}

	// Create hyperfleet DB client (Postgres via pgruntime)
	dbClient, err := hyperfleetdb.NewClient(context.Background(), cfg.DB.DSN, logger)
	if err != nil {
		return fmt.Errorf("failed to create hyperfleetdb client: %w", err)
	}
	defer dbClient.Close()

	// Create server
	srv, err := server.New(cfg, dbClient, logger)
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
		"aws_region", awsCfg.Region,
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
	for acc := range strings.SplitSeq(accounts, ",") {
		acc = strings.TrimSpace(acc)
		if acc != "" {
			result = append(result, acc)
		}
	}
	return result
}
