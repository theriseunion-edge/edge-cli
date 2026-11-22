package pack

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type sectionReadCloser struct {
	io.Reader
	closer io.Closer
}

func (s *sectionReadCloser) Close() error { return s.closer.Close() }

// OpenChunk 打开本地源文件中指定分片的内容，仅用于上传。
func (s *Spec) OpenChunk(source string, index int) (io.ReadCloser, error) {
	if index < 0 || index >= len(s.chunkRefs) {
		return nil, fmt.Errorf("分片索引越界: %d", index)
	}
	ref := s.chunkRefs[index]
	var filePath string
	switch {
	case ref.entry >= 0:
		filePath = filepath.Join(source, filepath.FromSlash(s.Entries[ref.entry].Path))
	default:
		filePath = source
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	return &sectionReadCloser{Reader: io.NewSectionReader(f, ref.offset, ref.size), closer: f}, nil
}
