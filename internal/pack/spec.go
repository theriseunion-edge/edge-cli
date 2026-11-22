// Package pack 处理离线包的读取、内容指纹计算、分片和还原。
package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

const (
	// SchemaVersion 是内部元数据格式版本。
	SchemaVersion = 1
	// DefaultChunkSize 是传输分片大小：512 MiB。
	DefaultChunkSize = 512 << 20

	FormTar = "tar"
	FormDir = "dir"

	KindFile = "file"
	KindDir  = "dir"
)

// Entry 是离线包内的一个文件或目录。
type Entry struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Chunks []int  `json:"chunks,omitempty"`
}

// Chunk 是一个传输分片的摘要信息。
type Chunk struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Spec 是内部元数据，随制品保存，不作为用户输入。
type Spec struct {
	SchemaVersion int     `json:"schemaVersion"`
	Form          string  `json:"form"`
	Compression   string  `json:"compression,omitempty"`
	Fingerprint   string  `json:"fingerprint"`
	ChunkSize     int64   `json:"chunkSize"`
	TarSize       int64   `json:"tarSize,omitempty"`
	TarSHA256     string  `json:"tarSha256,omitempty"`
	Entries       []Entry `json:"entries"`
	Chunks        []Chunk `json:"chunks"`

	chunkRefs []chunkRef
}

// chunkRef 记录本地分片在源文件中的位置，仅用于上传时读取本地内容。
type chunkRef struct {
	entry  int
	offset int64
	size   int64
}

type fingerprintEntry struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// Fingerprint 按路径排序后序列化文件清单并取 SHA-256。
// 只覆盖路径、类型、权限、大小和内容摘要，不包含容器形态、时间戳和属主。
func Fingerprint(entries []Entry) string {
	sorted := make([]Entry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Path < sorted[j].Path })

	items := make([]fingerprintEntry, 0, len(sorted))
	for _, e := range sorted {
		items = append(items, fingerprintEntry{Kind: e.Kind, Path: e.Path, Mode: e.Mode, Size: e.Size, SHA256: e.SHA256})
	}
	data, err := json.Marshal(items)
	if err != nil {
		// json.Marshal 对固定结构不会失败。
		panic(err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
