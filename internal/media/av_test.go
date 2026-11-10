package media

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func box(typ string, body ...[]byte) []byte {
	b := bytes.Join(body, nil)
	h := make([]byte, 8)
	binary.BigEndian.PutUint32(h, uint32(8+len(b)))
	copy(h[4:], typ)
	return append(h, b...)
}

func TestReadMP4(t *testing.T) {
	mvhd := make([]byte, 100)
	binary.BigEndian.PutUint32(mvhd[12:], 1000)  // timescale
	binary.BigEndian.PutUint32(mvhd[16:], 65500) // duration -> 65.5 s
	tkhd := make([]byte, 84)
	binary.BigEndian.PutUint32(tkhd[76:], 1280<<16)
	binary.BigEndian.PutUint32(tkhd[80:], 720<<16)
	data := append(box("ftyp", []byte("isom\x00\x00\x02\x00")), box("moov", box("mvhd", mvhd), box("trak", box("tkhd", tkhd)))...)
	i := readAV(bytes.NewReader(data), int64(len(data)), ".mp4")
	if i.Length != 66 || i.Width != 1280 || i.Height != 720 {
		t.Fatalf("%+v", i)
	}
}

func TestReadMP3CBRAndID3(t *testing.T) {
	text := func(id, s string) []byte {
		body := append([]byte{3}, s...)
		h := make([]byte, 10)
		copy(h, id)
		binary.BigEndian.PutUint32(h[4:], uint32(len(body)))
		return append(h, body...)
	}
	frames := append(text("TPE1", "The Artist"), text("TALB", "The Album")...)
	tag := []byte("ID3\x03\x00\x00")
	tag = append(tag, byte(len(frames)>>21&0x7f), byte(len(frames)>>14&0x7f), byte(len(frames)>>7&0x7f), byte(len(frames)&0x7f))
	tag = append(tag, frames...)
	// MPEG1 Layer III, 128 kbps, 44.1 kHz: FF FB 90 00; 10 seconds = 160000 bytes of audio.
	audio := make([]byte, 160000)
	copy(audio, []byte{0xFF, 0xFB, 0x90, 0x00})
	data := append(tag, audio...)
	i := readAV(bytes.NewReader(data), int64(len(data)), ".mp3")
	if i.Length != 10 || i.Bitrate != 128000 || i.Artist != "The Artist" || i.Album != "The Album" {
		t.Fatalf("%+v", i)
	}
}

func TestReadMP3XingAfterCRC(t *testing.T) {
	for _, crc := range []bool{false, true} {
		data := make([]byte, 417)
		copy(data, []byte{0xff, 0xfb, 0x90, 0})
		off := 4 + 32
		if crc {
			data[1] = 0xfa
			off += 2
		}
		copy(data[off:], "Xing")
		binary.BigEndian.PutUint32(data[off+4:], 1)
		binary.BigEndian.PutUint32(data[off+8:], 1000)
		info := readAV(bytes.NewReader(data), int64(len(data)), ".mp3")
		if info.Length != 26 || info.BitrateMode != "vbr" {
			t.Errorf("CRC %v: %+v", crc, info)
		}
	}
}

func TestReadMatroska(t *testing.T) {
	el := func(id []byte, body []byte) []byte {
		return append(append(append([]byte{}, id...), 0x80|byte(len(body))), body...)
	}
	dur := make([]byte, 8)
	binary.BigEndian.PutUint64(dur, 0x40C3880000000000) // 10000 ms
	info := el([]byte{0x15, 0x49, 0xA9, 0x66}, append(el([]byte{0x2A, 0xD7, 0xB1}, []byte{0x0F, 0x42, 0x40}), el([]byte{0x44, 0x89}, dur)...))
	video := el([]byte{0xE0}, append(el([]byte{0xB0}, []byte{0x02, 0x80}), el([]byte{0xBA}, []byte{0x01, 0xE0})...))
	tracks := el([]byte{0x16, 0x54, 0xAE, 0x6B}, el([]byte{0xAE}, video))
	seg := el([]byte{0x18, 0x53, 0x80, 0x67}, append(info, tracks...))
	data := append(el([]byte{0x1A, 0x45, 0xDF, 0xA3}, []byte{0x42, 0x82, 0x84, 'w', 'e', 'b', 'm'}), seg...)
	i := readAV(bytes.NewReader(data), int64(len(data)), ".webm")
	if i.Length != 10 || i.Width != 640 || i.Height != 480 {
		t.Fatalf("%+v", i)
	}
}

func TestReadAVGarbage(t *testing.T) {
	if i := readAV(bytes.NewReader([]byte("nonsense")), 8, ".mp4"); i.Length != 0 {
		t.Fatalf("%+v", i)
	}
}
