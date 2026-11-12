// Package coreupdate keeps a core update recoverable: the binary being replaced is kept as
// <exe>.prev, and a state file (<exe>.update.json) records the update until the new binary has
// started and answered requests. If the new binary dies before that and is started again (by
// Docker's restart policy, systemd, …), Guard puts the previous binary back and runs it, and the
// Updates screen says what happened. Once confirmed, the previous binary stays for PrevRetention
// (a manual "Reinstall" goes back to it without a download) and is then removed (PrunePrev); the
// state file keeps recording which version the site came from.
//
// It also picks the binary a container runs (Launch): the image's copy is installed into a
// persistent directory, so an update made from the admin survives container restarts, while a
// newer image still wins over an older installed binary.
//
// The package depends on nothing in the server, so main can call it before anything else.
package coreupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// State is the content of <exe>.update.json.
type State struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Status   string    `json:"status"` // pending | rolled_back | confirmed
	Attempts int       `json:"attempts"`
	At       time.Time `json:"at"`
	Message  string    `json:"message,omitempty"`
}

// execFn replaces the process (tests swap it).
var execFn = syscall.Exec

func statePath(exe string) string { return exe + ".update.json" }

// PrevPath is where the replaced binary is kept.
func PrevPath(exe string) string { return exe + ".prev" }

// Read returns the update state next to exe, or nil.
func Read(exe string) *State {
	b, err := os.ReadFile(statePath(exe))
	if err != nil {
		return nil
	}
	var st State
	if json.Unmarshal(b, &st) != nil {
		return nil
	}
	return &st
}

func write(exe string, st *State) error {
	b, _ := json.MarshalIndent(st, "", "  ")
	tmp := statePath(exe) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(exe))
}

// Clear removes the state file.
func Clear(exe string) { _ = os.Remove(statePath(exe)) }

// Prepare keeps the running binary as <exe>.prev and records an update from → to. Call it right
// before the new binary is renamed over exe.
func Prepare(exe, from, to string) error {
	prev := PrevPath(exe)
	_ = os.Remove(prev)
	if err := os.Link(exe, prev); err != nil { // same directory: a hard link costs nothing
		if err := copyFile(exe, prev); err != nil {
			return fmt.Errorf("keeping the current binary: %w", err)
		}
	}
	return write(exe, &State{From: from, To: to, Status: "pending", At: time.Now().UTC()})
}

// Guard runs first when the server starts. When exe is the new binary of a pending update that
// already failed to start once, it restores the previous binary and execs it (it does not return
// then). Otherwise it counts the start attempt and returns.
func Guard(exe, version string) {
	st := Read(exe)
	if st == nil || st.Status != "pending" || st.To != version {
		return
	}
	if st.Attempts < 1 {
		st.Attempts++
		_ = write(exe, st)
		return
	}
	prev := PrevPath(exe)
	if _, err := os.Stat(prev); err != nil {
		return // nothing to go back to; keep trying the new one
	}
	if err := os.Rename(prev, exe); err != nil {
		fmt.Fprintf(os.Stderr, "pursae: rolling back to %s failed: %v\n", st.From, err)
		return
	}
	st.Status = "rolled_back"
	st.Message = fmt.Sprintf("Pursae %s did not start, so the site went back to %s.", st.To, st.From)
	st.At = time.Now().UTC()
	_ = write(exe, st)
	fmt.Fprintln(os.Stderr, "pursae: "+st.Message)
	_ = execFn(exe, os.Args, os.Environ())
}

// Restore puts the previous binary back right away (the new one could not even be started) and
// leaves the notice for the Updates screen.
func Restore(exe string, cause error) {
	st := Read(exe)
	if st == nil || st.Status != "pending" {
		return
	}
	if err := os.Rename(PrevPath(exe), exe); err != nil {
		return
	}
	st.Status = "rolled_back"
	if Stopped() { // the server was already shut down: the process exits and restarts on the old binary
		st.Message = fmt.Sprintf("Pursae %s could not be started (%v), so the site restarted on %s.", st.To, cause, st.From)
	} else {
		st.Message = fmt.Sprintf("Pursae %s could not be started (%v), so the site stayed on %s.", st.To, cause, st.From)
	}
	st.At = time.Now().UTC()
	_ = write(exe, st)
}

