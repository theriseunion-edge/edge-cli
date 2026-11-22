package pack

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Scan 只读遍历离线包，得到形态、文件清单、内容指纹和分片清单。
// source 可以是目录，也可以是 tar 或 tar.gz 文件。
func Scan(source string, chunkSize int64) (*Spec, error) {
	if chunkSize <= 0 {
		return nil, errors.New("分片大小必须为正数")
	}
	info, err := os.Stat(source)
	if err != nil {
		return nil, err
	}
	spec := &Spec{SchemaVersion: SchemaVersion, ChunkSize: chunkSize}
	switch {
	case info.IsDir():
		spec.Form = FormDir
		err = scanDir(spec, source)
	case info.Mode().IsRegular():
		spec.Form = FormTar
		err = scanTar(spec, source)
	default:
		return nil, fmt.Errorf("%s: 只支持目录、tar 和 tar.gz 文件", source)
	}
	if err != nil {
		return nil, err
	}
	spec.Fingerprint = Fingerprint(spec.Entries)
	return spec, nil
}

func scanDir(spec *Spec, root string) error {
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("%s: 不支持符号链接", rel)
		case info.IsDir():
			spec.Entries = append(spec.Entries, Entry{Path: rel, Kind: KindDir, Mode: uint32(info.Mode().Perm())})
			return nil
		case info.Mode().IsRegular():
			return scanFile(spec, p, rel, info)
		default:
			return fmt.Errorf("%s: 只支持普通文件和目录", rel)
		}
	})
}

func scanFile(spec *Spec, sourcePath, rel string, info os.FileInfo) error {
	f, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer f.Close()

	entryIdx := len(spec.Entries)
	content := sha256.New()
	ch := newChunkHasher(spec.ChunkSize)
	if _, err := io.Copy(io.MultiWriter(content, ch), f); err != nil {
		return fmt.Errorf("%s: %w", rel, err)
	}
	chunks, offsets := ch.finish()
	var indices []int
	for i, c := range chunks {
		spec.chunkRefs = append(spec.chunkRefs, chunkRef{entry: entryIdx, offset: offsets[i], size: c.Size})
		indices = append(indices, len(spec.Chunks))
		spec.Chunks = append(spec.Chunks, c)
	}
	spec.Entries = append(spec.Entries, Entry{
		Path:   rel,
		Kind:   KindFile,
		Mode:   0,
		Size:   info.Size(),
		SHA256: hex.EncodeToString(content.Sum(nil)),
		Chunks: indices,
	})
	spec.Entries[len(spec.Entries)-1].Mode = uint32(info.Mode().Perm())
	return nil
}

func scanTar(spec *Spec, sourcePath string) error {
	f, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer f.Close()

	br := bufio.NewReader(f)
	magic, err := br.Peek(2)
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	compressed := len(magic) == 2 && magic[0] == 0x1f && magic[1] == 0x8b
	if compressed {
		spec.Compression = "gzip"
	}

	rawSHA := sha256.New()
	ch := newChunkHasher(spec.ChunkSize)
	raw := io.TeeReader(br, io.MultiWriter(rawSHA, ch))

	var tr *tar.Reader
	if compressed {
		gz, err := gzip.NewReader(raw)
		if err != nil {
			return fmt.Errorf("%s: 读取 gzip: %w", sourcePath, err)
		}
		defer gz.Close()
		tr = tar.NewReader(gz)
	} else {
		tr = tar.NewReader(raw)
	}

	entryIndex := make(map[string]int)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("读取 tar: %w", err)
		}
		name, err := normalizeTarPath(hdr.Name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		mode := uint32(hdr.Mode) & 0o777
		switch hdr.Typeflag {
		case tar.TypeDir:
			if idx, ok := entryIndex[name]; ok {
				spec.Entries[idx].Mode = mode
				continue
			}
			entryIndex[name] = len(spec.Entries)
			spec.Entries = append(spec.Entries, Entry{Path: name, Kind: KindDir, Mode: mode})
		case tar.TypeReg, tar.TypeRegA:
			content := sha256.New()
			size, err := io.Copy(content, tr)
			if err != nil {
				return fmt.Errorf("%s: 读取内容: %w", name, err)
			}
			if idx, ok := entryIndex[name]; ok {
				spec.Entries[idx].Size = size
				spec.Entries[idx].SHA256 = hex.EncodeToString(content.Sum(nil))
				spec.Entries[idx].Mode = mode
				continue
			}
			entryIndex[name] = len(spec.Entries)
			spec.Entries = append(spec.Entries, Entry{Path: name, Kind: KindFile, Mode: mode, Size: size, SHA256: hex.EncodeToString(content.Sum(nil))})
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		default:
			return fmt.Errorf("%s: 只支持普通文件和目录（tar 类型 %q）", name, hdr.Typeflag)
		}
	}

	// 排空剩余原始字节，确保分片和文件摘要覆盖完整文件。
	if _, err := io.Copy(io.Discard, raw); err != nil {
		return err
	}
	chunks, offsets := ch.finish()
	for i, c := range chunks {
		spec.chunkRefs = append(spec.chunkRefs, chunkRef{entry: -1, offset: offsets[i], size: c.Size})
		spec.Chunks = append(spec.Chunks, c)
	}
	spec.TarSize = ch.total
	spec.TarSHA256 = hex.EncodeToString(rawSHA.Sum(nil))
	return nil
}

func normalizeTarPath(name string) (string, error) {
	n := strings.ReplaceAll(name, "\\", "/")
	n = strings.TrimPrefix(n, "./")
	n = path.Clean(n)
	if n == "." || n == "" {
		return "", nil
	}
	if strings.HasPrefix(n, "/") || n == ".." || strings.HasPrefix(n, "../") {
		return "", fmt.Errorf("tar 条目路径非法: %q", name)
	}
	return n, nil
}
