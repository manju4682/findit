package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	rt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/findit/findit/internal/carve"
	"github.com/findit/findit/internal/device"
	"github.com/findit/findit/internal/jobs"
	"github.com/findit/findit/internal/model"
	"github.com/findit/findit/internal/recovery"
	"github.com/findit/findit/internal/storage"
)

// App is the Wails-bound backend: a thin layer over the recovery facade that
// the Vue frontend calls, plus a small index so preview/recover can resolve the
// file the user clicked back to its source.
type App struct {
	ctx context.Context

	mu          sync.Mutex
	src         storage.Source
	diag        *model.Diagnosis
	result      model.ScanResult
	sourcesByID map[string]model.RecoverySource
	filesByID   map[string]fileRef
	devices     map[string]device.Device
}

type fileRef struct {
	sourceID string
	file     model.RecoveredFile
}

// NewApp returns a ready App.
func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// SupportedRawTypes lists the file extensions the raw carver understands.
func (a *App) SupportedRawTypes() []string { return carve.Supported() }

// ListDevices returns physical storage devices and caches them so a later
// clone/scan can resolve a device by ID.
func (a *App) ListDevices() ([]device.Device, error) {
	devs, err := recovery.ListDevices(a.ctx)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.devices = map[string]device.Device{}
	for _, d := range devs {
		a.devices[d.ID] = d
	}
	a.mu.Unlock()
	return devs, nil
}

// SelectSaveImagePath opens a save dialog for the destination of a drive clone.
func (a *App) SelectSaveImagePath(defaultName string) (string, error) {
	return rt.SaveFileDialog(a.ctx, rt.SaveDialogOptions{
		Title:           "Save the drive copy as…",
		DefaultFilename: defaultName,
		Filters: []rt.FileFilter{
			{DisplayName: "Disk image (*.bin)", Pattern: "*.bin"},
		},
	})
}

// StartClone images a device to destPath, streaming events to the frontend:
//
//	clone:progress {message,done,total}
//	clone:done     {path,bytesCopied,badRegions}
//	clone:error    string
//
// Reading a raw device needs elevated privileges, so the copy runs in a bundled
// helper launched via the native macOS administrator prompt. The source is only
// ever read.
func (a *App) StartClone(deviceID, destPath string) error {
	a.mu.Lock()
	dev, ok := a.devices[deviceID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("device %q not found; list devices first", deviceID)
	}
	helper, err := imageCopyHelperPath()
	if err != nil {
		return err
	}

	shellCmd := fmt.Sprintf("%s -device %s -out %s",
		shellQuote(helper), shellQuote(deviceID), shellQuote(destPath))
	script := fmt.Sprintf("do shell script %q with administrator privileges", shellCmd)

	go func() {
		done := make(chan struct{})
		go a.pollCloneProgress(destPath, dev.Size, done)

		out, err := exec.CommandContext(a.ctx, "osascript", "-e", script).CombinedOutput()
		close(done)

		if err != nil {
			msg := strings.TrimSpace(string(out))
			if strings.Contains(msg, "-128") || strings.Contains(msg, "User canceled") {
				msg = "Permission was not granted, so the drive wasn’t copied."
			}
			if strings.Contains(strings.ToLower(msg), "operation not permitted") || strings.Contains(strings.ToLower(msg), "permission denied") {
				msg = "macOS blocked raw disk access. Open System Settings → Privacy & Security → Full Disk Access and allow FindIt, then retry. If the app still denies access, clone the drive using the built-in administrator prompt or work from an existing disk image."
			}
			if msg == "" {
				msg = err.Error()
			}
			rt.EventsEmit(a.ctx, "clone:error", msg)
			return
		}
		rt.EventsEmit(a.ctx, "clone:done", map[string]any{"path": destPath})
	}()
	return nil
}