// PrevRetention is how long the previous binary is kept after an update was confirmed.
const PrevRetention = 24 * time.Hour

// Confirm ends a pending update once the new binary serves requests. The state stays as
// "confirmed" (At is the confirmation time): it names the previous version and starts the
// retention of the previous binary.
func Confirm(exe, version string) {
	if st := Read(exe); st != nil && st.Status == "pending" && st.To == version {
		st.Status, st.Attempts, st.At, st.Message = "confirmed", 0, time.Now().UTC(), ""
		_ = write(exe, st)
	}
}

// PrunePrev removes the previous binary once a confirmed update is PrevRetention old, and a
// previous binary no update state refers to (left by a release before the retention). It reports
// whether a file was removed.
func PrunePrev(exe string, now time.Time) bool {
	prev := PrevPath(exe)
	if _, err := os.Stat(prev); err != nil {
		return false
	}
	st := Read(exe)
	switch {
	case st == nil:
		if _, err := os.Stat(statePath(exe)); !os.IsNotExist(err) {
			return false // unreadable or corrupt state is not proof that the binary is orphaned
		}
	case st.Status == "confirmed" && !st.At.IsZero() && now.Sub(st.At) >= PrevRetention:
	default:
		return false // pending or rolled back: the rollback may still need it; or still retained
	}
	return os.Remove(prev) == nil
}

// Previous is the version the running one (version) was updated from, with the time until which
// its binary is kept next to exe (zero when it is no longer there). from is "" when unknown.
func Previous(exe, version string) (from string, keptUntil time.Time) {
	st := Read(exe)
	if st == nil || st.Status != "confirmed" || st.To != version {
		return "", time.Time{}
	}
	if _, err := os.Stat(PrevPath(exe)); err == nil {
		keptUntil = st.At.Add(PrevRetention)
	}
	return st.From, keptUntil
}

// SwapPrev puts the kept previous binary (version to) back over exe, keeping the running one
// (version from) as the new previous binary, and records the change as a pending update, so a
// previous binary that does not start goes back like any update (Guard).
func SwapPrev(exe, from, to string) error {
	before := Read(exe)
	undo := func() {
		if before != nil {
			_ = write(exe, before)
		} else {
			Clear(exe)
		}
	}
	prev := PrevPath(exe)
	tmp := exe + ".reinstall"
	_ = os.Remove(tmp)
	if err := os.Rename(prev, tmp); err != nil {
		return fmt.Errorf("the previous binary is gone: %w", err)
	}
	if err := Prepare(exe, from, to); err != nil {
		_ = os.Remove(prev)
		_ = os.Rename(tmp, prev)
		undo()
		return err
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(prev)
		_ = os.Rename(tmp, prev)
		undo()
		return err
	}
	return nil
}

// WaitHealthy asks url until it answers below 500 (true) or ctx ends (false).
func WaitHealthy(ctx context.Context, url string) bool {
	client := &httpClient{}
	for {
		if client.ok(ctx, url) {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(2 * time.Second):
		}
	}
}

