package store

import "testing"

func TestNormalizeAssumedRoleARN(t *testing.T) {
	tests := []struct {
		name string
		arn  string
		want string
	}{
		{
			name: "STS assumed-role ARN is normalized to IAM role ARN",
			arn:  "arn:aws:sts::123456789012:assumed-role/MyRole/session-name",
			want: "arn:aws:iam::123456789012:role/MyRole",
		},
		{
			name: "IAM role ARN is returned unchanged",
			arn:  "arn:aws:iam::123456789012:role/MyRole",
			want: "arn:aws:iam::123456789012:role/MyRole",
		},
		{
			name: "IAM user ARN is returned unchanged",
			arn:  "arn:aws:iam::123456789012:user/MyUser",
			want: "arn:aws:iam::123456789012:user/MyUser",
		},
		{
			name: "session name with slashes is handled",
			arn:  "arn:aws:sts::123456789012:assumed-role/MyRole/session/with/slashes",
			want: "arn:aws:iam::123456789012:role/MyRole",
		},
		{
			name: "empty string is returned unchanged",
			arn:  "",
			want: "",
		},
		{
			name: "malformed ARN with too few parts is returned unchanged",
			arn:  "arn:aws:sts",
			want: "arn:aws:sts",
		},
		{
			name: "assumed-role without session suffix",
			arn:  "arn:aws:sts::123456789012:assumed-role/MyRole",
			want: "arn:aws:iam::123456789012:role/MyRole",
		},
		{
			name: "real-world EKS pod identity ARN",
			arn:  "arn:aws:sts::599476212575:assumed-role/eph-b1fe3c6f-regional-authz-platform-api/eks-eph-b1fe3c-platform-a-96beb35e",
			want: "arn:aws:iam::599476212575:role/eph-b1fe3c6f-regional-authz-platform-api",
		},
		{
			name: "BFF proxy role with session",
			arn:  "arn:aws:sts::754250776154:assumed-role/bff-sigv4-proxy-role/BFF-Session-1786164739",
			want: "arn:aws:iam::754250776154:role/bff-sigv4-proxy-role",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeAssumedRoleARN(tt.arn)
			if got != tt.want {
				t.Errorf("NormalizeAssumedRoleARN(%q) = %q, want %q", tt.arn, got, tt.want)
			}
		})
	}
}
