// Package store 定义离线包制品的存储接口和 manifest 结构。
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	// ArtifactType 是离线包制品的类型。
	ArtifactType = "application/vnd.theriseunion.edge.offline-package.v1"
	// MetadataMediaType 是内部元数据类型。
	MetadataMediaType = "application/vnd.theriseunion.edge.offline-package.v1+json"
	// ConfigMediaType 是空配置类型。
	ConfigMediaType = "application/vnd.oci.empty.v1+json"
	// ChunkMediaType 是传输分片的类型。
	ChunkMediaType = "application/octet-stream"
	// FingerprintAnnotation 保存内容指纹。
	FingerprintAnnotation = "io.theriseunion.edge.fingerprint"
	// TitleAnnotation 保存离线包名称。
	TitleAnnotation = "org.opencontainers.image.title"
)

// EmptyConfig 是空配置内容。
var EmptyConfig = []byte("{}")

// ErrConflict 表示同一发布地址已指向不同内容。
var ErrConflict = errors.New("发布地址已存在不同内容")

// Blob 是一个待上传的分片。
type Blob struct {
	Descriptor ocispec.Descriptor
	Open       func() (io.ReadCloser, error)
}

// PushOptions 是上传一次的输入。
type PushOptions struct {
	Reference   string
	Fingerprint string
	Title       string
	Metadata    []byte
	Blobs       []Blob
}

// PushResult 是上传结果。
type PushResult struct {
	Digest   string
	Reused   bool
	Uploaded int
	Skipped  int
}

// Artifact 是从存储取回的制品。
type Artifact struct {
	Digest       string
	Title        string
	Fingerprint  string
	Metadata     []byte
	MetadataDesc ocispec.Descriptor
	Layers       []ocispec.Descriptor
}

// Store 是离线包存储的抽象。
type Store interface {
	Push(ctx context.Context, opts PushOptions) (PushResult, error)
	Pull(ctx context.Context, reference string) (*Artifact, error)
	Fetch(ctx context.Context, reference string, desc ocispec.Descriptor) (io.ReadCloser, error)
}

// BuildManifest 生成 manifest 和层描述。
func BuildManifest(opts PushOptions) ([]byte, ocispec.Descriptor, ocispec.Descriptor, ocispec.Descriptor, []ocispec.Descriptor, error) {
	configDesc := ocispec.Descriptor{
		MediaType: ConfigMediaType,
		Digest:    digest.FromBytes(EmptyConfig),
		Size:      int64(len(EmptyConfig)),
	}
	metadataDesc := ocispec.Descriptor{
		MediaType:   MetadataMediaType,
		Digest:      digest.FromBytes(opts.Metadata),
		Size:        int64(len(opts.Metadata)),
		Annotations: map[string]string{TitleAnnotation: "metadata.json"},
	}
	layers := make([]ocispec.Descriptor, 0, len(opts.Blobs)+1)
	layers = append(layers, metadataDesc)
	for _, blob := range opts.Blobs {
		layers = append(layers, blob.Descriptor)
	}
	annotations := map[string]string{FingerprintAnnotation: opts.Fingerprint}
	if opts.Title != "" {
		annotations[TitleAnnotation] = opts.Title
	}
	manifest := ocispec.Manifest{
		Versioned:    specs.Versioned{SchemaVersion: 2},
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config:       configDesc,
		Layers:       layers,
		Annotations:  annotations,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, ocispec.Descriptor{}, ocispec.Descriptor{}, ocispec.Descriptor{}, nil, err
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(data),
		Size:      int64(len(data)),
	}
	return data, manifestDesc, configDesc, metadataDesc, layers, nil
}

// ParseManifest 解析 manifest，返回制品结构（不含元数据内容）。
func ParseManifest(data []byte, desc ocispec.Descriptor) (*Artifact, error) {
	var manifest ocispec.Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("解析 manifest: %w", err)
	}
	if manifest.ArtifactType != "" && manifest.ArtifactType != ArtifactType {
		return nil, fmt.Errorf("制品类型不匹配: %q", manifest.ArtifactType)
	}
	var metadataDesc *ocispec.Descriptor
	layers := make([]ocispec.Descriptor, 0, len(manifest.Layers))
	for i := range manifest.Layers {
		layer := manifest.Layers[i]
		if layer.MediaType == MetadataMediaType && metadataDesc == nil {
			metadataDesc = &layer
			continue
		}
		layers = append(layers, layer)
	}
	if metadataDesc == nil {
		return nil, errors.New("制品缺少内部元数据")
	}
	return &Artifact{
		Digest:       desc.Digest.String(),
		Title:        manifest.Annotations[TitleAnnotation],
		Fingerprint:  manifest.Annotations[FingerprintAnnotation],
		MetadataDesc: *metadataDesc,
		Layers:       layers,
	}, nil
}
