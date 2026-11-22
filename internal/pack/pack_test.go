package pack_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/theriseunion-edge/edge-cli/internal/pack"
	"github.com/theriseunion-edge/edge-cli/internal/testutil"
)

func setupTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"README.md":       "hello\n",
		"bin/install.sh":  "#!/bin/sh\necho install\n",
		"images/blob.txt": "0123456789012345678901234",
	}
	modes := map[string]os.FileMode{
		"bin/install.sh": 0o755,
	}
	if err := testutil.WriteTree(root, files, modes); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFingerprintMatchesBetweenDirAndTar(t *testing.T) {
	root := setupTree(t)
	for _, gzipEnabled := range []bool{false, true} {
		archive := filepath.Join(t.TempDir(), "bundle.tgz")
		if err := testutil.MakeTar(root, archive, gzipEnabled); err != nil {
			t.Fatal(err)
		}
		dirSpec, err := pack.Scan(root, 8)
		if err != nil {
			t.Fatal(err)
		}
		tarSpec, err := pack.Scan(archive, 8)
		if err != nil {
			t.Fatal(err)
		}
		if dirSpec.Form != pack.FormDir || tarSpec.Form != pack.FormTar {
			t.Fatalf("形态错误: %s %s", dirSpec.Form, tarSpec.Form)
		}
		if dirSpec.Fingerprint != tarSpec.Fingerprint {
			t.Fatalf("目录与压缩包指纹不一致: %s != %s", dirSpec.Fingerprint, tarSpec.Fingerprint)
		}
	}
}

func TestChunkSizes(t *testing.T) {
	root := t.TempDir()
	if err := testutil.WriteTree(root, map[string]string{"a.bin": "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ"}, nil); err != nil {
		t.Fatal(err)
	}
	spec, err := pack.Scan(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.Chunks) != 4 {
		t.Fatalf("分片数量错误: %d", len(spec.Chunks))
	}
	want := []int64{10, 10, 10, 6}
	for i, chunk := range spec.Chunks {
		if chunk.Size != want[i] {
			t.Fatalf("分片 %d 大小 %d，期望 %d", i, chunk.Size, want[i])
		}
	}
}

func TestRestoreDirRoundtrip(t *testing.T) {
	root := setupTree(t)
	spec, err := pack.Scan(root, 8)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored")
	open := func(index int) (io.ReadCloser, error) { return spec.OpenChunk(root, index) }
	if err := pack.RestoreDir(dest, spec, open); err != nil {
		t.Fatal(err)
	}
	restored, err := pack.Scan(dest, 8)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Fingerprint != restored.Fingerprint {
		t.Fatalf("还原后指纹不一致: %s != %s", spec.Fingerprint, restored.Fingerprint)
	}
	info, err := os.Stat(filepath.Join(dest, "bin/install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("权限未还原: %o", info.Mode().Perm())
	}
}

func TestRestoreTarRoundtrip(t *testing.T) {
	root := setupTree(t)
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := testutil.MakeTar(root, archive, true); err != nil {
		t.Fatal(err)
	}
	spec, err := pack.Scan(archive, 8)
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored.tar.gz")
	open := func(index int) (io.ReadCloser, error) { return spec.OpenChunk(archive, index) }
	if err := pack.RestoreTar(dest, spec, open); err != nil {
		t.Fatal(err)
	}
	restored, err := pack.Scan(dest, 8)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Fingerprint != restored.Fingerprint {
		t.Fatalf("还原后指纹不一致: %s != %s", spec.Fingerprint, restored.Fingerprint)
	}
}

func TestScanRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", filepath.Join(root, "link")); err != nil {
		t.Skipf("创建符号链接失败: %v", err)
	}
	if _, err := pack.Scan(root, 8); err == nil {
		t.Fatal("符号链接应当报错")
	}
}
