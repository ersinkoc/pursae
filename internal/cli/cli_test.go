package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type result struct {
	code     int
	out, err string
	ctx      *Ctx
}

func run(t *testing.T, r *Registry, stdin string, interactive bool, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := r.Run(context.Background(), args, IO{In: strings.NewReader(stdin), Out: &out, Err: &errb, Interactive: interactive})
	return result{code: code, out: out.String(), err: errb.String()}
}

// testRegistry builds a small tree and records the last Ctx a command saw.
func testRegistry(last **Ctx) *Registry {
	r := New("pursae")
	r.Description = "Pursae command line."
	r.AddGlobalFlag(Flag{Name: "user", Usage: "Act as this user."})
	r.Noun("post", "Manage posts.")
	r.Noun("post meta", "Manage post custom fields.")
	record := func(c *Ctx) error { *last = c; return nil }
	r.Add(Command{
		Path:  "post list",
		Short: "List posts.",
		Flags: []Flag{
			{Name: "post_type", Default: "post", Usage: "Post type."},
			{Name: "post_status", Values: []string{"publish", "draft", "any"}},
			{Name: "porcelain", Bool: true},
		},
		Examples: []string{"post list --format=ids"},
		Run: func(c *Ctx) error {
			*last = c
			return c.Print([]Row{
				{"ID": int64(1), "post_title": "Hello", "post_status": "publish"},
				{"ID": int64(2), "post_title": "World, again", "post_status": "draft"},
			}, View{Fields: []string{"ID", "post_title", "post_status"}})
		},
	})
	r.Add(Command{
		Path:  "post get",
		Short: "Get a post.",
		Args:  []Arg{{Name: "id"}},
		Run: func(c *Ctx) error {
			*last = c
			return c.PrintItem(Row{"ID": int64(7), "post_title": "Seven", "tags": []string{"a", "b"}}, []string{"ID", "post_title", "tags"})
		},
	})
	r.Add(Command{Path: "post delete", Short: "Delete posts.", Args: []Arg{{Name: "id", Repeat: true}}, Flags: []Flag{{Name: "force", Bool: true}},
		Run: func(c *Ctx) error {
			*last = c
			if err := c.Confirm("Delete %d post(s)?", len(c.Args)); err != nil {
				return err
			}
			c.Success("Deleted.")
			return nil
		}})
	r.Add(Command{Path: "post exists", Args: []Arg{{Name: "id"}}, Run: func(c *Ctx) error { *last = c; return Exit(1) }})
	r.Add(Command{Path: "post meta get", Short: "Get a meta value.", Args: []Arg{{Name: "id"}, {Name: "key"}, {Name: "extra", Optional: true}}, Formats: []string{"plaintext"}, DefaultFormat: "plaintext", Run: record})
	r.Add(Command{Path: "post create", AnyFlags: true, Flags: []Flag{{Name: "post_title", Required: true}}, Run: record})
	r.Add(Command{Path: "plugin pack", Short: "Pack a plugin.", Raw: true, Run: record})
	r.Add(Command{Path: "serve", Short: "Run the web server.", External: true})
	r.Add(Command{Path: "import", Short: "Import a WXR file.", Args: []Arg{{Name: "file"}}, Run: record})
	r.Add(Command{Path: "fail", Run: func(c *Ctx) error { return errors.New("boom") }, Hidden: true})
	r.Add(Command{Path: "usage", Run: func(c *Ctx) error { return Usagef("bad %s", "thing") }, Hidden: true})
	return r
}

func TestRegistryPanicsOnBadDeclarations(t *testing.T) {
	cases := map[string]Command{
		"empty path":      {Path: " ", Run: func(*Ctx) error { return nil }},
		"no run":          {Path: "x"},
		"repeat not last": {Path: "x", Args: []Arg{{Name: "a", Repeat: true}, {Name: "b"}}, Run: func(*Ctx) error { return nil }},
		"req after opt":   {Path: "x", Args: []Arg{{Name: "a", Optional: true}, {Name: "b"}}, Run: func(*Ctx) error { return nil }},
		"dup flag":        {Path: "x", Flags: []Flag{{Name: "a"}, {Name: "a"}}, Run: func(*Ctx) error { return nil }},
		"shadows global":  {Path: "x", Flags: []Flag{{Name: "format"}}, Run: func(*Ctx) error { return nil }},
	}
	for name, cmd := range cases {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			New("p").Add(cmd)
		})
	}
	t.Run("duplicate", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatal("no panic")
			}
		}()
		r := New("p")
		r.Add(Command{Path: "a b", Run: func(*Ctx) error { return nil }})
		r.Add(Command{Path: "a  b", Run: func(*Ctx) error { return nil }})
	})
}

