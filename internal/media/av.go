package media

import (
	"encoding/binary"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
)

// AVInfo is what wp_read_audio_metadata() / wp_read_video_metadata() keep in an attachment's
// metadata: the length in seconds, the video size and the audio tags. Zero values mean "unknown".
type AVInfo struct {
	Length        int // seconds
	Width, Height int
	Bitrate       int // bits per second
	BitrateMode   string
	Format        string // mp3, mp4, webm, wav
	Title, Artist string
	Album, Genre  string
}

// ReadAV reads the metadata of an audio or video file from its container headers (MP3/ID3, MP4/M4A/MOV,
// WebM/Matroska, WAV). A format it does not know, or a damaged file, gives an empty result.
func ReadAV(uploadsDir, relPath string) AVInfo {
	f, err := os.Open(filepath.Join(uploadsDir, filepath.FromSlash(relPath)))
	if err != nil {
		return AVInfo{}
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return AVInfo{}
	}
	return readAV(f, st.Size(), strings.ToLower(filepath.Ext(relPath)))
}

func readAV(r io.ReaderAt, size int64, ext string) (info AVInfo) {
	defer func() {
		if recover() != nil {
			info = AVInfo{}
		}
	}()
	head := make([]byte, 12)
	n, _ := r.ReadAt(head, 0)
	head = head[:n]
	switch {
	case len(head) >= 12 && string(head[4:8]) == "ftyp", ext == ".mp4" || ext == ".m4a" || ext == ".m4v" || ext == ".mov":
		return readMP4(r, size)
	case len(head) >= 4 && binary.BigEndian.Uint32(head) == 0x1A45DFA3:
		return readMatroska(r, size)
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WAVE":
		return readWAV(r, size)
	case len(head) >= 3 && string(head[:3]) == "ID3", ext == ".mp3", len(head) >= 2 && head[0] == 0xFF && head[1]&0xE0 == 0xE0:
		return readMP3(r, size)
	}
	return AVInfo{}
}

// --- MP3 -------------------------------------------------------------------------------------

var mpegBitrates = map[[2]int][15]int{ // {version(1|2), layer} -> kbps by index
	{1, 1}: {0, 32, 64, 96, 128, 160, 192, 224, 256, 288, 320, 352, 384, 416, 448},
	{1, 2}: {0, 32, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320, 384},
	{1, 3}: {0, 32, 40, 48, 56, 64, 80, 96, 112, 128, 160, 192, 224, 256, 320},
	{2, 1}: {0, 32, 48, 56, 64, 80, 96, 112, 128, 144, 160, 176, 192, 224, 256},
	{2, 2}: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
	{2, 3}: {0, 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112, 128, 144, 160},
}

func readMP3(r io.ReaderAt, size int64) AVInfo {
	info := AVInfo{Format: "mp3"}
	var start int64
	h := make([]byte, 10)
	if n, _ := r.ReadAt(h, 0); n == 10 && string(h[:3]) == "ID3" {
		tagLen := int64(h[6]&0x7f)<<21 | int64(h[7]&0x7f)<<14 | int64(h[8]&0x7f)<<7 | int64(h[9]&0x7f)
		start = 10 + tagLen
		if tagLen > 0 && tagLen < 4<<20 {
			buf := make([]byte, tagLen)
			m, _ := r.ReadAt(buf, 10)
			parseID3(&info, h[3], h[5], buf[:m])
		}
	}
	// First frame header (skip junk and padding).
	buf := make([]byte, 64<<10)
	n, _ := r.ReadAt(buf, start)
	buf = buf[:n]
	for i := 0; i+4 <= len(buf); i++ {
		if buf[i] != 0xFF || buf[i+1]&0xE0 != 0xE0 {
			continue
		}
		verBits, layerBits := (buf[i+1]>>3)&3, (buf[i+1]>>1)&3
		brIdx, srIdx := int(buf[i+2]>>4), int((buf[i+2]>>2)&3)
		if verBits == 1 || layerBits == 0 || brIdx == 0 || brIdx == 15 || srIdx == 3 {
			continue
		}
		ver := 1
		if verBits != 3 {
			ver = 2
		}
		layer := 4 - int(layerBits)
		rates := map[int][3]int{3: {44100, 48000, 32000}, 2: {22050, 24000, 16000}, 0: {11025, 12000, 8000}}[int(verBits)]
		sr := rates[srIdx]
		kbps := mpegBitrates[[2]int{ver, layer}][brIdx]
		spf := 1152
		if layer == 1 {
			spf = 384
		} else if layer == 3 && ver == 2 {
			spf = 576
		}
		info.Bitrate, info.BitrateMode = kbps*1000, "cbr"
		// A Xing/Info header (stereo/mono side-info offsets) carries the frame count.
		mono := buf[i+3]>>6 == 3
		side := map[[2]bool]int{{true, true}: 17, {true, false}: 32, {false, true}: 9, {false, false}: 17}[[2]bool{ver == 1, mono}]
		off := i + 4 + side
		if off+16 <= len(buf) {
			tag := string(buf[off : off+4])
			if tag == "Xing" || tag == "Info" {
				flags := binary.BigEndian.Uint32(buf[off+4:])
				if flags&1 != 0 && sr > 0 {
					frames := int64(binary.BigEndian.Uint32(buf[off+8:]))
					info.Length = int(math.Round(float64(frames) * float64(spf) / float64(sr)))
					if tag == "Xing" {
						info.BitrateMode = "vbr"
					}
					if flags&2 != 0 && off+16 <= len(buf) && info.Length > 0 {
						bytes := int64(binary.BigEndian.Uint32(buf[off+12:]))
						info.Bitrate = int(bytes * 8 / int64(info.Length))
					}
					return info
				}
			}
		}
		audio := size - start - int64(i)
		if end := tailID3v1(r, size); end {
			audio -= 128
		}
		if kbps > 0 && audio > 0 {
			info.Length = int(math.Round(float64(audio) * 8 / float64(kbps*1000)))
		}
		return info
	}
	return info
}

