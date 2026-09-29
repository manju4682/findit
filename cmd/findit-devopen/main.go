// Command findit-devopen is the privileged helper behind "scan the drive
// directly". Launched once via the macOS administrator prompt, it opens the
// device read-only, hands the descriptor to the app over a unix socket, and then
// stays connected to run The Sleuth Kit (fls/icat) as root on the app's behalf —
// TSK must open the disk itself, which only root can do. Requests are validated
// (see tsk.Args) and it exits as soon as the app closes the session or quits.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/manju4682/findit/internal/device"
	"github.com/manju4682/findit/internal/engines/tsk"
	"github.com/manju4682/findit/internal/privdev"
)

func main() {
	deviceID := flag.String("device", "", "device id, e.g. disk4")
	socket := flag.String("socket", "", "unix socket to connect back to")
	flag.Parse()

	if *deviceID == "" || *socket == "" {
		fmt.Fprintln(os.Stderr, "usage: findit-devopen -device diskN -socket /path.sock")
		os.Exit(2)
	}

	devs, err := device.List(context.Background())
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
	rawNode, tskNode := dev.RawNode, dev.Node
	if rawNode == "" {
		rawNode = dev.Node
	}
	if tskNode == "" {
		tskNode = rawNode
	}

	// The app reads the raw node itself (fast, sector-aligned); TSK reads the
	// buffered node, which tolerates its unaligned reads.
	local := tsk.LocalExecutor{Image: tskNode}
	run := func(ctx context.Context, req tsk.Request, w io.Writer) error {
		return local.Exec(ctx, req, w)
	}
	if err := privdev.Serve(*socket, rawNode, run); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
