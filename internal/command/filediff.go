package command

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/dconsole/dconsole/internal/alias"
	"github.com/dconsole/dconsole/internal/dlog"
	"github.com/dconsole/dconsole/internal/transport"
)

// FileEntry is one row from the remote `find` invocation: a path
// relative to the scan root plus the size + mtime used for the
// changed/unchanged decision.
type FileEntry struct {
	Path  string
	MTime int64 // unix seconds (GNU find's %T@ truncates here; sub-second precision is unnecessary for rsync semantics)
	Size  int64
}

// FileMap is path → entry, suitable for set-difference comparison.
type FileMap map[string]FileEntry

// scanRemote runs `find <absRoot> -type f -printf '%P\t%T@\t%s\n'` over
// the alias's transport, captures stdout, and parses it into a FileMap.
// Empty / missing directory returns an empty map (no error).
func scanRemote(ctx context.Context, t transport.Transport, absRoot string) (FileMap, error) {
	cmd := []string{"find", absRoot, "-type", "f", "-printf", "%P\t%T@\t%s\n"}
	dlog.Cmdf(t.Preview(cmd))
	var stdout bytes.Buffer
	if err := t.Pipe(ctx, cmd, nil, &stdout); err != nil {
		return nil, fmt.Errorf("scan %s: %w", absRoot, err)
	}
	return parseFindOutput(stdout.Bytes())
}

// scanRemoteConcurrent runs scanRemote on (sourceT, srcAbs) and
// (targetT, dstAbs) in parallel. Wall-time becomes max(src, dst)
// instead of sum.
func scanRemoteConcurrent(ctx context.Context,
	sourceT transport.Transport, srcAbs string,
	targetT transport.Transport, dstAbs string,
) (srcFiles, tgtFiles FileMap, err error) {

	var (
		wg          sync.WaitGroup
		srcErr      error
		tgtErr      error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		srcFiles, srcErr = scanRemote(ctx, sourceT, srcAbs)
	}()
	go func() {
		defer wg.Done()
		tgtFiles, tgtErr = scanRemote(ctx, targetT, dstAbs)
	}()
	wg.Wait()
	if srcErr != nil {
		return nil, nil, fmt.Errorf("source scan: %w", srcErr)
	}
	if tgtErr != nil {
		return nil, nil, fmt.Errorf("target scan: %w", tgtErr)
	}
	return srcFiles, tgtFiles, nil
}