// pollCloneProgress emits progress by watching the growing image file until the
// clone finishes.
func (a *App) pollCloneProgress(destPath string, total int64, done <-chan struct{}) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			fi, err := os.Stat(destPath)
			if err != nil {
				continue
			}
			d, t := fi.Size(), total
			msg := fmt.Sprintf("%d / %d MB", d/(1<<20), t/(1<<20))
			rt.EventsEmit(a.ctx, "clone:progress", map[string]any{
				"message": msg, "done": int(d / (1 << 20)), "total": int(t / (1 << 20)),
			})
		}
	}
}

// imageCopyHelperPath locates the bundled clone helper next to the app binary.
func imageCopyHelperPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), "findit-imagecopy")
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("clone helper not found; cloning is only available in the installed app")
	}
	return p, nil
}

// shellQuote single-quotes a string for safe use in a /bin/sh command line.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// SelectImageFile opens a file dialog and returns the chosen path ("" if cancelled).
func (a *App) SelectImageFile() (string, error) {
	return rt.OpenFileDialog(a.ctx, rt.OpenDialogOptions{
		Title: "Choose a disk image",
		Filters: []rt.FileFilter{
			{DisplayName: "Disk images (*.bin, *.img, *.dd, *.raw)", Pattern: "*.bin;*.img;*.dd;*.raw"},
			{DisplayName: "All files", Pattern: "*.*"},
		},
	})
}

// SelectDirectory opens a folder dialog for the recovery destination.
func (a *App) SelectDirectory() (string, error) {
	return rt.OpenDirectoryDialog(a.ctx, rt.OpenDialogOptions{
		Title: "Choose where to save recovered files",
	})
}

// RevealInFinder opens a folder in the OS file manager.
func (a *App) RevealInFinder(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// ScanDone is the payload of the "scan:done" event.
type ScanDone struct {
	Diagnosis *model.Diagnosis `json:"diagnosis"`
	Result    model.ScanResult `json:"result"`
}

// StartScan opens the image at path and runs a scan for the selected
// filesystems and raw extensions, streaming events to the frontend:
//
//	scan:progress {phase,message,done,total}
//	scan:source   RecoverySource
//	scan:done     ScanDone
//	scan:error    string
func (a *App) StartScan(path string, filesystems []string, raw bool, rawExts []string) error {
	src, err := recovery.OpenImage(path)
	if err != nil {
		return err
	}
	a.runScan(src, filesystems, raw, rawExts)
	return nil
}

// StartScanDevice scans a device directly (image-first is preferred; this backs
// the "skip clone" path). Reading a raw device may require elevated privileges.
func (a *App) StartScanDevice(deviceID string, filesystems []string, raw bool, rawExts []string) error {
	a.mu.Lock()
	dev, ok := a.devices[deviceID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("device %q not found; list devices first", deviceID)
	}
	src, err := recovery.OpenDevice(dev)
	if err != nil {
		return err
	}
	a.runScan(src, filesystems, raw, rawExts)
	return nil
}

func (a *App) runScan(src storage.Source, filesystems []string, raw bool, rawExts []string) {
	a.mu.Lock()
	if a.src != nil {
		a.src.Close()
	}
	a.src = src
	a.diag = nil
	a.result = model.ScanResult{}
	a.sourcesByID = map[string]model.RecoverySource{}
	a.filesByID = map[string]fileRef{}
	a.mu.Unlock()

	req := model.ScanRequest{Raw: raw, RawExtensions: rawExts}
	for _, f := range filesystems {
		req.Filesystems = append(req.Filesystems, model.FSType(f))
	}

	scan := recovery.Scan(a.ctx, src, req)
	go func() {
		for e := range scan.Events() {
			switch e.Kind {
			case jobs.KindProgress, jobs.KindLog, jobs.KindSourceStarted:
				rt.EventsEmit(a.ctx, "scan:progress", map[string]any{
					"phase": e.Phase, "message": e.Message, "done": e.Done, "total": e.Total,
				})
			case jobs.KindSourceDone:
				rt.EventsEmit(a.ctx, "scan:source", e.Source)
			}
		}
		if err := scan.Wait(); err != nil {
			rt.EventsEmit(a.ctx, "scan:error", err.Error())
			return
		}
		a.index(scan.Diagnosis(), scan.Result())
		a.mu.Lock()
		done := ScanDone{Diagnosis: a.diag, Result: a.result}
		a.mu.Unlock()
		rt.EventsEmit(a.ctx, "scan:done", done)
	}()
}

func (a *App) index(diag *model.Diagnosis, res model.ScanResult) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.diag = diag
	a.result = res
	for _, s := range res.Sources {
		a.sourcesByID[s.ID] = s
		if s.Kind == model.SourceRaw {
			for _, f := range s.Files {
				a.filesByID[f.ID] = fileRef{s.ID, f}
			}
		} else if s.Root != nil {
			indexTree(s.Root, s.ID, a.filesByID)
		}
	}
}

