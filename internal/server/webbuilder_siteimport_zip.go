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

	"wpgo/internal/media"
	"wpgo/internal/webbuilder"
)

// Unpacking the zip of a design (Web Builder › Import HTML site). The archive is untrusted input:
//
//   - the upload is streamed to a file, never held in memory, and read entry by entry;
//   - a path that leaves the folder (".." segment, absolute path, drive letter, backslashes treated
//     as slashes, NUL), a symbolic link and any entry that is not a plain file or folder reject the
//     whole archive, and so do two names that differ only in letter case;
//   - only the files the pages use are unpacked (webbuilder.SiteReferences: the pages' URLs, the
//     stylesheets' url() and @import, the scripts' and JSON's string literals naming a file, followed
//     transitively), so a saved copy of a live site with every size of every image and whole plugin
//     folders keeps what it shows; the others are left out and listed. HTML pages never go into a
//     kit and do not count towards its limits;
//   - bounds (wbZipLimits): entries in the archive, the bytes read from it in all (a zip bomb runs
//     into it: every byte read is counted, the declared sizes are only a first check), a page, a kit
//     file, the kit's files and bytes; a used file larger than a kit file but within the Media
//     Library's upload limit (a video) is unpacked for the Media Library (the import puts it there
//     and points the pages at it), a larger one is left out and listed;
//   - a used file a kit cannot keep (extension outside the kit's list, a name outside wbKitRel) is
//     left out and listed (the pages report it missing); unused ones are just left out. Folders and
//     files whose name starts with a dot (.git, .DS_Store, .htaccess), __MACOSX, Thumbs.db and
//     desktop.ini are skipped silently;
//   - one top folder that holds everything (the usual "zip the folder") is stripped.

// wbZipError is a problem with the archive that the user can fix; its text is shown as it is.
type wbZipError struct{ msg string }

func (e *wbZipError) Error() string { return e.msg }

func zipErrf(format string, a ...any) error { return &wbZipError{fmt.Sprintf(format, a...)} }

// wbExtracted is what was unpacked: the kit's files (assets), the files for the Media Library
// (media) and the HTML pages, path → size; and what was left out.
type wbExtracted struct {
	Assets map[string]int64
	Media  map[string]int64
	HTML   map[string]int64
	Bytes  int64 // the kit's bytes
	// Skipped: files no page uses; TooLarge: used files over the Media Library's limit (or over a
	// kit file's and not media); Unkeepable: used files a kit cannot hold. "path (size)".
	Skipped, TooLarge, Unkeepable []string
	SkippedBytes                  int64
}

// wbZipLimits bounds an archive; tests lower them.
type wbZipLimits struct {
	Entries int   // entries in the archive
	Files   int   // kit files kept
	Total   int64 // kit bytes kept
	File    int64 // one kit file
	Media   int64 // one file for the Media Library
	Page    int64 // one HTML page
	Read    int64 // bytes read from the archive in all
}

const (
	wbZipMaxEntries = 20000
	wbZipMaxRead    = 1 << 30
)

var wbZipDefaultLimits = wbZipLimits{Entries: wbZipMaxEntries, Files: wbKitMaxFiles, Total: wbKitMaxTotal, File: wbKitMaxFile,
	Media: maxUploadBytes, Page: wbWizMaxPageBytes, Read: wbZipMaxRead}

// wbZipMediaExts are the files that go to the Media Library when they are too large for a kit.
var wbZipMediaExts = map[string]bool{".mp4": true, ".webm": true, ".mp3": true, ".ogg": true, ".wav": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".avif": true, ".pdf": true}

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

func wbSizeLabel(rel string, n int64) string {
	return fmt.Sprintf("%s (%s)", rel, media.HumanSize(n))
}

