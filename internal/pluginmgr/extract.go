package pluginmgr

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractTarballTo extracts a tar.gz at src into dstDir. Returns the
// path to the installed plugin binary (the first executable file named
// dconsole-*).
//
// Supports flat archives (one binary at the root) and shallow nested
// archives (binary in a single top-level directory). Larger layouts
// require explicit handling.
func extractTarballTo(src, dstDir string) (string, error) {
	f, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("not a gzip archive: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)

	var installedBin string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("tar: %w", err)
		}
		// Strip a single leading directory component if present so a
		// tarball with a "foo-1.0/" prefix still lands flat.
		name := stripFirstDir(hdr.Name)
		if name == "" {
			continue
		}
		// Refuse any entry that would escape dstDir.
		clean := filepath.Clean(name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
			return "", fmt.Errorf("tar entry escapes destination: %s", hdr.Name)
		}
		dst := filepath.Join(dstDir, clean)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return "", err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return "", err
			}
			out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)|0o600)
			if err != nil {
				return "", err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return "", err
			}
			out.Close()
			if err := os.Chmod(dst, os.FileMode(hdr.Mode)|0o111); err != nil {
				return "", err
			}
			if installedBin == "" && strings.HasPrefix(filepath.Base(dst), "dconsole-") {
				installedBin = dst
			}
		}
	}
	if installedBin == "" {
		return "", fmt.Errorf("archive contained no dconsole-* binary")
	}
	return installedBin, nil
}

// stripFirstDir removes a single leading directory if the path has one.
// Used so plugin tarballs can ship as either "dconsole-foo" or
// "foo-1.0/dconsole-foo" and land in the same place.
func stripFirstDir(name string) string {
	name = filepath.ToSlash(name)
	if i := strings.Index(name, "/"); i >= 0 {
		// If the leading dir IS the binary, keep it.
		// E.g. "dconsole-foo/README.md" → "README.md".
		// We strip uniformly; user expects a tarball whose top-level
		// dir is wrapping content.
		return name[i+1:]
	}
	return name
}
