//go:build windows

package quill

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"time"
)

// waitForStop blocks until Enter, Ctrl+C, or the duration (if positive)
// elapses, printing an elapsed counter each second.
func waitForStop(duration time.Duration) {
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	defer signal.Stop(interrupt)

	enter := make(chan struct{})
	go func() {
		// EOF (no interactive stdin) must not count as Enter, or a
		// recording launched from a script stops instantly.
		if _, err := bufio.NewReader(os.Stdin).ReadString('\n'); err == nil {
			close(enter)
		}
	}()

	var timeout <-chan time.Time
	if duration > 0 {
		timeout = time.After(duration)
		fmt.Printf("stopping automatically after %s\n", duration)
	} else {
		fmt.Println("press Enter or Ctrl+C to stop")
	}

	started := time.Now()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-interrupt:
			return
		case <-enter:
			return
		case <-timeout:
			return
		case <-tick.C:
			fmt.Printf("\r  %s ", time.Since(started).Round(time.Second))
		}
	}
}