func TestFlagParsing(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	res := run(t, r, "", false, "post", "list", "--post_type", "page", "--post_status=draft", "--porcelain", "--user=admin", "-q")
	if res.code != 0 {
		t.Fatalf("code %d, err %s", res.code, res.err)
	}
	if got := last.Flag("post_type"); got != "page" {
		t.Errorf("post_type = %q", got)
	}
	if got := last.Flag("post_status"); got != "draft" {
		t.Errorf("post_status = %q", got)
	}
	if !last.Bool("porcelain") || !last.Quiet || last.Flag("user") != "admin" {
		t.Errorf("bool/global flags not set: %v", last.Flags())
	}

	run(t, r, "", false, "post", "list")
	if last.Flag("post_type") != "post" || last.Has("post_type") {
		t.Errorf("default not applied: %q has=%v", last.Flag("post_type"), last.Has("post_type"))
	}
	run(t, r, "", false, "post", "list", "--porcelain", "--no-porcelain")
	if last.Bool("porcelain") {
		t.Error("--no-porcelain did not clear the flag")
	}
	run(t, r, "", false, "post", "list", "--porcelain=false")
	if last.Bool("porcelain") {
		t.Error("--porcelain=false")
	}
	// leading global flags move behind the path
	run(t, r, "", false, "--format=json", "post", "list")
	if last.Format != "json" {
		t.Errorf("leading --format: %q", last.Format)
	}
}

func TestPositionalArgs(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	res := run(t, r, "", false, "post", "delete", "1", "2", "--force", "3", "--yes")
	if res.code != 0 {
		t.Fatalf("code %d: %s", res.code, res.err)
	}
	if strings.Join(last.Args, ",") != "1,2,3" {
		t.Errorf("args %v", last.Args)
	}
	ids, err := last.ArgIDs(0)
	if err != nil || len(ids) != 3 || ids[2] != 3 {
		t.Errorf("ArgIDs %v %v", ids, err)
	}
	// "--" ends flags; "-" and negative numbers are positional
	run(t, r, "", false, "post", "meta", "get", "5", "--", "--weird")
	if last.Arg(1) != "--weird" {
		t.Errorf("after --: %v", last.Args)
	}
	run(t, r, "", false, "post", "meta", "get", "-", "-1")
	if last.Arg(0) != "-" || last.Arg(1) != "-1" {
		t.Errorf("dash args: %v", last.Args)
	}
	if last.Format != "plaintext" {
		t.Errorf("default format %q", last.Format)
	}
	if last.Arg(9) != "" {
		t.Error("Arg out of range")
	}
}

func TestUsageErrors(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"post", "get"}, "missing argument <id>"},
		{[]string{"post", "get", "1", "2"}, "too many arguments"},
		{[]string{"post", "delete"}, "missing argument <id>"},
		{[]string{"post", "list", "--post_typ=page"}, "Did you mean --post_type?"},
		{[]string{"post", "list", "--post_status=bogus"}, "invalid value"},
		{[]string{"post", "list", "--post_type"}, "--post_type needs a value"},
		{[]string{"post", "list", "--porcelain=maybe"}, "expects true or false"},
		{[]string{"post", "list", "--format=xml"}, "invalid --format"},
		{[]string{"post", "list", "-x"}, "unknown flag -x"},
		{[]string{"post", "create"}, "missing --post_title"},
		{[]string{"post", "lst"}, "Did you mean 'post list'?"},
		{[]string{"pots"}, "Did you mean 'post'?"},
		{[]string{"usage"}, "bad thing"},
	}
	for _, c := range cases {
		res := run(t, r, "", false, c.args...)
		if res.code != ExitUsage {
			t.Errorf("%v: code %d, want %d (%s)", c.args, res.code, ExitUsage, res.err)
		}
		if !strings.Contains(res.err, c.want) {
			t.Errorf("%v: stderr %q, want %q", c.args, res.err, c.want)
		}
	}
}

