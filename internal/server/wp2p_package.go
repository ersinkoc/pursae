package server

import (
	"archive/tar"
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/crypto/nacl/secretbox"
)

// WP to Pursae packages: a whole WordPress site in one encrypted file, built by the "WP to Pursae"
// WordPress plugin (integrations/wordpress/wp-to-pursae) and read here (wp2p_import.go). The format
// is written down in docs/developer/operations/migrate-from-wordpress.md; in short:
//
// Envelope. "PURSAEWP", a version byte (1) and a 16-byte random nonce prefix, then chunks: a
// big-endian uint32 with the length of the sealed chunk, then the chunk sealed with NaCl secretbox
// (XSalsa20-Poly1305, libsodium's crypto_secretbox) under the 32-byte package key. Chunk i is sealed
// with the nonce prefix || uint64(i), the top bit of i set on the last chunk, so chunks cannot be
// reordered, dropped or cut off: a stream that ends before a last chunk, or goes on after it, fails.
// The package holds password hashes, which is why it is encrypted at all.
//
// Plaintext. A ustar archive: manifest.json first, then users.jsonl, options.json, content.xml (the
// WXR of WordPress' export_wp()), media.jsonl with media/<attachment id> (the original upload of each
// attachment), and woo/tax_rates.jsonl, woo/coupons.jsonl, woo/orders.jsonl, woo/customers.jsonl.
// The JSON-lines files and content.xml may come in numbered parts (users.000001.jsonl,
// content.000002.xml, ...): the plugin writes a part per batch, straight into the encrypted stream,
// so nothing holding password hashes or private content is ever stored unencrypted on the WordPress
// server. Parts are read in order, after the unnumbered file if there is one.
//
// Keys. A package downloaded from the plugin has a random key the plugin shows once (43 characters
// of unpadded base64url). A package pushed with a migration token is sealed with the key derived from
// the token: HMAC-SHA256(key = token, message = "pursae-wp2p-package-key-v1").

const (
	wp2pMagic        = "PURSAEWP"
	wp2pFormat       = 1
	wp2pMaxChunk     = 8 << 20 // largest plaintext chunk
	wp2pKeyLabel     = "pursae-wp2p-package-key-v1"
	wp2pHeaderLen    = len(wp2pMagic) + 1 + 16
	wp2pLastChunkBit = uint64(1) << 63
)

// wp2pKey is the 32-byte package key.
type wp2pKey [32]byte

// wp2pParseKey reads a key as the plugin shows it: 43 characters of base64url (padding, spaces and
// line breaks are ignored) or 64 hex digits.
func wp2pParseKey(v string) (wp2pKey, error) {
	var k wp2pKey
	v = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' || r == '=' {
			return -1
		}
		return r
	}, v)
	var raw []byte
	var err error
	if len(v) == 64 {
		raw, err = hex.DecodeString(v)
	} else {
		raw, err = base64.RawURLEncoding.DecodeString(strings.NewReplacer("+", "-", "/", "_").Replace(v))
	}
	if err != nil || len(raw) != 32 {
		return k, errors.New("the package key is not valid: copy it exactly as the WP to Pursae plugin showed it")
	}
	copy(k[:], raw)
	return k, nil
}

// String is the key as the plugin shows it.
func (k wp2pKey) String() string { return base64.RawURLEncoding.EncodeToString(k[:]) }

// wp2pTokenKey is the key of a package pushed with a migration token.
func wp2pTokenKey(token string) wp2pKey {
	m := hmac.New(sha256.New, []byte(token))
	m.Write([]byte(wp2pKeyLabel))
	var k wp2pKey
	copy(k[:], m.Sum(nil))
	return k
}

var errWP2PKey = errors.New("the package cannot be opened with this key (wrong key, or the file is damaged)")

// wp2pReader decrypts a package stream.
type wp2pReader struct {
	r      *bufio.Reader
	key    wp2pKey
	prefix [16]byte
	n      uint64
	buf    []byte // decrypted bytes not read yet
	last   bool   // the last chunk was opened
	err    error
}

// newWP2PReader checks the header and returns the plaintext stream. Every chunk is authenticated
// before any of its bytes are returned.
func newWP2PReader(r io.Reader, key wp2pKey) (*wp2pReader, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	hdr := make([]byte, wp2pHeaderLen)
	if _, err := io.ReadFull(br, hdr); err != nil {
		return nil, errors.New("this is not a WP to Pursae package (it is too short)")
	}
	if string(hdr[:len(wp2pMagic)]) != wp2pMagic {
		return nil, errors.New("this is not a WP to Pursae package")
	}
	if hdr[len(wp2pMagic)] != wp2pFormat {
		return nil, fmt.Errorf("this package has format %d; this version of Pursae reads format %d (update Pursae or the plugin)", hdr[len(wp2pMagic)], wp2pFormat)
	}
	d := &wp2pReader{r: br, key: key}
	copy(d.prefix[:], hdr[len(wp2pMagic)+1:])
	return d, nil
}

