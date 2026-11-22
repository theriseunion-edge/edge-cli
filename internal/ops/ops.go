// Package ops 实现离线包上传与下载的完整流程。
package ops

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/theriseunion-edge/edge-cli/internal/pack"
	"github.com/theriseunion-edge/edge-cli/internal/store"
)

// Result 是一次上传或下载的结果，供文本或 JSON 输出。
type Result struct {
	Command     string `json:"command"`
	Form        string `json:"form"`
	Size        int64  `json:"size"`
	Fingerprint string `json:"fingerprint"`
	Chunks      int    `json:"chunks"`
	Reference   string `json:"reference,omitempty"`
	Digest      string `json:"digest,omitempty"`
	Output      string `json:"output,omitempty"`
	Reused      bool   `json:"reused,omitempty"`
	Uploaded    int    `json:"uploaded,omitempty"`
	Skipped     int    `json:"skipped,omitempty"`
	Verified    bool   `json:"verified,omitempty"`
}

// LogFunc 输出进度日志。
type LogFunc func(format string, args ...any)

// Push 读取本地离线包并保存到镜像仓库。
func Push(ctx context.Context, st store.Store, source, reference string, chunkSize int64, logf LogFunc) (*Result, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	spec, err := pack.Scan(source, chunkSize)
	if err != nil {
		return nil, err
	}
	size := specSize(spec)
	logf("读取离线包: 形态=%s 条数=%d 大小=%d 分片=%d", spec.Form, len(spec.Entries), size, len(spec.Chunks))
	logf("内容指纹: %s", spec.Fingerprint)

	metadata, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	blobs := make([]store.Blob, len(spec.Chunks))
	for i := range spec.Chunks {
		index := i
		blobs[i] = store.Blob{
			Descriptor: ocispec.Descriptor{
				MediaType: store.ChunkMediaType,
				Digest:    digest.Digest("sha256:" + spec.Chunks[i].SHA256),
				Size:      spec.Chunks[i].Size,
			},
			Open: func() (io.ReadCloser, error) {
				return spec.OpenChunk(source, index)
			},
		}
	}
	pushed, err := st.Push(ctx, store.PushOptions{
		Reference:   reference,
		Fingerprint: spec.Fingerprint,
		Title:       filepath.Base(source),
		Metadata:    metadata,
		Blobs:       blobs,
	})
	if err != nil {
		return nil, err
	}
	if pushed.Reused {
		logf("发布地址已有相同内容，直接复用")
	} else {
		logf("上传完成: 新增分片 %d，复用分片 %d", pushed.Uploaded, pushed.Skipped)
	}
	digestRef := referenceWithDigest(reference, pushed.Digest)
	logf("发布地址: %s", digestRef)
	return &Result{
		Command:     "push",
		Form:        spec.Form,
		Size:        size,
		Fingerprint: spec.Fingerprint,
		Chunks:      len(spec.Chunks),
		Reference:   digestRef,
		Digest:      pushed.Digest,
		Reused:      pushed.Reused,
		Uploaded:    pushed.Uploaded,
		Skipped:     pushed.Skipped,
	}, nil
}