func indexTree(n *model.Node, sourceID string, m map[string]fileRef) {
	if n.File != nil {
		m[n.File.ID] = fileRef{sourceID, *n.File}
	}
	for _, c := range n.Children {
		indexTree(c, sourceID, m)
	}
}

// PreviewDTO is a preview result marshalled for the frontend.
type PreviewDTO struct {
	Kind             string `json:"kind"`
	Status           string `json:"status"`
	Note             string `json:"note"`
	Width            int    `json:"width"`
	Height           int    `json:"height"`
	ThumbnailDataURL string `json:"thumbnailDataUrl"`
}

// Preview returns a thumbnail/status for one file.
func (a *App) Preview(sourceID, fileID string) (PreviewDTO, error) {
	a.mu.Lock()
	src := a.src
	source, okS := a.sourcesByID[sourceID]
	ref, okF := a.filesByID[fileID]
	a.mu.Unlock()
	if src == nil || !okS || !okF {
		return PreviewDTO{}, fmt.Errorf("preview: file not found")
	}
	p := recovery.Preview(a.ctx, src, source, ref.file, 256)
	dto := PreviewDTO{Kind: string(p.Kind), Status: string(p.Status), Note: p.Note, Width: p.Width, Height: p.Height}
	if len(p.Thumbnail) > 0 {
		dto.ThumbnailDataURL = "data:image/png;base64," + base64.StdEncoding.EncodeToString(p.Thumbnail)
	}
	return dto, nil
}

// RecoverItemDTO is one file's extraction outcome.
type RecoverItemDTO struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

// RecoverDTO summarizes an extraction run.
type RecoverDTO struct {
	Written int              `json:"written"`
	Failed  int              `json:"failed"`
	Items   []RecoverItemDTO `json:"items"`
}

// Recover extracts the selected files (all from one source) to destDir.
func (a *App) Recover(sourceID string, fileIDs []string, destDir string, preservePaths bool) (RecoverDTO, error) {
	a.mu.Lock()
	src := a.src
	source, okS := a.sourcesByID[sourceID]
	var files []model.RecoveredFile
	for _, id := range fileIDs {
		if ref, ok := a.filesByID[id]; ok {
			files = append(files, ref.file)
		}
	}
	a.mu.Unlock()
	if src == nil || !okS {
		return RecoverDTO{}, fmt.Errorf("recover: no active scan")
	}
	if len(files) == 0 {
		return RecoverDTO{}, fmt.Errorf("recover: no files selected")
	}
	res, err := recovery.Recover(a.ctx, src, source, files, destDir, preservePaths)
	if err != nil {
		return RecoverDTO{}, err
	}
	dto := RecoverDTO{Written: res.Written, Failed: res.Failed}
	for _, it := range res.Items {
		msg := ""
		if it.Err != nil {
			msg = it.Err.Error()
		}
		dto.Items = append(dto.Items, RecoverItemDTO{Name: it.File.Name, Path: it.Path, Error: msg})
	}
	return dto, nil
}
