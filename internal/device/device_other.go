//go:build !darwin

package device

import (
	"context"
	"fmt"
	"runtime"
)

// listDevices is not yet implemented outside macOS; the Windows port will add a
// build-tagged implementation.
func listDevices(_ context.Context) ([]Device, error) {
	return nil, fmt.Errorf("device: enumeration not supported on %s yet", runtime.GOOS)
}