// Pull 按发布地址取回离线包，还原形态并校验内容指纹。
func Pull(ctx context.Context, st store.Store, reference, output string, chunkSize int64, logf LogFunc) (*Result, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if _, err := os.Lstat(output); err == nil {
		return nil, fmt.Errorf("输出已存在: %s", output)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	artifact, err := st.Pull(ctx, reference)
	if err != nil {
		return nil, err
	}
	var spec pack.Spec
	if err := json.Unmarshal(artifact.Metadata, &spec); err != nil {
		return nil, fmt.Errorf("解析内部元数据: %w", err)
	}
	if spec.SchemaVersion != pack.SchemaVersion {
		return nil, fmt.Errorf("内部元数据版本不支持: %d", spec.SchemaVersion)
	}
	if spec.ChunkSize <= 0 {
		spec.ChunkSize = chunkSize
	}
	if len(spec.Chunks) != len(artifact.Layers) {
		return nil, fmt.Errorf("元数据分片数 %d 与 manifest 分片数 %d 不一致", len(spec.Chunks), len(artifact.Layers))
	}
	logf("下载离线包: 形态=%s 分片=%d", spec.Form, len(spec.Chunks))

	cacheDir, err := chunkCacheDir()
	if err != nil {
		return nil, err
	}
	var downloadedChunks, cachedChunks int
	var downloadedBytes int64
	open := func(index int) (io.ReadCloser, error) {
		desc := artifact.Layers[index]
		reader, cached, err := fetchChunk(ctx, st, reference, desc, cacheDir)
		if err != nil {
			return nil, err
		}
		if cached {
			cachedChunks++
		} else {
			downloadedChunks++
			downloadedBytes += desc.Size
		}
		return reader, nil
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = os.RemoveAll(output)
		}
	}()
	switch spec.Form {
	case pack.FormTar:
		err = pack.RestoreTar(output, &spec, open)
	case pack.FormDir:
		err = pack.RestoreDir(output, &spec, open)
	default:
		err = fmt.Errorf("不支持的离线包形态: %q", spec.Form)
	}
	if err != nil {
		return nil, err
	}
	logf("下载完成: 分片 %d，缓存复用 %d，下载 %d 字节", downloadedChunks+cachedChunks, cachedChunks, downloadedBytes)
	logf("还原完成，校验内容指纹")
	restored, err := pack.Scan(output, spec.ChunkSize)
	if err != nil {
		return nil, fmt.Errorf("校验还原结果: %w", err)
	}
	if restored.Fingerprint != spec.Fingerprint {
		return nil, fmt.Errorf("内容指纹不一致: 期望 %s，实际 %s", spec.Fingerprint, restored.Fingerprint)
	}
	logf("内容指纹: %s", spec.Fingerprint)
	logf("校验通过")
	verified := true
	succeeded = true
	return &Result{
		Command:     "pull",
		Form:        spec.Form,
		Size:        specSize(&spec),
		Fingerprint: spec.Fingerprint,
		Chunks:      len(spec.Chunks),
		Reference:   reference,
		Digest:      artifact.Digest,
		Output:      output,
		Verified:    verified,
	}, nil
}

func specSize(spec *pack.Spec) int64 {
	if spec.Form == pack.FormTar {
		return spec.TarSize
	}
	var size int64
	for _, entry := range spec.Entries {
		if entry.Kind == pack.KindFile {
			size += entry.Size
		}
	}
	return size
}

// referenceWithDigest 把 tag 发布地址改写为带 digest 的地址。
func referenceWithDigest(reference, digestValue string) string {
	repo := reference
	if i := strings.LastIndex(reference, ":"); i > strings.LastIndex(reference, "/") {
		repo = reference[:i]
	}
	return repo + "@" + digestValue
}

func chunkCacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}
	dir := filepath.Join(base, "edge-cli", "chunks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("创建分片缓存目录: %w", err)
	}
	return dir, nil
}

// fetchChunk 优先使用本地分片缓存；未命中时下载并校验摘要后写入缓存。
// 第二个返回值表示是否命中缓存。
func fetchChunk(ctx context.Context, st store.Store, reference string, desc ocispec.Descriptor, cacheDir string) (io.ReadCloser, bool, error) {
	key := strings.TrimPrefix(desc.Digest.String(), "sha256:")
	cachePath := filepath.Join(cacheDir, key)
	if info, err := os.Stat(cachePath); err == nil && info.Size() == desc.Size {
		reader, err := os.Open(cachePath)
		return reader, true, err
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	reader, err := st.Fetch(ctx, reference, desc)
	if err != nil {
		return nil, false, err
	}
	tmp, err := os.CreateTemp(cacheDir, key+".part-*")
	if err != nil {
		reader.Close()
		return nil, false, err
	}
	hasher := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(tmp, hasher), reader)
	closeErr := reader.Close()
	if copyErr != nil || closeErr != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		if copyErr != nil {
			return nil, false, copyErr
		}
		return nil, false, closeErr
	}
	if size != desc.Size {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, false, fmt.Errorf("分片大小不一致: 期望 %d，实际 %d", desc.Size, size)
	}
	if got := "sha256:" + hex.EncodeToString(hasher.Sum(nil)); got != desc.Digest.String() {
		tmp.Close()
		os.Remove(tmp.Name())
		return nil, false, fmt.Errorf("分片摘要不一致: 期望 %s，实际 %s", desc.Digest, got)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return nil, false, err
	}
	if err := os.Rename(tmp.Name(), cachePath); err != nil {
		os.Remove(tmp.Name())
		return nil, false, err
	}
	reader, err = os.Open(cachePath)
	return reader, false, err
}
