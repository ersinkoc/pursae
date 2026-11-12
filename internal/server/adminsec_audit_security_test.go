package server

import "testing"

// A failed login typed as a spreadsheet formula must not export as a live formula.
func TestAdminSecAuditCSVNeutralizesFormulaCells(t *testing.T) {
	rec := asLogCSVRecord(asLogRow{Event: "login_failed", Severity: "warning", Username: "=1+1", IP: "+cmd", Object: "@x", Message: "-2", UserAgent: "=HYPERLINK(\"a\")"})
	for i, cell := range rec[1:] {
		if len(cell) > 0 && (cell[0] == '=' || cell[0] == '+' || cell[0] == '-' || cell[0] == '@') {
			t.Fatalf("cell %d exported as formula: %q", i+1, cell)
		}
	}
	if rec[3] != "'=1+1" {
		t.Fatalf("username = %q, want %q", rec[3], "'=1+1")
	}
}