func TestAnyFlagsAndRaw(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	res := run(t, r, "", false, "post", "create", "--post_title=Hi", "--meta_x=1", "--flagonly")
	if res.code != 0 {
		t.Fatalf("%d %s", res.code, res.err)
	}
	if last.Flag("meta_x") != "1" || last.Flag("flagonly") != "true" {
		t.Errorf("any flags: %v", last.Flags())
	}
	run(t, r, "", false, "plugin", "pack", "--key", "k.pem", "-o", "x", "dir")
	if strings.Join(last.Args, " ") != "--key k.pem -o x dir" {
		t.Errorf("raw args %v", last.Args)
	}
	res = run(t, r, "", false, "plugin", "pack", "--help")
	if res.code != 0 || !strings.Contains(res.out, "pursae plugin pack") {
		t.Errorf("raw help: %d %q", res.code, res.out)
	}
}

func TestExitCodes(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	if res := run(t, r, "", false, "post", "exists", "4"); res.code != 1 || res.err != "" {
		t.Errorf("exists: %d %q", res.code, res.err)
	}
	if res := run(t, r, "", false, "fail"); res.code != 1 || res.err != "Error: boom\n" {
		t.Errorf("fail: %d %q", res.code, res.err)
	}
	if res := run(t, r, "", false, "serve"); res.code != ExitUsage {
		t.Errorf("external: %d", res.code)
	}
}

func TestConfirm(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	// non-interactive without --yes refuses
	res := run(t, r, "y\n", false, "post", "delete", "1")
	if res.code != 1 || !strings.Contains(res.err, "pass --yes") {
		t.Errorf("non-interactive: %d %q", res.code, res.err)
	}
	// -y
	if res := run(t, r, "", false, "post", "delete", "1", "-y"); res.code != 0 || !strings.Contains(res.out, "Success: Deleted.") {
		t.Errorf("-y: %d %q %q", res.code, res.out, res.err)
	}
	// interactive yes / no
	if res := run(t, r, "yes\n", true, "post", "delete", "1"); res.code != 0 || !strings.Contains(res.err, "Delete 1 post(s)? [y/n]") {
		t.Errorf("interactive yes: %d %q", res.code, res.err)
	}
	if res := run(t, r, "n\n", true, "post", "delete", "1"); res.code != 1 || !strings.Contains(res.err, "Aborted.") {
		t.Errorf("interactive no: %d %q", res.code, res.err)
	}
	// --quiet hides Success
	if res := run(t, r, "", false, "post", "delete", "1", "--yes", "--quiet"); res.out != "" {
		t.Errorf("quiet: %q", res.out)
	}
}

