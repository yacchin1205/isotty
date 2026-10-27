package runtimecfg

import (
	"bytes"
	"crypto/rand"
	"io"
	"strings"
	"testing"
)

func sealAll(t *testing.T, key, plain []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w, err := NewSealWriter(key, &out)
	if err != nil {
		t.Fatal(err)
	}
	// Odd write sizes so chunk boundaries fall inside writes.
	for i := 0; i < len(plain); i += 700_001 {
		end := i + 700_001
		if end > len(plain) {
			end = len(plain)
		}
		if _, err := w.Write(plain[i:end]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func openAll(key, sealed []byte) ([]byte, error) {
	r, err := NewOpenReader(key, bytes.NewReader(sealed))
	if err != nil {
		return nil, err
	}
	return io.ReadAll(r)
}

func TestSealedStreamRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	for _, size := range []int{0, 1, streamChunkSize, streamChunkSize + 1, 2*streamChunkSize + 12345} {
		plain := make([]byte, size)
		rand.Read(plain)
		sealed := sealAll(t, key, plain)
		if size >= 64 && bytes.Contains(sealed, plain[:64]) {
			t.Fatalf("size %d: plaintext visible in the stream", size)
		}
		got, err := openAll(key, sealed)
		if err != nil || !bytes.Equal(got, plain) {
			t.Fatalf("size %d: open = %d bytes, %v", size, len(got), err)
		}
	}
}

func TestSealedStreamRejectsDamage(t *testing.T) {
	key := make([]byte, 32)
	rand.Read(key)
	plain := make([]byte, 2*streamChunkSize+100)
	rand.Read(plain)
	sealed := sealAll(t, key, plain)
	header := len(streamMagic) + streamPrefixLen
	chunk := 4 + streamChunkSize + 16

	other := make([]byte, 32)
	rand.Read(other)
	if _, err := openAll(other, sealed); err == nil || !strings.Contains(err.Error(), "does not decrypt") {
		t.Fatalf("wrong key error = %v", err)
	}
	truncated := sealed[:header+chunk]
	if _, err := openAll(key, truncated); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated after a whole chunk error = %v", err)
	}
	if _, err := openAll(key, sealed[:len(sealed)-1]); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("truncated inside the last chunk error = %v", err)
	}
	flipped := append([]byte(nil), sealed...)
	flipped[header+4+10] ^= 1
	if _, err := openAll(key, flipped); err == nil || !strings.Contains(err.Error(), "does not decrypt") {
		t.Fatalf("flipped byte error = %v", err)
	}
	// Swapping the first two chunks keeps every byte authentic but out of place.
	swapped := append([]byte(nil), sealed[:header]...)
	swapped = append(swapped, sealed[header+chunk:header+2*chunk]...)
	swapped = append(swapped, sealed[header:header+chunk]...)
	swapped = append(swapped, sealed[header+2*chunk:]...)
	if _, err := openAll(key, swapped); err == nil || !strings.Contains(err.Error(), "does not decrypt") {
		t.Fatalf("swapped chunks error = %v", err)
	}
	if _, err := NewOpenReader(key, strings.NewReader("not a sealed stream at all")); err == nil || !strings.Contains(err.Error(), "not a sealed isotty stream") {
		t.Fatalf("foreign data error = %v", err)
	}
}
