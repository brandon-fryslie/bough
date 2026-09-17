package pointer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fake is the environment variable that makes this test binary act as
// lowtalker: its value is what to play back, the exit status and what to say
// on stdout and stderr, and where to keep the script it was sent.
const fake = "BOUGH_FAKE_LOWTALKER"

// TestMain runs the binary as lowtalker when asked to, so Play can be tested
// against a program that behaves as lowtalker does on every platform CI runs.
func TestMain(m *testing.M) {
	if os.Getenv(fake) == "" {
		os.Exit(m.Run())
	}
	script, err := io.ReadAll(os.Stdin)
	if err != nil {
		os.Exit(99)
	}
	if err := os.WriteFile(os.Getenv(fake+"_SCRIPT"), script, 0o600); err != nil {
		os.Exit(98)
	}
	if strings.Join(os.Args[1:], " ") != "pointer play --into com.apple.Safari" {
		fmt.Fprintf(os.Stderr, "asked to %q", os.Args[1:])
		os.Exit(97)
	}
	if os.Getenv(fake+"_HOLD") != "" {
		hold()
	}
	fmt.Fprint(os.Stdout, os.Getenv(fake+"_STDOUT"))
	fmt.Fprint(os.Stderr, os.Getenv(fake+"_STDERR"))
	var status int
	fmt.Sscan(os.Getenv(fake), &status)
	os.Exit(status)
}

// hold plays a press and waits to be interrupted, as lowtalker mid-drag does,
// and notes that it let the button go when it was.
func hold() {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	fmt.Fprint(os.Stdout, report(0, 10))
	if err := os.WriteFile(os.Getenv(fake+"_HOLD"), []byte("held"), 0o600); err != nil {
		os.Exit(96)
	}
	select {
	case <-interrupt:
		_ = os.WriteFile(os.Getenv(fake+"_HOLD"), []byte("released"), 0o600)
		os.Exit(1)
	case <-time.After(time.Minute):
		os.Exit(95)
	}
}

// lowtalker is a Device that exits with status after writing stdout and
// stderr, and the file the script it was sent is kept in.
func lowtalker(t *testing.T, status int, stdout, stderr string) (Device, string) {
	t.Helper()
	script := t.TempDir() + "/script.jsonl"
	t.Setenv(fake, strconv.Itoa(status))
	t.Setenv(fake+"_SCRIPT", script)
	t.Setenv(fake+"_STDOUT", stdout)
	t.Setenv(fake+"_STDERR", stderr)
	return At(os.Args[0]), script
}

