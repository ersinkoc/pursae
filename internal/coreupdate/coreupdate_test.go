package coreupdate

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// fakeBinary writes a script that prints "Pursae <version>" for `version`.
func fakeBinary(t *testing.T, path, version string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell scripts")
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho Pursae "+version+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func stubExec(t *testing.T) *[]string {
	var got []string
	old := execFn
	execFn = func(path string, argv []string, env []string) error { got = append(got, path); return nil }
	t.Cleanup(func() { execFn = old })
	return &got
}

func TestRollbackAfterFailedStart(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "wpgo")
	fakeBinary(t, exe, "0.3.0")
	if err := Prepare(exe, "0.3.0", "0.3.1"); err != nil {
		t.Fatal(err)
	}
	fakeBinary(t, exe+".new", "0.3.1")
	if err := os.Rename(exe+".new", exe); err != nil {
		t.Fatal(err)
	}
	execs := stubExec(t)

	Guard(exe, "0.3.1") // first start of the new binary: counted, nothing else
	if st := Read(exe); st == nil || st.Status != "pending" || st.Attempts != 1 || len(*execs) != 0 {
		t.Fatalf("after the first start: %+v %v", st, *execs)
	}
	Guard(exe, "0.3.1") // it died and was started again: back to 0.3.0
	st := Read(exe)
	if st == nil || st.Status != "rolled_back" || len(*execs) != 1 || (*execs)[0] != exe {
		t.Fatalf("rollback: %+v %v", st, *execs)
	}
	if v := binaryVersion(exe); v != "0.3.0" {
		t.Fatalf("the executable is %q after the rollback", v)
	}
	Guard(exe, "0.3.0") // the old binary starts and leaves the notice for the Updates screen
	if st := Read(exe); st == nil || st.Status != "rolled_back" {
		t.Fatalf("the notice is gone: %+v", st)
	}
}

func TestConfirmEndsTheUpdate(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "wpgo")
	fakeBinary(t, exe, "0.3.0")
	if err := Prepare(exe, "0.3.0", "0.3.1"); err != nil {
		t.Fatal(err)
	}
	execs := stubExec(t)
	Guard(exe, "0.3.1")
	Confirm(exe, "0.3.1")
	if st := Read(exe); st == nil || st.Status != "confirmed" || st.From != "0.3.0" || st.To != "0.3.1" {
		t.Fatalf("a confirmed update is not recorded as confirmed: %+v", st)
	}
	Guard(exe, "0.3.1")
	if len(*execs) != 0 {
		t.Fatal("a confirmed update was rolled back")
	}
	if _, err := os.Stat(PrevPath(exe)); err != nil {
		t.Fatal("the previous binary is not kept")
	}
}

func TestLaunchPicksTheNewerBinary(t *testing.T) {
	dir := t.TempDir()
	image := filepath.Join(dir, "image", "wpgo")
	os.MkdirAll(filepath.Dir(image), 0o755)
	data := filepath.Join(dir, "data")
	fakeBinary(t, image, "0.3.0")
	execs := stubExec(t)

	// First start: the image's binary is installed into the data directory.
	Launch(image, data, []string{"serve"})
	installed := filepath.Join(data, "wpgo")
	if (*execs)[0] != installed || binaryVersion(installed) != "0.3.0" {
		t.Fatalf("first start: %v %q", *execs, binaryVersion(installed))
	}
	// An update made from the admin (0.3.1) survives a restart of the same image.
	fakeBinary(t, installed, "0.3.1")
	Launch(image, data, nil)
	if binaryVersion(installed) != "0.3.1" {
		t.Fatal("the updated binary was overwritten by the older image")
	}
	// A newer image wins.
	fakeBinary(t, image, "0.4.0")
	Launch(image, data, nil)
	if binaryVersion(installed) != "0.4.0" {
		t.Fatal("the newer image was not installed")
	}
	// A rebuilt image with the same version but other bytes replaces the stale installed copy.
	if err := os.WriteFile(image, []byte("#!/bin/sh\necho Pursae 0.4.0\n# rebuilt\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	Launch(image, data, nil)
	if !sameContent(image, installed) {
		t.Fatal("a rebuilt image with the same version did not replace the installed binary")
	}
	// The same image again: nothing to copy, the installed binary keeps running.
	*execs = nil
	Launch(image, data, nil)
	if (*execs)[0] != installed || !sameContent(image, installed) {
		t.Fatalf("same image restart: %v", *execs)
	}
	// No persistent directory: the image's binary runs.
	*execs = nil
	Launch(image, "", nil)
	if (*execs)[0] != image {
		t.Fatalf("without a directory: %v", *execs)
	}
}

func TestLess(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"0.3.0", "0.3.1", true}, {"0.9.9", "0.10.0", true}, {"0.10.0", "0.9.9", false}, {"1.0.0", "1.0.0", false}, {"v1.2", "1.2.1", true}} {
		if got := Less(c.a, c.b); got != c.want {
			t.Errorf("Less(%s, %s) = %v", c.a, c.b, got)
		}
	}
}

