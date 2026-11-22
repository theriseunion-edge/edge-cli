package pack

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"
)

// chunkHasher 按固定大小切分字节流，计算每片的摘要和偏移。
type chunkHasher struct {
	size    int64
	total   int64
	cur     hash.Hash
	chunks  []Chunk
	offsets []int64
}

func newChunkHasher(size int64) *chunkHasher {
	return &chunkHasher{size: size, cur: sha256.New()}
}

func (c *chunkHasher) Write(p []byte) (int, error) {
	written := 0
	for written < len(p) {
		remain := c.size - c.total%c.size
		n := int64(len(p) - written)
		if n > remain {
			n = remain
		}
		if _, err := c.cur.Write(p[written : written+int(n)]); err != nil {
			return written, err
		}
		c.total += n
		written += int(n)
		if c.total%c.size == 0 {
			c.flush(c.size)
		}
	}
	return len(p), nil
}

func (c *chunkHasher) flush(size int64) {
	c.chunks = append(c.chunks, Chunk{Size: size, SHA256: hex.EncodeToString(c.cur.Sum(nil))})
	c.offsets = append(c.offsets, c.total-size)
	c.cur = sha256.New()
}

// finish 收尾最后一个未满的分片。
func (c *chunkHasher) finish() ([]Chunk, []int64) {
	if remain := c.total % c.size; remain > 0 {
		c.flush(remain)
	}
	return c.chunks, c.offsets
}
