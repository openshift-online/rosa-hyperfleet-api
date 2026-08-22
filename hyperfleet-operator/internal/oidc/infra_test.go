/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"
)

func rsaKeyPEM(t *testing.T, bits int) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, bits)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})
}

func TestValidateRSAPrivateKey(t *testing.T) {
	t.Run("accepts a key at the minimum size", func(t *testing.T) {
		if err := ValidateRSAPrivateKey(rsaKeyPEM(t, minKeyBits)); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("rejects a key below the minimum size", func(t *testing.T) {
		if err := ValidateRSAPrivateKey(rsaKeyPEM(t, 1024)); err == nil {
			t.Error("expected an error for a key below the minimum size, got nil")
		}
	})
}

func TestResolveIssuerHost(t *testing.T) {
	// IP literals resolve without a real DNS lookup, so these cases don't
	// require network access.
	tests := []struct {
		name      string
		issuerURL string
		wantErr   bool
		wantAddr  string
	}{
		{"rejects non-https scheme", "http://93.184.216.34", true, ""},
		{"rejects loopback", "https://127.0.0.1", true, ""},
		{"rejects IPv6 loopback", "https://[::1]", true, ""},
		{"rejects link-local (cloud metadata)", "https://169.254.169.254", true, ""},
		{"rejects private range", "https://10.0.0.1", true, ""},
		{"rejects unspecified", "https://0.0.0.0", true, ""},
		{"accepts a public address", "https://93.184.216.34", false, "93.184.216.34:443"},
		{"accepts a public address with explicit port", "https://93.184.216.34:8443", false, "93.184.216.34:8443"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, dialAddr, err := resolveIssuerHost(context.Background(), tt.issuerURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error for %q, got nil", tt.issuerURL)
				}
				return
			}
			if err != nil {
				t.Fatalf("expected no error for %q, got %v", tt.issuerURL, err)
			}
			if dialAddr != tt.wantAddr {
				t.Errorf("expected dial address %q, got %q", tt.wantAddr, dialAddr)
			}
		})
	}
}
