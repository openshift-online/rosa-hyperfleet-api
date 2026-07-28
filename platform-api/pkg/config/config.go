package config

import (
	"time"

	"github.com/openshift-online/rosa-hyperfleet-api/platform-api/pkg/authz"
)

type Config struct {
	Server          ServerConfig
	DB              DBConfig
	Regional        RegionalConfig
	Logging         LoggingConfig
	Authz           *authz.Config
	Zoa             ZoaConfig
	AllowedAccounts []string
}

type DBConfig struct {
	DSN string
}

type RegionalConfig struct {
	OIDCIssuerBaseURL        string
	DefaultClusterExpiration time.Duration
	FeatureSet               string
}

type ZoaConfig struct {
	Enabled        bool
	TableName      string
	AuditTableName string
	BucketName     string
	AWSRegion      string
	TemplatesDir   string
	JobConfigDir   string
	PollInterval   time.Duration
}

type ServerConfig struct {
	APIBindAddress     string
	APIPort            int
	GRPCBindAddress    string
	GRPCPort           int
	HealthBindAddress  string
	HealthPort         int
	MetricsBindAddress string
	MetricsPort        int
	ShutdownTimeout    time.Duration
}

type LoggingConfig struct {
	Level  string
	Format string
}

func NewConfig() *Config {
	return &Config{
		Server: ServerConfig{
			APIBindAddress:     "0.0.0.0",
			APIPort:            8000,
			GRPCBindAddress:    "0.0.0.0",
			GRPCPort:           8090,
			HealthBindAddress:  "0.0.0.0",
			HealthPort:         8080,
			MetricsBindAddress: "0.0.0.0",
			MetricsPort:        9090,
			ShutdownTimeout:    30 * time.Second,
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Authz: authz.DefaultConfig(),
		Zoa: ZoaConfig{
			PollInterval: 15 * time.Second,
		},
	}
}
