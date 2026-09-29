package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	rt "github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/manju4682/findit/internal/carve"
	"github.com/manju4682/findit/internal/config"
	"github.com/manju4682/findit/internal/device"
	"github.com/manju4682/findit/internal/diagnosis"
	"github.com/manju4682/findit/internal/jobs"
	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/privdev"
	"github.com/manju4682/findit/internal/recovery"
	"github.com/manju4682/findit/internal/session"
	"github.com/manju4682/findit/internal/storage"
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

	scan            *session.Scan // active scan, for cancellation
	scanCanceled    bool
	cloneCancelPath string // sentinel the clone helper watches

	previewSlots chan struct{} // bounds concurrent preview decodes
}

type fileRef struct {
	sourceID string
	file     model.RecoveredFile
}

// NewApp returns a ready App.
func NewApp() *App {
	return &App{previewSlots: make(chan struct{}, max(2, runtime.NumCPU()/2))}
}

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

// shutdown stops background work so no privileged helper outlives the app.
func (a *App) shutdown(context.Context) {
	_ = a.CancelClone()
	a.mu.Lock()
	scan, src := a.scan, a.src
	a.scan, a.src = nil, nil
	a.mu.Unlock()
	if scan != nil {
		scan.Cancel()
		_ = scan.Wait()
	}
	if src != nil {
		_ = src.Close() // also ends a direct-scan helper session
	}
}

// SupportedRawTypes lists the file extensions the raw carver understands.
func (a *App) SupportedRawTypes() []string { return carve.Supported() }

// PartitionDTO describes one detected partition/volume for the partition picker.
type PartitionDTO struct {
	Offset int64  `json:"offset"`
	FSType string `json:"fsType"`
	Label  string `json:"label"`
	Size   int64  `json:"size"`
}

// DetectPartitions does a fast partition-table read of a disk image (no full
// content scan) so the frontend can offer an optional partition picker. It
// returns an empty list on any error, so callers can silently fall back to
// "scan everything".
func (a *App) DetectPartitions(imagePath string) ([]PartitionDTO, error) {
	if imagePath == "" {
		return nil, nil
	}
	src, err := storage.OpenImage(imagePath)
	if err != nil {
		return nil, nil
	}
	defer src.Close()
	var out []PartitionDTO
	for _, p := range diagnosis.Partitions(src) {
		out = append(out, PartitionDTO{Offset: p.Offset, FSType: string(p.Type), Label: p.Label, Size: p.Size})
	}
	return out, nil
}

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
//	clone:done     {path,unreadableBytes}
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
	if err := device.CheckImageDestination(destPath, dev); err != nil {
		return err
	}
	helper, err := helperPath("findit-imagecopy")
	if err != nil {
		return err
	}

	// A sentinel file the (root) helper polls; touching it aborts the clone.
	sentinel := destPath + ".cancel"
	_ = os.Remove(sentinel)
	a.mu.Lock()
	a.cloneCancelPath = sentinel
	a.mu.Unlock()

	go func() {
		done := make(chan struct{})
		go a.pollCloneProgress(destPath, dev.Size, done)

		msg, err := runElevated(a.ctx, "FindIt needs your password to copy the drive. The drive is only read, never changed.",
			helper, "-device", deviceID, "-out", destPath, "-cancel", sentinel,
			"-uid", strconv.Itoa(os.Getuid()), "-gid", strconv.Itoa(os.Getgid()))
		close(done)
		_ = os.Remove(sentinel)
		a.mu.Lock()
		a.cloneCancelPath = ""
		a.mu.Unlock()

		if err == nil {
			if strings.HasSuffix(msg, "CANCELED") {
				rt.EventsEmit(a.ctx, "clone:canceled", nil)
				return
			}
			// The helper prints "OK <unreadable bytes>".
			var unreadable int64
			if f := strings.Fields(msg); len(f) == 2 && f[0] == "OK" {
				unreadable, _ = strconv.ParseInt(f[1], 10, 64)
			}
			rt.EventsEmit(a.ctx, "clone:done", map[string]any{"path": destPath, "unreadableBytes": unreadable})
			return
		}

		if isUserCanceled(msg) {
			msg = "Permission was not granted, so the drive wasn’t copied."
		}
		if strings.Contains(strings.ToLower(msg), "operation not permitted") || strings.Contains(strings.ToLower(msg), "permission denied") {
			msg = "macOS blocked access to the drive. Allow FindIt under System Settings → Privacy & Security → Full Disk Access, then try again."
		}
		if msg == "" {
			msg = err.Error()
		}
		rt.EventsEmit(a.ctx, "clone:error", msg)
	}()
	return nil
}

// CancelClone aborts an in-progress clone by touching the sentinel the helper
// watches; the helper deletes the partial image and exits.
func (a *App) CancelClone() error {
	a.mu.Lock()
	p := a.cloneCancelPath
	a.mu.Unlock()
	if p == "" {
		return nil
	}
	return os.WriteFile(p, []byte("cancel"), 0o644)
}

