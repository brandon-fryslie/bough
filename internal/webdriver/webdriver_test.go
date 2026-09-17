package webdriver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDriver answers one command per method and path with a status and a body,
// and keeps what each command was sent.
type fakeDriver struct {
	replies map[string]reply
	mu      sync.Mutex
	sent    map[string]string
}

// body is what command was last sent.
func (f *fakeDriver) body(command string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sent[command]
}

type reply struct {
	status int
	body   string
}

func serve(t *testing.T, replies map[string]reply) (*fakeDriver, endpoint) {
	t.Helper()
	f := &fakeDriver{replies: replies, sent: map[string]string{}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		body, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.sent[key] = string(body)
		f.mu.Unlock()
		answer, ok := f.replies[key]
		if !ok {
			t.Errorf("unexpected command %s", key)
			answer = reply{http.StatusNotFound, `{"value":{"error":"unknown command","message":""}}`}
		}
		w.WriteHeader(answer.status)
		_, _ = io.WriteString(w, answer.body)
	}))
	t.Cleanup(s.Close)
	return f, endpoint{s.URL}
}

// sameJSON is whether two documents say the same thing, whatever their spacing
// and key order.
func sameJSON(t *testing.T, got, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("sent %q, which is not JSON: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(g, w)
}

// A session is opened, used and closed with the commands the protocol names,
// and whatever the page hands back arrives as it was sent.
func TestASessionSpeaksTheProtocol(t *testing.T) {
	f, e := serve(t, map[string]reply{
		"POST /session":                   {200, `{"value":{"sessionId":"s 1","capabilities":{}}}`},
		"POST /session/s 1/url":           {200, `{"value":null}`},
		"POST /session/s 1/execute/async": {200, `{"value":{"frames":[1.5]}}`},
		"DELETE /session/s 1":             {200, `{"value":null}`},
	})
	ctx := context.Background()

	s, err := newSession(ctx, e, "safari")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Navigate(ctx, "http://127.0.0.1:1/"); err != nil {
		t.Fatal(err)
	}
	got, err := s.ExecuteAsync(ctx, "arguments[0](1)")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(ctx); err != nil {
		t.Fatal(err)
	}

	if !sameJSON(t, string(got), `{"frames":[1.5]}`) {
		t.Errorf("script handed back %s", got)
	}
	for command, want := range map[string]string{
		"POST /session":                   `{"capabilities":{"alwaysMatch":{"browserName":"safari"}}}`,
		"POST /session/s 1/url":           `{"url":"http://127.0.0.1:1/"}`,
		"POST /session/s 1/execute/async": `{"script":"arguments[0](1)","args":[]}`,
	} {
		if !sameJSON(t, f.body(command), want) {
			t.Errorf("%s sent %s, want %s", command, f.body(command), want)
		}
	}
	if body := f.body("DELETE /session/s 1"); body != "" {
		t.Errorf("closing sent a body, %q, which chromedriver refuses", body)
	}
}

// Input reaches the driver as the protocol's sources and steps, with durations
// in milliseconds and a source left out when it has nothing to do.
func TestActionsReachTheDriverInTheProtocolsShape(t *testing.T) {
	f, e := serve(t, map[string]reply{
		"POST /s/actions": {200, `{"value":null}`},
	})
	s := &Session{endpoint{e.base + "/s"}}
	ctx := context.Background()

	drag := Actions{Mouse: []MouseStep{Move{X: 10, Y: 20}, Press{}, Move{X: 110, Y: 20, Over: 250 * time.Millisecond}, Pause(16 * time.Millisecond), Release{}}}
	if err := s.Perform(ctx, drag); err != nil {
		t.Fatal(err)
	}
	if want := `{"actions":[{"type":"pointer","id":"mouse","parameters":{"pointerType":"mouse"},"actions":[
		{"type":"pointerMove","origin":"viewport","x":10,"y":20,"duration":0},
		{"type":"pointerDown","button":0},
		{"type":"pointerMove","origin":"viewport","x":110,"y":20,"duration":250},
		{"type":"pause","duration":16},
		{"type":"pointerUp","button":0}]}]}`; !sameJSON(t, f.body("POST /s/actions"), want) {
		t.Errorf("a drag sent %s", f.body("POST /s/actions"))
	}

	zoom := Actions{Wheel: []WheelStep{Scroll{X: 300, Y: 200, DeltaY: -120, Over: 50 * time.Millisecond}, Pause(time.Second)}}
	if err := s.Perform(ctx, zoom); err != nil {
		t.Fatal(err)
	}
	if want := `{"actions":[{"type":"wheel","id":"wheel","actions":[
		{"type":"scroll","origin":"viewport","x":300,"y":200,"deltaX":0,"deltaY":-120,"duration":50},
		{"type":"pause","duration":1000}]}]}`; !sameJSON(t, f.body("POST /s/actions"), want) {
		t.Errorf("a zoom sent %s", f.body("POST /s/actions"))
	}
}

// Whatever the driver says went wrong reaches the caller in its words, and a
// reply that is not WebDriver at all says what came back instead.
func TestADriversComplaintArrivesInItsOwnWords(t *testing.T) {
	_, e := serve(t, map[string]reply{
		"POST /s/url":     {404, `{"value":{"error":"invalid session id","message":"no such session","stacktrace":""}}`},
		"POST /s/actions": {502, `<html>bad gateway</html>`},
		"DELETE /s":       {500, `{"value":{}}`},
	})
	s := &Session{endpoint{e.base + "/s"}}
	ctx := context.Background()

	err := s.Navigate(ctx, "http://127.0.0.1:1/")
	var complaint *Error
	if !errors.As(err, &complaint) || complaint.Code != "invalid session id" || complaint.Message != "no such session" {
		t.Errorf("navigating failed with %v, want the driver's invalid session id", err)
	}

	if err := s.Perform(ctx, Actions{Mouse: []MouseStep{Press{}}}); err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "bad gateway") {
		t.Errorf("a reply that is not WebDriver failed with %v, want its status and body", err)
	}

	if err := s.Close(ctx); err == nil || !strings.Contains(err.Error(), "without saying why") {
		t.Errorf("a failure with no error code failed with %v", err)
	}
}

