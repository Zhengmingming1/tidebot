// Package skillrepo reads remote skill directories from immutable Git snapshots.
package skillrepo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Zhengmingming1/tidebot/internal/netproxy"
	"github.com/Zhengmingming1/tidebot/internal/skillpkg"
)

const (
	MaxCacheBytes    = 256 << 20
	MaxTreeBytes     = 16 << 20
	MaxTreeEntries   = 100000
	MaxCandidates    = 2000
	OperationTimeout = 3 * time.Minute
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string        { return e.Message }
func fail(code, message string) error { return &Error{Code: code, Message: message} }

type Candidate struct {
	Path        string `json:"path"`
	Name        string `json:"name"`
	Description string `json:"description"`
	TreeHash    string `json:"tree_hash"`
	Files       int    `json:"files"`
	Size        int64  `json:"size"`
	Warning     string `json:"warning,omitempty"`
	Error       string `json:"error,omitempty"`
}
type Snapshot struct {
	Commit     string      `json:"commit"`
	Candidates []Candidate `json:"candidates"`
}

type Client struct {
	CacheDir string
	// runOverride lets package tests use real local Git fixtures without permitting local URLs in production.
	runOverride func(ctx context.Context, dir, remote string, limit int, stdin []byte, args ...string) ([]byte, error)
}

func New(cacheDir string) *Client { return &Client{CacheDir: cacheDir} }

var safeID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var sshUser = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)
var locks sync.Map