// pollCloneProgress emits progress by watching the growing image file until the
// clone finishes.
func (a *App) pollCloneProgress(destPath string, total int64, done <-chan struct{}) {
	ticker := time.NewTicker(config.Get().Clone.PollInterval())
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

// helperPath locates a bundled helper binary next to the app executable.
func helperPath(name string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("%s not found; reading a drive is only available in the packaged app (see scripts/build_dmg.sh)", name)
	}
	return p, nil
}

// elevatedScript runs item 2.. of argv as a command with administrator
// privileges. Every argument goes through AppleScript's `quoted form of`, so no
// path or name can change the command that runs as root.
const elevatedScript = `on run argv
	set cmd to quoted form of (item 2 of argv)
	repeat with i from 3 to count of argv
		set cmd to cmd & " " & quoted form of (item i of argv)
	end repeat
	do shell script cmd with prompt (item 1 of argv) with administrator privileges
end run`

// runElevated runs argv as root behind the native macOS password prompt and
// returns its combined, trimmed output.
func runElevated(ctx context.Context, prompt string, argv ...string) (string, error) {
	args := append([]string{"-e", elevatedScript, prompt}, argv...)
	out, err := exec.CommandContext(ctx, "/usr/bin/osascript", args...).CombinedOutput()
	msg := strings.TrimSpace(string(out))
	// Failures read "0:312: execution error: <helper stderr> (1)"; keep the middle.
	if i := strings.Index(msg, "execution error: "); err != nil && i >= 0 {
		msg = msg[i+len("execution error: "):]
		if j := strings.LastIndex(msg, " ("); j > 0 && strings.HasSuffix(msg, ")") {
			msg = msg[:j]
		}
	}
	return msg, err
}

// isUserCanceled reports whether osascript output means the password prompt
// was dismissed.
func isUserCanceled(msg string) bool {
	return strings.Contains(msg, "User canceled")
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

// RevealInFinder opens a folder in Finder. Only existing directories are
// accepted, so this can't be used to launch an app or document.
func (a *App) RevealInFinder(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("not an absolute path: %q", path)
	}
	if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
		return fmt.Errorf("not a folder: %q", path)
	}
	return exec.Command("/usr/bin/open", path).Start()
}

