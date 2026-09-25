package config

import "time"

type Config struct {
	Server    ServerConfig
	DB        DBConfig
	Regional  RegionalConfig
	Logging   LoggingConfig
	RateLimit RateLimitConfig
}

type RateLimitConfig struct {
	Enabled       bool
	RedisAddr     string
	InMemory      bool
	ConfigFile    string
	DefaultRate   int
	DefaultBurst  int
	DefaultWindow int
}

type DBConfig struct {
	DSN string
}

type RegionalConfig struct {
	OIDCIssuerBaseURL        string
	DefaultClusterExpiration time.Duration
	AWSRegion                string
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
	}
}
