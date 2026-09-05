package presentation

import (
	"fmt"
	"strings"
)

func (p *Presenter) Help(command, description, example, usage string, root bool) error {
	p.StopProgress()
	var b strings.Builder
	if root {
		b.WriteString(p.heading("Start here") + "\n")
		b.WriteString("  lettermint auth login --name work\n")
		b.WriteString("  lettermint messages send --project PROJECT_ID --file message.json --idempotency-key order-1042\n")
		b.WriteString("  lettermint webhooks listen --project PROJECT_ID --forward-to http://localhost:3000/webhooks/lettermint\n\n")
	} else {
		fmt.Fprintf(&b, "%s\n\n%s\n\n", p.heading(Text(command)), Text(description))
	}
	// Usage comes from Cobra, not from a server response. Keep its line breaks.
	b.WriteString(usage)
	if example != "" && !strings.Contains(usage, example) {
		fmt.Fprintf(&b, "\nExample:\n%s\n", example)
	}
	return p.write(p.out, p.output, b.String())
}
