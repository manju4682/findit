//go:build darwin

package device

import (
	"bufio"
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

var (
	reWholeDisk = regexp.MustCompile(`^/dev/(disk\d+) \(([^)]*)\):`)
	reBytes     = regexp.MustCompile(`\((\d+) Bytes\)`)
	reBlockSize = regexp.MustCompile(`(\d+) Bytes`)
)

// listDevices enumerates physical whole disks via diskutil.
func listDevices(ctx context.Context) ([]Device, error) {
	out, err := exec.CommandContext(ctx, "diskutil", "list").Output()
	if err != nil {
		return nil, err
	}

	var devices []Device
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		m := reWholeDisk.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		id, descr := m[1], m[2]
		if !strings.Contains(descr, "physical") { // skip synthesized/virtual disks
			continue
		}
		d, err := infoDevice(ctx, id)
		if err != nil {
			continue // a disk we can't introspect is not actionable
		}
		devices = append(devices, d)
	}
	return devices, sc.Err()
}

// infoDevice parses `diskutil info <id>` into a Device.
func infoDevice(ctx context.Context, id string) (Device, error) {
	out, err := exec.CommandContext(ctx, "diskutil", "info", id).Output()
	if err != nil {
		return Device{}, err
	}
	d := Device{ID: id, Node: "/dev/" + id, RawNode: "/dev/r" + id, BlockSize: 512}

	sc := bufio.NewScanner(strings.NewReader(string(out)))
	for sc.Scan() {
		key, val, ok := splitField(sc.Text())
		if !ok {
			continue
		}
		switch key {
		case "Device / Media Name", "Media Name":
			if d.Name == "" {
				d.Name = val
			}
		case "Protocol":
			d.Protocol = val
		case "Disk Size", "Total Size":
			if mm := reBytes.FindStringSubmatch(val); mm != nil {
				d.Size, _ = strconv.ParseInt(mm[1], 10, 64)
			}
		case "Device Block Size":
			if mm := reBlockSize.FindStringSubmatch(val); mm != nil {
				if bs, err := strconv.Atoi(mm[1]); err == nil && bs > 0 {
					d.BlockSize = bs
				}
			}
		case "Removable Media":
			d.Removable = strings.Contains(strings.ToLower(val), "removable")
		case "Internal":
			d.Internal = strings.EqualFold(val, "Yes")
		}
	}
	// External disks are effectively removable for recovery purposes.
	if !d.Internal {
		d.Removable = true
	}
	return d, sc.Err()
}

func splitField(line string) (key, val string, ok bool) {
	i := strings.IndexByte(line, ':')
	if i < 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
}