func TestHelp(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	for _, args := range [][]string{nil, {"help"}, {"--help"}, {"-h"}} {
		res := run(t, r, "", false, args...)
		if res.code != 0 {
			t.Fatalf("%v: code %d", args, res.code)
		}
		for _, want := range []string{"usage: pursae <command>", "Pursae command line.", "post", "Manage posts.", "serve", "Run the web server.", "import", "GLOBAL PARAMETERS", "--user=<user>"} {
			if !strings.Contains(res.out, want) {
				t.Errorf("%v: top help missing %q:\n%s", args, want, res.out)
			}
		}
		if strings.Contains(res.out, "fail") {
			t.Error("hidden command listed")
		}
	}
	res := run(t, r, "", false, "help", "post")
	for _, want := range []string{"pursae post", "Manage posts.", "SUBCOMMANDS", "list", "List posts.", "meta", "Manage post custom fields."} {
		if !strings.Contains(res.out, want) {
			t.Errorf("noun help missing %q:\n%s", want, res.out)
		}
	}
	if res2 := run(t, r, "", false, "post"); res2.out != res.out {
		t.Errorf("bare noun should print its help")
	}
	res = run(t, r, "", false, "help", "post", "list")
	for _, want := range []string{"NAME", "pursae post list", "SYNOPSIS", "[--post_type=<post_type>]", "Default: post", "Options: publish, draft, any", "EXAMPLES", "pursae post list --format=ids", "GLOBAL PARAMETERS"} {
		if !strings.Contains(res.out, want) {
			t.Errorf("command help missing %q:\n%s", want, res.out)
		}
	}
	if res2 := run(t, r, "", false, "post", "list", "--help"); res2.out != res.out {
		t.Error("--help differs from help <cmd>")
	}
	if res2 := run(t, r, "", false, "post", "list", "-h"); res2.out != res.out {
		t.Error("-h differs from help <cmd>")
	}
	res = run(t, r, "", false, "help", "post", "delete")
	if !strings.Contains(res.out, "<id>...") {
		t.Errorf("repeat arg syntax:\n%s", res.out)
	}
	res = run(t, r, "", false, "help", "post", "meta", "get")
	if !strings.Contains(res.out, "[<extra>]") {
		t.Errorf("optional arg syntax:\n%s", res.out)
	}
	res = run(t, r, "", false, "help", "serve")
	if res.code != 0 || !strings.Contains(res.out, "Run the web server.") {
		t.Errorf("external help: %q", res.out)
	}
	res = run(t, r, "", false, "help", "nope")
	if res.code != ExitUsage {
		t.Errorf("help nope: %d", res.code)
	}
	var b bytes.Buffer
	r.Help(&b, "post meta")
	if !strings.Contains(b.String(), "get") {
		t.Error("Registry.Help")
	}
}

func TestLookupAndCommands(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	if r.Lookup("post meta get") == nil || r.Lookup("post meta") != nil || r.Lookup("x y") != nil {
		t.Error("Lookup")
	}
	cmds := r.Commands()
	if len(cmds) == 0 || cmds[0].Path > cmds[len(cmds)-1].Path {
		t.Error("Commands not sorted")
	}
}

func TestSuggest(t *testing.T) {
	cands := []string{"list", "get", "create", "delete", "update"}
	cases := map[string]string{"lst": "list", "cre": "create", "updte": "update", "zzzzzz": "", "": ""}
	for in, want := range cases {
		if got := Suggest(in, cands); got != want {
			t.Errorf("Suggest(%q) = %q, want %q", in, got, want)
		}
	}
	if levenshtein("kitten", "sitting") != 3 {
		t.Error("levenshtein")
	}
}

func TestCtxHelpers(t *testing.T) {
	r := New("p")
	var last *Ctx
	r.Add(Command{Path: "x", Args: []Arg{{Name: "a", Optional: true, Repeat: true}}, Flags: []Flag{{Name: "n"}, {Name: "ids"}}, Run: func(c *Ctx) error { last = c; return nil }})
	run(t, r, "", false, "x", "--n=12", "--ids=a, b,,c")
	if n, err := last.Int("n", 0); n != 12 || err != nil {
		t.Errorf("Int %d %v", n, err)
	}
	if n, err := last.Int64("missing", 9); n != 9 || err != nil {
		t.Errorf("Int64 default %d %v", n, err)
	}
	if got := strings.Join(last.List("ids"), "|"); got != "a|b|c" {
		t.Errorf("List %q", got)
	}
	run(t, r, "", false, "x", "--n=abc")
	if _, err := last.Int("n", 0); err == nil {
		t.Error("Int accepted abc")
	} else {
		var ue *UsageError
		if !errors.As(err, &ue) {
			t.Error("Int error is not a UsageError")
		}
	}
	if _, err := ParseID("0"); err == nil {
		t.Error("ParseID(0)")
	}
	if id, _ := ParseID(" 42 "); id != 42 {
		t.Error("ParseID(42)")
	}
	run(t, r, "piped", false, "x", "-")
	b, err := last.ReadInput("-", nil)
	if err != nil || string(b) != "piped" {
		t.Errorf("ReadInput stdin %q %v", b, err)
	}
	b, _ = last.ReadInput("f", func(string) ([]byte, error) { return []byte("file"), nil })
	if string(b) != "file" {
		t.Error("ReadInput file")
	}
	for _, s := range []string{"yes", "1", "on", "true"} {
		if v, err := ParseBool(s); !v || err != nil {
			t.Errorf("ParseBool(%q)", s)
		}
	}
	if _, err := ParseBool("perhaps"); err == nil {
		t.Error("ParseBool(perhaps)")
	}
}

