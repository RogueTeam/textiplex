package reviews_test

import (
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/RogueTeam/textiplex"
)

// Kills Writer.Merge with SIGKILL at random points, over and over, then lets it
// finish. Whatever the kill points, the final index must hold every document
// exactly once, verify cleanly, and leave no records or temporary files.
func TestRegression_MergeSurvivesKill9(t *testing.T) {
	if isChild() {
		w := &textiplex.Writer{Directory: os.Getenv("POC_DIR"), TemporaryDirectory: os.Getenv("POC_TMP"), MaxWorkers: 2}
		if err := w.Merge(); err != nil {
			fmt.Println("MERGE ERROR:", err)
			os.Exit(3)
		}
		return
	}
	if testing.Short() {
		t.Skip("slow")
	}

	const segments, perSegment = 8, 15000
	trials := 40
	totalKills := 0

	build := func(trial int) (dir, tmp string) {
		dir, tmp = t.TempDir(), t.TempDir()
		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: tmp}
		for i := range segments {
			if err := w.Batch(mkBatch(manyDocs(fmt.Sprintf("t%d-s%d", trial, i), perSegment, "alpha", fmt.Sprintf("only%d", i))...)); err != nil {
				t.Fatal(err)
			}
		}
		return dir, tmp
	}
	runChild := func(dir, tmp string) *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^"+t.Name()+"$", "-test.count=1")
		cmd.Env = append(os.Environ(), "POC_CHILD=1", "POC_DIR="+dir, "POC_TMP="+tmp)
		return cmd
	}

	// Calibrate: how long does an uninterrupted merge (process start included) take?
	dir, tmp := build(-1)
	start := time.Now()
	if out, err := runChild(dir, tmp).CombinedOutput(); err != nil {
		t.Fatalf("calibration merge: %v\n%s", err, out)
	}
	window := time.Since(start)
	t.Logf("uninterrupted merge takes %v; kills land uniformly inside it", window)

	for trial := range trials {
		dir, tmp := build(trial)

		kills := 0
		for attempt := 0; ; attempt++ {
			if attempt > 200 {
				t.Fatalf("trial %d: merge never completed", trial)
			}
			cmd := runChild(dir, tmp)
			var out strings.Builder
			cmd.Stdout, cmd.Stderr = &out, &out
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()

			// Kill somewhere inside a full merge's duration; after a few kills,
			// sometimes let it run so the merge eventually completes.
			delay := time.Duration(rand.Int64N(int64(window) / 2))
			if attempt >= 6 && rand.IntN(3) == 0 {
				delay = 10 * window
			}
			var err error
			select {
			case err = <-done:
			case <-time.After(delay):
				cmd.Process.Kill()
				err = <-done
				kills++
				continue
			}
			if err != nil {
				t.Fatalf("trial %d: merge failed after %d kills: %v\n%s", trial, kills, err, out.String())
			}
			break
		}
		totalKills += kills

		n, err := countDocs(dir)
		if err != nil {
			t.Fatalf("trial %d after %d kills: %v", trial, kills, err)
		}
		if n != segments*perSegment {
			t.Fatalf("trial %d after %d kills: %d documents, want %d", trial, kills, n, segments*perSegment)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), textiplex.SegmentExtension) || strings.HasPrefix(e.Name(), ".") {
				t.Fatalf("trial %d: leftover file %s", trial, e.Name())
			}
		}
		r := newReader()
		if err := r.Reset(dir); err != nil {
			t.Fatal(err)
		}
		hits := query(t, r, "+tag:alpha")
		only := query(t, r, "+tag:only3")
		r.Close()
		if len(hits) != segments*perSegment || len(only) != perSegment {
			t.Fatalf("trial %d: hits=%d only3=%d", trial, len(hits), len(only))
		}
	}
	t.Logf("%d trials, %d kills in total, no loss or duplication", trials, totalKills)
}
