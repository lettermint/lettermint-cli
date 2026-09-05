package presentation

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/charmbracelet/colorprofile"
)

type progress struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func (p *Presenter) StartProgress(ctx context.Context, label string) {
	p.StopProgress()
	if p.JSON() || p.options.Plain || !p.diagnostic.TTY || p.diagnostic.Profile <= colorprofile.Ascii {
		return
	}
	p.progress = startProgress(ctx, consoleWriter{p.err}, Text(label), 400*time.Millisecond)
}

func startProgress(ctx context.Context, w io.Writer, label string, delay time.Duration) *progress {
	state := &progress{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(state.done)
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-state.stop:
			return
		case <-timer.C:
		}
		defer fmt.Fprint(w, "\r\x1b[2K")
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for n := 0; ; n++ {
			if _, err := fmt.Fprintf(w, "\r\x1b[2K%c %s", "|/-\\"[n%4], label); err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-state.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return state
}

func (p *Presenter) StopProgress() {
	if p.progress != nil {
		p.progress.once.Do(func() { close(p.progress.stop) })
		<-p.progress.done
		p.progress = nil
	}
}
