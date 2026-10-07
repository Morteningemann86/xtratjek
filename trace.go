package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime/metrics"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Morteningemann86/xtratjek/paths"
	tea "github.com/charmbracelet/bubbletea"
)

// trace.go is the opt-in answer to "the app felt slow just then". Set
// TJEK_TRACE=1 to write trace.log in the state directory, or
// TJEK_TRACE=/some/path to choose the file; unset, every function here is a
// branch on a nil channel.
//
// It times the two things the app controls — Update and View — and stamps each
// frame with the wall clock and the GC cycle count. That is enough to tell the
// three candidates apart when a keystroke feels late:
//
//   - our compute: update or view is large;
//   - the garbage collector: the frame is slow and gc jumped;
//   - everything else (terminal, input reader, ssh, the renderer): the frame is
//     fast, and the wall-clock gap to the previous frame holds the delay.
//
// Writing happens on its own goroutine behind a buffered channel, and a full
// channel drops the entry rather than blocking the loop: tracing must never
// become the thing it is measuring.

type traceEntry struct {
	at           time.Time
	kind         string
	update, view time.Duration
	gc           uint64
}

var (
	traceCh chan traceEntry

	// lastUpdate carries the Update duration to the View call of the same
	// frame. The Bubble Tea loop is single-threaded, so no lock is needed.
	lastUpdate     time.Duration
	lastUpdateKind string
)

// tracePath resolves TJEK_TRACE into a file path, or "" when tracing is off.
func tracePath() string {
	v := strings.TrimSpace(os.Getenv("TJEK_TRACE"))
	switch v {
	case "", "0", "false", "off":
		return ""
	case "1", "true", "on":
		if _, err := paths.Ensure(paths.State); err != nil {
			return ""
		}
		return paths.For(paths.State, "trace.log")
	}
	return v
}

// startTrace opens the trace file and starts the writer. Returns a stop func
// that flushes and closes; it is a no-op when tracing is off.
func startTrace() (stop func()) {
	path := tracePath()
	if path == "" {
		return func() {}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tjek: cannot write trace to %s: %v\n", path, err)
		return func() {}
	}
	ch := make(chan traceEntry, 4096)
	done := make(chan struct{})
	traceCh = ch

	go func() {
		defer close(done)
		w := bufio.NewWriter(f)
		// Keep the durations so the session can end with numbers rather than a
		// log to scroll: a percentile summary is what tells you whether the
		// app was ever slow, and the per-frame lines are there to explain why.
		var updates, views, gaps []time.Duration
		defer func() {
			writeTraceSummary(w, updates, views, gaps)
			w.Flush()
			f.Close()
		}()
		fmt.Fprintf(w, "\n# tjek %s trace started %s\n", appVersion, time.Now().Format(time.RFC3339))
		fmt.Fprintf(w, "# time                     gap_ms  update_ms  view_ms  gc  msg\n")
		var prev time.Time
		flush := time.NewTicker(time.Second)
		defer flush.Stop()
		for {
			select {
			case e, ok := <-ch:
				if !ok {
					return
				}
				gap := 0.0
				if !prev.IsZero() {
					gap = float64(e.at.Sub(prev)) / float64(time.Millisecond)
				}
				prev = e.at
				updates = append(updates, e.update)
				views = append(views, e.view)
				if gap > 0 {
					gaps = append(gaps, time.Duration(gap*float64(time.Millisecond)))
				}
				fmt.Fprintf(w, "%s %8.1f %10.3f %8.3f %3d  %s\n",
					e.at.Format("15:04:05.000"), gap,
					float64(e.update)/float64(time.Millisecond),
					float64(e.view)/float64(time.Millisecond),
					e.gc, e.kind)
			case <-flush.C:
				w.Flush()
			}
		}
	}()
	// The Once is per session, not package-wide: a second startTrace (a test,
	// a restarted trace) gets its own stop, and a stop called twice is still
	// safe.
	var once sync.Once
	return func() {
		once.Do(func() {
			close(ch)
			<-done
			traceCh = nil
		})
	}
}

// traceFrame records one Update+View pair. Cheap and non-blocking when tracing
// is off (a nil channel) or when the writer has fallen behind.
func traceFrame(kind string, update, view time.Duration) {
	if traceCh == nil {
		return
	}
	select {
	case traceCh <- traceEntry{at: time.Now(), kind: kind, update: update, view: view, gc: gcCycles()}:
	default:
	}
}

// gcCycles reports completed GC cycles. A slow frame whose gc count moved was
// (at least partly) a collection, not our code.
func gcCycles() uint64 {
	sample := []metrics.Sample{{Name: "/gc/cycles/total:gc-cycles"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}

// msgKind names a message for the trace in the terms the reader cares about:
// which key, or which kind of internal event.
func msgKind(msg tea.Msg) string {
	switch m := msg.(type) {
	case tea.KeyMsg:
		return "key " + m.String()
	case tea.WindowSizeMsg:
		return "resize"
	case tea.MouseMsg:
		return "mouse"
	default:
		return fmt.Sprintf("%T", msg)
	}
}

// writeTraceSummary closes a session with the distribution, so a report can be
// five lines instead of a log. p95 rather than the mean: a stutter is a tail.
func writeTraceSummary(w *bufio.Writer, updates, views, gaps []time.Duration) {
	if len(updates) == 0 {
		fmt.Fprintf(w, "# no frames recorded\n")
		return
	}
	fmt.Fprintf(w, "# summary over %d frames (ms)\n", len(updates))
	for _, row := range []struct {
		name string
		d    []time.Duration
	}{{"update", updates}, {"view", views}, {"gap", gaps}} {
		p50, p95, max := percentiles(row.d)
		fmt.Fprintf(w, "#   %-7s p50 %7.3f   p95 %7.3f   max %7.3f\n",
			row.name, ms(p50), ms(p95), ms(max))
	}
	fmt.Fprintf(w, "# if update and view are small and the delay is real, it is not the app's compute\n")
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func percentiles(d []time.Duration) (p50, p95, max time.Duration) {
	if len(d) == 0 {
		return 0, 0, 0
	}
	sorted := append([]time.Duration(nil), d...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(q float64) time.Duration {
		i := int(q * float64(len(sorted)-1))
		return sorted[i]
	}
	return at(0.50), at(0.95), sorted[len(sorted)-1]
}
