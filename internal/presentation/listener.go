package presentation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/lettermint/lettermint-cli/internal/api"
)

func (p *Presenter) ListenerStarted(id, destination string, events []string, machine bool, scope Context) error {
	p.StopProgress()
	if p.JSON() {
		return p.Notice("Listener " + id + " is active. Use listeners secret to configure local signature verification.")
	}
	var b strings.Builder
	fmt.Fprintln(&b, p.heading("Listener active"))
	fmt.Fprintf(&b, "Session: %s\nProfile: %s\nProject: %s\nRoute: %s\nForward to: %s\n",
		Text(id), Text(scope.Profile), scalar(scope.Project), scalar(scope.Route), Text(destination))
	filter := "all message and suppression events"
	if len(events) > 0 {
		filter = strings.Join(events, ", ")
	}
	fmt.Fprintf(&b, "Events: %s\nMachine events: %s\n", Text(filter), scalar(machine))
	fmt.Fprintf(&b, "\nUse listeners secret %s to configure signature verification.\n", Text(id))
	fmt.Fprintf(&b, "Replay an attempt: lettermint listeners replay %s --sequence SEQUENCE --profile %s\n", Text(id), Text(scope.Profile))
	b.WriteString("Press Ctrl+C to stop this listener.\n\n")
	return p.write(p.err, p.diagnostic, b.String())
}

func (p *Presenter) ListenerAttempt(event api.Event, attempt api.Attempt) error {
	if p.JSON() {
		return json.NewEncoder(p.out).Encode(map[string]any{"event": event, "result": attempt})
	}
	status := "FAILED"
	style := lipgloss.NewStyle().Foreground(lipgloss.Red)
	if attempt.Error == "" && attempt.Status >= 200 && attempt.Status < 300 {
		status = "OK"
		style = lipgloss.NewStyle().Foreground(lipgloss.Green)
	}
	result := fmt.Sprintf("%d %s", attempt.Status, status)
	if attempt.Status == 0 {
		result = status
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %4d ms  %s  seq=%d attempt=%d delivery=%s",
		p.now().In(p.location).Format("2006-01-02 15:04:05 MST"), style.Render(fmt.Sprintf("%-10s", result)),
		attempt.DurationMS, Text(event.Event), event.Sequence, event.Attempt, Text(event.DeliveryID))
	if status == "FAILED" {
		detail := attempt.Error
		if reason := http.StatusText(attempt.Status); reason != "" {
			if detail == "" {
				detail = reason
			} else {
				detail += " (" + reason + ")"
			}
		}
		if detail != "" {
			fmt.Fprintf(&b, "  error=%s", Text(detail))
		}
	}
	b.WriteByte('\n')
	return p.write(p.out, p.output, b.String())
}

func (p *Presenter) ListenerConnection(connected bool) {
	if p.JSON() {
		return
	}
	if connected {
		_ = p.Notice("Connection restored. Listening for events.")
	} else {
		_ = p.Notice("Connection lost. Reconnecting automatically.")
	}
}