// ValidateURL accepts clone URLs only. Secrets are never persisted as part of a URL.
func ValidateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	bad := func() (string, error) {
		return "", fail("invalid_url", "use an HTTPS or SSH Git clone URL without credentials")
	}
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\x00\r\n\t \\") || strings.HasPrefix(raw, "-") {
		return bad()
	}
	if !strings.Contains(raw, "://") {
		// scp syntax has a relative server path; retain that distinction from ssh://.
		colon := strings.Index(raw, ":")
		if colon < 2 {
			return bad()
		}
		host, repo := raw[:colon], raw[colon+1:]
		if strings.ContainsAny(host, "/[]?#") || strings.ContainsAny(repo, ":?#") || repo == "" || strings.HasPrefix(repo, "-") {
			return bad()
		}
		switch strings.ToLower(host) {
		case "file", "ext", "http", "https", "git", "ftp":
			return bad()
		}
		if at := strings.LastIndex(host, "@"); at >= 0 {
			if !sshUser.MatchString(host[:at]) {
				return bad()
			}
			host = host[at+1:]
		}
		if !validHost(host) || !validRemotePath(repo) {
			return bad()
		}
		return strings.TrimRight(raw, "/"), nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "ssh") || !validHost(u.Hostname()) || u.RawQuery != "" || u.Fragment != "" || !validRemotePath(u.Path) {
		return bad()
	}
	if u.User != nil {
		if u.Scheme != "ssh" || !sshUser.MatchString(u.User.Username()) {
			return bad()
		}
		if _, set := u.User.Password(); set {
			return bad()
		}
	}
	if u.Port() != "" {
		p, err := strconv.Atoi(u.Port())
		if err != nil || p < 1 || p > 65535 {
			return bad()
		}
	}
	u.Host = strings.ToLower(u.Host)
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""
	return u.String(), nil
}
func validHost(s string) bool {
	return s != "" && !strings.HasPrefix(s, "-") && !strings.ContainsAny(s, " /\\\x00\r\n\t@?#%`$;|&\"'")
}
func validRemotePath(s string) bool {
	if s == "" || s == "/" || strings.ContainsAny(s, "\x00\r\n\\") {
		return false
	}
	for _, seg := range strings.Split(s, "/") {
		if seg == "." || seg == ".." {
			return false
		}
	}
	return true
}
func ValidateRef(ref string) error {
	if len(ref) > 256 || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\r\n\x00:~^?*[\\") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") || strings.HasSuffix(ref, "/") || strings.HasPrefix(ref, "/") || strings.Contains(ref, "//") {
		return fail("invalid_ref", "invalid branch or tag")
	}
	return nil
}
func ValidateRoot(root string) (string, error) {
	if root == "" || root == "." {
		return ".", nil
	}
	p, err := skillpkg.CleanRel(root)
	if err != nil || p != root || strings.Contains(root, "\\") || strings.ContainsAny(root, "\r\n\x00") {
		return "", fail("invalid_path", "invalid repository directory")
	}
	return p, nil
}
func (c *Client) directory(id string) (string, error) {
	if !safeID.MatchString(id) {
		return "", fail("invalid_id", "invalid repository ID")
	}
	return filepath.Join(c.CacheDir, id), nil
}
func acquire(ctx context.Context, dir string) (func(), error) {
	key, _ := filepath.Abs(dir)
	v, _ := locks.LoadOrStore(key, make(chan struct{}, 1))
	ch := v.(chan struct{})
	select {
	case ch <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Keep the lock inode outside the deletable cache. Kernel locks are released
	// after a crash and also coordinate different Tidebot processes sharing DataDir.
	lockDir := filepath.Join(filepath.Dir(key), ".locks")
	if err := os.MkdirAll(lockDir, 0700); err != nil {
		<-ch
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(lockDir, filepath.Base(key)+".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		<-ch
		return nil, err
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		locked, err := tryFileLock(f)
		if err != nil {
			f.Close()
			<-ch
			return nil, err
		}
		if locked {
			return func() { unlockFile(f); f.Close(); <-ch }, nil
		}
		select {
		case <-ctx.Done():
			f.Close()
			<-ch
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// Scan retains its commit under a private ref so later refreshes cannot invalidate a preview.
func (c *Client) Scan(ctx context.Context, id, raw, ref, root string) (Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	u, err := ValidateURL(raw)
	if err != nil {
		return Snapshot{}, err
	}
	if err = ValidateRef(ref); err != nil {
		return Snapshot{}, err
	}
	root, err = ValidateRoot(root)
	if err != nil {
		return Snapshot{}, err
	}
	dir, err := c.directory(id)
	if err != nil {
		return Snapshot{}, err
	}
	unlock, err := acquire(ctx, dir)
	if err != nil {
		return Snapshot{}, err
	}
	defer unlock()
	if err = os.MkdirAll(dir, 0700); err != nil {
		return Snapshot{}, err
	}
	if _, err = os.Stat(filepath.Join(dir, "HEAD")); os.IsNotExist(err) {
		if _, err = c.run(ctx, dir, u, 4096, nil, "init", "--bare", "--template=", dir); err != nil {
			return Snapshot{}, err
		}
	} else if err != nil {
		return Snapshot{}, err
	}
	if ref == "" {
		ref = "HEAD"
	}
	if _, err = c.run(ctx, dir, u, 4096, nil, "fetch", "--depth=1", "--no-tags", "--no-recurse-submodules", "--", u, ref); err != nil {
		return Snapshot{}, err
	}
	b, err := c.run(ctx, dir, u, 256, nil, "rev-parse", "--verify", "FETCH_HEAD^{commit}")
	if err != nil {
		return Snapshot{}, err
	}
	commit := strings.TrimSpace(string(b))
	if !objectID.MatchString(commit) {
		return Snapshot{}, fail("invalid_snapshot", "invalid Git commit")
	}
	entries, err := c.entries(ctx, dir, commit)
	if err != nil {
		return Snapshot{}, err
	}
	if root != "." {
		found := false
		for _, e := range entries {
			if e.Path == root && e.Kind == "tree" {
				found = true
				break
			}
		}
		if !found {
			return Snapshot{}, fail("path_not_found", "repository directory not found")
		}
	}
	candidates := []Candidate{}
	// SKILL.md files are read together after the tree walk: one Git process per
	// batch instead of one per candidate.
	var manifests []entry
	var owners []int
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		if path.Base(e.Path) != "SKILL.md" || !within(e.Path, root) {
			continue
		}
		if len(candidates) >= MaxCandidates {
			return Snapshot{}, fail("scan_limit", "too many skill candidates")
		}
		candidate := summarize(entries, path.Dir(e.Path))
		if candidate.Error == "" {
			manifests = append(manifests, e)
			owners = append(owners, len(candidates))
		}
		candidates = append(candidates, candidate)
	}
	err = c.readBlobs(ctx, dir, manifests, func(i int, b []byte) error {
		candidate := &candidates[owners[i]]
		if isLFS(b) {
			candidate.Error = "lfs_unsupported"
			return nil
		}
		meta, parseErr := skillpkg.ParseFrontmatter(b)
		if parseErr != nil {
			candidate.Error = "invalid_frontmatter"
			return nil
		}
		candidate.Name = meta.Name
		candidate.Description = meta.Description
		if bytes.Contains(b, []byte("../")) {
			candidate.Warning = "external_resources"
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	for i := range candidates {
		if candidates[i].Name == "" {
			d := candidates[i].Path
			candidates[i].Name = path.Base(d)
			if d == "." {
				candidates[i].Name = "skill"
			}
		}
	}
	for i := range candidates {
		for j := range candidates {
			if i != j && within(candidates[j].Path, candidates[i].Path) {
				candidates[i].Warning = "nested_skills"
				break
			}
		}
	}
	if _, err = c.run(ctx, dir, "", 256, nil, "update-ref", "refs/tidebot/snapshots/"+commit, commit); err != nil {
		return Snapshot{}, err
	}
	SortCandidates(candidates)
	return Snapshot{Commit: commit, Candidates: candidates}, nil
}

type entry struct {
	Path, Mode, Kind, OID string
	Size                  int64
}

func (c *Client) entries(ctx context.Context, dir, commit string) ([]entry, error) {
	b, err := c.run(ctx, dir, "", MaxTreeBytes, nil, "ls-tree", "-r", "-t", "-l", "-z", "--full-tree", commit)
	if err != nil {
		return nil, err
	}
	parts := bytes.Split(b, []byte{0})
	out := make([]entry, 0, len(parts))
	for _, line := range parts {
		if len(line) == 0 {
			continue
		}
		if len(out) >= MaxTreeEntries {
			return nil, fail("scan_limit", "repository tree exceeds scan limit")
		}
		meta, p, ok := bytes.Cut(line, []byte{'\t'})
		fields := strings.Fields(string(meta))
		if !ok || len(fields) != 4 || !objectID.MatchString(fields[2]) {
			return nil, fail("invalid_snapshot", "invalid Git tree")
		}
		size := int64(0)
		if fields[3] != "-" {
			size, err = strconv.ParseInt(fields[3], 10, 64)
			if err != nil || size < 0 {
				return nil, fail("invalid_snapshot", "invalid Git file size")
			}
		}
		out = append(out, entry{Path: string(p), Mode: fields[0], Kind: fields[1], OID: fields[2], Size: size})
	}
	return out, nil
}
func within(p, root string) bool { return root == "." || p == root || strings.HasPrefix(p, root+"/") }
func relative(p, root string) string {
	if root == "." {
		return p
	}
	return strings.TrimPrefix(p, root+"/")
}
func summarize(entries []entry, root string) Candidate {
	c := Candidate{Path: root}
	h := sha256.New()
	folded := map[string]bool{}
	for _, e := range entries {
		if !within(e.Path, root) || e.Kind == "tree" {
			continue
		}
		p := relative(e.Path, root)
		c.Files++
		c.Size += e.Size
		// Skill versions currently preserve bytes, not executable mode.
		fmt.Fprintf(h, "%s\x00%s\x00", p, e.OID)
		if e.Kind != "blob" || (e.Mode != "100644" && e.Mode != "100755") {
			c.Error = "unsupported_file"
		}
		clean, err := skillpkg.CleanRel(p)
		if err != nil || clean != p || strings.ContainsAny(p, "\\\r\n") {
			c.Error = "unsafe_path"
		}
		for _, seg := range strings.Split(p, "/") {
			if strings.EqualFold(seg, ".git") {
				c.Error = "unsafe_path"
			}
		}
		lower := strings.ToLower(p)
		if folded[lower] {
			c.Error = "path_collision"
		}
		folded[lower] = true
		if e.Size > skillpkg.MaxFileBytes || c.Size > skillpkg.MaxTotalBytes || c.Files > skillpkg.MaxEntries {
			c.Error = "skill_size_limit"
		}
	}
	c.TreeHash = hex.EncodeToString(h.Sum(nil))
	return c
}
func isLFS(b []byte) bool {
	return bytes.HasPrefix(b, []byte("version https://git-lfs.github.com/spec/v1\n"))
}

// blobBatchBytes bounds how much file content one `git cat-file --batch` call
// buffers, so a repository with many large SKILL.md files cannot pin memory.
const blobBatchBytes = 8 << 20

// readBlobs streams the entries through `git cat-file --batch`, one process per
// blobBatchBytes of content rather than one per file, and hands each file to
// each in order. Process start-up dominated scans and installs before.
func (c *Client) readBlobs(ctx context.Context, dir string, list []entry, each func(int, []byte) error) error {
	for start := 0; start < len(list); {
		end, total := start, int64(0)
		var in bytes.Buffer
		for ; end < len(list); end++ {
			e := list[end]
			if e.Kind != "blob" || (e.Mode != "100644" && e.Mode != "100755") {
				return fail("unsupported_file", "symlinks and submodules are not supported")
			}
			if e.Size > skillpkg.MaxFileBytes {
				return fail("skill_size_limit", "skill file exceeds size limit")
			}
			if end > start && total+e.Size > blobBatchBytes {
				break
			}
			total += e.Size
			in.WriteString(e.OID)
			in.WriteByte('\n')
		}
		// Each object is "<oid> blob <size>\n<content>\n".
		out, err := c.run(ctx, dir, "", int(total)+(end-start)*96, in.Bytes(), "cat-file", "--batch")
		if err != nil {
			return err
		}
		for i := start; i < end; i++ {
			e := list[i]
			nl := bytes.IndexByte(out, '\n')
			if nl < 0 {
				return fail("invalid_snapshot", "invalid Git object")
			}
			fields := strings.Fields(string(out[:nl]))
			if len(fields) != 3 || fields[0] != e.OID || fields[1] != "blob" {
				return fail("invalid_snapshot", "invalid Git object")
			}
			if size, perr := strconv.ParseInt(fields[2], 10, 64); perr != nil || size != e.Size {
				return fail("invalid_snapshot", "Git file size mismatch")
			}
			out = out[nl+1:]
			if int64(len(out)) < e.Size+1 || out[e.Size] != '\n' {
				return fail("invalid_snapshot", "Git file size mismatch")
			}
			if err := each(i, out[:e.Size:e.Size]); err != nil {
				return err
			}
			out = out[e.Size+1:]
		}
		if len(out) != 0 {
			return fail("invalid_snapshot", "invalid Git object")
		}
		start = end
	}
	return nil
}

// ReadSkill accepts only a previously scanned immutable commit, never a moving branch.
// ReadSnapshot reads an inert, bounded directory at an already pinned commit.
// It retains the same path, symlink, submodule and LFS checks as skill imports.
func (c *Client) ReadSnapshot(ctx context.Context, id, commit, root string) (map[string][]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, OperationTimeout)
	defer cancel()
	if !objectID.MatchString(commit) {
		return nil, fail("invalid_snapshot", "invalid snapshot commit")
	}
	root, err := ValidateRoot(root)
	if err != nil {
		return nil, err
	}
	dir, err := c.directory(id)
	if err != nil {
		return nil, err
	}
	unlock, err := acquire(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer unlock()
	if _, err = c.run(ctx, dir, "", 256, nil, "rev-parse", "--verify", "refs/tidebot/snapshots/"+commit); err != nil {
		return nil, fail("snapshot_missing", "snapshot unavailable; refresh the repository")
	}
	entries, err := c.entries(ctx, dir, commit)
	if err != nil {
		return nil, err
	}
	summary := summarize(entries, root)
	if summary.Error != "" {
		return nil, fail(summary.Error, "skill contains unsupported paths, files, or exceeds size limits")
	}
	var list []entry
	for _, e := range entries {
		if within(e.Path, root) && e.Kind != "tree" {
			list = append(list, e)
		}
	}
	files := make(map[string][]byte, len(list))
	err = c.readBlobs(ctx, dir, list, func(i int, b []byte) error {
		if isLFS(b) {
			return fail("lfs_unsupported", "Git LFS content must be included as ordinary files")
		}
		files[relative(list[i].Path, root)] = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (c *Client) ReadSkill(ctx context.Context, id, commit, root string) (map[string][]byte, error) {
	files, err := c.ReadSnapshot(ctx, id, commit, root)
	if err != nil {
		return nil, err
	}

	if _, ok := files["SKILL.md"]; !ok {
		return nil, fail("skill_missing", "directory does not contain SKILL.md")
	}
	if _, err := skillpkg.ParseFrontmatter(files["SKILL.md"]); err != nil {
		return nil, fail("invalid_frontmatter", "SKILL.md frontmatter is invalid")
	}
	return files, nil
}
func (c *Client) RemoveCache(ctx context.Context, id string) error {
	dir, err := c.directory(id)
	if err != nil {
		return err
	}
	unlock, err := acquire(ctx, dir)
	if err != nil {
		return err
	}
	defer unlock()
	return os.RemoveAll(dir)
}

type boundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	exceeded bool
	cancel   context.CancelFunc
}

func (w *boundedOutput) Len() int       { return w.buffer.Len() }
func (w *boundedOutput) Bytes() []byte  { return w.buffer.Bytes() }
func (w *boundedOutput) String() string { return w.buffer.String() }

func (w *boundedOutput) Write(p []byte) (int, error) {
	if w.Len()+len(p) > w.limit {
		w.exceeded = true
		w.cancel()
		return 0, errors.New("Git output exceeds limit")
	}
	return w.buffer.Write(p)
}
func cacheSize(dir string) (int64, error) {
	var size int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			info, err := d.Info()
			if err != nil {
				return err
			}
			size += info.Size()
			if size > MaxCacheBytes {
				return fail("cache_limit", "repository cache exceeds 256 MiB")
			}
		}
		return nil
	})
	return size, err
}
func (c *Client) run(ctx context.Context, dir, remote string, limit int, stdin []byte, args ...string) ([]byte, error) {
	if c.runOverride != nil {
		return c.runOverride(ctx, dir, remote, limit, stdin, args...)
	}
	return runGit(ctx, dir, remote, limit, stdin, args...)
}

// writesCache reports Git commands that can grow the cache. Reads (ls-tree,
// cat-file, rev-parse) skip the cache walk, which used to run after every file.
func writesCache(args []string) bool {
	return len(args) > 0 && (args[0] == "init" || args[0] == "fetch" || args[0] == "update-ref")
}
func runGit(ctx context.Context, dir, remote string, limit int, stdin []byte, args ...string) ([]byte, error) {
	bin, err := exec.LookPath("git")
	if err != nil {
		return nil, fail("git_missing", "Git is not installed on the Tidebot host")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	flags := []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always", "-c", "http.sslVerify=true", "-c", "core.hooksPath=" + filepath.Join(dir, "disabled-hooks"), "-c", "gc.auto=0", "-c", "maintenance.auto=false", "-c", "submodule.recurse=false"}
	if strings.HasPrefix(remote, "https://") {
		proxy := ""
		if p := netproxy.ProxyFor(remote); p != nil {
			proxy = p.String()
		}
		flags = append(flags, "-c", "http.proxy="+proxy)
	}
	cmd := exec.CommandContext(ctx, bin, append(flags, args...)...)
	cmd.Dir = dir
	cmd.Env = gitEnv()
	cmd.WaitDelay = 2 * time.Second
	setupProcess(cmd)
	stdout := &boundedOutput{limit: limit, cancel: cancel}
	stderr := &boundedOutput{limit: 64 << 10, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	writes := writesCache(args)
	if err = cmd.Start(); err != nil {
		return nil, fail("git_failed", "could not start Git")
	}
	cleanup := afterStart(cmd)
	defer cleanup()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case err = <-done:
			if stdout.exceeded || stderr.exceeded {
				return nil, fail("scan_limit", "Git output exceeds limit")
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err != nil {
				return nil, classify(stderr.String())
			}
			if writes {
				if _, err = cacheSize(dir); err != nil {
					return nil, err
				}
			}
			return stdout.Bytes(), nil
		case <-ticker.C:
			if !writes {
				continue
			}
			if _, sizeErr := cacheSize(dir); sizeErr != nil {
				cancel()
				<-done
				return nil, sizeErr
			}
		}
	}
}
func gitEnv() []string {
	env := []string{}
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		upper := strings.ToUpper(key)
		// Enterprise CA configuration is safe to retain while verification stays mandatory.
		if upper == "GIT_SSL_CAINFO" || upper == "GIT_SSL_CAPATH" {
			env = append(env, value)
			continue
		}
		if strings.HasPrefix(upper, "GIT_") || upper == "SSH_ASKPASS" || upper == "SSH_ASKPASS_REQUIRE" || upper == "GCM_INTERACTIVE" || upper == "HTTP_PROXY" || upper == "HTTPS_PROXY" || upper == "ALL_PROXY" || upper == "NO_PROXY" {
			continue
		}
		env = append(env, value)
	}
	env = append(env, netproxy.Env()...)
	return append(env, "GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS=", "GIT_SSL_NO_VERIFY=false", "SSH_ASKPASS_REQUIRE=never", "GCM_INTERACTIVE=never", "GIT_SSH_COMMAND=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes -o ConnectTimeout=20", "LC_ALL=C")
}
func classify(stderr string) error {
	s := strings.ToLower(stderr)
	switch {
	case strings.Contains(s, "host key verification failed"), strings.Contains(s, "remote host identification has changed"):
		return fail("ssh_host_key", "SSH host identity is not trusted; configure known_hosts on the Tidebot host")
	case strings.Contains(s, "certificate"), strings.Contains(s, "ssl peer"):
		return fail("certificate", "Git server certificate could not be verified")
	case strings.Contains(s, "authentication"), strings.Contains(s, "permission denied"), strings.Contains(s, "could not read username"), strings.Contains(s, "terminal prompts disabled"):
		return fail("authentication", "configure Git credentials or SSH agent for the Tidebot host account")
	case strings.Contains(s, "couldn't find remote ref"), strings.Contains(s, "invalid refspec"):
		return fail("ref_not_found", "branch or tag was not found")
	case strings.Contains(s, "could not resolve"), strings.Contains(s, "failed to connect"), strings.Contains(s, "connection timed out"), strings.Contains(s, "connection refused"):
		return fail("network", "Git server is unreachable from the Tidebot host")
	default:
		return fail("git_failed", "Git operation failed; check repository address, access and server availability")
	}
}

// SortCandidates provides a stable display order independent of Git traversal order.
func SortCandidates(candidates []Candidate) {
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Path < candidates[j].Path })
}
