//go:build integration
// +build integration

package e2e

import (
	"errors"
	"testing"
)

func TestIsRetryableDeployError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "matches cert-manager webhook failure",
			err:  errors.New(`failed calling webhook "webhook.cert-manager.io"`),
			want: true,
		},
		{
			name: "matches missing certificate resource mapping",
			err:  errors.New("resource mapping not found for name: certificates.cert-manager.io"),
			want: true,
		},
		{
			name: "matches missing issuer kind",
			err:  errors.New("no matches for kind \"Issuer\" in version \"cert-manager.io/v1\""),
			want: true,
		},
		{
			name: "matches transient api server overload",
			err:  errors.New("the server is currently unable to handle the request"),
			want: true,
		},
		{
			name: "matches webhook transport timeout",
			err:  errors.New("context deadline exceeded"),
			want: true,
		},
		{
			name: "rejects unrelated error",
			err:  errors.New("boom"),
			want: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := isRetryableDeployError(tc.err); got != tc.want {
				t.Fatalf("isRetryableDeployError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
