//go:build windows

// quill-tray is the one-click face of quill: a tray icon that starts and
// stops recordings, mirroring the original's menu-bar app. Built with
// -H=windowsgui so no console window appears.
package main

import (
	_ "embed"
	"fmt"
	"os/exec"
	"time"

	"github.com/getlantern/systray"
	"github.com/gt12889/quill-win/internal/quill"
)

//go:embed idle.ico
var iconIdle []byte

//go:embed recording.ico
var iconRecording []byte

//go:embed busy.ico
var iconBusy []byte

func main() {
	systray.Run(onReady, nil)
}

func onReady() {
	systray.SetIcon(iconIdle)
	systray.SetTooltip("quill — not recording")

	status := systray.AddMenuItem("Not recording", "")
	status.Disable()
	toggle := systray.AddMenuItem("Start recording", "")
	systray.AddSeparator()
	open := systray.AddMenuItem("Open recordings folder", "")
	quit := systray.AddMenuItem("Quit", "")

	go func() {
		var rec *quill.Recording
		// transcribing counts sessions still in FinishSession, so the
		// icon only returns to idle when the last one lands.
		transcribing := 0
		finished := make(chan struct{}, 8)

		setIdleOrBusy := func() {
			if rec != nil {
				return
			}
			if transcribing > 0 {
				systray.SetIcon(iconBusy)
				status.SetTitle("Transcribing…")
				systray.SetTooltip("quill — transcribing")
			} else {
				systray.SetIcon(iconIdle)
				status.SetTitle("Not recording")
				systray.SetTooltip("quill — not recording")
			}
		}

		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-toggle.ClickedCh:
				if rec == nil {
					r, err := quill.StartRecording()
					if err != nil {
						status.SetTitle("start failed: " + err.Error())
						continue
					}
					rec = r
					systray.SetIcon(iconRecording)
					status.SetTitle("Recording 0:00")
					toggle.SetTitle("Stop recording")
					systray.SetTooltip("quill — recording")
				} else {
					r := rec
					rec = nil
					toggle.SetTitle("Start recording")
					transcribing++
					setIdleOrBusy()
					// Stopping and transcribing happen off the menu
					// loop; a new recording can start meanwhile, same
					// as the original's serial queue.
					go func() {
						r.Stop()
						quill.FinishSession(r.Dir, true)
						finished <- struct{}{}
					}()
				}
			case <-finished:
				transcribing--
				setIdleOrBusy()
			case <-tick.C:
				if rec != nil {
					status.SetTitle("Recording " + clock(rec.Elapsed()))
				}
			case <-open.ClickedCh:
				exec.Command("explorer.exe", quill.RecordingsRoot()).Start()
			case <-quit.ClickedCh:
				if rec != nil {
					rec.Stop()
				}
				systray.Quit()
				return
			}
		}
	}()
}

func clock(d time.Duration) string {
	s := int(d.Seconds())
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, (s/60)%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}
