package textfold

import "testing"

func TestFold(t *testing.T) {
	for _, in := range []string{"IŞIK", "ışık", "Işık", "işik", "İŞİK"} {
		if got := Fold(in); got != "işik" {
			t.Errorf("Fold(%q) = %q, want %q", in, got, "işik")
		}
	}
	// No Turkish casing on other text: "INFO" stays "info", never "ınfo".
	for in, want := range map[string]string{"INFO": "info", "İstanbul": "istanbul", "ISTANBUL": "istanbul", "Straße": "straße"} {
		if got := Fold(in); got != want {
			t.Errorf("Fold(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestContains(t *testing.T) {
	if !Contains("IŞIK Eklentisi", "ışık") || !Contains("ışık lamba", "IŞIK") || !Contains("Info box", "INFO") {
		t.Error("Contains missed a dotted/dotless i match")
	}
	if Contains("Info", "ışıl") {
		t.Error("Contains matched unrelated text")
	}
}