// Launch picks the binary to run in a container and execs it with args (it returns only on
// error). image is the binary shipped in the image, dir a persistent, writable directory. The
// image binary is installed into dir when dir has none, an older version, or the same version
// with different bytes (a rebuilt image); an installed binary that is newer (an update made from
// the admin) is kept.
func Launch(image, dir string, args []string) error {
	target := image
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err == nil {
			installed := filepath.Join(dir, filepath.Base(image))
			iv, cv := binaryVersion(image), binaryVersion(installed)
			switch {
			// A rebuilt image with the same version (development builds all report the same one)
			// also wins when its binary differs: an admin update always installs a newer version,
			// so an equal version with other bytes can only be a stale copy of an older image.
			case cv == "" || Less(cv, iv) || (cv == iv && !sameContent(image, installed)):
				if err := copyFile(image, installed+".new"); err == nil && os.Rename(installed+".new", installed) == nil {
					target = installed
					switch {
					case cv == iv:
						fmt.Fprintf(os.Stderr, "pursae: the image has a different %s build than the installed one; using the image's\n", iv)
					case cv != "":
						fmt.Fprintf(os.Stderr, "pursae: the image has %s, newer than the installed %s; using the image's\n", iv, cv)
					}
				}
			default:
				target = installed
			}
		}
	}
	return execFn(target, append([]string{target}, args...), os.Environ())
}

// sameContent reports whether two files have the same bytes (size first, then a streamed compare).
// Any error counts as different, so the caller reinstalls the image's binary.
func sameContent(a, b string) bool {
	fa, err := os.Open(a)
	if err != nil {
		return false
	}
	defer fa.Close()
	fb, err := os.Open(b)
	if err != nil {
		return false
	}
	defer fb.Close()
	sa, errA := fa.Stat()
	sb, errB := fb.Stat()
	if errA != nil || errB != nil || sa.Size() != sb.Size() {
		return false
	}
	bufA, bufB := make([]byte, 1<<20), make([]byte, 1<<20)
	for {
		na, errA := io.ReadFull(fa, bufA)
		nb, errB := io.ReadFull(fb, bufB)
		if na != nb || string(bufA[:na]) != string(bufB[:nb]) {
			return false
		}
		if errA == io.EOF || errA == io.ErrUnexpectedEOF {
			return errB == errA
		}
		if errA != nil || errB != nil {
			return false
		}
	}
}

// binaryVersion runs `<bin> version` ("Pursae 0.3.1") and returns the version, or "".
func binaryVersion(bin string) string {
	if _, err := os.Stat(bin); err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "version").Output()
	if err != nil {
		return ""
	}
	f := strings.Fields(string(out))
	if len(f) < 2 {
		return ""
	}
	return f[len(f)-1]
}

// Less compares dotted versions numerically, then prerelease identifiers; a missing core part
// counts as 0. A prerelease precedes its final release, and build metadata is ignored.
func Less(a, b string) bool {
	a, _, _ = strings.Cut(strings.TrimPrefix(a, "v"), "+")
	b, _, _ = strings.Cut(strings.TrimPrefix(b, "v"), "+")
	a, preA, _ := strings.Cut(a, "-")
	b, preB, _ := strings.Cut(b, "-")
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < max(len(pa), len(pb)); i++ {
		x, y := "0", "0"
		if i < len(pa) {
			x = pa[i]
		}
		if i < len(pb) {
			y = pb[i]
		}
		if c := compareNumber(x, y); c != 0 {
			return c < 0
		}
	}
	if preA == "" || preB == "" {
		return preA != "" && preB == ""
	}
	ia, ib := strings.Split(preA, "."), strings.Split(preB, ".")
	for i := 0; i < min(len(ia), len(ib)); i++ {
		if ia[i] == ib[i] {
			continue
		}
		numA, numB := numeric(ia[i]), numeric(ib[i])
		switch {
		case numA && numB:
			return compareNumber(ia[i], ib[i]) < 0
		case numA || numB:
			return numA
		default:
			return ia[i] < ib[i]
		}
	}
	return len(ia) < len(ib)
}

func numeric(s string) bool {
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func compareNumber(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	if a == "" {
		a = "0"
	}
	if b == "" {
		b = "0"
	}
	if len(a) != len(b) {
		if len(a) < len(b) {
			return -1
		}
		return 1
	}
	return strings.Compare(a, b)
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
