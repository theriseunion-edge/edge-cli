// Package testutil 提供测试用的离线包构造工具。
package testutil

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// WriteTree 在 root 下写入文件，mode 可指定；目录通过路径结尾的 "/" 表示。
func WriteTree(root string, files map[string]string, modes map[string]os.FileMode) error {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		target := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, []byte(files[p]), 0o644); err != nil {
			return err
		}
		if mode, ok := modes[p]; ok {
			if err := os.Chmod(target, mode); err != nil {
				return err
			}
		}
	}
	return nil
}

// MakeTar 把目录打成 tar 或 tar.gz，保留相对路径和权限。
func MakeTar(source, target string, gzipEnabled bool) error {
	f, err := os.Create(target)
	if err != nil {
		return err
	}
	defer f.Close()
	var writer io.Writer = f
	var gz *gzip.Writer
	if gzipEnabled {
		gz = gzip.NewWriter(f)
		writer = gz
	}
	tw := tar.NewWriter(writer)
	err = filepath.Walk(source, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := filepath.ToSlash(rel)
		header := &tar.Header{Name: name, Mode: int64(info.Mode().Perm()), ModTime: info.ModTime()}
		if info.IsDir() {
			header.Typeflag = tar.TypeDir
			header.Name += "/"
			return tw.WriteHeader(header)
		}
		header.Typeflag = tar.TypeReg
		header.Size = info.Size()
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		file, err := os.Open(p)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if gz != nil {
		return gz.Close()
	}
	return nil
}
