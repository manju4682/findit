// Package tsk adapts The Sleuth Kit (fls/icat) as a subprocess: it enumerates a
// filesystem at a given offset into a directory tree and extracts files by
// inode, covering NTFS, FAT, and exFAT without linking any GPL code.
package tsk

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/findit/findit/internal/model"
)

var (
	engineDirOnce sync.Once
	engineBinDir  string
)

// bundledBinDir returns the app's vendored engine directory
// (findit.app/Contents/Resources/engines/bin), or "" when not bundled (dev).
func bundledBinDir() string {
	engineDirOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		d := filepath.Join(filepath.Dir(exe), "..", "Resources", "engines", "bin")
		if fi, err := os.Stat(d); err == nil && fi.IsDir() {
			engineBinDir = d
		}
	})
	return engineBinDir
}

// binPath resolves a TSK tool, preferring the bundled copy, then PATH. It
// returns the bare name (unresolved) if neither is found.
func binPath(name string) string {
	if d := bundledBinDir(); d != "" {
		if p := filepath.Join(d, name); fileExists(p) {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Available reports whether the required Sleuth Kit binaries can be found
// (bundled with the app or on PATH).
func Available() bool {
	return binPath("fls") != "fls" && binPath("icat") != "icat"
}

// Engine enumerates and extracts one filesystem within an image.
type Engine struct {
	image      string
	offsetByte int64
	fsType     model.FSType
}

// New returns an engine bound to the filesystem at offsetByte within image.
func New(image string, offsetByte int64, fsType model.FSType) *Engine {
	return &Engine{image: image, offsetByte: offsetByte, fsType: fsType}
}

// tskFS maps a FindIt filesystem type to TSK's -f value. Empty means autodetect.
func tskFS(t model.FSType) string {
	switch t {
	case model.FSNTFS:
		return "ntfs"
	case model.FSFAT32:
		return "fat"
	case model.FSExFAT:
		return "exfat"
	default:
		return ""
	}
}

// baseArgs returns the common -o/-f flags shared by fls and icat.
func (e *Engine) baseArgs() []string {
	var args []string
	if e.offsetByte > 0 {
		args = append(args, "-o", strconv.FormatInt(e.offsetByte/512, 10)) // TSK -o is in sectors
	}
	if f := tskFS(e.fsType); f != "" {
		args = append(args, "-f", f)
	}
	return args
}

// entry is one parsed line of `fls -rpl`.
type entry struct {
	inode   string
	path    string
	isDir   bool
	deleted bool
	size    int64
}

// Enumerate walks the filesystem into a RecoverySource whose Root is a directory
// tree. Virtual/system entries and volume labels are skipped.
func (e *Engine) Enumerate(ctx context.Context) (*model.RecoverySource, error) {
	args := append([]string{"-r", "-p", "-l"}, e.baseArgs()...)
	args = append(args, e.image)
	out, err := run(ctx, "fls", args...)
	if err != nil {
		return nil, fmt.Errorf("tsk fls: %w", err)
	}

	sourceID := fmt.Sprintf("fs-%s-%d", e.fsType, e.offsetByte)
	root := &model.Node{Name: "", IsDir: true}
	var fileCount, recoverable int

	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		en, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if en.isDir {
			ensureDir(root, en.path)
			continue
		}
		rf := e.toFile(sourceID, en)
		insertFile(root, en.path, rf)
		fileCount++
		if rf.Recoverable {
			recoverable++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	return &model.RecoverySource{
		ID:               sourceID,
		Kind:             model.SourceFilesystem,
		FSType:           e.fsType,
		Offset:           e.offsetByte,
		Root:             root,
		FileCount:        fileCount,
		RecoverableCount: recoverable,
	}, nil
}

// ExtractFile streams a file's bytes (by inode) to w using icat.
func (e *Engine) ExtractFile(ctx context.Context, inode string, w io.Writer) error {
	args := append(e.baseArgs(), e.image, inode)
	cmd := exec.CommandContext(ctx, binPath("icat"), args...)
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tsk icat inode %s: %w: %s", inode, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (e *Engine) toFile(sourceID string, en entry) model.RecoveredFile {
	name := path.Base(en.path)
	dir := path.Dir(en.path)
	if dir == "." {
		dir = ""
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))

	rf := model.RecoveredFile{
		ID:          "tsk-" + en.inode,
		SourceID:    sourceID,
		Name:        name,
		Path:        dir,
		Ext:         ext,
		Size:        en.size,
		Deleted:     en.deleted,
		Ref:         en.inode,
		Recoverable: !en.deleted,
		Previewable: isPreviewable(ext),
	}
	if en.deleted {
		// A deleted file's directory entry survives, but its content may not.
		rf.Assessment = model.RecoveryAssessment{Metadata: 100, Allocation: 0, Status: model.StatusMetadataOnly}
	} else {
		rf.Assessment = model.RecoveryAssessment{Metadata: 100, Allocation: 100, Content: 100, Physical: 100, Status: model.StatusGood}
	}
	return rf
}

func isPreviewable(ext string) bool {
	switch ext {
	case "jpg", "jpeg", "png", "gif", "mp4", "mov", "m4v":
		return true
	}
	return false
}

// parseLine parses one `fls -rpl` line:
//
//	"r/r 54:\tDCIM/IMG_0001.jpg\t<mtime>\t...\t<size>\t<uid>\t<gid>"
//	"r/r * 58:\tDCIM/_MG_0004.jpg\t..."  (deleted)
//	"d/d 6:\tDCIM\t..."                  (directory)
//
// It returns ok=false for virtual entries ($MBR, $OrphanFiles, …) and volume
// labels.
func parseLine(line string) (entry, bool) {
	cols := strings.Split(line, "\t")
	if len(cols) < 2 {
		return entry{}, false
	}
	metaFields := strings.Fields(cols[0])
	if len(metaFields) < 2 {
		return entry{}, false
	}
	typeCode := metaFields[0] // e.g. "r/r", "d/d", "v/v"
	if strings.HasPrefix(typeCode, "v") || strings.HasPrefix(typeCode, "V") {
		return entry{}, false // virtual/system
	}
	deleted := false
	for _, f := range metaFields[1:] {
		if f == "*" {
			deleted = true
		}
	}
	inode := strings.TrimSuffix(metaFields[len(metaFields)-1], ":")

	p := strings.TrimSpace(cols[1])
	if p == "" || strings.HasPrefix(path.Base(p), "$") || strings.Contains(cols[1], "(Volume Label Entry)") {
		return entry{}, false
	}

	var size int64
	if len(cols) >= 4 { // size sits before the trailing uid/gid columns
		if v, err := strconv.ParseInt(strings.TrimSpace(cols[len(cols)-3]), 10, 64); err == nil {
			size = v
		}
	}

	return entry{
		inode:   inode,
		path:    p,
		isDir:   strings.HasPrefix(typeCode, "d"),
		deleted: deleted,
		size:    size,
	}, true
}

// ensureDir creates (or finds) the directory node at p and returns it.
func ensureDir(root *model.Node, p string) *model.Node {
	node := root
	for _, comp := range splitPath(p) {
		node = childDir(node, comp)
	}
	return node
}

// insertFile places rf as a file node at p, creating parent directories.
func insertFile(root *model.Node, p string, rf model.RecoveredFile) {
	comps := splitPath(p)
	if len(comps) == 0 {
		return
	}
	parent := root
	for _, comp := range comps[:len(comps)-1] {
		parent = childDir(parent, comp)
	}
	f := rf
	parent.Children = append(parent.Children, &model.Node{Name: comps[len(comps)-1], IsDir: false, File: &f})
}

// childDir returns the child directory named name under parent, creating it if
// absent.
func childDir(parent *model.Node, name string) *model.Node {
	for _, c := range parent.Children {
		if c.IsDir && c.Name == name {
			return c
		}
	}
	child := &model.Node{Name: name, IsDir: true}
	parent.Children = append(parent.Children, child)
	return child
}

func splitPath(p string) []string {
	var out []string
	for _, comp := range strings.Split(p, "/") {
		if comp != "" {
			out = append(out, comp)
		}
	}
	return out
}

// run executes a TSK command and returns stdout.
func run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binPath(bin), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}