// updated leaves exe at version to after a confirmed update from, with from kept as <exe>.prev.
func updated(t *testing.T, from, to string) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "wpgo")
	fakeBinary(t, exe, from)
	if err := Prepare(exe, from, to); err != nil {
		t.Fatal(err)
	}
	fakeBinary(t, exe+".new", to)
	if err := os.Rename(exe+".new", exe); err != nil {
		t.Fatal(err)
	}
	Confirm(exe, to)
	return exe
}

func TestPrunePrevAfterRetention(t *testing.T) {
	exe := updated(t, "0.3.0", "0.3.1")
	confirmed := Read(exe).At
	if PrunePrev(exe, confirmed.Add(PrevRetention-time.Minute)) {
		t.Fatal("the previous binary was removed before the retention ended")
	}
	if from, until := Previous(exe, "0.3.1"); from != "0.3.0" || !until.Equal(confirmed.Add(PrevRetention)) {
		t.Fatalf("Previous while kept: %q %v", from, until)
	}
	if !PrunePrev(exe, confirmed.Add(PrevRetention)) {
		t.Fatal("the previous binary was not removed after the retention")
	}
	if _, err := os.Stat(PrevPath(exe)); !os.IsNotExist(err) {
		t.Fatalf("the previous binary is still there: %v", err)
	}
	// The state still names the previous version (it can be downloaded again), without a local copy.
	if from, until := Previous(exe, "0.3.1"); from != "0.3.0" || !until.IsZero() {
		t.Fatalf("Previous after pruning: %q %v", from, until)
	}
	if v := binaryVersion(exe); v != "0.3.1" {
		t.Fatalf("pruning touched the running binary: %q", v)
	}
	if from, _ := Previous(exe, "0.3.2"); from != "" {
		t.Fatalf("Previous for another running version: %q", from)
	}
}

func TestPrunePrevKeepsPendingAndRemovesOrphans(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "wpgo")
	fakeBinary(t, exe, "0.3.0")
	if err := Prepare(exe, "0.3.0", "0.3.1"); err != nil {
		t.Fatal(err)
	}
	if PrunePrev(exe, time.Now().Add(48*time.Hour)) {
		t.Fatal("the previous binary of a pending update was removed")
	}
	Clear(exe) // a .prev left by a release that deleted the state on confirmation
	if !PrunePrev(exe, time.Now()) {
		t.Fatal("an orphan previous binary was kept")
	}
	if PrunePrev(exe, time.Now()) {
		t.Fatal("PrunePrev reported a removal without a file")
	}
}

