//go:build linux || darwin || freebsd || netbsd

package fsutil

import (
	"fmt"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSELinuxPolicyDenialClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"eacces", unix.EACCES, true},
		{"eperm", unix.EPERM, true},
		{"enotsup", unix.ENOTSUP, true},
		{"eopnotsupp", unix.EOPNOTSUPP, true},
		{"eio", unix.EIO, false},
		{"enospc", unix.ENOSPC, false},
		{"wrapped eacces", fmt.Errorf("setxattr: %w", unix.EACCES), true},
		{"wrapped eio", fmt.Errorf("setxattr: %w", unix.EIO), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isSELinuxPolicyDenial(tc.err); got != tc.want {
				t.Fatalf("isSELinuxPolicyDenial(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
