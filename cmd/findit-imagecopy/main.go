// Command findit-imagecopy clones a whole device to a .bin image, read-only.
// The app runs it with administrator privileges (via the native macOS prompt)
// so a raw device can be read; it never writes to the source, and it makes the
// finished image readable by the user.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/findit/findit/internal/device"
	"github.com/findit/findit/internal/imaging"
	"github.com/findit/findit/internal/recovery"
)

func main() {
	deviceID := flag.String("device", "", "device id, e.g. disk4")
	out := flag.String("out", "", "destination .bin path")
	flag.Parse()

	if *deviceID == "" || *out == "" {
		fmt.Fprintln(os.Stderr, "usage: findit-imagecopy -device diskN -out path.bin")
		os.Exit(2)
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

	job, err := recovery.ImageDevice(ctx, *dev, *out, imaging.Options{OnReadError: imaging.ZeroFill})
	if err != nil {
		fatal(err)
	}
	for range job.Events() { // drain to completion
	}
	if err := job.Wait(); err != nil {
		fatal(err)
	}
	// The image was written as root; let the user read it.
	_ = os.Chmod(*out, 0o644)
	fmt.Println("OK")
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
