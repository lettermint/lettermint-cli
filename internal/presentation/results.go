package presentation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/x/ansi"
)

type Context struct {
	Profile, Project, Route string
}

type column struct {
	title, key string
	full       bool
}

var columns = map[string][]column{
	"projects list":   {{"Name", "name", false}, {"ID", "id", true}, {"Created", "created_at", true}},
	"routes list":     {{"Name", "name", false}, {"Slug", "slug", false}, {"Type", "route_type", false}, {"Default", "is_default", false}, {"ID", "id", true}},
	"domains list":    {{"Domain", "domain", false}, {"Status", "status", false}, {"ID", "id", true}},
	"webhooks list":   {{"Name", "name", false}, {"Enabled", "enabled", false}, {"URL", "url", false}, {"ID", "id", true}},
	"messages list":   {{"Created", "created_at", true}, {"Type", "type", false}, {"Status", "status", false}, {"Recipient", "to", false}, {"Subject", "subject", false}, {"ID", "id", true}},
	"messages events": {{"Time", "timestamp", true}, {"Event", "event", false}},
	"listeners list":  {{"ID", "id", true}, {"Project", "project_id", true}, {"State", "state", false}, {"Expiry", "expires_at", true}},
	"profiles list":   {{"Selected", "selected", false}, {"Name", "name", false}, {"Team", "team_id", true}, {"Project", "project", true}, {"Route", "route", true}},
	"skills list":     {{"Name", "name", false}, {"Version", "version", true}},
}

