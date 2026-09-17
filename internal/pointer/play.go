package pointer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Device is lowtalker, the program that sends a virtual mouse's reports.
type Device struct {
	path string
}

// ErrNotSetUp is a device that could not send anything, because its keyboard
// helper or driver extension is not set up. `lowtalker onboard` names the step.
var ErrNotSetUp = errors.New("lowtalker's keyboard helper cannot be reached; run lowtalker onboard to see the step left")

// Find is lowtalker as found on PATH.
func Find() (Device, error) {
	path, err := exec.LookPath("lowtalker")
	if err != nil {
		return Device{}, fmt.Errorf("playing input through a pointing device needs lowtalker on PATH: %w", err)
	}
	return At(path), nil
}

// At is lowtalker at path.
func At(path string) Device {
	return Device{path: path}
}

// Sent is one report as the device sent it: when it was meant to go, when it
// went, and when the system took it.
type Sent struct {
	Scheduled time.Time
	Sent      time.Time
	Acked     time.Time
}

// Late is how long after its time the report went.
func (s Sent) Late() time.Duration {
	return s.Sent.Sub(s.Scheduled)
}

// Played is every report of a script as it went, in the script's order.
type Played []Sent

// Latest is how late the latest report to go went.
func (p Played) Latest() time.Duration {
	var latest time.Duration
	for _, s := range p {
		latest = max(latest, s.Late())
	}
	return latest
}

// interrupted is how long a play interrupted by its context has to let go of
// the buttons it holds before it is killed.
const interrupted = 5 * time.Second

// Play plays s in the app whose bundle id is into, raising it first, and hands
// back when each report went. A play that stopped short is an error, with how
// far it got: the app left the front, a report was refused, or ctx ended.
func (d Device) Play(ctx context.Context, into string, s Script) (Played, error) {
	script, err := s.lines()
	if err != nil {
		return nil, fmt.Errorf("writing the script: %w", err)
	}
	cmd := exec.CommandContext(ctx, d.path, "pointer", "play", "--into", into) //#nosec G204 -- the lowtalker the caller chose
	// An interrupted play releases every button it holds; a killed one would
	// leave the button down on the user's screen.
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = interrupted
	cmd.Stdin = bytes.NewReader(script)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	ran := cmd.Run()
	played, done, read := readPlayed(stdout.Bytes())
	var exit *exec.ExitError
	switch {
	case errors.As(ran, &exit) && (exit.ExitCode() == 4 || exit.ExitCode() == 5):
		return nil, fmt.Errorf("%w: %s", ErrNotSetUp, strings.TrimSpace(stderr.String()))
	case ran != nil:
		return nil, fmt.Errorf("playing through lowtalker stopped after %d of %d reports: %w: %s",
			len(played), len(s.Reports), ran, strings.TrimSpace(stderr.String()))
	case read != nil:
		return nil, fmt.Errorf("reading what lowtalker played: %w", read)
	case !done:
		return nil, fmt.Errorf("lowtalker exited without saying it was done, after %d of %d reports", len(played), len(s.Reports))
	case len(played) != len(s.Reports):
		return nil, fmt.Errorf("lowtalker played %d reports of a script of %d", len(played), len(s.Reports))
	}
	return played, nil
}

// readPlayed reads lowtalker's report lines in order, and whether it said it
// was done. A line out of order, or anything after done, is refused: no play
// could have written it.
func readPlayed(out []byte) (Played, bool, error) {
	var played Played
	lines := bufio.NewScanner(bytes.NewReader(out))
	for n := 1; lines.Scan(); n++ {
		var line struct {
			Report *struct {
				Index       *int   `json:"index"`
				ScheduledUS *int64 `json:"scheduled_us"`
				SentUS      *int64 `json:"sent_us"`
				AckedUS     *int64 `json:"acked_us"`
			} `json:"report"`
			Done *struct {
				Reports *int `json:"reports"`
			} `json:"done"`
		}
		if err := json.Unmarshal(lines.Bytes(), &line); err != nil {
			return played, false, fmt.Errorf("line %d: %w", n, err)
		}
		switch r, d := line.Report, line.Done; {
		case r != nil && d == nil && r.Index != nil && r.ScheduledUS != nil && r.SentUS != nil && r.AckedUS != nil:
			if *r.Index != len(played) {
				return played, false, fmt.Errorf("line %d: report %d where report %d was next", n, *r.Index, len(played))
			}
			played = append(played, Sent{
				Scheduled: time.UnixMicro(*r.ScheduledUS),
				Sent:      time.UnixMicro(*r.SentUS),
				Acked:     time.UnixMicro(*r.AckedUS),
			})
		case d != nil && r == nil && d.Reports != nil:
			if *d.Reports != len(played) {
				return played, false, fmt.Errorf("line %d: done after %d reports, having reported %d", n, *d.Reports, len(played))
			}
			if lines.Scan() {
				return played, false, fmt.Errorf("line %d: %s after done", n+1, lines.Bytes())
			}
			return played, true, nil
		default:
			return played, false, fmt.Errorf("line %d is neither a whole report nor done: %s", n, lines.Bytes())
		}
	}
	return played, false, lines.Err()
}
