//go:build !darwin

package device

import (
	"fmt"
	"runtime"
)

func volumeOf(string) (Volume, error) {
	return Volume{}, fmt.Errorf("device: volume lookup not supported on %s yet", runtime.GOOS)
}
