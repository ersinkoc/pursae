package server

import (
	"image"
	"testing"

	"wpgo/internal/media"
)

// The history image-edit.js sends: crop width/height are strings, the rest numbers.
func TestParseHistoryLooseNumbers(t *testing.T) {
	ops := parseHistory(`[{"c":{"x":40,"y":30,"w":"201","h":"132","r":1}},{"r":90},{"f":1}]`)
	if len(ops) != 3 {
		t.Fatalf("ops = %d, want 3", len(ops))
	}
	c := ops[0]
	if !c.Crop || c.X != 40 || c.Y != 30 || c.W != 201 || c.H != 132 || c.Ratio != 1 {
		t.Errorf("crop op = %+v", c)
	}
	if ops[1].Rotate != 90 || ops[2].Flip != 1 {
		t.Errorf("rotate/flip = %+v %+v", ops[1], ops[2])
	}

	img := media.ApplyOps(image.NewRGBA(image.Rect(0, 0, 447, 447)), ops[:1])
	if b := img.Bounds(); b.Dx() != 201 || b.Dy() != 132 {
		t.Errorf("cropped size = %dx%d, want 201x132", b.Dx(), b.Dy())
	}
}