func (d *wp2pReader) nonce(last bool) *[24]byte {
	var n [24]byte
	copy(n[:16], d.prefix[:])
	i := d.n
	if last {
		i |= wp2pLastChunkBit
	}
	binary.BigEndian.PutUint64(n[16:], i)
	return &n
}

func (d *wp2pReader) Read(p []byte) (int, error) {
	for len(d.buf) == 0 {
		if d.err != nil {
			return 0, d.err
		}
		if d.last {
			// Nothing may follow the last chunk.
			if _, err := d.r.ReadByte(); err != io.EOF {
				d.err = errors.New("the package has data after its end (damaged file)")
				return 0, d.err
			}
			d.err = io.EOF
			return 0, io.EOF
		}
		var lb [4]byte
		if _, err := io.ReadFull(d.r, lb[:]); err != nil {
			d.err = errors.New("the package ends too early (incomplete download or upload)")
			return 0, d.err
		}
		size := binary.BigEndian.Uint32(lb[:])
		if size < secretbox.Overhead || size > wp2pMaxChunk+secretbox.Overhead {
			d.err = errWP2PKey
			return 0, d.err
		}
		sealed := make([]byte, size)
		if _, err := io.ReadFull(d.r, sealed); err != nil {
			d.err = errors.New("the package ends too early (incomplete download or upload)")
			return 0, d.err
		}
		k := [32]byte(d.key)
		out, ok := secretbox.Open(nil, sealed, d.nonce(false), &k)
		if !ok {
			out, ok = secretbox.Open(nil, sealed, d.nonce(true), &k)
			if !ok {
				d.err = errWP2PKey
				return 0, d.err
			}
			d.last = true
		}
		d.n++
		d.buf = out
	}
	n := copy(p, d.buf)
	d.buf = d.buf[n:]
	return n, nil
}

// wp2pWriter seals a package stream (the Go twin of the plugin's writer; tests and tools use it).
type wp2pWriter struct {
	w      io.Writer
	key    wp2pKey
	prefix [16]byte
	n      uint64
	buf    []byte
	closed bool
}

func newWP2PWriter(w io.Writer, key wp2pKey, prefix [16]byte) (*wp2pWriter, error) {
	hdr := append([]byte(wp2pMagic), wp2pFormat)
	hdr = append(hdr, prefix[:]...)
	if _, err := w.Write(hdr); err != nil {
		return nil, err
	}
	return &wp2pWriter{w: w, key: key, prefix: prefix}, nil
}

func (e *wp2pWriter) seal(chunk []byte, last bool) error {
	var n [24]byte
	copy(n[:16], e.prefix[:])
	i := e.n
	if last {
		i |= wp2pLastChunkBit
	}
	binary.BigEndian.PutUint64(n[16:], i)
	k := [32]byte(e.key)
	sealed := secretbox.Seal(nil, chunk, &n, &k)
	var lb [4]byte
	binary.BigEndian.PutUint32(lb[:], uint32(len(sealed)))
	if _, err := e.w.Write(lb[:]); err != nil {
		return err
	}
	_, err := e.w.Write(sealed)
	e.n++
	return err
}

func (e *wp2pWriter) Write(p []byte) (int, error) {
	if e.closed {
		return 0, errors.New("write after close")
	}
	e.buf = append(e.buf, p...)
	for len(e.buf) > wp2pMaxChunk {
		if err := e.seal(e.buf[:wp2pMaxChunk], false); err != nil {
			return 0, err
		}
		e.buf = append([]byte(nil), e.buf[wp2pMaxChunk:]...)
	}
	return len(p), nil
}

// Close seals what is left as the last chunk.
func (e *wp2pWriter) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	return e.seal(e.buf, true)
}

// wp2pManifest is manifest.json.
type wp2pManifest struct {
	Format       int            `json:"format"`
	Generator    string         `json:"generator"` // "wp-to-pursae/1.0.0"
	SiteURL      string         `json:"site_url"`
	HomeURL      string         `json:"home_url"`
	UploadsURL   string         `json:"uploads_url"`
	WPVersion    string         `json:"wp_version"`
	WCVersion    string         `json:"wc_version,omitempty"`
	PHPVersion   string         `json:"php_version"`
	Multisite    bool           `json:"multisite"`
	OrderStorage string         `json:"order_storage,omitempty"` // "hpos" or "posts": where WooCommerce kept the orders
	CreatedGMT   string         `json:"created_gmt"`
	Counts       map[string]int `json:"counts"`
	Skipped      []wp2pSkip     `json:"skipped,omitempty"` // what the plugin left out (files over the size limit, ...)
}

// wp2pSkip is one item that was not moved, and why.
type wp2pSkip struct {
	Kind   string `json:"kind"`
	Item   string `json:"item"`
	Reason string `json:"reason"`
}

// wp2pEntryRE are the archive entries an import reads; anything else is ignored.
var wp2pEntryRE = regexp.MustCompile(`^(manifest\.json|options\.json|media/[0-9]{1,19}|(users|media|woo/(orders|coupons|customers|tax_rates))(\.[0-9]{6})?\.jsonl|content(\.[0-9]{6})?\.xml)$`)

