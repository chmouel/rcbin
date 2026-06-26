package output

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

const progressBarWidth = 24

var progressFrames = []string{"|", "/", "-", "\\"}

// Progress renders a single-line progress bar on stderr when stderr is an
// interactive colored terminal. For redirected output it is a no-op.
type Progress struct {
	rep     *Reporter
	title   string
	total   int
	current int
	detail  string
	frame   int
	enabled bool
	done    chan struct{}
	once    sync.Once
	mu      sync.Mutex
}

// Progress starts a progress bar for a finite task.
func (r *Reporter) Progress(title string, total int) *Progress {
	p := &Progress{
		rep:     r,
		title:   title,
		total:   total,
		enabled: r.color && total > 0,
		done:    make(chan struct{}),
	}
	if !p.enabled {
		return p
	}
	p.render()
	go p.loop()
	return p
}

func (p *Progress) loop() {
	ticker := time.NewTicker(120 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			if p.enabled {
				p.frame = (p.frame + 1) % len(progressFrames)
				p.renderLocked()
			}
			p.mu.Unlock()
		case <-p.done:
			return
		}
	}
}

// Advance marks one unit of work done and updates the optional detail text.
func (p *Progress) Advance(detail string) {
	if !p.enabled {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.current < p.total {
		p.current++
	}
	p.detail = detail
	p.renderLocked()
}

// Stop clears the progress line.
func (p *Progress) Stop() {
	p.once.Do(func() {
		if !p.enabled {
			close(p.done)
			return
		}
		p.mu.Lock()
		p.enabled = false
		fmt.Fprint(p.rep.err, "\r\033[K")
		p.mu.Unlock()
		close(p.done)
	})
}

func (p *Progress) render() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.renderLocked()
}

func (p *Progress) renderLocked() {
	filled := p.current * progressBarWidth / p.total
	if filled > progressBarWidth {
		filled = progressBarWidth
	}
	bar := strings.Repeat("=", filled) + strings.Repeat("-", progressBarWidth-filled)
	percent := p.current * 100 / p.total
	line := fmt.Sprintf(
		"\r%s %s [%s] %d/%d %3d%%",
		p.rep.paint(cyan, progressFrames[p.frame]),
		p.rep.Accent(p.title),
		p.rep.paint(cyan, bar),
		p.current,
		p.total,
		percent,
	)
	if p.detail != "" {
		line += " " + p.rep.Dim(p.detail)
	}
	fmt.Fprint(p.rep.err, line+"\033[K")
}
