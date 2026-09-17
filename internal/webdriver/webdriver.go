// Package webdriver drives a real browser through its W3C WebDriver server.
//
// Input sent this way goes through the browser's own input path - hit
// testing, pointer capture, wheel handling - which input dispatched from
// script skips, so what it costs a page is what a person's input costs. It
// speaks only the parts of the protocol bough measures with: open a session,
// load a page, run a script, move a mouse and turn a wheel.
package webdriver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// Error is a failure the driver reported, in its own words.
type Error struct {
	Code    string // the W3C error code, such as "session not created"
	Message string // what the driver said about it
}

func (e *Error) Error() string {
	if e.Message == "" {
		return "webdriver: " + e.Code
	}
	return "webdriver: " + e.Code + ": " + e.Message
}

// endpoint is a WebDriver server, or one of its sessions, reached over HTTP.
type endpoint struct {
	base string
}

// post sends a command that carries parameters, as every POST does.
func (e endpoint) post(ctx context.Context, path string, params any) (json.RawMessage, error) {
	payload, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("webdriver: encoding POST %s: %w", path, err)
	}
	return e.call(ctx, http.MethodPost, path, bytes.NewReader(payload))
}

// call sends one command and hands back its value. Every reply is read the
// same way, so a driver's complaint always arrives as an *Error. Only a POST
// has a body; chromedriver refuses one on anything else.
//
// [LAW:effects-at-boundaries] this is the only place the package touches the
// network; everything above it passes values.
func (e endpoint) call(ctx context.Context, method, path string, body io.Reader) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, method, e.base+path, body)
	if err != nil {
		return nil, fmt.Errorf("webdriver: %s %s: %w", method, path, err)
	}
	req.Header.Set("Content-Type", "application/json; charset=utf-8")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webdriver: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("webdriver: reading the reply to %s %s: %w", method, path, err)
	}

	// [LAW:parse-dont-validate] the reply is read into what it means once,
	// here: a value, or the driver's error.
	var reply struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return nil, fmt.Errorf("webdriver: %s %s answered %s with something other than WebDriver: %q", method, path, resp.Status, raw)
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		if err := json.Unmarshal(reply.Value, &failure); err != nil || failure.Error == "" {
			return nil, fmt.Errorf("webdriver: %s %s answered %s without saying why: %q", method, path, resp.Status, raw)
		}
		return nil, &Error{Code: failure.Error, Message: failure.Message}
	}
	return reply.Value, nil
}

// Session is one browser window under a driver's control.
type Session struct {
	endpoint
}

// newSession asks the driver at e for a session in the named browser.
func newSession(ctx context.Context, e endpoint, browserName string) (*Session, error) {
	body := map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{"browserName": browserName},
		},
	}
	value, err := e.post(ctx, "/session", body)
	if err != nil {
		return nil, err
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(value, &created); err != nil || created.SessionID == "" {
		return nil, fmt.Errorf("webdriver: the driver made a session without naming it: %s", value)
	}
	return &Session{endpoint{e.base + "/session/" + url.PathEscape(created.SessionID)}}, nil
}

// Navigate loads url and returns once the page has loaded.
func (s *Session) Navigate(ctx context.Context, url string) error {
	_, err := s.post(ctx, "/url", map[string]string{"url": url})
	return err
}

// ExecuteAsync runs script in the page as the body of a function. Its
// arguments are args followed by a callback, and whatever the script passes
// that callback comes back as JSON.
func (s *Session) ExecuteAsync(ctx context.Context, script string, args ...any) (json.RawMessage, error) {
	return s.post(ctx, "/execute/async", map[string]any{
		"script": script,
		// Never null: the protocol wants a list, even an empty one.
		"args": append([]any{}, args...),
	})
}

// Close ends the session and closes its window.
func (s *Session) Close(ctx context.Context) error {
	_, err := s.call(ctx, http.MethodDelete, "", http.NoBody)
	return err
}
