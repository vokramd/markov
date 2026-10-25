package codify

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/jctanner/markov/pkg/outputlimit"
)

// A run codify starts is contained, so a runaway candidate is one rejected iteration rather than
// a machine out of memory. Where systemd's user manager is available the run goes in its own
// scope with a memory limit, and the kernel kills inside it; otherwise the run raises its own
// oom_score_adj, so a machine-wide out-of-memory kill takes it first.

// OOMScoreAdjEnv asks a markov process to raise its own oom_score_adj to the value given.
const OOMScoreAdjEnv = "MARKOV_OOM_SCORE_ADJ"

var scopeCheck struct {
	once sync.Once
	ok   bool
}

// scopeAvailable reports whether a run can be put in a systemd user scope with a memory limit:
// checked once, by running true in one.
func scopeAvailable() bool {
	scopeCheck.once.Do(func() {
		scopeCheck.ok = exec.Command("systemd-run", "--user", "--scope", "--quiet", "--collect", "-p", "MemoryMax=64M", "true").Run() == nil
	})
	return scopeCheck.ok
}

var scopeSeq atomic.Int64

// contained is the command for a run limited to mem bytes (0: not contained): name and args to
// exec, and the scope's unit name ("" without one).
func contained(mem int64, binary string, args []string) (name string, cmdArgs []string, unit string) {
	if mem <= 0 || !scopeAvailable() {
		return binary, args, ""
	}
	unit = fmt.Sprintf("markov-codify-%d-%d", os.Getpid(), scopeSeq.Add(1))
	// --collect: a scope the kernel killed is not kept as a failed unit; the journal keeps why.
	cmdArgs = append([]string{"--user", "--scope", "--quiet", "--collect", "--unit", unit,
		"-p", "MemoryMax=" + strconv.FormatInt(mem, 10), "-p", "MemorySwapMax=0", "--", binary}, args...)
	return "systemd-run", cmdArgs, unit
}

// outOfMemory turns a run's SIGKILL into what most likely happened: the kernel ran it out of
// memory, inside its scope when it had one (confirmed from the scope's journal).
func outOfMemory(err error, unit string, mem int64) error {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return err
	}
	ws, ok := exit.Sys().(syscall.WaitStatus)
	if !ok || !ws.Signaled() || ws.Signal() != syscall.SIGKILL {
		return err
	}
	if unit != "" && scopeOOMKilled(unit) {
		return fmt.Errorf("ran out of memory: the kernel killed it at its limit of %s (codify --run-memory)", outputlimit.Format(mem))
	}
	return fmt.Errorf("was killed (SIGKILL), most likely out of memory: %w", err)
}

// scopeOOMKilled reports whether systemd recorded the scope as ended by the OOM killer.
func scopeOOMKilled(unit string) bool {
	out, err := exec.Command("journalctl", "--user", "-u", unit+".scope", "-o", "cat", "--no-pager", "-q").Output()
	return err == nil && strings.Contains(string(out), "oom-kill")
}

// DefaultRunMemory is the memory limit for codify's runs when none is given: a quarter of the
// machine's memory, or 0 (no limit) where that is unknown.
func DefaultRunMemory() int64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "MemTotal:" {
			kb, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024 / 4
		}
	}
	return 0
}

// RaiseOOMScore applies OOMScoreAdjEnv to this process, if it is set. Raising the score needs no
// privilege; a failure is ignored (the run goes ahead, uncontained).
func RaiseOOMScore() {
	if v := os.Getenv(OOMScoreAdjEnv); v != "" {
		_ = os.WriteFile("/proc/self/oom_score_adj", []byte(v), 0)
	}
}