func (p *Presenter) Result(command string, value any, scope Context) error {
	p.StopProgress()
	if p.JSON() {
		return json.NewEncoder(p.out).Encode(value)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	var decoded any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	root, _ := decoded.(map[string]any)
	data := decoded
	if v, ok := root["data"]; ok {
		data = v
	}
	if command == "version" {
		return p.write(p.out, p.output, "Lettermint "+scalar(root["version"])+"\n")
	}
	var b strings.Builder
	fmt.Fprintln(&b, p.heading(resultTitle(command)))
	if scope.Profile != "" {
		fmt.Fprintf(&b, "Profile: %s", Text(scope.Profile))
		if scope.Project != "" {
			fmt.Fprintf(&b, "  Project: %s", Text(scope.Project))
		}
		if scope.Route != "" {
			fmt.Fprintf(&b, "  Route: %s", Text(scope.Route))
		}
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	if cols, ok := columns[command]; ok {
		rows := p.rows(command, root, data)
		p.list(&b, cols, rows)
		unit := "results"
		if len(rows) == 1 {
			unit = "result"
		}
		fmt.Fprintf(&b, "\n%d %s shown.\n", len(rows), unit)
		if cursor, ok := root["next_cursor"].(string); ok && cursor != "" {
			fmt.Fprintf(&b, "\nMore results are available. Repeat this command with the same context and:\n--cursor %s\n", Text(cursor))
		}
	} else {
		if command == "listeners get" {
			// Show session behavior, not server storage and connection fields.
			if m, ok := data.(map[string]any); ok {
				selected := map[string]any{}
				for _, key := range []string{"id", "project_id", "route_id", "events", "include_machine_events", "acknowledged_sequence", "expires_at", "last_seen_at", "stopped_at", "terminal_reason", "attempts"} {
					if v, ok := m[key]; ok {
						selected[key] = v
					}
				}
				selected["state"] = p.listenerState(m)
				data = selected
			}
		}
		p.fields(&b, data, "", command == "listeners secret")
		switch command {
		case "messages send":
			b.WriteString("\nAccepted for processing. Delivery is not yet confirmed.\n")
			if m, ok := data.(map[string]any); ok {
				fmt.Fprintf(&b, "Check delivery with: lettermint messages events %s\n", scalar(m["message_id"]))
			}
		case "auth logout":
			if revoked, ok := root["revoked"].(bool); ok && !revoked {
				b.WriteString("\nServer access can remain active. Revoke it in Applications if needed.\n")
			}
		}
	}
	return p.write(p.out, p.output, b.String())
}

func resultTitle(command string) string {
	switch command {
	case "messages send":
		return "Message accepted"
	case "auth login":
		return "Login complete"
	case "auth status":
		return "Login status"
	case "auth logout":
		return "Saved login removed"
	case "profiles use":
		return "Profile selected"
	case "context show":
		return "Current context"
	case "context set":
		return "Context saved"
	case "skills export":
		return "Skills exported"
	case "doctor":
		return "Connection check"
	case "messages events":
		return "Message events"
	}
	parts := strings.Fields(command)
	if len(parts) == 2 {
		noun := label(strings.TrimSuffix(parts[0], "s"))
		switch parts[1] {
		case "list":
			return label(parts[0])
		case "get":
			return noun
		case "create":
			return noun + " created"
		case "update":
			return noun + " updated"
		case "delete":
			return noun + " deleted"
		case "stop":
			return noun + " stopped"
		case "assign":
			return "Domain projects updated"
		case "verify", "verify-inbound-domain":
			return "Verification result"
		case "test":
			return "Webhook test result"
		case "replay":
			return "Replay queued"
		case "secret":
			return "Listener signing secret"
		}
	}
	return label(command)
}

func (p *Presenter) rows(command string, root map[string]any, data any) []map[string]any {
	var rows []map[string]any
	switch command {
	case "profiles list":
		profiles, _ := root["profiles"].(map[string]any)
		for _, name := range sortedKeys(profiles) {
			m, _ := profiles[name].(map[string]any)
			row := map[string]any{"name": name, "selected": name == root["selected"]}
			for _, key := range []string{"team_id", "project", "route"} {
				row[key] = m[key]
			}
			rows = append(rows, row)
		}
	case "skills list":
		items, _ := root["skills"].([]any)
		for _, item := range items {
			rows = append(rows, map[string]any{"name": item, "version": root["version"]})
		}
	default:
		items, _ := data.([]any)
		for _, item := range items {
			if row, ok := item.(map[string]any); ok {
				if command == "listeners list" {
					row["state"] = p.listenerState(row)
				}
				rows = append(rows, row)
			}
		}
	}
	return rows
}

func (p *Presenter) listenerState(row map[string]any) any {
	if row["stopped_at"] != nil && row["stopped_at"] != "" {
		return "stopped"
	}
	if expiry, ok := row["expires_at"].(string); ok {
		if at, err := time.Parse(time.RFC3339Nano, expiry); err == nil {
			if !at.After(p.now()) {
				return "expired"
			}
			return "active"
		}
	}
	return nil
}

func (p *Presenter) list(b *strings.Builder, cols []column, rows []map[string]any) {
	if len(rows) == 0 {
		b.WriteString("No results found.\n")
		return
	}
	headers, widths := make([]string, len(cols)), make([]int, len(cols))
	values := make([][]string, len(rows))
	for i, c := range cols {
		headers[i], widths[i] = c.title, lipgloss.Width(c.title)
	}
	for i, row := range rows {
		values[i] = make([]string, len(cols))
		for j, col := range cols {
			text := p.cell(col.key, row[col.key])
			if !col.full {
				text = ansi.Truncate(text, 26, "…")
			}
			values[i][j] = text
			widths[j] = max(widths[j], lipgloss.Width(text))
		}
	}
	width := (len(cols) - 1) * 2
	for _, n := range widths {
		width += n
	}
	// Reduce descriptive columns before changing to stacked records. IDs and
	// timestamps retain their full width.
	for width > p.output.Width {
		largest := -1
		for j, col := range cols {
			if !col.full && widths[j] > max(8, lipgloss.Width(col.title)) && (largest < 0 || widths[j] > widths[largest]) {
				largest = j
			}
		}
		if largest < 0 {
			break
		}
		widths[largest]--
		width--
	}
	if width > p.output.Width {
		for i, row := range rows {
			if i > 0 {
				b.WriteByte('\n')
			}
			for _, col := range cols {
				// Stacked records keep full values and IDs available for copying.
				fmt.Fprintf(b, "%s: %s\n", col.title, p.cell(col.key, row[col.key]))
			}
		}
		return
	}
	for i := range values {
		for j, col := range cols {
			if !col.full {
				values[i][j] = ansi.Truncate(values[i][j], widths[j], "…")
			}
		}
	}
	grid := table.New().Headers(headers...).Rows(values...).Width(width).Wrap(false).
		BorderTop(false).BorderBottom(false).BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderHeader(false).BorderRow(false).
		StyleFunc(func(row, col int) lipgloss.Style {
			style := lipgloss.NewStyle().Width(widths[col])
			if col < len(cols)-1 {
				style = style.PaddingRight(2).Width(widths[col] + 2)
			}
			if row == table.HeaderRow {
				return style.Bold(true).Foreground(lipgloss.Color("#40916c"))
			}
			return style
		})
	b.WriteString(grid.String())
	b.WriteByte('\n')
}

func (p *Presenter) cell(key string, value any) string {
	if key == "to" {
		if recipients, ok := value.([]any); ok {
			var addresses []string
			for _, recipient := range recipients {
				if m, ok := recipient.(map[string]any); ok {
					addresses = append(addresses, scalar(m["email"]))
				} else {
					addresses = append(addresses, scalar(recipient))
				}
			}
			if len(addresses) > 0 {
				return strings.Join(addresses, ", ")
			}
			return "—"
		}
	}
	if key == "timestamp" || strings.HasSuffix(key, "_at") {
		if raw, ok := value.(string); ok {
			if at, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				return at.In(p.location).Format("2006-01-02 15:04:05 MST")
			}
		}
	}
	text := scalar(value)
	if key == "status" || key == "state" || key == "enabled" || key == "is_default" || key == "selected" {
		style := lipgloss.NewStyle()
		switch strings.ToLower(text) {
		case "delivered", "verified", "active", "yes":
			style = style.Foreground(lipgloss.Green)
		case "failed", "bounced", "rejected", "expired":
			style = style.Foreground(lipgloss.Red)
		case "pending", "accepted", "stopped", "no":
			style = style.Foreground(lipgloss.Yellow)
		}
		return style.Render(text)
	}
	return text
}

func (p *Presenter) fields(b *strings.Builder, value any, indent string, secrets bool) {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range sortedKeys(v) {
			if !secrets && (strings.Contains(strings.ToLower(key), "secret") || key == "access_token" || key == "refresh_token") {
				continue
			}
			switch nested := v[key].(type) {
			case map[string]any, []any:
				fmt.Fprintf(b, "%s%s:\n", indent, label(key))
				p.fields(b, nested, indent+"  ", secrets)
			default:
				fmt.Fprintf(b, "%s%s: %s\n", indent, label(key), p.cell(key, nested))
			}
		}
	case []any:
		if len(v) == 0 {
			fmt.Fprintf(b, "%s—\n", indent)
		}
		for i, item := range v {
			switch item.(type) {
			case map[string]any, []any:
				fmt.Fprintf(b, "%s%d.\n", indent, i+1)
				p.fields(b, item, indent+"  ", secrets)
			default:
				fmt.Fprintf(b, "%s- %s\n", indent, scalar(item))
			}
		}
	default:
		fmt.Fprintf(b, "%s%s\n", indent, scalar(value))
	}
}

func scalar(v any) string {
	if v == nil || v == "" {
		return "—"
	}
	switch value := v.(type) {
	case bool:
		if value {
			return "yes"
		}
		return "no"
	case string:
		return Text(value)
	default:
		return Text(fmt.Sprint(value))
	}
}

func label(key string) string {
	switch key {
	case "id":
		return "ID"
	case "url":
		return "URL"
	case "api":
		return "API"
	case "api_url":
		return "API URL"
	case "smtp_enabled":
		return "SMTP enabled"
	case "dns_records":
		return "DNS records"
	}
	if strings.HasSuffix(key, "_id") {
		return label(strings.TrimSuffix(key, "_id")) + " ID"
	}
	if strings.HasSuffix(key, "_ids") {
		return label(strings.TrimSuffix(key, "_ids")) + " IDs"
	}
	key = Text(strings.ReplaceAll(key, "_", " "))
	if key == "" {
		return key
	}
	runes := []rune(key)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		rank := func(s string) int {
			switch s {
			case "name":
				return 0
			case "id", "message_id":
				return 1
			default:
				return 2
			}
		}
		if rank(keys[i]) != rank(keys[j]) {
			return rank(keys[i]) < rank(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}
