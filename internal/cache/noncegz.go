package cache

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash/crc32"
)

// Precompressed bodies that hold the CSP nonce placeholder.
//
// The server swaps the placeholder for a fresh nonce in every response, so such a body cannot be
// stored as one gzip stream: before this, every gzip hit on a nonced page (every page under the
// nonce policy) was compressed again by the server's gzip middleware, which was most of the CPU a
// cache hit cost. Instead the text between placeholders is deflated once at store time, each part
// ending on a byte boundary (a sync flush). A response is then that deflate data with every nonce
// spliced in as a stored (uncompressed) block, a gzip header and a CRC-32 trailer: no compression
// per request, only a CRC over the body.
//
// A part may refer back into the text before it (the preset dictionary below), where the decoder
// holds the actual nonce, not the placeholder. The dictionary fills each placeholder with a byte the
// body never contains, so no back-reference can reach into a nonce. A body that contains every byte
// value gets no precompressed copy.

// nonceGzMinSize is the smallest nonced body worth precompressing (as NewEntry's gzip copy).
const nonceGzMinSize = 256

// nonceGzMaxParts caps the placeholders of a precompressed body. Each part needs a deflate writer
// of its own (about 1 MB of state), so a page full of nonces would cost hundreds of megabytes to
// store; such a page is gzipped per response instead.
const nonceGzMaxParts = 128

// buildNonceGz deflates body around the placeholder ph. It returns the deflate parts back to back,
// the end offset of each part in that data, and the placeholders' offsets in body (one fewer than
// the parts). ok is false when the body is too small, does not shrink, or has no unused byte value.
func buildNonceGz(body, ph []byte) (data []byte, cuts, at []int, ok bool) {
	if len(body) < nonceGzMinSize || len(ph) == 0 || len(ph) > 0xffff {
		return nil, nil, nil, false
	}
	var seen [256]bool
	for _, b := range body {
		seen[b] = true
	}
	filler, found := byte(0), false
	for i := 255; i >= 0; i-- {
		if !seen[i] {
			filler, found = byte(i), true
			break
		}
	}
	if !found {
		return nil, nil, nil, false
	}
	for off := 0; ; {
		i := bytes.Index(body[off:], ph)
		if i < 0 {
			break
		}
		if len(at) == nonceGzMaxParts {
			return nil, nil, nil, false
		}
		at = append(at, off+i)
		off += i + len(ph)
	}
	if len(at) == 0 {
		return nil, nil, nil, false
	}
	// dict is the body as the decoder sees it, with each nonce as filler bytes.
	dict := bytes.Clone(body)
	for _, p := range at {
		for j := range ph {
			dict[p+j] = filler
		}
	}
	var buf bytes.Buffer
	start := 0
	for i := 0; i <= len(at); i++ {
		end := len(body)
		if i < len(at) {
			end = at[i]
		}
		var zw *flate.Writer
		if start == 0 {
			zw, _ = flate.NewWriter(&buf, flate.DefaultCompression)
		} else {
			zw, _ = flate.NewWriterDict(&buf, flate.DefaultCompression, dict[max(0, start-32*1024):start])
		}
		if _, err := zw.Write(body[start:end]); err != nil {
			return nil, nil, nil, false
		}
		if err := zw.Flush(); err != nil { // sync flush: the part ends on a byte boundary, not final
			return nil, nil, nil, false
		}
		cuts = append(cuts, buf.Len())
		start = end + len(ph)
	}
	if buf.Len()+len(at)*(5+len(ph))+20 >= len(body) {
		return nil, nil, nil, false
	}
	return buf.Bytes(), cuts, at, true
}

// gzipHeader is a gzip member header: deflate, no flags, no mtime, unknown OS (as compress/gzip).
var gzipHeader = []byte{0x1f, 0x8b, 8, 0, 0, 0, 0, 0, 0, 0xff}

// nonceGzip assembles the gzip body of e with nonce in place of every placeholder. ok is false
// when e has no precompressed nonced copy or nonce is not the placeholder's length.
func (e *Entry) nonceGzip(nonce string) ([]byte, bool) {
	if e.NonceGz == nil || len(e.NonceGzCuts) != len(e.NonceAt)+1 || len(nonce) != len(e.Placeholder) {
		return nil, false
	}
	n := len(gzipHeader) + len(e.NonceGz) + len(e.NonceAt)*(5+len(nonce)) + 2 + 8
	out := make([]byte, 0, n)
	out = append(out, gzipHeader...)
	crc := uint32(0)
	prevCut, prevEnd := 0, 0
	for i, cut := range e.NonceGzCuts {
		out = append(out, e.NonceGz[prevCut:cut]...)
		prevCut = cut
		if i == len(e.NonceAt) {
			crc = crc32.Update(crc, crc32.IEEETable, e.Body[prevEnd:])
			break
		}
		p := e.NonceAt[i]
		crc = crc32.Update(crc, crc32.IEEETable, e.Body[prevEnd:p])
		crc = crc32.Update(crc, crc32.IEEETable, []byte(nonce))
		prevEnd = p + len(nonce)
		// A stored block (BFINAL 0, BTYPE 00) on a byte boundary: LEN, NLEN, the bytes.
		l := uint16(len(nonce))
		out = append(out, 0, byte(l), byte(l>>8), byte(^l), byte(^l>>8))
		out = append(out, nonce...)
	}
	out = append(out, 0x03, 0x00) // the final block: empty, fixed Huffman
	out = binary.LittleEndian.AppendUint32(out, crc)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(e.Body)))
	return out, true
}
