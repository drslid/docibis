// Package backup makes, keeps and restores the backups of the database: every
// table in a portable stream (store.Dump), compressed, then encrypted with a
// key derived from APP_SECRET_KEY. They are kept next to the continuous copy
// (STATE_URL): in its bucket, or in its folder.
package backup

import (
	"bufio"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

// The file is the magic, a random salt, then chunks of 64 KiB sealed with
// AES-256-GCM. The nonce numbers the chunk and marks the last one, so a file
// cut, reordered or modified does not open.
const (
	magic     = "DIBK\x01"
	saltSize  = 32
	chunkSize = 64 << 10
)

// ErrKey means the backup was made with another APP_SECRET_KEY, or damaged.
var ErrKey = errors.New("backup cannot be opened: made with another APP_SECRET_KEY, or damaged")

func aead(secret, salt []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, secret, salt, "docibis backup v1", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonce(n uint64, last bool) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint64(b[3:11], n)
	if last {
		b[11] = 1
	}
	return b
}

type sealer struct {
	w    io.Writer
	gcm  cipher.AEAD
	buf  []byte
	n    uint64
	done bool
}

// Seal encrypts what is written to the returned writer; Close writes the
// last chunk and must be called.
func Seal(w io.Writer, secret []byte) (io.WriteCloser, error) {
	salt := make([]byte, saltSize)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	gcm, err := aead(secret, salt)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(append([]byte(magic), salt...)); err != nil {
		return nil, err
	}
	return &sealer{w: w, gcm: gcm, buf: make([]byte, 0, chunkSize)}, nil
}

func (s *sealer) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		if len(s.buf) == chunkSize {
			if err := s.flush(false); err != nil {
				return n, err
			}
		}
		k := copy(s.buf[len(s.buf):chunkSize], p)
		s.buf = s.buf[:len(s.buf)+k]
		p = p[k:]
		n += k
	}
	return n, nil
}

func (s *sealer) flush(last bool) error {
	out := s.gcm.Seal(nil, nonce(s.n, last), s.buf, nil)
	s.n++
	s.buf = s.buf[:0]
	_, err := s.w.Write(out)
	return err
}

func (s *sealer) Close() error {
	if s.done {
		return nil
	}
	s.done = true
	return s.flush(true)
}

type opener struct {
	r   *bufio.Reader
	gcm cipher.AEAD
	in  []byte
	out []byte
	n   uint64
	err error
}

// Open decrypts a backup written by Seal. A wrong key or a damaged file gives
// ErrKey while reading.
func Open(r io.Reader, secret []byte) (io.Reader, error) {
	head := make([]byte, len(magic)+saltSize)
	if _, err := io.ReadFull(r, head); err != nil || string(head[:len(magic)]) != magic {
		return nil, errors.New("not a DocIbis backup")
	}
	gcm, err := aead(secret, head[len(magic):])
	if err != nil {
		return nil, err
	}
	size := chunkSize + gcm.Overhead()
	return &opener{r: bufio.NewReaderSize(r, size+1), gcm: gcm, in: make([]byte, size)}, nil
}

func (o *opener) Read(p []byte) (int, error) {
	for len(o.out) == 0 {
		if o.err != nil {
			return 0, o.err
		}
		o.next()
	}
	n := copy(p, o.out)
	o.out = o.out[n:]
	return n, nil
}

func (o *opener) next() {
	n, err := io.ReadFull(o.r, o.in)
	var last bool
	switch {
	case err == io.ErrUnexpectedEOF:
		last = true
	case err == io.EOF:
		// Nothing after a chunk that was not the last: the file was cut.
		o.err = ErrKey
		return
	case err != nil:
		o.err = err
		return
	default:
		_, err := o.r.Peek(1)
		if err != nil && err != io.EOF {
			o.err = err
			return
		}
		last = err == io.EOF
	}
	plain, err := o.gcm.Open(nil, nonce(o.n, last), o.in[:n], nil)
	if err != nil {
		o.err = ErrKey
		return
	}
	o.n++
	o.out = plain
	if last {
		o.err = io.EOF
	}
}
