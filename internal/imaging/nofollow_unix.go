//go:build unix

package imaging

import "syscall"

// oNoFollow refuses to write through a symlink planted at the destination.
const oNoFollow = syscall.O_NOFOLLOW
