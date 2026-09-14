package codex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The desktop app writes a shape the original parser was not built for, and
// each test here pins one thing it got wrong on a real session. The figures
// come from a rollout pair on disk rather than being invented.

// A record can hold several injected blocks and nothing else. Every session
// opens with one, so counting it added a prompt to every session in the graph.
func TestInjectionOnlyRecordIsNotAPrompt(t *testing.T) {
	// The environment block arrives first here on purpose. A filter that only
	// looks at how the joined text begins happens to reject that one, so it
	// looks correct. The desktop app sends the plugin catalogue first, and then
	// the same filter sees a tag it does not know and calls the record a
	// prompt, which is what put an extra prompt in every session.
	both := []string{
		"<recommended_plugins>\n- Airtable\n</recommended_plugins>",
		"<environment_context>\n  <cwd>C:\\work</cwd>\n</environment_context>",
	}

	if rec := userMessage("m1", both[1], both[0]); rec.IsHumanPrompt() {
		t.Error("harness injections were counted as a prompt")
	}
	if rec := userMessage("m1", both[0], both[1]); rec.IsHumanPrompt() {
		t.Error("harness injections were counted as a prompt, in the order the app sends them")
	}
}

// The blocks are stripped rather than the record rejected outright, so a prompt
// that happens to arrive alongside one still counts.
func TestPromptSurvivesAnInjectionInTheSameRecord(t *testing.T) {
	rec := userMessage("m2",
		"<recommended_plugins>\n- Airtable\n</recommended_plugins>",
		"make me a pixel themed site")

	if !rec.IsHumanPrompt() {
		t.Fatal("a real prompt was dropped because an injection shared its record")
	}
	if got := rec.PromptText(); got != "make me a pixel themed site" {
		t.Errorf("prompt = %q, want the typed text alone", got)
	}
}

// Both usage streams appear in one session and report the same figures, so
// adding both doubles every total in the graph.
func TestUsageIsNotCountedTwice(t *testing.T) {
	recs := []*Record{
		userMessage("m1", "do the thing"),
		raw("token_usage_record", `{"response_id":"r1","usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":7}}`),
		raw("event_msg", `{"type":"token_count","info":{"last_token_usage":{"input_tokens":100,"cached_input_tokens":40,"output_tokens":7}}}`),
	}

	turns := ExtractTurns(recs)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	// 60 fresh out of the 100 the record reports, the other 40 being cached.
	if got := turns[0].Tokens.Input; got != 60 {
		t.Errorf("input = %d, want 60: the two streams report the same response", got)
	}
	if got := turns[0].Tokens.Output; got != 7 {
		t.Errorf("output = %d, want 7", got)
	}
}

// Rollouts predating the usage record still have to be read.
func TestLegacyUsageStillCounts(t *testing.T) {
	recs := []*Record{
		userMessage("m1", "do the thing"),
		raw("event_msg", `{"type":"token_count","info":{"last_token_usage":{"input_tokens":55,"cached_input_tokens":5,"output_tokens":3}}}`),
	}

	turns := ExtractTurns(recs)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	// 50 rather than the 55 the record says, because 5 of those are the cached
	// part and Input means the fresh remainder.
	if got := turns[0].Tokens.Input; got != 50 {
		t.Errorf("input = %d, want 50: a session with no usage records must fall back", got)
	}
	if got := turns[0].Tokens.CacheRead; got != 5 {
		t.Errorf("cacheRead = %d, want 5", got)
	}
}

// A sub-agent's rollout carries no user message at all. Its work arrives as a
// delegated task, and without that its session came back empty and was dropped.
func TestDelegatedTaskOpensATurn(t *testing.T) {
	recs := []*Record{
		raw("response_item", `{"type":"agent_message","id":"a1","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nTask name: /root/pixel_art\nSender: /root\nPayload:\n"}]}`),
		raw("token_usage_record", `{"response_id":"r1","usage":{"input_tokens":12,"cached_input_tokens":0,"output_tokens":4}}`),
	}

	turns := ExtractTurns(recs)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1: a sub-agent's work went missing", len(turns))
	}
	if got := turns[0].Text; got != "/root/pixel_art" {
		t.Errorf("label = %q, want the task name", got)
	}
}

