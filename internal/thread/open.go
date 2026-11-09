package thread

import (
	"io"
	"os"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// OpenTranscript opens a session file to Decode: plain JSONL, or
// zstd-compressed (.zst, as Codex writes its rollouts).
func OpenTranscript(path string) (io.ReadCloser, error) {
	f, err := os.Open(path)
	if err != nil || !strings.HasSuffix(path, ".zst") {
		return f, err
	}
	d, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	if err != nil {
		f.Close()
		return nil, err
	}
	return zstdFile{d, f}, nil
}

type zstdFile struct {
	*zstd.Decoder
	f *os.File
}

func (z zstdFile) Close() error {
	z.Decoder.Close()
	return z.f.Close()
}