func tailID3v1(r io.ReaderAt, size int64) bool {
	if size < 128 {
		return false
	}
	t := make([]byte, 3)
	n, _ := r.ReadAt(t, size-128)
	return n == 3 && string(t) == "TAG"
}

func parseID3(info *AVInfo, ver, flags byte, b []byte) {
	if flags&0x40 != 0 && len(b) >= 4 && ver >= 3 { // extended header
		var l int
		if ver == 4 {
			l = int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
		} else {
			l = int(binary.BigEndian.Uint32(b)) + 4
		}
		if l > len(b) {
			return
		}
		b = b[l:]
	}
	idLen, hdr := 4, 10
	if ver == 2 {
		idLen, hdr = 3, 6
	}
	for len(b) >= hdr {
		id := string(b[:idLen])
		if b[0] == 0 {
			return
		}
		var l int
		switch ver {
		case 2:
			l = int(b[3])<<16 | int(b[4])<<8 | int(b[5])
		case 4:
			l = int(b[4]&0x7f)<<21 | int(b[5]&0x7f)<<14 | int(b[6]&0x7f)<<7 | int(b[7]&0x7f)
		default:
			l = int(binary.BigEndian.Uint32(b[4:8]))
		}
		if l < 0 || hdr+l > len(b) {
			return
		}
		body := b[hdr : hdr+l]
		switch id {
		case "TIT2", "TT2":
			info.Title = id3Text(body)
		case "TPE1", "TP1":
			info.Artist = id3Text(body)
		case "TALB", "TAL":
			info.Album = id3Text(body)
		case "TCON", "TCO":
			info.Genre = id3Text(body)
		}
		b = b[hdr+l:]
	}
}