// wbZipExtract unpacks what the pages of the zip at zipPath use into dst (which must exist and be
// empty); see the file comment.
func wbZipExtract(zipPath, dst string, lim wbZipLimits) (*wbExtracted, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, zipErrf("That file is not a readable zip archive.")
	}
	defer zr.Close()
	if len(zr.File) > lim.Entries {
		return nil, zipErrf("The zip holds too many entries (%d; at most %d).", len(zr.File), lim.Entries)
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
	out := &wbExtracted{Assets: map[string]int64{}, Media: map[string]int64{}, HTML: map[string]int64{}}
	seen := map[string]string{}
	entries := map[string]*zip.File{}
	sizes := map[string]int64{}
	var pages []webbuilder.SitePage
	var badPages []string
	var read int64
	readEntry := func(f *zip.File, max int64) ([]byte, error) {
		rc, err := f.Open()
		if err != nil {
			return nil, zipErrf("%s cannot be read from the zip.", truncRunes(f.Name, 80))
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, min(max, lim.Read-read)+1))
		read += int64(len(b))
		if err != nil {
			return nil, zipErrf("%s cannot be read from the zip.", truncRunes(f.Name, 80))
		}
		if read > lim.Read {
			return nil, zipErrf("The zip unpacks to more than %d MB.", lim.Read>>20)
		}
		if int64(len(b)) > max {
			return nil, zipErrf("%s is larger than %d MB.", truncRunes(f.Name, 80), max>>20)
		}
		return b, nil
	}
	for _, k := range keep {
		rel := strings.Join(k.segs, "/")
		if prev, dup := seen[strings.ToLower(rel)]; dup {
			return nil, zipErrf("The zip holds two files that differ only in letter case: %s and %s.", truncRunes(prev, 80), truncRunes(rel, 80))
		}
		seen[strings.ToLower(rel)] = rel
		ext := strings.ToLower(path.Ext(rel))
		if ext == ".html" || ext == ".htm" {
			if !wbZipHTMLRel(rel) {
				badPages = append(badPages, rel)
				continue
			}
			if k.f.UncompressedSize64 > uint64(lim.Page) {
				return nil, zipErrf("%s is larger than %d MB.", truncRunes(rel, 80), lim.Page>>20)
			}
			b, err := readEntry(k.f, lim.Page)
			if err != nil {
				return nil, err
			}
			pages = append(pages, webbuilder.SitePage{Path: rel, HTML: string(b)})
			continue
		}
		entries[rel] = k.f
		sizes[rel] = int64(min(k.f.UncompressedSize64, uint64(1)<<62))
	}
	if len(badPages) > 0 {
		sort.Strings(badPages)
		return nil, zipErrf("The zip holds pages with names that cannot be imported: %s.", truncRunes(strings.ToValidUTF8(strings.Join(wbFirst(badPages, 8), ", "), "?"), 400))
	}
	// What the pages use (CSS, JS and JSON are read for what they name).
	var readErr error
	refs := webbuilder.SiteReferences(pages, sizes, func(rel string) ([]byte, bool) {
		f := entries[rel]
		if f == nil || readErr != nil || sizes[rel] > lim.File {
			return nil, false
		}
		b, err := readEntry(f, lim.File)
		if err != nil {
			readErr = err
			return nil, false
		}
		return b, true
	})
	if readErr != nil {
		return nil, readErr
	}
	rels := make([]string, 0, len(sizes))
	for rel := range sizes {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	type item struct {
		rel   string
		max   int64
		media bool
	}
	var items []item
	var kitFiles int
	var kitBytes int64
	for _, rel := range rels {
		n := sizes[rel]
		if !refs.Used[rel] {
			out.Skipped = append(out.Skipped, wbSizeLabel(rel, n))
			out.SkippedBytes += n
			continue
		}
		if _, ok := wbKitRel(rel); !ok {
			out.Unkeepable = append(out.Unkeepable, rel)
			continue
		}
		switch {
		case n <= lim.File:
			items = append(items, item{rel, lim.File, false})
			kitFiles++
			kitBytes += n
		case n <= lim.Media && wbZipMediaExts[strings.ToLower(path.Ext(rel))]:
			items = append(items, item{rel, lim.Media, true})
		default:
			out.TooLarge = append(out.TooLarge, wbSizeLabel(rel, n))
		}
	}
	if kitFiles > lim.Files || kitBytes > lim.Total {
		// The largest used files, so the user knows what to take out.
		sort.SliceStable(items, func(i, j int) bool { return sizes[items[i].rel] > sizes[items[j].rel] })
		var big []string
		for _, it := range items {
			if !it.media && len(big) < 8 {
				big = append(big, wbSizeLabel(it.rel, sizes[it.rel]))
			}
		}
		return nil, zipErrf("The pages use %d files (%d MB); a design kit holds at most %d files and %d MB. The largest: %s.",
			kitFiles, kitBytes>>20, lim.Files, lim.Total>>20, strings.Join(big, ", "))
	}
	base, err := filepath.Abs(dst)
	if err != nil {
		return nil, err
	}
	write := func(rel string, f *zip.File, max int64, body []byte) (int64, error) {
		target := filepath.Join(base, filepath.FromSlash(rel))
		if r, err := filepath.Rel(base, target); err != nil || r == "." || strings.HasPrefix(r, "..") {
			return 0, zipErrf("The zip holds a path that leaves its folder (%s).", truncRunes(rel, 80))
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, err
		}
		if body != nil {
			return int64(len(body)), os.WriteFile(target, body, 0o644)
		}
		n, err := wbZipCopy(f, target, max, lim.Read-read)
		read += n
		return n, err
	}
	for _, p := range pages {
		n, err := write(p.Path, nil, 0, []byte(p.HTML))
		if err != nil {
			return nil, err
		}
		out.HTML[p.Path] = max(n, 1)
	}
	for _, it := range items {
		n, err := write(it.rel, entries[it.rel], it.max, nil)
		if err != nil {
			return nil, err
		}
		if it.media {
			out.Media[it.rel] = n
		} else {
			out.Assets[it.rel] = n
			out.Bytes += n
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
