package coreupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
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
	if Read(exe) != nil {
		t.Fatal("a confirmed update keeps its state file")
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
