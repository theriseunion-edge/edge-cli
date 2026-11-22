package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// ChunkOpener 按分片索引返回内容。
type ChunkOpener func(index int) (io.ReadCloser, error)

// RestoreTar 按分片顺序还原压缩包，并校验文件 SHA-256。
func RestoreTar(dest string, spec *Spec, open ChunkOpener) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	written := sha256.New()
	for i := range spec.Chunks {
		rc, err := open(i)
		if err != nil {
			f.Close()
			return err
		}
		_, copyErr := io.Copy(io.MultiWriter(f, written), rc)
		closeErr := rc.Close()
		if copyErr != nil {
			f.Close()
			return fmt.Errorf("写入分片 %d: %w", i, copyErr)
		}
		if closeErr != nil {
			f.Close()
			return closeErr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if spec.TarSHA256 != "" {
		got := hex.EncodeToString(written.Sum(nil))
		if got != spec.TarSHA256 {
			return fmt.Errorf("压缩包 SHA-256 不一致: 期望 %s，实际 %s", spec.TarSHA256, got)
		}
	}
	return nil
}

// RestoreDir 按清单还原目录，写出文件内容和权限，并逐文件校验摘要。
func RestoreDir(dest string, spec *Spec, open ChunkOpener) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, entry := range spec.Entries {
		target := filepath.Join(dest, filepath.FromSlash(entry.Path))
		switch entry.Kind {
		case KindDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			if err := os.Chmod(target, os.FileMode(entry.Mode)); err != nil {
				return err
			}
		case KindFile:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := restoreFile(target, entry, open); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%s: 不支持的条目类型 %q", entry.Path, entry.Kind)
		}
	}
	return nil
}

func restoreFile(target string, entry Entry, open ChunkOpener) error {
	f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	content := sha256.New()
	var size int64
	for _, index := range entry.Chunks {
		rc, err := open(index)
		if err != nil {
			f.Close()
			return err
		}
		n, copyErr := io.Copy(io.MultiWriter(f, content), rc)
		closeErr := rc.Close()
		if copyErr != nil {
			f.Close()
			return fmt.Errorf("%s: %w", entry.Path, copyErr)
		}
		if closeErr != nil {
			f.Close()
			return closeErr
		}
		size += n
	}
	if err := f.Close(); err != nil {
		return err
	}
	if size != entry.Size {
		return fmt.Errorf("%s: 大小不一致: 期望 %d，实际 %d", entry.Path, entry.Size, size)
	}
	if got := hex.EncodeToString(content.Sum(nil)); got != entry.SHA256 {
		return fmt.Errorf("%s: SHA-256 不一致: 期望 %s，实际 %s", entry.Path, entry.SHA256, got)
	}
	return os.Chmod(target, os.FileMode(entry.Mode))
}