// parseFindOutput consumes `find -printf '%P\t%T@\t%s\n'` output.
// Lines that don't have three tab-separated fields are skipped (and
// logged at -vv) — this tolerates an empty trailing line or the
// occasional stderr-merge accident.
func parseFindOutput(body []byte) (FileMap, error) {
	out := FileMap{}
	sc := bufio.NewScanner(bytes.NewReader(body))
	// Bump the buffer so absurdly-long paths don't get truncated.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) != 3 {
			dlog.Infof("filediff: skipping malformed find line %q", line)
			continue
		}
		// %T@ is "<seconds>.<fraction>"; take the integer part.
		mt, _ := strconv.ParseFloat(parts[1], 64)
		size, _ := strconv.ParseInt(parts[2], 10, 64)
		out[parts[0]] = FileEntry{
			Path:  parts[0],
			MTime: int64(mt),
			Size:  size,
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// diffSets compares source and target file maps:
//
//   - "changed" — files that need to flow source → target: new on
//     source AND files whose source size differs from target's OR
//     whose source mtime is newer than target's.
//   - "deletedOnTarget" — files only on target. Caller decides whether
//     to remove them (the --delete flag).
//
// Equal mtime + size → no transfer. Newer-on-target is left alone
// (matches rsync's default; user can `touch` on source to force).
func diffSets(source, target FileMap) (changed, deletedOnTarget []string) {
	for path, srcEntry := range source {
		tgtEntry, ok := target[path]
		if !ok {
			changed = append(changed, path)
			continue
		}
		if srcEntry.Size != tgtEntry.Size {
			changed = append(changed, path)
			continue
		}
		if srcEntry.MTime > tgtEntry.MTime {
			changed = append(changed, path)
			continue
		}
	}
	for path := range target {
		if _, ok := source[path]; !ok {
			deletedOnTarget = append(deletedOnTarget, path)
		}
	}
	return changed, deletedOnTarget
}

// streamChanged builds a gzipped tar on the source containing only
// the listed paths and unpacks it on the target — exactly mirroring
// what `rsync` would transfer in whole-file mode.
//
// The pipeline:
//
//	[file list bytes] →stdin→ source:`tar czf - -C <srcAbs> --files-from=-`
//	  → stdout (gzipped tarball) → io.Pipe → target's stdin
//	  → target:`tar xzf - -C <dstAbs>`
//
// Source and target tar invocations run via their respective
// transports concurrently, with an io.Pipe relaying bytes between
// them. dconsole stays the middle relay; the two ends never need
// direct connectivity.
func streamChanged(ctx context.Context,
	sourceT transport.Transport, srcAbs string,
	targetT transport.Transport, dstAbs string,
	changed []string,
) error {
	if len(changed) == 0 {
		return nil
	}
	// File list fed to source-side tar via stdin.
	fileList := strings.Join(changed, "\n") + "\n"

	srcCmd := []string{"tar", "czf", "-", "-C", srcAbs, "--files-from=-"}
	dstCmd := []string{"tar", "xzf", "-", "-C", dstAbs}
	dlog.Cmdf(append(append([]string(nil), sourceT.Preview(srcCmd)...), "|", "(relay)", "|"))
	dlog.Cmdf(targetT.Preview(dstCmd))

	pr, pw := io.Pipe()
	var srcErr error
	var srcWg sync.WaitGroup
	srcWg.Add(1)
	go func() {
		defer srcWg.Done()
		// Closing pw with an error propagates to the target reader so
		// it bails fast on source-side problems.
		err := sourceT.Pipe(ctx, srcCmd, strings.NewReader(fileList), pw)
		if err != nil {
			srcErr = err
			pw.CloseWithError(err)
		} else {
			pw.Close()
		}
	}()
	tgtErr := targetT.Pipe(ctx, dstCmd, pr, io.Discard)
	srcWg.Wait()
	if srcErr != nil {
		return fmt.Errorf("source tar: %w", srcErr)
	}
	if tgtErr != nil {
		return fmt.Errorf("target tar: %w", tgtErr)
	}
	return nil
}

// removeOnTarget deletes the given relative paths from dstAbs on the
// target. Used by --delete in diff mode. Implementation chunks paths
// into a single `xargs rm -f` over the transport's stdin so we don't
// spawn one rm per file. Empty paths are a no-op.
func removeOnTarget(ctx context.Context, targetT transport.Transport, dstAbs string, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	// Use null-separated paths to survive filenames with spaces /
	// newlines. xargs -0 reads paths, rm -f deletes them.
	var body bytes.Buffer
	for _, p := range paths {
		body.WriteString(dstAbs + "/" + p)
		body.WriteByte(0)
	}
	cmd := []string{"sh", "-c", `xargs -0 rm -f --`}
	dlog.Cmdf(targetT.Preview(cmd))
	return targetT.Pipe(ctx, cmd, &body, io.Discard)
}

// DiffSummary is a small struct callers can render in one line.
type DiffSummary struct {
	Changed         int
	DeletedOnTarget int
	UnchangedFiles  int
}

// Summarise is a tiny formatter so the orchestrator's logging stays
// consistent.
func Summarise(source FileMap, changed, deletedOnTarget []string) DiffSummary {
	return DiffSummary{
		Changed:         len(changed),
		DeletedOnTarget: len(deletedOnTarget),
		UnchangedFiles:  len(source) - len(changed),
	}
}

// expandPathspec returns the absolute path for a pathspec token
// (%files or %private) for the given alias. Used by both the diff
// mode and the rsync strategies. Wraps sitepath.Resolve + sitepath.Expand
// in one helper so callers don't duplicate that pattern.
func expandPathspec(a *alias.Alias, paths map[string]string, pathspec string) (string, error) {
	// paths is the resolved sitepath map for this alias (so callers
	// can cache the resolve cost). Caller is responsible for passing
	// in a populated map.
	key := strings.TrimPrefix(pathspec, "%")
	abs, ok := paths[key]
	if !ok || abs == "" {
		return "", fmt.Errorf("alias @%s.%s has no path for pathspec %s", a.Site, a.Env, pathspec)
	}
	return abs, nil
}