// OpenFullDiskAccessSettings opens System Settings at Privacy & Security →
// Full Disk Access.
func (a *App) OpenFullDiskAccessSettings() error {
	return exec.Command("/usr/bin/open", "x-apple.systempreferences:com.apple.preference.security?Privacy_AllFiles").Start()
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
//	scan:done     ScanDone
//	scan:canceled
//	scan:error    string
//
// partitionOffsets optionally restricts filesystem enumeration to the given
// partition byte offsets (empty means scan every detected partition).
func (a *App) StartScan(path string, filesystems []string, raw bool, rawExts []string, partitionOffsets []int64) error {
	src, err := recovery.OpenImage(path)
	if err != nil {
		return err
	}
	a.runScan(src, filesystems, raw, rawExts, partitionOffsets)
	return nil
}

// StartScanDevice scans a device directly. Reading a whole raw disk needs
// administrator privileges (Full Disk Access is not enough), so a short-lived
// privileged helper opens the device and hands its descriptor back — one
// password prompt, no clone. The source is only ever read.
func (a *App) StartScanDevice(deviceID string, filesystems []string, raw bool, rawExts []string, partitionOffsets []int64) error {
	a.mu.Lock()
	dev, ok := a.devices[deviceID]
	a.mu.Unlock()
	if !ok {
		return fmt.Errorf("device %q not found; list devices first", deviceID)
	}
	src, err := a.openDeviceElevated(dev)
	if err != nil {
		return err
	}
	a.runScan(src, filesystems, raw, rawExts, partitionOffsets)
	return nil
}

// openDeviceElevated opens a device read-only, first trying a direct open (works
// if the app itself runs as root) and otherwise launching the privileged helper,
// which passes back the device descriptor and then stays alive to run TSK as
// root for this session (so the filesystem tree works on a live drive too).
func (a *App) openDeviceElevated(dev device.Device) (storage.Source, error) {
	if src, err := recovery.OpenDevice(dev); err == nil {
		return src, nil
	}

	helper, err := helperPath("findit-devopen")
	if err != nil {
		return nil, err
	}
	// $TMPDIR is per-user and private on macOS, so only this user can bind here.
	sock := filepath.Join(os.TempDir(), fmt.Sprintf("findit-dev-%d.sock", time.Now().UnixNano()))
	ln, err := privdev.Listen(sock)
	if err != nil {
		return nil, err
	}

	// osascript only returns when the helper exits, i.e. at the end of the
	// session — or early, if the password prompt is dismissed or the helper fails.
	osaFailed := make(chan error, 1)
	go func() {
		msg, e := runElevated(a.ctx, "FindIt needs your password to read the drive. The drive is only read, never changed.",
			helper, "-device", dev.ID, "-socket", sock)
		switch {
		case isUserCanceled(msg):
			msg = "Permission was not granted, so the drive couldn’t be opened."
		case msg == "" && e != nil:
			msg = e.Error()
		case msg == "":
			msg = "the drive helper exited unexpectedly"
		}
		osaFailed <- errors.New(msg)
	}()

	type accepted struct {
		sess *privdev.Session
		f    *os.File
		err  error
	}
	got := make(chan accepted, 1)
	go func() {
		s, f, err := ln.Accept(dev.RawNode, 180*time.Second)
		got <- accepted{s, f, err}
	}()

	select {
	case r := <-got:
		if r.err != nil {
			ln.Close()
			return nil, fmt.Errorf("couldn’t open the drive for scanning: %v", r.err)
		}
		return device.NewHelperSource(r.f, dev, r.sess), nil
	case err := <-osaFailed:
		ln.Close() // unblocks Accept
		if r := <-got; r.err == nil {
			r.sess.Close()
			r.f.Close()
		}
		return nil, err
	}
}

func (a *App) runScan(src storage.Source, filesystems []string, raw bool, rawExts []string, partitionOffsets []int64) {
	a.mu.Lock()
	prev, prevSrc := a.scan, a.src
	a.scan = nil
	a.mu.Unlock()
	// Stop the previous scan before closing the source it is still reading.
	if prev != nil {
		prev.Cancel()
		_ = prev.Wait()
	}
	if prevSrc != nil {
		_ = prevSrc.Close()
	}

	req := model.ScanRequest{Raw: raw, RawExtensions: rawExts, PartitionOffsets: partitionOffsets}
	for _, f := range filesystems {
		req.Filesystems = append(req.Filesystems, model.FSType(f))
	}

	scan := recovery.Scan(a.ctx, src, req)
	a.mu.Lock()
	a.src = src
	a.diag = nil
	a.result = model.ScanResult{}
	a.sourcesByID = map[string]model.RecoverySource{}
	a.filesByID = map[string]fileRef{}
	a.scan = scan
	a.scanCanceled = false
	a.mu.Unlock()

	// emit drops events from a scan that has since been replaced, so a late
	// "canceled" from the old scan can't reset the UI during the new one.
	emit := func(name string, data any) {
		a.mu.Lock()
		current := a.scan == scan
		a.mu.Unlock()
		if current {
			rt.EventsEmit(a.ctx, name, data)
		}
	}
	go func() {
		for e := range scan.Events() {
			switch e.Kind {
			case jobs.KindProgress, jobs.KindLog, jobs.KindSourceStarted, jobs.KindSourceDone:
				emit("scan:progress", map[string]any{
					"phase": e.Phase, "message": e.Message, "done": e.Done, "total": e.Total,
				})
			}
		}
		if err := scan.Wait(); err != nil {
			a.mu.Lock()
			canceled := a.scanCanceled
			a.mu.Unlock()
			if canceled || errors.Is(err, context.Canceled) {
				emit("scan:canceled", nil)
			} else {
				emit("scan:error", err.Error())
			}
			return
		}
		if !a.index(scan) {
			return
		}
		a.mu.Lock()
		done := ScanDone{Diagnosis: a.diag, Result: a.result}
		a.mu.Unlock()
		emit("scan:done", done)
	}()
}

// CancelScan stops an in-progress scan; the scan goroutine emits scan:canceled.
func (a *App) CancelScan() {
	a.mu.Lock()
	s := a.scan
	a.scanCanceled = true
	a.mu.Unlock()
	if s != nil {
		s.Cancel()
	}
}

// index records a finished scan's results, unless the scan was superseded.
func (a *App) index(scan *session.Scan) bool {
	diag, res := scan.Diagnosis(), scan.Result()
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.scan != scan {
		return false
	}
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
	return true
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
	if src == nil || !okS || !okF || ref.sourceID != sourceID {
		return PreviewDTO{}, fmt.Errorf("preview: file not found")
	}
	// Wails runs each call on its own goroutine; cap concurrent decodes so a
	// grid full of large photos can't exhaust memory.
	select {
	case a.previewSlots <- struct{}{}:
		defer func() { <-a.previewSlots }()
	case <-a.ctx.Done():
		return PreviewDTO{}, a.ctx.Err()
	}
	p := recovery.Preview(a.ctx, src, source, ref.file, config.Get().Preview.MaxDimension)
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
	if !filepath.IsAbs(destDir) {
		return RecoverDTO{}, fmt.Errorf("choose a destination folder (a full path such as /Users/you/Recovered)")
	}
	a.mu.Lock()
	src := a.src
	source, okS := a.sourcesByID[sourceID]
	var files []model.RecoveredFile
	for _, id := range fileIDs {
		if ref, ok := a.filesByID[id]; ok && ref.sourceID == sourceID {
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