// Agents message each other both ways. A reply is not new work, and counting
// one opened a turn on the parent that nobody asked for.
func TestReplyFromSubAgentIsNotATurn(t *testing.T) {
	recs := []*Record{
		userMessage("m1", "build it"),
		raw("response_item", `{"type":"agent_message","id":"a2","content":[{"type":"input_text","text":"Message Type: TASK_COMPLETE\nTask name: /root/pixel_art\nSender: /root/pixel_art\n"}]}`),
	}

	if got := len(ExtractTurns(recs)); got != 1 {
		t.Errorf("got %d turns, want 1: a sub-agent reporting back is not a request", got)
	}
}

// The brief one agent hands another is stored encrypted. It is unreadable, and
// printing it put a wall of base64 where the reader expects a prompt.
func TestEncryptedBriefIsNotShown(t *testing.T) {
	cipher := "gAAAAABqoVGCbKXgAMRcpFtdMQ_z-aiA-mueExipbREEEXsLqrlxT7LvTrKNNvcx6bWRm1eCocaxCUE7CrzP9Sjivyi7D6glGbJF"

	if !Encrypted(cipher) {
		t.Fatal("a Fernet token was not recognised as ciphertext")
	}
	if Encrypted("make me a website") {
		t.Error("ordinary prose was mistaken for ciphertext")
	}

	spawn := `{"type":"function_call","id":"f1","name":"spawn_agent","arguments":"{\"task_name\":\"pixel_art\",\"message\":\"` + cipher + `\"}"}`
	recs := []*Record{
		userMessage("m1", "build it"),
		raw("response_item", spawn),
	}

	turns := ExtractTurns(recs)
	if len(turns) != 1 || len(turns[0].Delegated) != 1 {
		t.Fatal("the delegation was not recorded")
	}
	d := turns[0].Delegated[0]
	if d.Description != "" {
		t.Errorf("description = %q, want empty: ciphertext must not be shown", d.Description)
	}
	if d.Kind != "pixel_art" {
		t.Errorf("kind = %q, want the task name", d.Kind)
	}
}

// A sub-agent's rollout carries the parent's session_id. Grouping on that alone
// folded the two together and hid the sub-agent's prompts and tokens inside the
// agent that spawned it.
func TestSubAgentIsItsOwnSession(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, "2026", "09", "09")
	if err := os.MkdirAll(day, 0o750); err != nil {
		t.Fatal(err)
	}

	parent := strings.Join([]string{
		`{"timestamp":"2026-09-09T12:30:09Z","type":"session_meta","payload":{"session_id":"S","id":"S","cwd":"C:\\work\\site"}}`,
		`{"timestamp":"2026-09-09T12:30:11Z","type":"response_item","payload":{"type":"message","role":"user","id":"m1","content":[{"type":"input_text","text":"build me a site"}]}}`,
	}, "\n")

	child := strings.Join([]string{
		`{"timestamp":"2026-09-09T12:30:59Z","type":"session_meta","payload":{"session_id":"S","id":"C","parent_thread_id":"S","cwd":"C:\\work\\site"}}`,
		`{"timestamp":"2026-09-09T12:31:03Z","type":"response_item","payload":{"type":"agent_message","id":"a1","content":[{"type":"input_text","text":"Message Type: NEW_TASK\nTask name: /root/pixel_art\nSender: /root\n"}]}}`,
	}, "\n")

	writeRollout(t, filepath.Join(day, "rollout-parent.jsonl"), parent)
	writeRollout(t, filepath.Join(day, "rollout-child.jsonl"), child)

	s := Source{Root: dir}
	projects, err := s.Detect()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("got %d projects, want 1", len(projects))
	}

	sessions, err := s.Sessions(projects[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("got %d sessions, want 2: a spawned agent is its own work", len(sessions))
	}

	var total int
	for _, sess := range sessions {
		total += len(sess.Turns)
	}
	if total != 2 {
		t.Errorf("got %d turns across the sessions, want 2", total)
	}
}