// --- output formats ------------------------------------------------------------------

func printWith(t *testing.T, args ...string) string {
	t.Helper()
	var last *Ctx
	r := testRegistry(&last)
	res := run(t, r, "", false, args...)
	if res.code != 0 {
		t.Fatalf("%v: %d %s", args, res.code, res.err)
	}
	return res.out
}

func TestPrintTable(t *testing.T) {
	got := printWith(t, "post", "list")
	want := "" +
		"+----+--------------+-------------+\n" +
		"| ID | post_title   | post_status |\n" +
		"+----+--------------+-------------+\n" +
		"| 1  | Hello        | publish     |\n" +
		"| 2  | World, again | draft       |\n" +
		"+----+--------------+-------------+\n"
	if got != want {
		t.Errorf("table:\n%s\nwant:\n%s", got, want)
	}
	got = printWith(t, "post", "list", "--fields=post_status, ID")
	if !strings.HasPrefix(strings.Split(got, "\n")[1], "| post_status | ID |") {
		t.Errorf("--fields order:\n%s", got)
	}
}

func TestPrintFormats(t *testing.T) {
	if got := printWith(t, "post", "list", "--format=json"); got != `[{"ID":1,"post_title":"Hello","post_status":"publish"},{"ID":2,"post_title":"World, again","post_status":"draft"}]`+"\n" {
		t.Errorf("json: %s", got)
	}
	if got := printWith(t, "post", "list", "--format=csv", "--fields=ID,post_title"); got != "ID,post_title\n1,Hello\n2,\"World, again\"\n" {
		t.Errorf("csv: %q", got)
	}
	if got := printWith(t, "post", "list", "--format=ids"); got != "1 2\n" {
		t.Errorf("ids: %q", got)
	}
	if got := printWith(t, "post", "list", "--format=count"); got != "2\n" {
		t.Errorf("count: %q", got)
	}
	if got := printWith(t, "post", "list", "--field=post_title"); got != "Hello\nWorld, again\n" {
		t.Errorf("field: %q", got)
	}
	if got := printWith(t, "post", "list", "--format=yaml", "--fields=ID,post_title"); got != "- ID: 1\n  post_title: Hello\n- ID: 2\n  post_title: World, again\n" {
		t.Errorf("yaml: %q", got)
	}
}

func TestPrintItem(t *testing.T) {
	got := printWith(t, "post", "get", "7")
	if !strings.Contains(got, "| Field      | Value |") || !strings.Contains(got, "| tags       | a,b   |") {
		t.Errorf("item table:\n%s", got)
	}
	if got := printWith(t, "post", "get", "7", "--format=json"); got != `{"ID":7,"post_title":"Seven","tags":["a","b"]}`+"\n" {
		t.Errorf("item json: %s", got)
	}
	if got := printWith(t, "post", "get", "7", "--format=yaml"); got != "ID: 7\npost_title: Seven\ntags:\n  - a\n  - b\n" {
		t.Errorf("item yaml: %q", got)
	}
	if got := printWith(t, "post", "get", "7", "--field=post_title"); got != "Seven\n" {
		t.Errorf("item field: %q", got)
	}
	var last *Ctx
	if res := run(t, testRegistry(&last), "", false, "post", "get", "7", "--field=nope"); res.code != 1 {
		t.Errorf("unknown field: %d", res.code)
	}
	if res := run(t, testRegistry(&last), "", false, "post", "get", "7", "--format=count"); res.code != ExitUsage {
		t.Errorf("count on item: %d", res.code)
	}
}

