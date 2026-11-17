package server

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Unpacking the zip of a design (Web Builder › Import HTML site). The archive is untrusted input:
//
//   - the upload is streamed to a file, never held in memory, and unpacked entry by entry;
//   - a path that leaves the folder (".." segment, absolute path, drive letter, backslashes treated
//     as slashes, NUL), a symbolic link and any entry that is not a plain file or folder reject the
//     whole archive;
//   - limits are the design kit's (100 MB, 2000 files, 15 MB a file); the declared sizes are checked
//     first and the bytes actually written are counted, so a lying header cannot get past them;
//   - only what a kit may hold is accepted (wbKitRel: plain names, web asset extensions) plus .html
//     and .htm, which are kept aside for the importer and never go into a kit. Folders and files
//     whose name starts with a dot (.git, .DS_Store, .htaccess), __MACOSX, Thumbs.db and desktop.ini
//     are left out silently;
//   - one top folder that holds everything (the usual "zip the folder") is stripped.

// wbZipError is a problem with the archive that the user can fix; its text is shown as it is.
type wbZipError struct{ msg string }

func (e *wbZipError) Error() string { return e.msg }

func zipErrf(format string, a ...any) error { return &wbZipError{fmt.Sprintf(format, a...)} }

// wbExtracted is what was unpacked: the kit's files (assets) and the HTML pages, path → size.
type wbExtracted struct {
	Assets map[string]int64
	HTML   map[string]int64
	Bytes  int64
}

// wbZipLimits bounds an archive; tests lower them.
type wbZipLimits struct {
	Files int
	Total int64
	File  int64
}

var wbZipDefaultLimits = wbZipLimits{Files: wbKitMaxFiles, Total: wbKitMaxTotal, File: wbKitMaxFile}

func wbZipIgnored(segs []string) bool {
	for _, s := range segs {
		l := strings.ToLower(s)
		if strings.HasPrefix(s, ".") || l == "__macosx" || l == "thumbs.db" || l == "desktop.ini" {
			return true
		}
	}
	return false
}

// wbZipHTMLRel checks the path of an HTML page: plain, no control characters, as deep as a kit file.
func wbZipHTMLRel(p string) bool {
	segs := strings.Split(p, "/")
	if len(p) > 512 || len(segs) > 8 {
		return false
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." || len(s) > 128 || strings.ContainsAny(s, "\x00<>:\"|?*") {
			return false
		}
		for _, r := range s {
			if r < 0x20 || r == 0x7f {
				return false
			}
		}
	}
	return true
}

type wbZipFile struct {
	f    *zip.File
	segs []string
}

