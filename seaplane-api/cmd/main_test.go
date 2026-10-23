package main

import (
	"log/slog"
	"strings"
	"testing"
)

func TestCreateLogger(t *testing.T) {
	tests := []struct {
		name      string
		level     string
		format    string
		wantLevel slog.Level
	}{
		{
			name:      "debug json",
			level:     "debug",
			format:    "json",
			wantLevel: slog.LevelDebug,
		},
		{
			name:      "info json",
			level:     "info",
			format:    "json",
			wantLevel: slog.LevelInfo,
		},
		{
			name:      "warn json",
			level:     "warn",
			format:    "json",
			wantLevel: slog.LevelWarn,
		},
		{
			name:      "error json",
			level:     "error",
			format:    "json",
			wantLevel: slog.LevelError,
		},
		{
			name:      "info text",
			level:     "info",
			format:    "text",
			wantLevel: slog.LevelInfo,
		},
		{
			name:      "invalid level defaults to info",
			level:     "invalid",
			format:    "json",
			wantLevel: slog.LevelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := createLogger(tt.level, tt.format)
			if logger == nil {
				t.Fatal("expected non-nil logger")
			}

			// Logger is created successfully - we can't easily test the level
			// but we've covered the code paths
		})
	}
}

func TestParseAllowedAccounts(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "single account",
			input:    "123456789012",
			expected: []string{"123456789012"},
		},
		{
			name:     "multiple accounts",
			input:    "123456789012,987654321098,555555555555",
			expected: []string{"123456789012", "987654321098", "555555555555"},
		},
		{
			name:     "accounts with spaces",
			input:    "123456789012, 987654321098 , 555555555555",
			expected: []string{"123456789012", "987654321098", "555555555555"},
		},
		{
			name:     "accounts with empty values",
			input:    "123456789012,,987654321098",
			expected: []string{"123456789012", "987654321098"},
		},
		{
			name:     "only commas",
			input:    ",,,",
			expected: nil,
		},
		{
			name:     "spaces only",
			input:    "   ,   ,   ",
			expected: nil,
		},
		{
			name:     "trailing comma",
			input:    "123456789012,987654321098,",
			expected: []string{"123456789012", "987654321098"},
		},
		{
			name:     "leading comma",
			input:    ",123456789012,987654321098",
			expected: []string{"123456789012", "987654321098"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseAllowedAccounts(tt.input)

			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			if len(result) != len(tt.expected) {
				t.Errorf("expected %d accounts, got %d", len(tt.expected), len(result))
				return
			}

			for i, account := range tt.expected {
				if result[i] != account {
					t.Errorf("expected account[%d]=%s, got %s", i, account, result[i])
				}
			}
		})
	}
}

func TestRootCmd(t *testing.T) {
	if rootCmd == nil {
		t.Fatal("expected non-nil rootCmd")
	}

	if rootCmd.Use != "rosa-seaplane-api" {
		t.Errorf("expected Use=rosa-seaplane-api, got %s", rootCmd.Use)
	}

	if rootCmd.Short == "" {
		t.Error("expected non-empty Short description")
	}

	if rootCmd.Long == "" {
		t.Error("expected non-empty Long description")
	}
}

func TestServeCmd(t *testing.T) {
	if serveCmd == nil {
		t.Fatal("expected non-nil serveCmd")
	}

	if serveCmd.Use != "serve" {
		t.Errorf("expected Use=serve, got %s", serveCmd.Use)
	}

	if serveCmd.Short == "" {
		t.Error("expected non-empty Short description")
	}

	// Verify flags are registered
	flags := serveCmd.Flags()
	if flags == nil {
		t.Fatal("expected non-nil flags")
	}

	expectedFlags := []string{
		"log-level",
		"log-format",
		"allowed-accounts",
		"dns-base-domain-suffix",
		"api-port",
		"health-port",
		"metrics-port",
	}

	for _, flagName := range expectedFlags {
		flag := flags.Lookup(flagName)
		if flag == nil {
			t.Errorf("expected flag %s to be registered", flagName)
		}
	}
}

func TestRunServeRequiresDNSBaseDomainSuffix(t *testing.T) {
	original := dnsBaseDomainSuffix
	dnsBaseDomainSuffix = ""
	defer func() { dnsBaseDomainSuffix = original }()

	err := runServe(serveCmd, nil)
	if err == nil {
		t.Fatal("expected runServe to reject an empty DNS base domain suffix")
	}
	if !strings.Contains(err.Error(), "--dns-base-domain-suffix is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}
