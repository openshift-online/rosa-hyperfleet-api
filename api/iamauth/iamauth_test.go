package iamauth

import (
	"regexp"
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

func TestCreatorSubjectPattern(t *testing.T) {
	tests := []struct {
		name      string
		callerARN string
		sub       string
		wantMatch bool
		wantErr   bool
	}{
		{
			name:      "When the creator assumed a role it should match the role ARN",
			callerARN: "arn:aws:sts::111122223333:assumed-role/PlatformAdmins/alice",
			sub:       "arn:aws:iam::111122223333:role/PlatformAdmins",
			wantMatch: true,
		},
		{
			name:      "When the role has a path it should still match",
			callerARN: "arn:aws:sts::111122223333:assumed-role/PlatformAdmins/alice",
			sub:       "arn:aws:iam::111122223333:role/team/x/PlatformAdmins",
			wantMatch: true,
		},
		{
			name:      "When a role name only shares a suffix it should not match",
			callerARN: "arn:aws:sts::111122223333:assumed-role/PlatformAdmins/alice",
			sub:       "arn:aws:iam::111122223333:role/XPlatformAdmins",
		},
		{
			name:      "When a role name only shares a prefix it should not match",
			callerARN: "arn:aws:sts::111122223333:assumed-role/PlatformAdmins/alice",
			sub:       "arn:aws:iam::111122223333:role/PlatformAdmins2",
		},
		{
			name:      "When the role is in another account it should not match",
			callerARN: "arn:aws:sts::111122223333:assumed-role/PlatformAdmins/alice",
			sub:       "arn:aws:iam::444455556666:role/PlatformAdmins",
		},
		{
			name:      "When the role name has regex characters they should be literal",
			callerARN: "arn:aws:sts::111122223333:assumed-role/a.b+c/alice",
			sub:       "arn:aws:iam::111122223333:role/aXbbc",
		},
		{
			name:      "When the creator is an IAM user it should match exactly",
			callerARN: "arn:aws:iam::111122223333:user/ops/bob",
			sub:       "arn:aws:iam::111122223333:user/ops/bob",
			wantMatch: true,
		},
		{
			name:      "When the creator is an IAM user it should not match another user",
			callerARN: "arn:aws:iam::111122223333:user/ops/bob",
			sub:       "arn:aws:iam::111122223333:user/ops/bobby",
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
			pattern, err := CreatorSubjectPattern(tt.callerARN)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got pattern %q", pattern)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := regexp.MustCompile(pattern).MatchString(tt.sub); got != tt.wantMatch {
				t.Errorf("pattern %q matching %q = %v, want %v", pattern, tt.sub, got, tt.wantMatch)
			}
		})
	}
}