func id3Text(b []byte) string {
	if len(b) < 2 {
		return ""
	}
	enc, b := b[0], b[1:]
	var s string
	switch enc {
	case 1, 2:
		bo := binary.ByteOrder(binary.BigEndian)
		if enc == 1 && len(b) >= 2 {
			if b[0] == 0xFF && b[1] == 0xFE {
				bo = binary.LittleEndian
				b = b[2:]
			} else if b[0] == 0xFE && b[1] == 0xFF {
				b = b[2:]
			}
		}
		u := make([]uint16, 0, len(b)/2)
		for i := 0; i+1 < len(b); i += 2 {
			u = append(u, bo.Uint16(b[i:]))
		}
		s = string(utf16.Decode(u))
	case 0:
		rs := make([]rune, len(b)) // ISO-8859-1
		for i, c := range b {
			rs[i] = rune(c)
		}
		s = string(rs)
	default:
		s = string(b)
	}
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// --- MP4 / M4A / MOV ----------------------------------------------------------------------------

func readMP4(r io.ReaderAt, size int64) AVInfo {
	info := AVInfo{Format: "mp4"}
	var walk func(start, end int64, depth int)
	walk = func(start, end int64, depth int) {
		for start+8 <= end {
			h := make([]byte, 16)
			n, _ := r.ReadAt(h, start)
			if n < 8 {
				return
			}
			bs := int64(binary.BigEndian.Uint32(h))
			typ := string(h[4:8])
			hl := int64(8)
			switch bs {
			case 1:
				if n < 16 {
					return
				}
				bs, hl = int64(binary.BigEndian.Uint64(h[8:])), 16
			case 0:
				bs = end - start
			}
			if bs < hl || start+bs > end {
				if start+bs > end && bs >= hl {
					bs = end - start
				} else {
					return
				}
			}
			switch typ {
			case "moov", "trak", "mdia":
				if depth < 6 {
					walk(start+hl, start+bs, depth+1)
				}
			case "mvhd":
				b := make([]byte, 32)
				m, _ := r.ReadAt(b, start+hl)
				if m >= 20 && b[0] == 0 {
					ts, d := binary.BigEndian.Uint32(b[12:]), binary.BigEndian.Uint32(b[16:])
					if ts > 0 {
						info.Length = int(math.Round(float64(d) / float64(ts)))
					}
				} else if m >= 32 && b[0] == 1 {
					ts, d := binary.BigEndian.Uint32(b[20:]), binary.BigEndian.Uint64(b[24:])
					if ts > 0 {
						info.Length = int(math.Round(float64(d) / float64(ts)))
					}
				}
			case "tkhd":
				// width/height are the last two 16.16 fixed-point numbers of the box.
				if bs-hl >= 8 {
					b := make([]byte, 8)
					if m, _ := r.ReadAt(b, start+bs-8); m == 8 {
						w, hh := int(binary.BigEndian.Uint32(b)>>16), int(binary.BigEndian.Uint32(b[4:])>>16)
						if w > 0 && hh > 0 && info.Width == 0 {
							info.Width, info.Height = w, hh
						}
					}
				}
			}
			start += bs
		}
	}
	walk(0, size, 0)
	return info
}

// --- WebM / Matroska ----------------------------------------------------------------------------

func readMatroska(r io.ReaderAt, size int64) AVInfo {
	info := AVInfo{Format: "webm"}
	timescale := uint64(1000000)
	var duration float64
	readID := func(off int64) (uint32, int64) {
		b := make([]byte, 4)
		n, _ := r.ReadAt(b, off)
		if n < 1 || b[0] == 0 {
			return 0, 0
		}
		l := 1
		for m := byte(0x80); b[0]&m == 0; m >>= 1 {
			l++
		}
		if l > 4 || l > n {
			return 0, 0
		}
		var id uint32
		for i := 0; i < l; i++ {
			id = id<<8 | uint32(b[i])
		}
		return id, int64(l)
	}
	readSize := func(off int64) (int64, int64) {
		b := make([]byte, 8)
		n, _ := r.ReadAt(b, off)
		if n < 1 || b[0] == 0 {
			return 0, 0
		}
		l := 1
		m := byte(0x80)
		for ; b[0]&m == 0; m >>= 1 {
			l++
		}
		if l > n {
			return 0, 0
		}
		v := int64(b[0] & (m - 1))
		unknown := v == int64(m-1)
		for i := 1; i < l; i++ {
			v = v<<8 | int64(b[i])
			if b[i] != 0xFF {
				unknown = false
			}
		}
		if unknown {
			return -1, int64(l)
		}
		return v, int64(l)
	}
	uintAt := func(off, l int64) uint64 {
		b := make([]byte, 8)
		n, _ := r.ReadAt(b[:min(l, 8)], off)
		var v uint64
		for i := 0; i < n; i++ {
			v = v<<8 | uint64(b[i])
		}
		return v
	}
	containers := map[uint32]bool{0x18538067: true, 0x1549A966: true, 0x1654AE6B: true, 0xAE: true, 0xE0: true}
	var walk func(start, end int64, depth int)
	walk = func(start, end int64, depth int) {
		for start < end {
			id, il := readID(start)
			if il == 0 {
				return
			}
			sz, sl := readSize(start + il)
			if sl == 0 {
				return
			}
			body := start + il + sl
			if sz < 0 || body+sz > end {
				sz = end - body
			}
			switch {
			case containers[id] && depth < 6:
				walk(body, body+sz, depth+1)
			case id == 0x2AD7B1:
				timescale = uintAt(body, sz)
			case id == 0x4489:
				b := make([]byte, 8)
				n, _ := r.ReadAt(b[:min(sz, 8)], body)
				if n == 4 {
					duration = float64(math.Float32frombits(binary.BigEndian.Uint32(b)))
				} else if n == 8 {
					duration = math.Float64frombits(binary.BigEndian.Uint64(b))
				}
			case id == 0xB0 && info.Width == 0:
				info.Width = int(uintAt(body, sz))
			case id == 0xBA && info.Height == 0:
				info.Height = int(uintAt(body, sz))
			case id == 0x1F43B675: // first Cluster: Info and Tracks come before it
				return
			}
			start = body + sz
		}
	}
	walk(0, size, 0)
	if duration > 0 && timescale > 0 {
		info.Length = int(math.Round(duration * float64(timescale) / 1e9))
	}
	return info
}

// --- WAV ----------------------------------------------------------------------------------------

func readWAV(r io.ReaderAt, size int64) AVInfo {
	info := AVInfo{Format: "wav", BitrateMode: "cbr"}
	var byteRate, dataLen uint32
	for off := int64(12); off+8 <= size; {
		h := make([]byte, 8)
		if n, _ := r.ReadAt(h, off); n < 8 {
			break
		}
		l := binary.LittleEndian.Uint32(h[4:])
		switch string(h[:4]) {
		case "fmt ":
			b := make([]byte, 16)
			if n, _ := r.ReadAt(b, off+8); n == 16 {
				byteRate = binary.LittleEndian.Uint32(b[8:])
			}
		case "data":
			dataLen = l
			if int64(l) > size-off-8 {
				dataLen = uint32(size - off - 8)
			}
		}
		off += 8 + int64(l) + int64(l&1)
	}
	if byteRate > 0 {
		info.Bitrate = int(byteRate) * 8
		info.Length = int(math.Round(float64(dataLen) / float64(byteRate)))
	}
	return info
}
