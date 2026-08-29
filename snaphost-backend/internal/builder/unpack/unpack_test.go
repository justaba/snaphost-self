package unpack

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type entry struct {
	name     string
	typeflag byte
	mode     int64
	data     string
	linkname string
}

func makeTarGz(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: e.typeflag,
			Mode:     e.mode,
			Linkname: e.linkname,
		}
		if e.typeflag == tar.TypeReg {
			hdr.Size = int64(len(e.data))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %q: %v", e.name, err)
		}
		if e.typeflag == tar.TypeReg {
			if _, err := tw.Write([]byte(e.data)); err != nil {
				t.Fatalf("write data %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func file(name, data string) entry {
	return entry{name: name, typeflag: tar.TypeReg, mode: 0o644, data: data}
}

func TestTarGz_HappyPath(t *testing.T) {
	dest := t.TempDir()
	archive := makeTarGz(t, []entry{
		{name: "app", typeflag: tar.TypeDir, mode: 0o755},
		file("app/index.js", "console.log('hi')"),
		file("package.json", `{"name":"x"}`),
		{name: "run.sh", typeflag: tar.TypeReg, mode: 0o755, data: "#!/bin/sh\n"},
	})

	res, err := TarGz(archive, dest, Limits{})
	if err != nil {
		t.Fatalf("TarGz: %v", err)
	}
	if res.Files != 4 {
		t.Errorf("Files = %d, want 4", res.Files)
	}

	got, err := os.ReadFile(filepath.Join(dest, "app", "index.js"))
	if err != nil || string(got) != "console.log('hi')" {
		t.Errorf("app/index.js content = %q, err %v", got, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dest, "run.sh"))
		if err != nil {
			t.Fatalf("stat run.sh: %v", err)
		}
		if info.Mode().Perm()&0o100 == 0 {
			t.Errorf("run.sh lost its exec bit: %v", info.Mode())
		}
	}
}

func TestTarGz_RejectsTraversalAndAbsolute(t *testing.T) {
	cases := []struct {
		name  string
		entry entry
	}{
		{"absolute path", file("/etc/cron.d/evil", "x")},
		{"parent traversal", file("../evil", "x")},
		{"nested traversal", file("a/../../evil", "x")},
		{"deep traversal", file("a/b/../../../evil", "x")},
		{"windows drive", file(`C:\evil`, "x")},
		{"windows separator traversal", file(`..\evil`, "x")},
		{"empty name", entry{name: "", typeflag: tar.TypeReg, mode: 0o644}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dest := t.TempDir()
			archive := makeTarGz(t, []entry{file("ok.txt", "fine"), tc.entry})
			if _, err := TarGz(archive, dest, Limits{}); err == nil {
				t.Fatal("expected error, got nil")
			}
			if _, err := os.Stat(filepath.Join(dest, "..", "evil")); err == nil {
				t.Fatal("traversal file was written outside dest")
			}
		})
	}
}

func TestTarGz_RejectsLinks(t *testing.T) {
	for _, tc := range []struct {
		name     string
		typeflag byte
	}{
		{"symlink", tar.TypeSymlink},
		{"hardlink", tar.TypeLink},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := makeTarGz(t, []entry{
				{name: "link", typeflag: tc.typeflag, mode: 0o777, linkname: "/etc/passwd"},
			})
			_, err := TarGz(archive, t.TempDir(), Limits{})
			if err == nil || !strings.Contains(err.Error(), "not allowed") {
				t.Fatalf("expected links-not-allowed error, got %v", err)
			}
		})
	}
}

func TestTarGz_SkipsSpecialEntries(t *testing.T) {
	dest := t.TempDir()
	archive := makeTarGz(t, []entry{
		{name: "dev", typeflag: tar.TypeChar, mode: 0o666},
		{name: "pipe", typeflag: tar.TypeFifo, mode: 0o666},
		file("ok.txt", "fine"),
	})
	res, err := TarGz(archive, dest, Limits{})
	if err != nil {
		t.Fatalf("TarGz: %v", err)
	}
	if res.Files != 1 {
		t.Errorf("Files = %d, want 1 (special entries skipped)", res.Files)
	}
	if _, err := os.Stat(filepath.Join(dest, "dev")); err == nil {
		t.Error("special entry was materialized")
	}
}

func TestTarGz_EnforcesFileCount(t *testing.T) {
	entries := []entry{file("a.txt", "x"), file("b.txt", "x"), file("c.txt", "x")}
	archive := makeTarGz(t, entries)
	if _, err := TarGz(archive, t.TempDir(), Limits{MaxFiles: 2}); err == nil ||
		!strings.Contains(err.Error(), "maximum of 2 entries") {
		t.Fatalf("expected file-count error, got %v", err)
	}
}

func TestTarGz_EnforcesPerFileSize(t *testing.T) {
	archive := makeTarGz(t, []entry{file("big.bin", strings.Repeat("a", 1024))})
	if _, err := TarGz(archive, t.TempDir(), Limits{MaxFileBytes: 512}); err == nil ||
		!strings.Contains(err.Error(), "per-file limit") {
		t.Fatalf("expected per-file error, got %v", err)
	}
}

func TestTarGz_EnforcesTotalSize(t *testing.T) {
	archive := makeTarGz(t, []entry{
		file("a.bin", strings.Repeat("a", 600)),
		file("b.bin", strings.Repeat("b", 600)),
	})
	if _, err := TarGz(archive, t.TempDir(), Limits{MaxTotalBytes: 1000, MaxRatio: 1 << 30}); err == nil ||
		!strings.Contains(err.Error(), "total uncompressed limit") {
		t.Fatalf("expected total-size error, got %v", err)
	}
}

func TestTarGz_EnforcesDecompressionRatio(t *testing.T) {
	// 10 MB of zeros compresses to ~10 KB — a classic decompression bomb.
	archive := makeTarGz(t, []entry{file("zeros.bin", strings.Repeat("\x00", 10*1024*1024))})
	_, err := TarGz(archive, t.TempDir(), Limits{MaxRatio: 10})
	if err == nil || !strings.Contains(err.Error(), "total uncompressed limit") {
		t.Fatalf("expected ratio-capped total error, got %v", err)
	}
}

func TestTarGz_StripsSetuid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode bits are not meaningful on windows")
	}
	dest := t.TempDir()
	archive := makeTarGz(t, []entry{
		{name: "suid", typeflag: tar.TypeReg, mode: 0o4755, data: "x"},
	})
	if _, err := TarGz(archive, dest, Limits{}); err != nil {
		t.Fatalf("TarGz: %v", err)
	}
	info, err := os.Stat(filepath.Join(dest, "suid"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode()&os.ModeSetuid != 0 {
		t.Errorf("setuid bit survived: %v", info.Mode())
	}
}

func TestTarGz_RejectsGarbage(t *testing.T) {
	if _, err := TarGz([]byte("not gzip at all"), t.TempDir(), Limits{}); err == nil {
		t.Fatal("expected gzip error")
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write([]byte("gzip but not tar"))
	_ = gz.Close()
	if _, err := TarGz(buf.Bytes(), t.TempDir(), Limits{}); err == nil {
		t.Fatal("expected tar error")
	}
	empty := makeTarGz(t, nil)
	if _, err := TarGz(empty, t.TempDir(), Limits{}); err == nil {
		t.Fatal("expected empty-archive error")
	}
}