// wbZipExtract unpacks the zip at zipPath into dst (which must exist and be empty).
func wbZipExtract(zipPath, dst string, lim wbZipLimits) (*wbExtracted, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, zipErrf("That file is not a readable zip archive.")
	}
	defer zr.Close()
	if len(zr.File) > 10*lim.Files+1000 {
		return nil, zipErrf("The zip holds too many entries.")
	}
	var keep []wbZipFile
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.ContainsRune(name, 0) || strings.HasPrefix(name, "/") || (len(name) > 1 && name[1] == ':') {
			return nil, zipErrf("The zip holds an absolute or invalid path: %s", truncRunes(strings.ToValidUTF8(name, "?"), 80))
		}
		var segs []string
		for _, s := range strings.Split(name, "/") {
			if s == ".." {
				return nil, zipErrf("The zip holds a path that leaves its folder (%s).", truncRunes(strings.ToValidUTF8(name, "?"), 80))
			}
			if s != "" && s != "." {
				segs = append(segs, s)
			}
		}
		mode := f.Mode()
		if mode&os.ModeSymlink != 0 {
			return nil, zipErrf("The zip holds a symbolic link (%s); links are not allowed.", truncRunes(strings.ToValidUTF8(name, "?"), 80))
		}
		if f.FileInfo().IsDir() || strings.HasSuffix(name, "/") {
			continue
		}
		if !mode.IsRegular() {
			return nil, zipErrf("The zip holds an entry that is not a plain file (%s).", truncRunes(strings.ToValidUTF8(name, "?"), 80))
		}
		if len(segs) == 0 || wbZipIgnored(segs) {
			continue
		}
		keep = append(keep, wbZipFile{f, segs})
	}
	// One folder around everything: strip it (repeatedly, "site.zip" → "site/" → "html/").
	for strip := 0; strip < 4 && len(keep) > 0; strip++ {
		first := keep[0].segs[0]
		ok := true
		for _, k := range keep {
			if len(k.segs) < 2 || k.segs[0] != first {
				ok = false
				break
			}
		}
		if !ok {
			break
		}
		for i := range keep {
			keep[i].segs = keep[i].segs[1:]
		}
	}
	if len(keep) == 0 {
		return nil, zipErrf("The zip holds no files.")
	}
	if len(keep) > lim.Files {
		return nil, zipErrf("The zip holds %d files; at most %d are allowed.", len(keep), lim.Files)
	}
	out := &wbExtracted{Assets: map[string]int64{}, HTML: map[string]int64{}}
	var bad []string
	var declared int64
	seen := map[string]string{}
	type item struct {
		f    *zip.File
		rel  string
		html bool
	}
	var items []item
	for _, k := range keep {
		rel := strings.Join(k.segs, "/")
		ext := strings.ToLower(path.Ext(rel))
		isHTML := ext == ".html" || ext == ".htm"
		if isHTML {
			if !wbZipHTMLRel(rel) {
				bad = append(bad, rel)
				continue
			}
		} else if _, ok := wbKitRel(rel); !ok {
			bad = append(bad, rel)
			continue
		}
		if prev, dup := seen[strings.ToLower(rel)]; dup {
			return nil, zipErrf("The zip holds two files that differ only in letter case: %s and %s.", truncRunes(prev, 80), truncRunes(rel, 80))
		}
		seen[strings.ToLower(rel)] = rel
		if k.f.UncompressedSize64 > uint64(lim.File) {
			return nil, zipErrf("%s is larger than %d MB.", truncRunes(rel, 80), lim.File>>20)
		}
		declared += int64(k.f.UncompressedSize64)
		if declared > lim.Total {
			return nil, zipErrf("The zip unpacks to more than %d MB.", lim.Total>>20)
		}
		items = append(items, item{k.f, rel, isHTML})
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		list := bad
		more := ""
		if len(list) > 8 {
			more = fmt.Sprintf(" (and %d more)", len(list)-8)
			list = list[:8]
		}
		return nil, zipErrf("The zip holds files that a design kit cannot keep: %s%s. Only web assets (CSS, JavaScript, fonts, images, video, audio, JSON, PDF) with plain file names (letters, digits, . _ - @ +, no spaces) and the HTML pages are accepted; take the others out of the zip.",
			truncRunes(strings.ToValidUTF8(strings.Join(list, ", "), "?"), 400), more)
	}
	base, err := filepath.Abs(dst)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		target := filepath.Join(base, filepath.FromSlash(it.rel))
		if r, err := filepath.Rel(base, target); err != nil || r == "." || strings.HasPrefix(r, "..") {
			return nil, zipErrf("The zip holds a path that leaves its folder (%s).", truncRunes(it.rel, 80))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return nil, err
		}
		n, err := wbZipCopy(it.f, target, lim.File, lim.Total-out.Bytes)
		if err != nil {
			return nil, err
		}
		out.Bytes += n
		if it.html {
			out.HTML[it.rel] = n
		} else {
			out.Assets[it.rel] = n
		}
	}
	return out, nil
}

// wbZipCopy writes one entry to target (a new file), counting the bytes really written.
func wbZipCopy(f *zip.File, target string, maxFile, maxLeft int64) (int64, error) {
	rc, err := f.Open()
	if err != nil {
		return 0, zipErrf("%s cannot be read from the zip.", truncRunes(f.Name, 80))
	}
	defer rc.Close()
	w, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	limit := min(maxFile, maxLeft)
	n, err := io.Copy(w, io.LimitReader(rc, limit+1))
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		var ze *wbZipError
		if errors.As(err, &ze) {
			return 0, err
		}
		return 0, zipErrf("%s cannot be read from the zip.", truncRunes(f.Name, 80))
	}
	if n > limit {
		if maxLeft < maxFile {
			return 0, zipErrf("The zip unpacks to more than the allowed size.")
		}
		return 0, zipErrf("%s is larger than %d MB.", truncRunes(f.Name, 80), maxFile>>20)
	}
	return n, nil
}
