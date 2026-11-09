package judge_runner

import (
	"encoding/binary"
	"errors"
	"io"
	"regexp"
)

var className = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*\.class$`)
var ErrArtifact = errors.New("compiler artifacts missing, unsafe or over budget")

// The Docker attachment stream frames stdout with an eight-byte header.
type logReader struct {
	source    io.Reader
	remaining uint32
}

func (r *logReader) Read(p []byte) (int, error) {
	for r.remaining == 0 {
		var header [8]byte
		if _, err := io.ReadFull(r.source, header[:]); err != nil {
			return 0, err
		}
		if header[0] != 1 || header[1] != 0 || header[2] != 0 || header[3] != 0 {
			return 0, ErrSandbox
		}
		r.remaining = binary.BigEndian.Uint32(header[4:])
		if r.remaining > 32<<20 {
			return 0, ErrSandbox
		}
	}
	if uint32(len(p)) > r.remaining {
		p = p[:int(r.remaining)]
	}
	n, err := r.source.Read(p)
	r.remaining -= uint32(n)
	return n, err
}
