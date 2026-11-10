package cache

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// On-disk format of one entry file:
//
//	magic "WPGC1\n" | uint32 big-endian length of meta | gob(diskMeta) | gob(Entry)
//
// The length-prefixed metadata lets startup index keys, tags and expiry without
// decoding bodies.
const (
	diskMagic = "WPGC1\n"
	diskExt   = ".wpc"
)

type diskMeta struct {
	Key     string
	Tags    []string
	Expires time.Time
}

type diskStore struct{ dir string }

func openDisk(dir string) (*diskStore, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("cache: creating disk dir: %w", err)
	}
	return &diskStore{dir: dir}, nil
}

func (d *diskStore) path(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(d.dir, hex.EncodeToString(sum[:])+diskExt)
}

// save writes e atomically: to a temp file in the same directory, synced, then renamed.
func (d *diskStore) save(e *Entry) error {
	var meta bytes.Buffer
	if err := gob.NewEncoder(&meta).Encode(diskMeta{Key: e.Key, Tags: e.Tags, Expires: e.Expires}); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(d.dir, ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op after a successful rename
	w := bufio.NewWriter(tmp)
	w.WriteString(diskMagic)
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], uint32(meta.Len()))
	w.Write(n[:])
	w.Write(meta.Bytes())
	if err := gob.NewEncoder(w).Encode(e); err != nil {
		tmp.Close()
		return err
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), d.path(e.Key))
}

func readMeta(r *bufio.Reader) (diskMeta, error) {
	var m diskMeta
	magic := make([]byte, len(diskMagic))
	if _, err := io.ReadFull(r, magic); err != nil || string(magic) != diskMagic {
		return m, errors.New("cache: bad entry file")
	}
	var n [4]byte
	if _, err := io.ReadFull(r, n[:]); err != nil {
		return m, err
	}
	size := binary.BigEndian.Uint32(n[:])
	if size > 1<<20 {
		return m, errors.New("cache: entry metadata too large")
	}
	buf := make([]byte, size)
	if _, err := io.ReadFull(r, buf); err != nil {
		return m, err
	}
	err := gob.NewDecoder(bytes.NewReader(buf)).Decode(&m)
	return m, err
}

// load reads the full entry for key; a missing file returns (nil, nil).
func (d *diskStore) load(key string) (*Entry, error) {
	f, err := os.Open(d.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := bufio.NewReader(f)
	m, err := readMeta(r)
	if err != nil {
		return nil, err
	}
	if m.Key != key {
		return nil, nil // hash collision or stale file
	}
	var e Entry
	if err := gob.NewDecoder(r).Decode(&e); err != nil {
		return nil, err
	}
	return &e, nil
}

// scan calls fn with the metadata of every readable entry file; corrupt files and
// leftover temp files are removed.
func (d *diskStore) scan(fn func(m diskMeta, size int64)) error {
	des, err := os.ReadDir(d.dir)
	if err != nil {
		return err
	}
	for _, de := range des {
		name := de.Name()
		full := filepath.Join(d.dir, name)
		if strings.HasPrefix(name, ".tmp-") {
			os.Remove(full)
			continue
		}
		if de.IsDir() || !strings.HasSuffix(name, diskExt) {
			continue
		}
		f, err := os.Open(full)
		if err != nil {
			continue
		}
		var size int64
		if fi, err := f.Stat(); err == nil {
			size = fi.Size()
		}
		m, err := readMeta(bufio.NewReaderSize(f, 4096))
		f.Close()
		if err != nil || d.path(m.Key) != full {
			os.Remove(full)
			continue
		}
		fn(m, size)
	}
	return nil
}

func (d *diskStore) remove(key string) { os.Remove(d.path(key)) }

// clear removes every entry file (and temp file) in the directory.
func (d *diskStore) clear() {
	des, err := os.ReadDir(d.dir)
	if err != nil {
		return
	}
	for _, de := range des {
		name := de.Name()
		if !de.IsDir() && (strings.HasSuffix(name, diskExt) || strings.HasPrefix(name, ".tmp-")) {
			os.Remove(filepath.Join(d.dir, name))
		}
	}
}
