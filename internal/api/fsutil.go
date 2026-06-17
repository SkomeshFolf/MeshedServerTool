package api

import (
	"io"
	"io/fs"
)

// readSeeker adapts an fs.File to io.ReadSeeker so http.ServeContent can
// stream it with range support. fs.File is io.Reader but not io.Seeker on
// every embed.FS entry, so we read the whole thing in memory for the
// static-asset path. (Static assets are small — this is fine for now.)
func readSeeker(f fs.File) io.ReadSeeker {
	bs, err := io.ReadAll(f)
	if err != nil {
		return &errReader{err: err}
	}
	return &bytesReader{bs: bs}
}

type bytesReader struct {
	bs []byte
	i  int
}

func (b *bytesReader) Read(p []byte) (int, error) {
	if b.i >= len(b.bs) {
		return 0, io.EOF
	}
	n := copy(p, b.bs[b.i:])
	b.i += n
	return n, nil
}

func (b *bytesReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		b.i = int(offset)
	case io.SeekCurrent:
		b.i += int(offset)
	case io.SeekEnd:
		b.i = len(b.bs) + int(offset)
	}
	if b.i < 0 {
		b.i = 0
	}
	if b.i > len(b.bs) {
		b.i = len(b.bs)
	}
	return int64(b.i), nil
}

type errReader struct{ err error }

func (e *errReader) Read(p []byte) (int, error) { return 0, e.err }
func (e *errReader) Seek(int64, int) (int64, error) {
	return 0, e.err
}