func TestSwapPrevReinstallsThePreviousBinary(t *testing.T) {
	exe := updated(t, "0.3.0", "0.3.1")
	if err := SwapPrev(exe, "0.3.1", "0.3.0"); err != nil {
		t.Fatal(err)
	}
	if v := binaryVersion(exe); v != "0.3.0" {
		t.Fatalf("the executable is %q after the reinstall", v)
	}
	if v := binaryVersion(PrevPath(exe)); v != "0.3.1" {
		t.Fatalf("the replaced binary is %q, want 0.3.1 kept", v)
	}
	if st := Read(exe); st == nil || st.Status != "pending" || st.From != "0.3.1" || st.To != "0.3.0" {
		t.Fatalf("state after the reinstall: %+v", st)
	}
	// The reinstalled binary that does not start goes back like any update.
	execs := stubExec(t)
	Guard(exe, "0.3.0")
	Guard(exe, "0.3.0")
	if v := binaryVersion(exe); v != "0.3.1" || len(*execs) != 1 {
		t.Fatalf("no rollback of a failed reinstall: %q %v", v, *execs)
	}
}

func TestSwapPrevWithoutPreviousChangesNothing(t *testing.T) {
	exe := updated(t, "0.3.0", "0.3.1")
	if err := os.Remove(PrevPath(exe)); err != nil {
		t.Fatal(err)
	}
	before := Read(exe)
	if err := SwapPrev(exe, "0.3.1", "0.3.0"); err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SwapPrev without a previous binary: %v", err)
	}
	if v := binaryVersion(exe); v != "0.3.1" {
		t.Fatalf("the executable changed: %q", v)
	}
	if st := Read(exe); st == nil || *st != *before {
		t.Fatalf("the state changed: %+v (was %+v)", st, before)
	}
}

func TestLessPrereleases(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"0.3.0-beta.1", "0.3.0", true},
		{"0.3.0", "0.3.0-beta.1", false},
		{"0.3.0-beta.2", "0.3.0-beta.10", true},
		{"0.3.0-alpha", "0.3.0-beta", true},
		{"0.3.0-1", "0.3.0-alpha", true},
		{"0.3.0-beta", "0.3.0-beta.1", true},
		{"0.3.0+build.9", "0.3.0+build.1", false},
	} {
		if got := Less(tc.a, tc.b); got != tc.want {
			t.Errorf("Less(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestLaunchStableAndPrerelease(t *testing.T) {
	for _, tc := range []struct{ image, installed, want string }{
		{"0.3.0", "0.3.0-beta.1", "0.3.0"},
		{"0.3.0-beta.1", "0.3.0", "0.3.0"},
	} {
		t.Run(tc.image+"-over-"+tc.installed, func(t *testing.T) {
			dir := t.TempDir()
			image := filepath.Join(dir, "wpgo")
			data := filepath.Join(dir, "installed")
			if err := os.Mkdir(data, 0o755); err != nil {
				t.Fatal(err)
			}
			fakeBinary(t, image, tc.image)
			installed := filepath.Join(data, "wpgo")
			fakeBinary(t, installed, tc.installed)
			stubExec(t)
			if err := Launch(image, data, nil); err != nil {
				t.Fatal(err)
			}
			if got := binaryVersion(installed); got != tc.want {
				t.Fatalf("installed %s, want %s", got, tc.want)
			}
		})
	}
}

func TestPrunePrevKeepsUnknownState(t *testing.T) {
	for _, state := range []string{"corrupt", "unreadable"} {
		t.Run(state, func(t *testing.T) {
			exe := filepath.Join(t.TempDir(), "wpgo")
			if err := os.WriteFile(PrevPath(exe), []byte("rollback"), 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			if state == "corrupt" {
				err = os.WriteFile(statePath(exe), []byte("{"), 0o600)
			} else {
				err = os.Mkdir(statePath(exe), 0o700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if PrunePrev(exe, time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)) {
				t.Fatal("unknown state authorized deletion")
			}
			if b, err := os.ReadFile(PrevPath(exe)); err != nil || string(b) != "rollback" {
				t.Fatalf("rollback binary lost: %q, %v", b, err)
			}
		})
	}
}