// A browser that will not open a session says what to set up, beside the
// driver's own reason.
func TestARefusedSessionSaysWhatToSetUp(t *testing.T) {
	_, e := serve(t, map[string]reply{
		"POST /session": {500, `{"value":{"error":"session not created","message":"Could not create a session"}}`},
	})
	d := &Driver{browser: Safari(), endpoint: e}

	_, err := d.NewSession(context.Background())
	var complaint *Error
	if !errors.As(err, &complaint) || !strings.Contains(err.Error(), "Could not create a session") {
		t.Errorf("failed with %v, want the driver's reason", err)
	}
	if err == nil || !strings.Contains(err.Error(), "Allow remote automation") {
		t.Errorf("failed with %v, want what to turn on", err)
	}
}

// A driver that is not there says how to get one, and one that dies before it
// answers says so instead of waiting out the clock.
func TestADriverThatCannotRunSaysWhy(t *testing.T) {
	ctx := context.Background()

	_, err := Start(ctx, Chrome().WithDriver("/nonexistent/chromedriver"))
	if err == nil || !strings.Contains(err.Error(), "npx @puppeteer/browsers install chromedriver") {
		t.Errorf("a missing driver failed with %v, want how to install one", err)
	}

	begun := time.Now()
	_, err = Start(ctx, Chrome().WithDriver("false"))
	if err == nil || !strings.Contains(err.Error(), "exited before it was ready") {
		t.Errorf("a driver that exits failed with %v", err)
	}
	if waited := time.Since(begun); waited > ready/2 {
		t.Errorf("waited %v for a driver that had already exited", waited)
	}
}
