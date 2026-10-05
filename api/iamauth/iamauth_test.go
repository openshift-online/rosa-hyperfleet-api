package iamauth

import (
	"testing"
)

func TestValidIssuerURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "When the URL is an STS issuer it should be accepted", url: "https://a1b2c3d4-e5f6.tokens.sts.global.api.aws", want: true},
		{name: "When the URL has a trailing slash it should be rejected", url: "https://a1b2c3d4.tokens.sts.global.api.aws/"},
		{name: "When the URL has a path it should be rejected", url: "https://a1b2c3d4.tokens.sts.global.api.aws/x"},
		{name: "When the URL uses http it should be rejected", url: "http://a1b2c3d4.tokens.sts.global.api.aws"},
		{name: "When the host only ends with the STS domain it should be rejected", url: "https://evil.example.com/.tokens.sts.global.api.aws"},
		{name: "When the URL has user info it should be rejected", url: "https://x@a1b2c3d4.tokens.sts.global.api.aws"},
		{name: "When the URL has a port it should be rejected", url: "https://a1b2c3d4.tokens.sts.global.api.aws:8443"},
		{name: "When the URL is another host it should be rejected", url: "https://169.254.169.254"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidIssuerURL(tt.url); got != tt.want {
				t.Errorf("ValidIssuerURL(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestCreatorSubjectCondition(t *testing.T) {
	tests := []struct {
		name      string
		callerARN string
		want      string
		wantErr   bool
	}{
		{
			name:      "When the creator assumed a role it should match the role in that account with any path",
			callerARN: "arn:aws:sts::111122223333:assumed-role/PlatformAdmins/alice",
			want:      "claims.sub.startsWith('arn:aws:iam::111122223333:role/') && claims.sub.endsWith('/PlatformAdmins')",
		},
		{
			name:      "When the creator is an IAM user it should match exactly",
			callerARN: "arn:aws:iam::111122223333:user/ops/bob",
			want:      "claims.sub == 'arn:aws:iam::111122223333:user/ops/bob'",
		},
		{
			name:      "When the creator is in GovCloud it should keep the partition",
			callerARN: "arn:aws-us-gov:sts::111122223333:assumed-role/Admins/alice",
			want:      "claims.sub.startsWith('arn:aws-us-gov:iam::111122223333:role/') && claims.sub.endsWith('/Admins')",
		},
		{
			name:      "When the creator is root it should be rejected",
			callerARN: "arn:aws:iam::111122223333:root",
			wantErr:   true,
		},
		{
			name:      "When the creator is a federated user it should be rejected",
			callerARN: "arn:aws:sts::111122223333:federated-user/bob",
			wantErr:   true,
		},
		{
			name:      "When the role name contains a quote it should be rejected",
			callerARN: "arn:aws:sts::111122223333:assumed-role/a'b/alice",
			wantErr:   true,
		},
		{
			name:      "When the user name contains a quote it should be rejected",
			callerARN: "arn:aws:iam::111122223333:user/a'b",
			wantErr:   true,
		},
		{
			name:      "When the account is not 12 digits it should be rejected",
			callerARN: "arn:aws:sts::1111' || true:assumed-role/a/alice",
			wantErr:   true,
		},
		{
			name:      "When the input is not an ARN it should be rejected",
			callerARN: "not-an-arn",
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CreatorSubjectCondition(tt.callerARN)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("CreatorSubjectCondition() = %q, want %q", got, tt.want)
			}
		})
	}
}
