// Command findit-diagnose is a headless CLI for the Diagnosis Engine. It runs
// the same diagnosis code the app uses (internal/diagnosis) against a disk
// image and prints the result for a human or as JSON.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/findit/findit/internal/diagnosis"
	"github.com/findit/findit/internal/model"
)

func main() {
	image := flag.String("image", "", "path to the disk image (.bin) to diagnose")
	asJSON := flag.Bool("json", false, "emit the diagnosis as JSON")
	flag.Parse()

	if *image == "" {
		fmt.Fprintln(os.Stderr, "usage: findit-diagnose -image <path.bin> [-json]")
		os.Exit(2)
	}

	d, err := diagnosis.Diagnose(*image)
	if err != nil {
		fmt.Fprintln(os.Stderr, "diagnose:", err)
		os.Exit(1)
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(d)
		return
	}
	printHuman(d)
}

func printHuman(d *model.Diagnosis) {
	fmt.Printf("Image:      %s (%.1f MB)\n", d.ImagePath, float64(d.ImageSize)/(1<<20))
	fmt.Printf("Current FS: %s", d.Current.Type)
	if d.Current.Label != "" {
		fmt.Printf(" (label %q)", d.Current.Label)
	}
	fmt.Println()

	fmt.Println("\nPrevious filesystem candidates:")
	if len(d.Candidates) == 0 {
		fmt.Println("  (none)")
	}
	for _, c := range d.Candidates {
		fmt.Printf("  • %-6s  score %3d  %-6s", c.Type, c.Score, c.Confidence)
		if c.Label != "" {
			fmt.Printf("  label %q", c.Label)
		}
		fmt.Println()
		for _, e := range c.Evidence {
			fmt.Printf("      - %-15s @ 0x%X  %s\n", e.Kind, e.Offset, e.Detail)
		}
	}

	fmt.Println("\nRaw media found (content scan):")
	fmt.Printf("  photos: %d   videos: %d   (jpg=%d png=%d mp4=%d)\n",
		d.Carve.Photos(), d.Carve.Videos(),
		d.Carve.Counts["jpg"], d.Carve.Counts["png"], d.Carve.Counts["mp4"])

	fmt.Println("\nDiagnosis:")
	fmt.Printf("  %s\n", d.Narrative)
}