func writeRollout(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func raw(kind, payload string) *Record {
	return &Record{
		Timestamp: "2026-09-09T12:30:11Z",
		Type:      kind,
		Payload:   []byte(payload),
	}
}

func userMessage(id string, chunks ...string) *Record {
	var b strings.Builder
	b.WriteString(`{"type":"message","role":"user","id":"` + id + `","content":[`)
	for i, c := range chunks {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"type":"input_text","text":`)
		b.WriteString(quoteJSON(c))
		b.WriteString("}")
	}
	b.WriteString("]}")
	return raw("response_item", b.String())
}

func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Codex reports the cached tokens as part of the input, not beside it.
//
// agent.Tokens means something different: its four fields are disjoint and
// Total adds them up, so Input has to be the fresh remainder. Reading Codex's
// two numbers straight into those two fields counted the cache twice. On one
// real project that turned 1.59M tokens into 3.04M, a 91% overstatement, and
// it fed everything downstream that reads a token count.
//
// The fixture that should have caught this could not: it held 200 input
// against 1500 cached, which cannot happen, and the arithmetic looks the same
// either way when the numbers are impossible.
func TestCachedInputIsNotCountedTwice(t *testing.T) {
	// Taken from a real rollout: the total confirms input already holds cache.
	recs := []*Record{
		userMessage("m1", "go"),
		raw("token_usage_record", `{"response_id":"r1","usage":{"input_tokens":28739,"cached_input_tokens":28032,"output_tokens":11}}`),
	}

	turns := ExtractTurns(recs)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	tok := turns[0].Tokens

	if tok.Input != 707 {
		t.Errorf("input = %d, want 707: the fresh part is what is left after the cache", tok.Input)
	}
	if tok.CacheRead != 28032 {
		t.Errorf("cacheRead = %d, want 28032", tok.CacheRead)
	}
	// The record's own total_tokens is 28750, which is input plus output. The
	// sum of the parts has to agree with it.
	if got := tok.Total(); got != 28750 {
		t.Errorf("total = %d, want 28750 to match the record's own total_tokens", got)
	}
}

// A record that disagrees with itself must not produce a negative count.
func TestMoreCacheThanInputDoesNotGoNegative(t *testing.T) {
	recs := []*Record{
		userMessage("m1", "go"),
		raw("token_usage_record", `{"response_id":"r1","usage":{"input_tokens":10,"cached_input_tokens":99,"output_tokens":1}}`),
	}
	turns := ExtractTurns(recs)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if got := turns[0].Tokens.Input; got != 0 {
		t.Errorf("input = %d, want 0 rather than a negative count", got)
	}
}

// A commit is settled by its own call's result, not by whatever finishes next.
//
// Codex issues tool calls in parallel and the results come back interleaved.
// With a single outstanding slot, the first output to arrive decided the
// commit: here an unrelated command fails first, and a real commit was thrown
// away on that command's exit code. Claude has always paired by id; this is
// the same rule.
func TestCommitIsSettledByItsOwnCall(t *testing.T) {
	recs := []*Record{
		userMessage("m1", "commit it"),
		raw("response_item", `{"type":"function_call","id":"f1","call_id":"c1","name":"exec_command","arguments":"{\"cmd\":\"git commit -m x\",\"workdir\":\"/w\"}"}`),
		raw("response_item", `{"type":"function_call","id":"f2","call_id":"c2","name":"exec_command","arguments":"{\"cmd\":\"go test ./...\",\"workdir\":\"/w\"}"}`),
		// The unrelated command fails, and its failure arrives first.
		raw("response_item", `{"type":"function_call_output","call_id":"c2","output":"Process exited with code 1\nFAIL"}`),
		raw("response_item", `{"type":"function_call_output","call_id":"c1","output":"Process exited with code 0\n[main abc1234] x"}`),
	}

	turns := ExtractTurns(recs)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	got := turns[0].Committed
	if len(got) != 1 {
		t.Fatalf("got %d commits, want 1: the failing command settled the commit", len(got))
	}
	if got[0].SHA != "abc1234" {
		t.Errorf("sha = %q, want abc1234 from the commit's own output", got[0].SHA)
	}
}

// A failed commit whose own result says so is still not a commit.
func TestFailedCommitIsNotCounted(t *testing.T) {
	recs := []*Record{
		userMessage("m1", "commit it"),
		raw("response_item", `{"type":"function_call","id":"f1","call_id":"c1","name":"exec_command","arguments":"{\"cmd\":\"git commit -m x\",\"workdir\":\"/w\"}"}`),
		raw("response_item", `{"type":"function_call_output","call_id":"c1","output":"Process exited with code 1\nnothing to commit"}`),
	}
	turns := ExtractTurns(recs)
	if len(turns) != 1 {
		t.Fatalf("got %d turns, want 1", len(turns))
	}
	if n := len(turns[0].Committed); n != 0 {
		t.Errorf("got %d commits, want 0: the commit was refused", n)
	}
}
