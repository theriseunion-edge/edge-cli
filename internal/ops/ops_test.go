package ops_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/theriseunion-edge/edge-cli/internal/ops"
	"github.com/theriseunion-edge/edge-cli/internal/pack"
	"github.com/theriseunion-edge/edge-cli/internal/store"
	"github.com/theriseunion-edge/edge-cli/internal/testutil"
)

type memoryStore struct {
	blobs     map[digest.Digest][]byte
	manifests map[digest.Digest][]byte
	refs      map[string]digest.Digest
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		blobs:     map[digest.Digest][]byte{},
		manifests: map[digest.Digest][]byte{},
		refs:      map[string]digest.Digest{},
	}
}

func (m *memoryStore) Push(_ context.Context, opts store.PushOptions) (store.PushResult, error) {
	if existing, ok := m.refs[opts.Reference]; ok {
		artifact, err := store.ParseManifest(m.manifests[existing], ocispec.Descriptor{Digest: existing})
		if err != nil {
			return store.PushResult{}, err
		}
		if artifact.Fingerprint == opts.Fingerprint {
			return store.PushResult{Digest: existing.String(), Reused: true}, nil
		}
		return store.PushResult{}, store.ErrConflict
	}
	manifest, desc, configDesc, metadataDesc, _, err := store.BuildManifest(opts)
	if err != nil {
		return store.PushResult{}, err
	}
	m.blobs[configDesc.Digest] = store.EmptyConfig
	m.blobs[metadataDesc.Digest] = opts.Metadata
	result := store.PushResult{}
	for _, blob := range opts.Blobs {
		reader, err := blob.Open()
		if err != nil {
			return store.PushResult{}, err
		}
		data, readErr := io.ReadAll(reader)
		closeErr := reader.Close()
		if readErr != nil {
			return store.PushResult{}, readErr
		}
		if closeErr != nil {
			return store.PushResult{}, closeErr
		}
		if digest.FromBytes(data) != blob.Descriptor.Digest {
			return store.PushResult{}, errors.New("分片摘要不一致")
		}
		if _, ok := m.blobs[blob.Descriptor.Digest]; ok {
			result.Skipped++
			continue
		}
		m.blobs[blob.Descriptor.Digest] = data
		result.Uploaded++
	}
	m.manifests[desc.Digest] = manifest
	m.refs[opts.Reference] = desc.Digest
	result.Digest = desc.Digest.String()
	return result, nil
}

func (m *memoryStore) Pull(_ context.Context, reference string) (*store.Artifact, error) {
	ref, ok := m.refs[reference]
	if !ok {
		return nil, errors.New("not found")
	}
	artifact, err := store.ParseManifest(m.manifests[ref], ocispec.Descriptor{Digest: ref})
	if err != nil {
		return nil, err
	}
	artifact.Metadata = m.blobs[artifact.MetadataDesc.Digest]
	return artifact, nil
}

func (m *memoryStore) Fetch(_ context.Context, _ string, desc ocispec.Descriptor) (io.ReadCloser, error) {
	data, ok := m.blobs[desc.Digest]
	if !ok {
		return nil, errors.New("blob not found")
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

func makeSource(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"README.md":       "edge offline package " + t.Name() + "\n",
		"bin/install.sh":  "#!/bin/sh\necho install\n",
		"images/blob.bin": "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ",
	}
	modes := map[string]os.FileMode{"bin/install.sh": 0o755}
	if err := testutil.WriteTree(root, files, modes); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestPushPullDirRoundtrip(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st := newMemoryStore()
	source := makeSource(t)

	pushed, err := ops.Push(ctx, st, source, "registry.example.com/edge/pkg:v1", 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pushed.Form != pack.FormDir || pushed.Fingerprint == "" || pushed.Chunks == 0 {
		t.Fatalf("上传结果不完整: %+v", pushed)
	}

	output := filepath.Join(t.TempDir(), "restored")
	pulled, err := ops.Pull(ctx, st, "registry.example.com/edge/pkg:v1", output, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !pulled.Verified || pulled.Fingerprint != pushed.Fingerprint {
		t.Fatalf("下载校验失败: %+v", pulled)
	}
	restored, err := pack.Scan(output, 8)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Fingerprint != pushed.Fingerprint {
		t.Fatalf("还原目录指纹不一致")
	}

	if _, err := ops.Pull(ctx, st, "registry.example.com/edge/pkg:v1", output, 8, nil); err == nil {
		t.Fatal("输出已存在时应当报错")
	}
}

func TestPushPullTarRoundtrip(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st := newMemoryStore()
	source := makeSource(t)
	archive := filepath.Join(t.TempDir(), "bundle.tar.gz")
	if err := testutil.MakeTar(source, archive, true); err != nil {
		t.Fatal(err)
	}

	pushed, err := ops.Push(ctx, st, archive, "registry.example.com/edge/pkg:tar-v1", 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "restored.tar.gz")
	if _, err := ops.Pull(ctx, st, "registry.example.com/edge/pkg:tar-v1", output, 8, nil); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	originalSum := sha256.Sum256(original)
	restoredSum := sha256.Sum256(restored)
	if hex.EncodeToString(originalSum[:]) != hex.EncodeToString(restoredSum[:]) {
		t.Fatalf("还原压缩包与原始文件不一致")
	}
	restoredSpec, err := pack.Scan(output, 8)
	if err != nil {
		t.Fatal(err)
	}
	if restoredSpec.Fingerprint != pushed.Fingerprint {
		t.Fatalf("还原压缩包指纹不一致")
	}
}

func TestPushReuseAndConflict(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st := newMemoryStore()
	source := makeSource(t)
	reference := "registry.example.com/edge/pkg:v1"

	if _, err := ops.Push(ctx, st, source, reference, 8, nil); err != nil {
		t.Fatal(err)
	}
	reused, err := ops.Push(ctx, st, source, reference, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reused.Reused {
		t.Fatal("相同内容重传应复用")
	}
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ops.Push(ctx, st, source, reference, 8, nil); !errors.Is(err, store.ErrConflict) {
		t.Fatalf("相同 tag 不同内容应冲突，实际: %v", err)
	}
}

func TestPullRejectsCorruptedChunk(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st := newMemoryStore()
	source := makeSource(t)
	if _, err := ops.Push(ctx, st, source, "registry.example.com/edge/pkg:v1", 8, nil); err != nil {
		t.Fatal(err)
	}
	artifact, err := st.Pull(ctx, "registry.example.com/edge/pkg:v1")
	if err != nil {
		t.Fatal(err)
	}
	corrupted := artifact.Layers[0].Digest
	data := st.blobs[corrupted]
	data[0] ^= 0xff
	st.blobs[corrupted] = data

	output := filepath.Join(t.TempDir(), "restored")
	if _, err := ops.Pull(ctx, st, "registry.example.com/edge/pkg:v1", output, 8, nil); err == nil {
		t.Fatal("分片被篡改时应当报错")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("失败后不应保留输出")
	}
}

func TestPushSkipsExistingChunks(t *testing.T) {
	ctx := context.Background()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	st := newMemoryStore()
	source := makeSource(t)
	if _, err := ops.Push(ctx, st, source, "registry.example.com/edge/pkg:v1", 8, nil); err != nil {
		t.Fatal(err)
	}
	second, err := ops.Push(ctx, st, source, "registry.example.com/edge/pkg:v2", 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.Uploaded != 0 || second.Skipped == 0 {
		t.Fatalf("应复用已有分片: %+v", second)
	}
}
