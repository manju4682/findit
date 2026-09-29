//go:build darwin

package device

import (
	"bufio"
	"context"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var reDiskID = regexp.MustCompile(`^(?:/dev/)?r?(disk\d+)`)

func volumeOf(path string) (Volume, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return Volume{}, err
	}
	v := Volume{
		FSType: unix.ByteSliceToString(st.Fstypename[:]),
		Free:   int64(st.Bavail) * int64(st.Bsize),
	}
	from := unix.ByteSliceToString(st.Mntfromname[:])
	if v.FSType == "apfs" {
		// APFS volumes live on a synthesized container disk; the physical
		// store is what matters when comparing against a physical device.
		if store := apfsPhysicalStore(from); store != "" {
			from = store
		}
	}
	if m := reDiskID.FindStringSubmatch(from); m != nil {
		v.Disk = m[1]
	}
	return v, nil
}

// apfsPhysicalStore returns the physical partition backing an APFS volume
// (e.g. "disk4s2"), or "" if it can't be determined.
func apfsPhysicalStore(dev string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, diskutil, "info", dev).Output()
	if err != nil {
		return ""
	}
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		if key, val, ok := splitField(sc.Text()); ok && key == "APFS Physical Store" {
			return val
		}
	}
	return ""
}
