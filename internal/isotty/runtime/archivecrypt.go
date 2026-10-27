package runtimecfg

import (
	"bytes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// A sealed stream is for archives too large to hold in memory twice: the
// audit log of a long session. It is a header followed by AES-256-GCM
// chunks, each authenticated on its own and bound to its position, so a
// chunk cannot be dropped, repeated or reordered, and the last one is
// marked, so a truncated file is told from a complete one.
//
//	magic "ISOTTYENC1" | 8-byte random nonce prefix
//	repeated: 4-byte big-endian ciphertext length | ciphertext
//
// The nonce of chunk i is prefix || i (4 bytes, big-endian); the additional
// data is one byte, 0 for a chunk that is followed by another and 1 for the
// last. A chunk holds up to streamChunkSize bytes of plaintext; the last
// may be empty.
const (
	streamMagic     = "ISOTTYENC1"
	streamPrefixLen = 8
	streamChunkSize = 1 << 20
)

var (
	streamMore  = []byte{0}
	streamFinal = []byte{1}
)

type sealWriter struct {
	w       io.Writer
	gcm     cipher.AEAD
	prefix  []byte
	counter uint32
	buf     bytes.Buffer
	closed  bool
}

// NewSealWriter encrypts what is written to it under key and writes the
// sealed stream to w. Close writes the final chunk; a stream without it
// does not open.
func NewSealWriter(key []byte, w io.Writer) (io.WriteCloser, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	prefix := make([]byte, streamPrefixLen)
	if _, err := rand.Read(prefix); err != nil {
		return nil, err
	}
	if _, err := w.Write(append([]byte(streamMagic), prefix...)); err != nil {
		return nil, err
	}
	return &sealWriter{w: w, gcm: gcm, prefix: prefix}, nil
}

func (s *sealWriter) Write(p []byte) (int, error) {
	if s.closed {
		return 0, errors.New("write to a closed sealed stream")
	}
	n := len(p)
	for len(p) > 0 {
		room := streamChunkSize - s.buf.Len()
		if room > len(p) {
			room = len(p)
		}
		s.buf.Write(p[:room])
		p = p[room:]
		if s.buf.Len() == streamChunkSize {
			if err := s.flush(streamMore); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

func (s *sealWriter) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.flush(streamFinal)
}

func (s *sealWriter) flush(flag []byte) error {
	if s.counter == ^uint32(0) {
		return errors.New("sealed stream is too long")
	}
	nonce := make([]byte, s.gcm.NonceSize())
	copy(nonce, s.prefix)
	binary.BigEndian.PutUint32(nonce[streamPrefixLen:], s.counter)
	s.counter++
	sealed := s.gcm.Seal(nil, nonce, s.buf.Bytes(), flag)
	s.buf.Reset()
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(sealed)))
	if _, err := s.w.Write(length[:]); err != nil {
		return err
	}
	_, err := s.w.Write(sealed)
	return err
}

type openReader struct {
	r       io.Reader
	gcm     cipher.AEAD
	prefix  []byte
	counter uint32
	plain   bytes.Reader
	done    bool
}

// NewOpenReader decrypts a stream written by NewSealWriter. A stream that
// ends before its final chunk, or whose chunks were changed, reordered or
// sealed under another key, fails with an error rather than yielding
// partial data silently.
func NewOpenReader(key []byte, r io.Reader) (io.Reader, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	header := make([]byte, len(streamMagic)+streamPrefixLen)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, fmt.Errorf("read sealed stream header: %w", err)
	}
	if string(header[:len(streamMagic)]) != streamMagic {
		return nil, errors.New("not a sealed isotty stream")
	}
	return &openReader{r: r, gcm: gcm, prefix: header[len(streamMagic):]}, nil
}

func (o *openReader) Read(p []byte) (int, error) {
	for o.plain.Len() == 0 {
		if o.done {
			return 0, io.EOF
		}
		if err := o.next(); err != nil {
			return 0, err
		}
	}
	return o.plain.Read(p)
}

func (o *openReader) next() error {
	var length [4]byte
	if _, err := io.ReadFull(o.r, length[:]); err != nil {
		return fmt.Errorf("sealed stream is truncated: %w", err)
	}
	size := binary.BigEndian.Uint32(length[:])
	if size > streamChunkSize+uint32(o.gcm.Overhead()) {
		return errors.New("sealed stream chunk is too long")
	}
	sealed := make([]byte, size)
	if _, err := io.ReadFull(o.r, sealed); err != nil {
		return fmt.Errorf("sealed stream is truncated: %w", err)
	}
	nonce := make([]byte, o.gcm.NonceSize())
	copy(nonce, o.prefix)
	binary.BigEndian.PutUint32(nonce[streamPrefixLen:], o.counter)
	o.counter++
	plain, err := o.gcm.Open(nil, nonce, sealed, streamMore)
	if err != nil {
		plain, err = o.gcm.Open(nil, nonce, sealed, streamFinal)
		if err != nil {
			return errors.New("sealed stream does not decrypt: wrong key, or the data was changed")
		}
		o.done = true
	}
	o.plain.Reset(plain)
	return nil
}
