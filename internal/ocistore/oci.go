// Package ocistore 用 OCI 镜像仓库实现离线包存储。
package ocistore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"

	"github.com/theriseunion-edge/edge-cli/internal/store"
)

// Store 通过 OCI 镜像仓库保存离线包。
type Store struct {
	creds credentials.Store
}

// New 创建使用本机容器凭据的存储。
func New() (*Store, error) {
	creds, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return nil, fmt.Errorf("读取本机容器凭据: %w", err)
	}
	return &Store{creds: creds}, nil
}

func (s *Store) credential(ctx context.Context, host string) (auth.Credential, error) {
	cred, err := s.creds.Get(ctx, host)
	if err != nil {
		// 没有配置凭据时按匿名访问处理，由仓库返回真实的认证错误。
		return auth.EmptyCredential, nil
	}
	return cred, nil
}

func (s *Store) repository(reference string) (*remote.Repository, string, error) {
	ref, err := registry.ParseReference(reference)
	if err != nil {
		return nil, "", fmt.Errorf("发布地址不合法: %w", err)
	}
	if ref.Reference == "" {
		return nil, "", fmt.Errorf("发布地址缺少 tag 或 digest: %s", reference)
	}
	repo, err := remote.NewRepository(ref.Registry + "/" + ref.Repository)
	if err != nil {
		return nil, "", err
	}
	repo.Client = &auth.Client{Cache: auth.NewCache(), Credential: s.credential}
	if isLocalRegistry(ref.Registry) {
		repo.PlainHTTP = true
	}
	return repo, ref.Reference, nil
}

func isLocalRegistry(host string) bool {
	h := host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		h = host[:i]
	}
	return h == "localhost" || strings.HasPrefix(h, "127.")
}

// Push 上传制品；同一发布地址已有相同内容时直接复用，不同内容返回冲突。
func (s *Store) Push(ctx context.Context, opts store.PushOptions) (store.PushResult, error) {
	repo, ref, err := s.repository(opts.Reference)
	if err != nil {
		return store.PushResult{}, err
	}
	if digest.Digest(ref).Validate() == nil {
		return store.PushResult{}, errors.New("上传的发布地址必须是 tag，不能用 digest")
	}

	existing, err := repo.Resolve(ctx, ref)
	switch {
	case err == nil:
		artifact, err := s.fetchArtifact(ctx, repo, existing)
		if err != nil {
			return store.PushResult{}, err
		}
		if artifact.Fingerprint == opts.Fingerprint {
			return store.PushResult{Digest: existing.Digest.String(), Reused: true}, nil
		}
		return store.PushResult{}, fmt.Errorf("%w: %s", store.ErrConflict, opts.Reference)
	case !errors.Is(err, errdef.ErrNotFound):
		return store.PushResult{}, fmt.Errorf("解析发布地址: %w", err)
	}

	manifestData, manifestDesc, configDesc, metadataDesc, _, err := store.BuildManifest(opts)
	if err != nil {
		return store.PushResult{}, err
	}
	if err := pushReader(ctx, repo, configDesc, bytes.NewReader(store.EmptyConfig)); err != nil {
		return store.PushResult{}, err
	}
	if err := pushReader(ctx, repo, metadataDesc, bytes.NewReader(opts.Metadata)); err != nil {
		return store.PushResult{}, fmt.Errorf("上传内部元数据: %w", err)
	}

	result := store.PushResult{}
	for i, blob := range opts.Blobs {
		exists, err := repo.Blobs().Exists(ctx, blob.Descriptor)
		if err != nil {
			return store.PushResult{}, fmt.Errorf("检查分片 %d: %w", i, err)
		}
		if exists {
			result.Skipped++
			continue
		}
		reader, err := blob.Open()
		if err != nil {
			return store.PushResult{}, fmt.Errorf("读取分片 %d: %w", i, err)
		}
		if err := repo.Blobs().Push(ctx, blob.Descriptor, reader); err != nil {
			return store.PushResult{}, fmt.Errorf("上传分片 %d: %w", i, err)
		}
		result.Uploaded++
	}
	if err := repo.Manifests().Push(ctx, manifestDesc, bytes.NewReader(manifestData)); err != nil {
		return store.PushResult{}, fmt.Errorf("提交 manifest: %w", err)
	}
	if err := repo.Tag(ctx, manifestDesc, ref); err != nil {
		return store.PushResult{}, fmt.Errorf("写入 tag: %w", err)
	}
	result.Digest = manifestDesc.Digest.String()
	return result, nil
}

func pushReader(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor, reader io.Reader) error {
	exists, err := repo.Blobs().Exists(ctx, desc)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return repo.Blobs().Push(ctx, desc, reader)
}

// Pull 按发布地址取回制品。
func (s *Store) Pull(ctx context.Context, reference string) (*store.Artifact, error) {
	repo, ref, err := s.repository(reference)
	if err != nil {
		return nil, err
	}
	desc, err := repo.Resolve(ctx, ref)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, fmt.Errorf("镜像仓库中没有该离线包: %s", reference)
		}
		return nil, fmt.Errorf("解析发布地址: %w", err)
	}
	return s.fetchArtifact(ctx, repo, desc)
}

func (s *Store) fetchArtifact(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor) (*store.Artifact, error) {
	manifestData, err := fetchBytes(ctx, repo, desc)
	if err != nil {
		return nil, fmt.Errorf("读取 manifest: %w", err)
	}
	artifact, err := store.ParseManifest(manifestData, desc)
	if err != nil {
		return nil, err
	}
	metadata, err := fetchBytes(ctx, repo, artifact.MetadataDesc)
	if err != nil {
		return nil, fmt.Errorf("读取内部元数据: %w", err)
	}
	artifact.Metadata = metadata
	return artifact, nil
}

// Fetch 按描述符读取内容。
func (s *Store) Fetch(ctx context.Context, reference string, desc ocispec.Descriptor) (io.ReadCloser, error) {
	repo, _, err := s.repository(reference)
	if err != nil {
		return nil, err
	}
	return repo.Fetch(ctx, desc)
}

func fetchBytes(ctx context.Context, repo *remote.Repository, desc ocispec.Descriptor) ([]byte, error) {
	reader, err := repo.Fetch(ctx, desc)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	if digest.FromBytes(data) != desc.Digest {
		return nil, fmt.Errorf("内容摘要不一致: 期望 %s", desc.Digest)
	}
	return data, nil
}