// A script is written as lowtalker reads it: where the cursor starts, then a
// line a report at its time in milliseconds.
func TestAScriptIsWrittenAsLowtalkerReadsIt(t *testing.T) {
	s := Script{Start: ScreenPoint{X: 800, Y: 500}, Reports: []Report{
		{At: 0, Does: Press{}},
		{At: 8333 * time.Microsecond, Does: Move{DX: 4, DY: -2}},
		{At: 16700 * time.Microsecond, Does: Turn{Ticks: -1}},
		{At: time.Second, Does: Release{}},
	}}
	got, err := s.lines()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"to":{"x":800,"y":500}}
{"down":"left","t_ms":0}
{"move":{"dx":4,"dy":-2},"t_ms":8.333}
{"t_ms":16.7,"wheel":{"h":0,"v":-1}}
{"t_ms":1000,"up":true}
`
	if string(got) != want {
		t.Errorf("wrote\n%s\nwant\n%s", got, want)
	}
}

// report is lowtalker's line for report index, sent late microseconds after
// its time.
func report(index int, late int64) string {
	at := int64(1_789_646_000_000_000) + int64(index)*8333
	return fmt.Sprintf(`{"report":{"index":%d,"scheduled_us":%d,"sent_us":%d,"acked_us":%d}}`+"\n", index, at, at+late, at+late+150)
}

func done(reports int) string {
	return fmt.Sprintf(`{"done":{"reports":%d,"start_reports":3,"late_us":{"p50":10,"p90":20,"p99":30,"max":40}}}`+"\n", reports)
}

var twoMoves = Script{Start: ScreenPoint{X: 1, Y: 2}, Reports: []Report{
	{At: 0, Does: Move{DX: 1}},
	{At: 8333 * time.Microsecond, Does: Move{DX: 1}},
}}

// A play that went out whole hands back when each report went, having sent
// lowtalker the script.
func TestAPlayHandsBackWhenEachReportWent(t *testing.T) {
	d, kept := lowtalker(t, 0, report(0, 120)+report(1, 2500)+done(2), "")
	played, err := d.Play(context.Background(), "com.apple.Safari", twoMoves)
	if err != nil {
		t.Fatal(err)
	}
	if len(played) != 2 || played[0].Late() != 120*time.Microsecond || played.Latest() != 2500*time.Microsecond {
		t.Errorf("played %+v, latest %v", played, played.Latest())
	}
	if played[1].Acked.Sub(played[1].Sent) != 150*time.Microsecond {
		t.Errorf("report 1 acked %v after it was sent, want 150µs", played[1].Acked.Sub(played[1].Sent))
	}
	sent, err := os.ReadFile(kept)
	if err != nil {
		t.Fatal(err)
	}
	want, err := twoMoves.lines()
	if err != nil {
		t.Fatal(err)
	}
	if string(sent) != string(want) {
		t.Errorf("sent lowtalker\n%s\nwant\n%s", sent, want)
	}
}

// A play that did not go out whole is an error that says how far it got and
// why, and one that could not start for want of setup says so apart from the
// rest, since nothing a run does will change it.
func TestAPlayThatDidNotGoOutWholeIsRefused(t *testing.T) {
	for _, c := range []struct {
		name           string
		status         int
		stdout, stderr string
		says           string
		notSetUp       bool
	}{
		{"helper not registered", 4, "", "the keyboard helper is not registered", "not registered", true},
		{"driver extension off", 5, "", "the driver extension is not activated", "not activated", true},
		{"focus moved", 1, report(0, 10), "Safari left the front after 1 report", "stopped after 1 of 2 reports", false},
		{"no done line", 0, report(0, 10) + report(1, 10), "", "without saying it was done", false},
		{"done early", 0, report(0, 10) + done(1), "", "played 1 reports of a script of 2", false},
		{"done disagrees", 0, report(0, 10) + report(1, 10) + done(3), "", "done after 3 reports", false},
		{"reports out of order", 0, report(1, 10) + report(0, 10) + done(2), "", "report 1 where report 0 was next", false},
		{"a report missing its times", 0, `{"report":{"index":0}}` + "\n", "", "neither a whole report nor done", false},
		{"something after done", 0, report(0, 10) + report(1, 10) + done(2) + report(2, 10), "", "after done", false},
		{"not JSON", 0, "played\n", "", "line 1", false},
	} {
		d, _ := lowtalker(t, c.status, c.stdout, c.stderr)
		_, err := d.Play(context.Background(), "com.apple.Safari", twoMoves)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: played with %v, want an error saying %q", c.name, err, c.says)
			continue
		}
		if errors.Is(err, ErrNotSetUp) != c.notSetUp {
			t.Errorf("%s: %v is not-set-up %v, want %v", c.name, err, errors.Is(err, ErrNotSetUp), c.notSetUp)
		}
	}
}

// A play whose context ends is interrupted rather than killed, so lowtalker
// lets go of the buttons it holds instead of leaving one down on the screen.
func TestAPlayCutShortLetsGoOfTheButton(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("lowtalker runs on macOS, and Windows has no interrupt to send")
	}
	d, _ := lowtalker(t, 0, "", "")
	held := t.TempDir() + "/held"
	t.Setenv(fake+"_HOLD", held)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		for {
			if b, err := os.ReadFile(held); err == nil && string(b) == "held" {
				cancel()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()
	if _, err := d.Play(ctx, "com.apple.Safari", Script{Reports: []Report{{Does: Press{}}, {At: time.Second, Does: Release{}}}}); err == nil {
		t.Error("a play cut short came back as played")
	}
	if b, _ := os.ReadFile(held); string(b) != "released" {
		t.Errorf("the play cut short left the button %q", b)
	}
}
