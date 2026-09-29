// Command findit-imagecopy clones a whole device to a .bin image, read-only.
// The app runs it with administrator privileges (via the native macOS prompt)
// so a raw device can be read; it never writes to the source, and it hands the
// finished image (mode 0600) to the requesting user.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/manju4682/findit/internal/device"
	"github.com/manju4682/findit/internal/imaging"
	"github.com/manju4682/findit/internal/recovery"
)

func main() {
	deviceID := flag.String("device", "", "device id, e.g. disk4")
	out := flag.String("out", "", "destination .bin path")
	cancel := flag.String("cancel", "", "sentinel path; if it appears, abort and delete the partial image")
	uid := flag.Int("uid", -1, "user to own the finished image")
	gid := flag.Int("gid", -1, "group to own the finished image")
	flag.Parse()

	if *deviceID == "" || *out == "" || !filepath.IsAbs(*out) {
		fmt.Fprintln(os.Stderr, "usage: findit-imagecopy -device diskN -out /abs/path.bin [-uid N -gid N] [-cancel /abs/path]")
		os.Exit(2)
	}
	// Running as root: only replace a file the requesting user already owns, so
	// the helper can't be pointed at someone else's (or a system) file.
	if fi, err := os.Lstat(*out); err == nil {
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !fi.Mode().IsRegular() || !ok || (*uid >= 0 && int(st.Uid) != *uid) {
			fatal(fmt.Errorf("refusing to overwrite %s", *out))
		}
	}

	ctx := context.Background()
	devs, err := device.List(ctx)
	if err != nil {
		fatal(err)
	}
	var dev *device.Device
	for i := range devs {
		if devs[i].ID == *deviceID {
			dev = &devs[i]
			break
		}
	}
	if dev == nil {
		fatal(fmt.Errorf("device %q not found", *deviceID))
	}
	if err := device.CheckImageDestination(*out, *dev); err != nil {
		fatal(err)
	}

	job, err := recovery.ImageDevice(ctx, *dev, *out, imaging.Options{OnReadError: imaging.ZeroFill, CancelPath: *cancel})
	if err != nil {
		fatal(err)
	}
	for range job.Events() { // drain to completion
	}
	err = job.Wait()
	if errors.Is(err, imaging.ErrCanceled) {
		_ = os.Remove(*out) // discard the partial image
		fmt.Println("CANCELED")
		return
	}
	// The image was written as root; hand it to the user who asked for it.
	if *uid >= 0 {
		_ = os.Lchown(*out, *uid, *gid)
	}
	if err != nil {
		fatal(err)
	}
	var unreadable int64
	for _, r := range job.Result().BadRegions {
		unreadable += r.Length
	}
	fmt.Println("OK", unreadable)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