func TestWriteEdgeCases(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, "table", nil, []string{"a"}); err != nil || b.Len() != 0 {
		t.Errorf("empty table printed %q", b.String())
	}
	b.Reset()
	Write(&b, "json", nil, []string{"a"})
	if b.String() != "[]\n" {
		t.Errorf("empty json %q", b.String())
	}
	b.Reset()
	Write(&b, "yaml", nil, []string{"a"})
	if b.String() != "[]\n" {
		t.Errorf("empty yaml %q", b.String())
	}
	b.Reset()
	Write(&b, "table", []Row{{"a": "x\ny", "b": nil}}, []string{"a", "b"})
	if !strings.Contains(b.String(), "| x y |   |") {
		t.Errorf("newline in cell:\n%s", b.String())
	}
	b.Reset()
	Write(&b, "table", []Row{{"n": "ünï"}}, []string{"n"})
	if !strings.Contains(b.String(), "| ünï |") || !strings.Contains(b.String(), "+-----+") {
		t.Errorf("unicode width:\n%s", b.String())
	}
	b.Reset()
	Write(&b, "json", []Row{{"h": "<a&b>", "m": map[string]any{"z": 1, "a": []any{true, nil}}}}, []string{"h", "m", "missing"})
	var decoded []map[string]any
	if err := json.Unmarshal(b.Bytes(), &decoded); err != nil || decoded[0]["h"] != "<a&b>" || decoded[0]["missing"] != nil {
		t.Errorf("json nested: %s %v", b.String(), err)
	}
	if !strings.Contains(b.String(), "<a&b>") {
		t.Error("json escaped HTML")
	}
	b.Reset()
	Write(&b, "yaml", []Row{{"m": map[string]any{"z": 1, "a": []any{"x", map[string]any{"k": "v"}}}, "e": map[string]any{}, "s": "true", "q": "a: b", "n": nil}}, []string{"m", "e", "s", "q", "n"})
	want := "- m:\n    a:\n      - x\n      - k: v\n    z: 1\n  e: {}\n  s: \"true\"\n  q: \"a: b\"\n  \"n\": null\n"
	if b.String() != want {
		t.Errorf("yaml nested:\n%s\nwant:\n%s", b.String(), want)
	}
	if err := Write(&b, "xml", nil, nil); err == nil {
		t.Error("unknown format accepted")
	}
}

type stringer struct{}

func (stringer) String() string { return "S" }

