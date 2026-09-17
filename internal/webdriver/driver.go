package webdriver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os/exec"
	"strconv"
	"sync"
	"time"
)

// Browser is a browser bough can drive, and the driver program that drives it.
type Browser struct {
	name   string                  // the W3C browserName
	driver string                  // the driver program, by name or path
	listen func(port int) []string // the driver's arguments to serve on port
	setup  string                  // what to set up when the driver or browser is not ready
	drops  string                  // what was seen of input sent through the driver not reaching the page, if it does not
}

// Chrome is Google Chrome, driven by a chromedriver matching its version.
func Chrome() Browser {
	return Browser{
		name:   "chrome",
		driver: "chromedriver",
		listen: func(port int) []string { return []string{"--port=" + strconv.Itoa(port)} },
		setup: "install the chromedriver matching your Chrome's version, for example " +
			"npx @puppeteer/browsers install chromedriver@<version from chrome://version>, " +
			"and put it on PATH or name it with WithDriver",
	}
}

// Safari is Safari, driven by the safaridriver that ships with it on macOS.
func Safari() Browser {
	return Browser{
		name:   "safari",
		driver: "safaridriver",
		listen: func(port int) []string { return []string{"-p", strconv.Itoa(port)} },
		setup: "safaridriver ships with Safari on macOS; " +
			"turn on Safari > Settings > Developer > Allow remote automation",
		// Seen in Safari 26.3 on macOS 26.3: most wheel steps in an action
		// sequence reach the page as no event, and sequences after the first in
		// a session reach it as nothing, though the driver reports success.
		drops: "safaridriver drops most of the input it is sent",
	}
}

// Browsers is every browser bough can drive.
func Browsers() []Browser {
	return []Browser{Chrome(), Safari()}
}

// Named is the browser called name: chrome or safari.
func Named(name string) (Browser, error) {
	for _, b := range Browsers() {
		if b.name == name {
			return b, nil
		}
	}
	return Browser{}, fmt.Errorf("webdriver: no browser called %q; there are chrome and safari", name)
}

// Name is what b is called, as Named knows it.
func (b Browser) Name() string {
	return b.name
}

// Installed is nil when b's driver program is there to run, or why it is not
// and what to set up.
func (b Browser) Installed() error {
	_, err := b.locate()
	return err
}

// locate is the path of b's driver program.
//
// [LAW:single-enforcer] the one place a driver is looked for, so Installed
// and Open cannot disagree about whether it is there.
func (b Browser) locate() (string, error) {
	path, err := exec.LookPath(b.driver)
	if err != nil {
		return "", fmt.Errorf("webdriver: cannot run a driver for %s (%s): %w", b.name, b.setup, err)
	}
	return path, nil
}

// Input is whether input performed through b's driver reaches the page: nil
// when it does, or what was seen of it not arriving. The driver reports
// success either way, so this is the only place that says.
func (b Browser) Input() error {
	if b.drops == "" {
		return nil
	}
	return errors.New(b.drops)
}

// WithDriver is b driven by the driver program at path.
func (b Browser) WithDriver(path string) Browser {
	b.driver = path
	return b
}

// Window is a browser window under bough's control, in a driver program
// started for it alone.
type Window struct {
	*Session

	driver *driver
}

// Open starts b's driver and opens a window in it. Close the window when done:
// closing it ends the driver too, and a window never closed stays open.
//
// [LAW:no-ambient-temporal-coupling] the window owns its driver, so the
// session is always closed before the driver it lives in is stopped.
func Open(ctx context.Context, b Browser) (*Window, error) {
	d, err := start(ctx, b)
	if err != nil {
		return nil, err
	}
	s, err := d.session(ctx)
	if err != nil {
		d.stop()
		return nil, err
	}
	return &Window{Session: s, driver: d}, nil
}

// Close ends the session, closing the window, and then stops the driver,
// whether or not the session would end.
func (w *Window) Close(ctx context.Context) error {
	err := w.Session.Close(ctx)
	w.driver.stop()
	return err
}

// driver is a running driver program, ready for sessions.
type driver struct {
	endpoint

	browser Browser
	process *exec.Cmd
	exited  chan struct{} // closed once the process has exited
	output  *lockedBuffer // everything the process printed
}

// ready is how long a driver has to start answering before start gives up.
const ready = 10 * time.Second

// start runs b's driver on a free local port and returns once it answers.
// stop it when done, or the program outlives the run.
func start(ctx context.Context, b Browser) (*driver, error) {
	path, err := b.locate()
	if err != nil {
		return nil, err
	}
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("webdriver: finding a port for %s: %w", b.driver, err)
	}

	d := &driver{
		browser:  b,
		endpoint: endpoint{fmt.Sprintf("http://127.0.0.1:%d", port)},
		// The driver lives until stop, not until ctx ends, so it is not tied
		// to ctx.
		process: exec.Command(path, b.listen(port)...), //#nosec G204 G702 -- the path is the driver the caller chose
		exited:  make(chan struct{}),
		output:  &lockedBuffer{},
	}
	d.process.Stdout = d.output
	d.process.Stderr = d.output
	// The browser the driver starts inherits its output, and can outlive it.
	// Once the driver itself has exited, stop waiting on what it left behind.
	d.process.WaitDelay = time.Second
	if err := d.process.Start(); err != nil {
		return nil, fmt.Errorf("webdriver: starting %s: %w", path, err)
	}
	go func() {
		_ = d.process.Wait()
		close(d.exited)
	}()

	if err := d.await(ctx); err != nil {
		d.stop()
		return nil, err
	}
	return d, nil
}

// await polls the driver's status until it says it is ready, it exits, or
// time runs out.
func (d *driver) await(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, ready)
	defer cancel()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()

	for {
		value, err := d.call(ctx, http.MethodGet, "/status", http.NoBody)
		if err == nil {
			var status struct {
				Ready bool `json:"ready"`
			}
			if json.Unmarshal(value, &status) == nil && status.Ready {
				return nil
			}
		}
		select {
		case <-d.exited:
			return fmt.Errorf("webdriver: %s exited before it was ready: %s", d.browser.driver, d.output)
		case <-ctx.Done():
			return fmt.Errorf("webdriver: %s was not ready: %w %s", d.browser.driver, errors.Join(ctx.Err(), err), d.output)
		case <-tick.C:
		}
	}
}

// session opens a window in the driver's browser.
func (d *driver) session(ctx context.Context) (*Session, error) {
	s, err := newSession(ctx, d.endpoint, d.browser.name)
	var refused *Error
	if errors.As(err, &refused) && refused.Code == "session not created" {
		return nil, fmt.Errorf("%w (%s)", err, d.browser.setup)
	}
	return s, err
}

// stop ends the driver program and waits for it to go. Close sessions first:
// no driver closes its browser when it is killed, so a session never closed
// leaves its window open.
func (d *driver) stop() {
	_ = d.process.Process.Kill()
	<-d.exited
}

// freePort is a local port nothing was listening on a moment ago.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	addr, err := netip.ParseAddrPort(l.Addr().String())
	return int(addr.Port()), errors.Join(err, l.Close())
}

// lockedBuffer collects a process's output while it is still writing.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
