package redmine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

// extractTimeout bounds turning one attachment into text, OCR included.
const extractTimeout = 5 * time.Minute

// ocrLanguages are the Tesseract models the host needs: Japanese text with
// English words in it.
const ocrLanguages = "jpn+eng"

// imageTypes are the image formats Tesseract reads.
var imageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true,
	"image/tiff": true, "image/bmp": true, "image/webp": true,
}

// extractText turns a PDF or an image into its text on the host, so that
// the text filter can read it: the text layer of each PDF page, OCR for a
// page without one and for an image. The tools are poppler's pdftotext
// and pdftoppm, and tesseract with the jpn model.
func extractText(content []byte, isPDF bool) (string, error) {
	dir, err := os.MkdirTemp("", "isotty-redmine-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
	defer cancel()
	in := filepath.Join(dir, "in")
	if err := os.WriteFile(in, content, 0o600); err != nil {
		return "", err
	}
	if !isPDF {
		return ocr(ctx, in)
	}
	layer, err := run(ctx, "pdftotext", "-layout", in, "-")
	if err != nil {
		return "", err
	}
	// pdftotext ends every page with a form feed, which the answer keeps.
	pages := strings.Split(strings.TrimSuffix(layer, "\f"), "\f")
	for i, page := range pages {
		if strings.TrimSpace(page) != "" {
			continue
		}
		n := strconv.Itoa(i + 1)
		prefix := filepath.Join(dir, "page")
		if _, err := run(ctx, "pdftoppm", "-f", n, "-l", n, "-r", "300", "-png", "-singlefile", in, prefix); err != nil {
			return "", err
		}
		if pages[i], err = ocr(ctx, prefix+".png"); err != nil {
			return "", err
		}
	}
	return strings.Join(pages, "\f"), nil
}

// decodeText turns a text attachment into UTF-8, so that the filter reads
// what the VM would: UTF-8 as it is, UTF-16 by its byte order mark, and
// otherwise Shift_JIS or EUC-JP when exactly one of them decodes it
// without an error. A text both decode, or neither, is refused rather
// than guessed: decoded the wrong way, its names would pass as mojibake.
// So is one with NUL bytes, which UTF-16 without a byte order mark has
// and UTF-8 lets through.
func decodeText(content []byte) (string, error) {
	if bytes.HasPrefix(content, []byte{0xFF, 0xFE}) || bytes.HasPrefix(content, []byte{0xFE, 0xFF}) {
		return decodeStrict(unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM), content)
	}
	if bytes.IndexByte(content, 0) >= 0 {
		return "", errors.New("it holds NUL bytes, as UTF-16 without a byte order mark does")
	}
	if utf8.Valid(content) {
		return string(content), nil
	}
	var found []string
	for _, enc := range []encoding.Encoding{japanese.ShiftJIS, japanese.EUCJP} {
		if text, err := decodeStrict(enc, content); err == nil {
			found = append(found, text)
		}
	}
	if len(found) != 1 {
		return "", errors.New("its encoding is neither UTF-8 nor UTF-16 with a byte order mark, and Shift_JIS and EUC-JP do not tell it apart")
	}
	return found[0], nil
}

// decodeStrict fails where the decoder would put a replacement character:
// the encodings here have no such character of their own.
func decodeStrict(enc encoding.Encoding, content []byte) (string, error) {
	out, err := enc.NewDecoder().Bytes(content)
	if err != nil {
		return "", err
	}
	if bytes.ContainsRune(out, utf8.RuneError) || !utf8.Valid(out) {
		return "", errors.New("it does not decode")
	}
	return string(out), nil
}

func ocr(ctx context.Context, path string) (string, error) {
	return run(ctx, "tesseract", path, "stdout", "-l", ocrLanguages)
}

// run returns what the tool printed; its stderr carries diagnostics, not
// the document's text, and goes to the error when it fails.
func run(ctx context.Context, tool string, args ...string) (string, error) {
	if _, err := exec.LookPath(tool); err != nil {
		return "", fmt.Errorf("%s is not installed on the host; PDF and image attachments need poppler (pdftotext, pdftoppm) and tesseract with the jpn model", tool)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", fmt.Errorf("%s gave up after %s", tool, extractTimeout)
		}
		return "", fmt.Errorf("%s: %w: %s", tool, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
