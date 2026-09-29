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
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/manju4682/findit/internal/model"
	"github.com/manju4682/findit/internal/storage"
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

// Request is one TSK invocation, expressed structurally (never as a raw command
// line) so a privileged helper can validate it before running anything as root.
type Request struct {
	Op     string       `json:"op"`     // "fls" | "icat"
	Offset int64        `json:"offset"` // filesystem byte offset within the image
	FSType model.FSType `json:"fs"`
	Inode  string       `json:"inode,omitempty"` // icat only
}

// Executor runs a Request, streaming the tool's stdout to w.
type Executor interface {
	Exec(ctx context.Context, req Request, w io.Writer) error
}

// ExecutorProvider is implemented by sources whose bytes TSK cannot open
// directly (a live disk needs root), so TSK must run through the source's own
// executor — typically a privileged helper.
type ExecutorProvider interface {
	TSKExecutor() Executor
}

// inodeRe matches TSK metadata addresses: "123", "128-1", "128-128-4".
var inodeRe = regexp.MustCompile(`^[0-9]+(-[0-9]+){0,2}$`)

// Args validates req and returns the TSK tool and arguments that run it against
// image. Only fls/icat are allowed and every argument is derived from typed
// fields, so no caller-controlled string reaches the command line unchecked.
func Args(req Request, image string) (string, []string, error) {
	if req.Offset < 0 {
		return "", nil, fmt.Errorf("tsk: negative offset %d", req.Offset)
	}
	var base []string
	if req.Offset > 0 {
		base = append(base, "-o", strconv.FormatInt(req.Offset/512, 10)) // TSK -o is in sectors
	}
	if f := tskFS(req.FSType); f != "" {
		base = append(base, "-f", f)
	}
	switch req.Op {
	case "fls":
		return "fls", append(append([]string{"-r", "-p", "-l"}, base...), image), nil
	case "icat":
		if !inodeRe.MatchString(req.Inode) {
			return "", nil, fmt.Errorf("tsk: invalid inode %q", req.Inode)
		}
		return "icat", append(base, image, req.Inode), nil
	default:
		return "", nil, fmt.Errorf("tsk: unsupported op %q", req.Op)
	}
}

// LocalExecutor runs TSK tools in this process's own privilege context against
// an image path (a .bin file, or a device node when running as root).
type LocalExecutor struct{ Image string }

// Exec implements Executor.
func (l LocalExecutor) Exec(ctx context.Context, req Request, w io.Writer) error {
	tool, args, err := Args(req, l.Image)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, binPath(tool), args...)
	cmd.Stdout = w
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w: %s", tool, err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// Engine enumerates and extracts one filesystem within an image.
type Engine struct {
	exec       Executor
	offsetByte int64
	fsType     model.FSType
}

// New returns an engine bound to the filesystem at offsetByte within image.
func New(image string, offsetByte int64, fsType model.FSType) *Engine {
	return &Engine{exec: LocalExecutor{Image: image}, offsetByte: offsetByte, fsType: fsType}
}

// ForSource returns an engine for the filesystem at offsetByte in src, routing
// through the source's own executor when it provides one.
func ForSource(src storage.Source, offsetByte int64, fsType model.FSType) *Engine {
	if p, ok := src.(ExecutorProvider); ok {
		if ex := p.TSKExecutor(); ex != nil {
			return &Engine{exec: ex, offsetByte: offsetByte, fsType: fsType}
		}
	}
	return New(src.Name(), offsetByte, fsType)
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

// entry is one parsed line of `fls -rpl`.
type entry struct {
	inode   string
	path    string
	isDir   bool
	deleted bool
	size    int64
}

// Enumerate walks the filesystem into a RecoverySource whose Root is a directory
// tree. Virtual/system entries and volume labels are skipped. fls output is
// parsed as it streams, so a large filesystem's listing is never held whole.
func (e *Engine) Enumerate(ctx context.Context) (*model.RecoverySource, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	pr, pw := io.Pipe()
	defer pr.Close()
	go func() {
		pw.CloseWithError(e.exec.Exec(ctx, Request{Op: "fls", Offset: e.offsetByte, FSType: e.fsType}, pw))
	}()

	sourceID := fmt.Sprintf("fs-%s-%d", e.fsType, e.offsetByte)
	t := newTree()
	var fileCount, recoverable int

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		en, ok := parseLine(sc.Text())
		if !ok {
			continue
		}
		if en.isDir {
			t.dir(en.path)
			continue
		}
		rf := e.toFile(sourceID, en)
		t.addFile(en.path, rf)
		fileCount++
		if rf.Recoverable {
			recoverable++
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("tsk %w", err)
	}

	return &model.RecoverySource{
		ID:               sourceID,
		Kind:             model.SourceFilesystem,
		FSType:           e.fsType,
		Offset:           e.offsetByte,
		Root:             t.root,
		FileCount:        fileCount,
		RecoverableCount: recoverable,
	}, nil
}

// ExtractFile streams a file's bytes (by inode) to w using icat.
func (e *Engine) ExtractFile(ctx context.Context, inode string, w io.Writer) error {
	if err := e.exec.Exec(ctx, Request{Op: "icat", Offset: e.offsetByte, FSType: e.fsType, Inode: inode}, w); err != nil {
		return fmt.Errorf("tsk inode %s: %w", inode, err)
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
		ID:          sourceID + ":" + en.inode, // inodes repeat across filesystems
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

// tree builds the directory hierarchy, indexing directories by path so each
// insert costs O(1) instead of a scan of every sibling (folders like DCIM can
// hold tens of thousands of entries).
type tree struct {
	root *model.Node
	dirs map[string]*model.Node
}

func newTree() *tree {
	root := &model.Node{Name: "", IsDir: true}
	return &tree{root: root, dirs: map[string]*model.Node{"": root}}
}

// dir returns the directory node at p, creating it and any missing parents.
func (t *tree) dir(p string) *model.Node {
	p = cleanPath(p)
	if n, ok := t.dirs[p]; ok {
		return n
	}
	parent, name := splitLast(p)
	n := &model.Node{Name: name, IsDir: true}
	pn := t.dir(parent)
	pn.Children = append(pn.Children, n)
	t.dirs[p] = n
	return n
}

// addFile places rf as a file node at p, creating parent directories.
func (t *tree) addFile(p string, rf model.RecoveredFile) {
	p = cleanPath(p)
	if p == "" {
		return
	}
	parent, name := splitLast(p)
	pn := t.dir(parent)
	pn.Children = append(pn.Children, &model.Node{Name: name, File: &rf})
}

// splitLast splits "a/b/c" into ("a/b", "c").
func splitLast(p string) (string, string) {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[:i], p[i+1:]
	}
	return "", p
}

// cleanPath drops empty components ("/a//b/" → "a/b").
func cleanPath(p string) string {
	var comps []string
	for _, c := range strings.Split(p, "/") {
		if c != "" {
			comps = append(comps, c)
		}
	}
	return strings.Join(comps, "/")
}
