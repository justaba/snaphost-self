// Package unpack extracts untrusted user-uploaded tar.gz archives into a
// build workdir (Task 14b-2). The archive is attacker-controlled input,
// so extraction is deliberately restrictive:
//
//   - only regular files and directories are materialized;
//   - symlinks and hardlinks are rejected (they enable path escapes and
//     reading host files into the build context);
//   - device/fifo/socket and other special entries are skipped;
//   - absolute paths, `..` traversal, and anything resolving outside the
//     destination are rejected;
//   - per-file size, total uncompressed size, file count, and overall
//     decompression ratio are capped (zip-bomb defence);
//   - setuid/setgid/sticky bits are stripped from file modes.
package unpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Limits bounds extraction of an untrusted archive. Zero values are
// replaced by the package defaults.
type Limits struct {
	// MaxFiles caps the number of materialized entries (files + dirs).
	MaxFiles int
	// MaxFileBytes caps a single file's uncompressed size.
	MaxFileBytes int64
	// MaxTotalBytes caps the sum of all uncompressed file sizes.
	MaxTotalBytes int64
	// MaxRatio caps total uncompressed bytes relative to the compressed
	// archive size (zip-bomb defence).
	MaxRatio int64
}

// Defaults applied when a Limits field is zero.
const (
	DefaultMaxFiles      = 5000
	DefaultMaxFileBytes  = 50 * 1024 * 1024
	DefaultMaxTotalBytes = 200 * 1024 * 1024
	DefaultMaxRatio      = 100
)

func (l Limits) withDefaults() Limits {
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultMaxFiles
	}
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultMaxTotalBytes
	}
	if l.MaxRatio <= 0 {
		l.MaxRatio = DefaultMaxRatio
	}
	return l
}

// Result summarizes a successful extraction.
type Result struct {
	Files      int
	TotalBytes int64
}

// TarGz extracts a gzip-compressed tar archive into destDir, enforcing
// the limits. destDir must exist and be empty-ish (the build workdir).
// Any violation aborts extraction with an error; the caller is expected
// to discard the workdir on failure.
func TarGz(archive []byte, destDir string, limits Limits) (*Result, error) {
	limits = limits.withDefaults()

	absDest, err := filepath.Abs(destDir)
	if err != nil {
		return nil, fmt.Errorf("resolve dest dir: %w", err)
	}

	// Effective total cap: the configured ceiling or the ratio-derived
	// one, whichever is smaller.
	maxTotal := limits.MaxTotalBytes
	if ratioCap := int64(len(archive)) * limits.MaxRatio; ratioCap > 0 && ratioCap < maxTotal {
		maxTotal = ratioCap
	}

	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf("archive is not valid gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	res := &Result{}

	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("archive is not a valid tar stream: %w", err)
		}

		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeDir:
			// handled below
		case tar.TypeSymlink, tar.TypeLink:
			return nil, fmt.Errorf("archive entry %q: symlinks and hardlinks are not allowed", hdr.Name)
		default:
			// Device nodes, fifos, sockets, and exotic types are skipped.
			continue
		}

		target, err := secureJoin(absDest, hdr.Name)
		if err != nil {
			return nil, err
		}

		res.Files++
		if res.Files > limits.MaxFiles {
			return nil, fmt.Errorf("archive exceeds the maximum of %d entries", limits.MaxFiles)
		}

		if hdr.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return nil, fmt.Errorf("create dir %q: %w", hdr.Name, err)
			}
			continue
		}

		if hdr.Size > limits.MaxFileBytes {
			return nil, fmt.Errorf("archive entry %q exceeds the per-file limit of %d bytes", hdr.Name, limits.MaxFileBytes)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, fmt.Errorf("create parent dir for %q: %w", hdr.Name, err)
		}

		// Strip setuid/setgid/sticky; keep rwx bits only (exec bit
		// matters for scripts COPYed into images).
		mode := os.FileMode(hdr.Mode) & 0o777
		if mode == 0 {
			mode = 0o644
		}
		f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
		if err != nil {
			return nil, fmt.Errorf("create file %q: %w", hdr.Name, err)
		}

		// Copy at most one byte over the declared size: a tar entry that
		// yields more data than its header claims is malformed.
		written, err := io.Copy(f, io.LimitReader(tr, hdr.Size+1))
		closeErr := f.Close()
		if err != nil {
			return nil, fmt.Errorf("write file %q: %w", hdr.Name, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close file %q: %w", hdr.Name, closeErr)
		}
		if written > hdr.Size {
			return nil, fmt.Errorf("archive entry %q is larger than its declared size", hdr.Name)
		}

		res.TotalBytes += written
		if res.TotalBytes > maxTotal {
			return nil, fmt.Errorf("archive exceeds the total uncompressed limit of %d bytes", maxTotal)
		}
	}

	if res.Files == 0 {
		return nil, errors.New("archive contains no files")
	}
	return res, nil
}

// secureJoin resolves an archive entry name inside root, rejecting
// absolute paths and any form of traversal.
func secureJoin(root, name string) (string, error) {
	if name == "" {
		return "", errors.New("archive entry has an empty name")
	}
	// Windows-style separators and drive letters never appear in
	// legitimate project archives; normalize so filepath handles them.
	// The absolute-path check runs BEFORE filepath.Clean: on Windows,
	// Clean turns "/etc/x" into `\etc\x`, which IsAbs treats as relative.
	norm := strings.ReplaceAll(name, `\`, "/")
	if strings.HasPrefix(norm, "/") || hasDrivePrefix(norm) {
		return "", fmt.Errorf("archive entry %q: absolute paths are not allowed", name)
	}
	cleaned := filepath.Clean(norm)
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("archive entry %q: absolute paths are not allowed", name)
	}
	target := filepath.Join(root, cleaned)
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes the extraction directory", name)
	}
	return target, nil
}

// hasDrivePrefix reports a Windows drive-letter path like C:\ or C:/.
func hasDrivePrefix(p string) bool {
	return len(p) >= 2 && p[1] == ':' &&
		(('a' <= p[0] && p[0] <= 'z') || ('A' <= p[0] && p[0] <= 'Z'))
}
