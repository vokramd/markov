package codify

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Lines pass through whole, however they arrive; one too long to keep becomes a summary of its
// header, and the lines after it are still read.
func TestLineSplitterSummarizesOversizedLines(t *testing.T) {
	var got []string
	s := newLineSplitter(1000, func(b []byte) { got = append(got, string(b)) })
	big := `{"timestamp":"t1","run_id":"r1","event_type":"step_completed","workflow_name":"github-agent","step_name":"command-inputs","output":{"x":"` + strings.Repeat("y", 5000) + `"}}`
	input := `{"event_type":"a"}` + "\n" + big + "\n" + `{"event_type":"b"}` + "\n" + `{"event_type":"c"}`
	for i := 0; i < len(input); i += 7 { // in small pieces, so lines span writes
		s.Write([]byte(input[i:min(i+7, len(input))]))
	}
	s.Close()
	if len(got) != 4 || got[0] != `{"event_type":"a"}` || got[2] != `{"event_type":"b"}` || got[3] != `{"event_type":"c"}` {
		t.Fatalf("lines = %q", got)
	}
	var sum map[string]any
	if err := json.Unmarshal([]byte(got[1]), &sum); err != nil {
		t.Fatal(err)
	}
	if sum["step_name"] != "command-inputs" || sum["event_type"] != "step_completed" || sum["oversized_event_bytes"] != float64(len(big)) || sum["output"] != nil {
		t.Fatalf("summary = %v", sum)
	}
}

func TestLoadEventsReadsPastAHugeLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	var b bytes.Buffer
	b.WriteString(`{"event_type":"run_started"}` + "\n")
	b.WriteString(`{"event_type":"step_completed","step_name":"huge","output":{"x":"` + strings.Repeat("y", maxEventLine+10) + `"}}` + "\n")
	b.WriteString(`{"event_type":"run_completed"}` + "\n")
	if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	evs := LoadEvents(path)
	if len(evs) != 3 || evs[1]["step_name"] != "huge" || evs[1]["oversized_event_bytes"] == nil || evs[2]["event_type"] != "run_completed" {
		t.Fatalf("events = %d %v", len(evs), evs[len(evs)-1])
	}
	dst := filepath.Join(t.TempDir(), "copy.jsonl")
	if err := copyEventLog(path, dst); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(dst); info.Size() > 4096 {
		t.Fatalf("evidence copy is %d bytes, want the huge line summarized", info.Size())
	}
}

// Under a memory limit, a run that allocates past it is killed alone and reported as out of
// memory. Needs systemd's user manager.
func TestContainedRunOutOfMemory(t *testing.T) {
	if !scopeAvailable() {
		t.Skip("no systemd user scope here")
	}
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("no python3")
	}
	const mem = 64 << 20
	name, args, unit := contained(mem, py, []string{"-c", "x = bytearray(512 << 20); print('survived')"})
	if name != "systemd-run" || unit == "" {
		t.Fatalf("contained = %s %v", name, args)
	}
	out, err := exec.Command(name, args...).CombinedOutput()
	if err == nil {
		t.Fatalf("the run survived: %s", out)
	}
	err = outOfMemory(err, unit, mem)
	if !strings.Contains(err.Error(), "ran out of memory") || !strings.Contains(err.Error(), "64 MB") {
		t.Fatalf("err = %v", err)
	}
	if name, _, _ := contained(0, py, nil); name != py {
		t.Error("no limit, no scope")
	}
}
