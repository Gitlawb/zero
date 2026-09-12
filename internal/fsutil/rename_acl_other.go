//go:build !darwin

package fsutil

import "os"

// preserveNativeACL is a no-op on platforms without the Darwin native ACL
// representation. Linux POSIX ACLs travel through preserveXattrs as
// system.posix_acl_access; on Windows the destination DACL is applied to the
// staging file by protectStaging before the write and carried across
// publication by the DACL-preserving replace primitive.
func preserveNativeACL(*os.File, string) error {
	return nil
}