func TestStringify(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var nilTime *time.Time
	n := 5
	cases := []struct {
		in   any
		want string
	}{
		{nil, ""}, {"s", "s"}, {[]byte("b"), "b"}, {true, "true"}, {false, "false"},
		{3, "3"}, {int64(-4), "-4"}, {uint8(7), "7"}, {2.0, "2"}, {2.5, "2.5"}, {float32(1.5), "1.5"},
		{ts, "2026-01-02 03:04:05"}, {time.Time{}, ""}, {&ts, "2026-01-02 03:04:05"}, {nilTime, ""},
		{[]string{"a", "b"}, "a,b"}, {[]int{1, 2}, "[1,2]"}, {map[string]int{"a": 1}, `{"a":1}`},
		{stringer{}, "S"}, {&n, "5"}, {errors.New("e"), "e"}, {json.Number("12"), "12"},
	}
	for _, c := range cases {
		if got := Stringify(c.in); got != c.want {
			t.Errorf("Stringify(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestYAMLScalars(t *testing.T) {
	plain := []string{"hello", "hello world", "a-b", "x:y", "ünï"}
	quoted := []string{"", " x", "yes", "No", "null", "~", "12", "1.5", "-x", "#c", "a: b", "a #b", "end:", "line\nbreak", "*star", "@at", "'q"}
	for _, s := range plain {
		if yamlString(s) != s {
			t.Errorf("%q should be plain, got %s", s, yamlString(s))
		}
	}
	for _, s := range quoted {
		if yamlString(s) == s {
			t.Errorf("%q should be quoted", s)
		}
	}
	var b bytes.Buffer
	writeYAMLValue(&b, struct {
		A int    `json:"a"`
		B string `json:"b"`
	}{1, "x"}, 0)
	if b.String() != "a: 1\nb: x\n" {
		t.Errorf("struct yaml %q", b.String())
	}
}

func TestPrintValue(t *testing.T) {
	r := New("p")
	val := any(map[string]any{"a": []any{1, "x"}})
	r.Add(Command{Path: "v", Formats: []string{"plaintext"}, Run: func(c *Ctx) error { return c.PrintValue(val) }})
	if got := run(t, r, "", false, "v", "--format=json"); got.out != `{"a":[1,"x"]}`+"\n" {
		t.Errorf("json %q", got.out)
	}
	if got := run(t, r, "", false, "v", "--format=yaml"); got.out != "a:\n  - 1\n  - x\n" {
		t.Errorf("yaml %q", got.out)
	}
	val = "plain"
	if got := run(t, r, "", false, "v", "--format=plaintext"); got.out != "plain\n" {
		t.Errorf("plain %q", got.out)
	}
}

func TestNounWithCommandAndChildren(t *testing.T) {
	r := New("p")
	var ran string
	r.Add(Command{Path: "cache", Short: "Cache status.", Run: func(c *Ctx) error { ran = "cache"; return nil }})
	r.Add(Command{Path: "cache flush", Run: func(c *Ctx) error { ran = "flush"; return nil }})
	run(t, r, "", false, "cache")
	if ran != "cache" {
		t.Error("noun command not run")
	}
	run(t, r, "", false, "cache", "flush")
	if ran != "flush" {
		t.Error("child not run")
	}
	res := run(t, r, "", false, "help", "cache")
	if !strings.Contains(res.out, "Cache status.") || !strings.Contains(res.out, "SUBCOMMANDS") {
		t.Errorf("mixed help:\n%s", res.out)
	}
}

func TestLeadingGlobalFlagWithSpacedValue(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	res := run(t, r, "", false, "--format", "json", "--user", "admin", "-q", "post", "list")
	if res.code != 0 {
		t.Fatalf("code %d, err %s", res.code, res.err)
	}
	if last.Format != "json" || last.Flag("user") != "admin" || !last.Quiet {
		t.Fatalf("format=%q user=%q quiet=%v", last.Format, last.Flag("user"), last.Quiet)
	}
}

func TestHelpHelp(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	res := run(t, r, "", false, "help", "help")
	if res.code != 0 || !strings.Contains(res.out, "pursae help [<command>...]") {
		t.Fatalf("code %d out %q err %q", res.code, res.out, res.err)
	}
}

func TestSanitizeOutput(t *testing.T) {
	if got := Sanitize("a\x1b]52;c;eA==\x07b\tc\r\nd\re\u009bf"); got != `a\x1b]52;c;eA==\x07b`+"\tc\r\nd"+`\x0de\u009bf` {
		t.Fatalf("Sanitize = %q", got)
	}
	var out bytes.Buffer
	rows := []Row{{"a": "\x1b[2Kx", "b": "=HYPERLINK(1)"}, {"a": "-5", "b": "@x"}}
	if err := Write(&out, "table", rows, []string{"a", "b"}); err != nil || strings.Contains(out.String(), "\x1b") {
		t.Fatalf("table %q %v", out.String(), err)
	}
	out.Reset()
	if err := Write(&out, "csv", rows, []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}
	if want := "a,b\n\\x1b[2Kx,'=HYPERLINK(1)\n-5,'@x\n"; out.String() != want {
		t.Fatalf("csv %q, want %q", out.String(), want)
	}
	out.Reset()
	if err := Write(&out, "json", rows[:1], []string{"a"}); err != nil || !strings.Contains(out.String(), `\u001b`) {
		t.Fatalf("json keeps the exact value: %q %v", out.String(), err)
	}
}

func TestUnknownFieldIsAnError(t *testing.T) {
	var last *Ctx
	r := testRegistry(&last)
	for _, args := range [][]string{
		{"post", "list", "--field=titel"},
		{"post", "list", "--fields=ID,titel"},
		{"post", "get", "1", "--fields=ID,titel"},
	} {
		if res := run(t, r, "", false, args...); res.code != ExitFailure || !strings.Contains(res.err, "invalid field: titel") {
			t.Errorf("%v: code %d err %q", args, res.code, res.err)
		}
	}
	if res := run(t, r, "", false, "post", "list", "--field=POST_TITLE"); res.code != 0 || res.out != "Hello\nWorld, again\n" {
		t.Errorf("known field: code %d out %q", res.code, res.out)
	}
}