// wp2pMaxEntry bounds one archive entry (a media file, or the WXR of a very large site).
const wp2pMaxEntry = int64(16) << 30

// wp2pExtract opens the package at pkg with key and writes its entries into dir (created, 0700).
// Entry names are checked against wp2pEntryRE, so nothing can be written outside dir. It returns the
// manifest and the names of entries it ignored.
func wp2pExtract(pkg string, key wp2pKey, dir string) (*wp2pManifest, []string, error) {
	f, err := os.Open(pkg)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	dec, err := newWP2PReader(f, key)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "media"), 0o700); err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "woo"), 0o700); err != nil {
		return nil, nil, err
	}
	tr := tar.NewReader(dec)
	var man *wp2pManifest
	var ignored []string
	first := true
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if errors.Is(err, errWP2PKey) || first {
				return nil, nil, errWP2PKey
			}
			return nil, nil, fmt.Errorf("reading the package: %w", err)
		}
		first = false
		name := strings.TrimPrefix(h.Name, "./")
		if h.Typeflag != tar.TypeReg || !wp2pEntryRE.MatchString(name) {
			if h.Typeflag != tar.TypeDir {
				ignored = append(ignored, name)
			}
			continue
		}
		if h.Size > wp2pMaxEntry {
			return nil, nil, fmt.Errorf("the package entry %s is larger than %d GB", name, wp2pMaxEntry>>30)
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return nil, nil, err
		}
		_, err = io.Copy(out, io.LimitReader(tr, wp2pMaxEntry))
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, nil, fmt.Errorf("reading the package: %w", err)
		}
		if name == "manifest.json" {
			m, err := wp2pReadManifest(dst)
			if err != nil {
				return nil, nil, err
			}
			man = m
		}
	}
	if man == nil {
		return nil, nil, errors.New("the package has no manifest.json")
	}
	return man, ignored, nil
}

func wp2pReadManifest(path string) (*wp2pManifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m wp2pManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("the package manifest is not valid JSON: %w", err)
	}
	if m.Format != wp2pFormat {
		return nil, fmt.Errorf("the package content has format %d; this version of Pursae reads format %d", m.Format, wp2pFormat)
	}
	if m.Counts == nil {
		m.Counts = map[string]int{}
	}
	return &m, nil
}

// wp2pParts are the files of the logical file path (dir/users.jsonl): the file itself if it is there,
// then its numbered parts (dir/users.000001.jsonl, ...) in order.
func wp2pParts(path string) []string {
	var out []string
	if _, err := os.Stat(path); err == nil { //nolint:gosec // path is the work directory's own file name, not input
		out = append(out, path)
	}
	ext := filepath.Ext(path)
	parts, _ := filepath.Glob(strings.TrimSuffix(path, ext) + ".[0-9][0-9][0-9][0-9][0-9][0-9]" + ext)
	sort.Strings(parts)
	return append(out, parts...)
}

// wp2pSize is the size of the logical file path (all its parts).
func wp2pSize(path string) int64 {
	var n int64
	for _, p := range wp2pParts(path) {
		if st, err := os.Stat(p); err == nil {
			n += st.Size()
		}
	}
	return n
}

// wp2pOpen opens the logical file path as one stream of its parts (os.ErrNotExist when it has none).
func wp2pOpen(path string) (io.ReadCloser, error) {
	parts := wp2pParts(path)
	if len(parts) == 0 {
		return nil, os.ErrNotExist
	}
	return &wp2pPartsReader{parts: parts}, nil
}

// wp2pPartsReader reads files one after the other, holding one open at a time.
type wp2pPartsReader struct {
	parts []string
	cur   *os.File
}

func (p *wp2pPartsReader) Read(b []byte) (int, error) {
	for {
		if p.cur == nil {
			if len(p.parts) == 0 {
				return 0, io.EOF
			}
			f, err := os.Open(p.parts[0])
			if err != nil {
				return 0, err
			}
			p.cur, p.parts = f, p.parts[1:]
		}
		n, err := p.cur.Read(b)
		if err == io.EOF {
			p.cur.Close()
			p.cur = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (p *wp2pPartsReader) Close() error {
	if p.cur != nil {
		return p.cur.Close()
	}
	return nil
}

// wp2pEachLine calls fn with every line of the JSON-lines file at path and its parts (a missing file
// has none). Lines may be long (an order with many items), so they are not limited to bufio's default
// size. It stops with ctx's error when ctx ends, so a phase interrupted by a shutdown is never taken
// for a finished one.
func wp2pEachLine(ctx context.Context, path string, fn func(raw []byte) error) error {
	for _, part := range wp2pParts(path) {
		if err := wp2pEachLineOf(ctx, part, fn); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func wp2pEachLineOf(ctx context.Context, path string, fn func(raw []byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 1<<20)
	for {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		line, err := r.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			if ferr := fn(line); ferr != nil {
				return ferr
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
