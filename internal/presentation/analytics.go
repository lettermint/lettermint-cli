package presentation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// keyOrder keeps the order of a JSON object's keys, which the API returns in
// the order of the query.
type keyOrder []string

func (k *keyOrder) UnmarshalJSON(b []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(b))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return err
	}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		*k = append(*k, fmt.Sprint(token))
		var skip json.RawMessage
		if err = decoder.Decode(&skip); err != nil {
			return err
		}
	}
	return nil
}

type analyticsOrder struct {
	Data struct {
		Summary *struct {
			Metrics keyOrder `json:"metrics"`
		} `json:"summary"`
		TimeSeries []struct {
			Metrics keyOrder `json:"metrics"`
		} `json:"time_series"`
		Breakdown []struct {
			Dimensions keyOrder `json:"dimensions"`
			Metrics    keyOrder `json:"metrics"`
		} `json:"breakdown"`
	} `json:"data"`
}

func (p *Presenter) analytics(b *strings.Builder, encoded []byte, root map[string]any) {
	var order analyticsOrder
	_ = json.Unmarshal(encoded, &order)
	var metrics, dimensions []string
	switch {
	case order.Data.Summary != nil:
		metrics = order.Data.Summary.Metrics
	case len(order.Data.TimeSeries) > 0:
		metrics = order.Data.TimeSeries[0].Metrics
	case len(order.Data.Breakdown) > 0:
		metrics = order.Data.Breakdown[0].Metrics
	}
	if len(order.Data.Breakdown) > 0 {
		dimensions = order.Data.Breakdown[0].Dimensions
	}
	data, _ := root["data"].(map[string]any)
	meta, _ := root["meta"].(map[string]any)
	compare := meta["comparison"] != nil

	fmt.Fprintf(b, "Window: %s to %s", p.cell("timestamp", meta["from"]), p.cell("timestamp", meta["to"]))
	if meta["ongoing"] == true {
		fmt.Fprintf(b, " (ongoing; data until %s)", p.cell("timestamp", meta["effective_to"]))
	}
	b.WriteByte('\n')
	if comparison, ok := meta["comparison"].(map[string]any); ok {
		fmt.Fprintf(b, "Compared with: %s to %s\n", p.cell("timestamp", comparison["from"]), p.cell("timestamp", comparison["to"]))
	}
	fmt.Fprintf(b, "Time zone: %s\n", scalar(meta["timezone"]))
	if meta["partial"] == true {
		fmt.Fprintf(b, "No data before %s.\n", p.cell("timestamp", meta["available_since"]))
	}

	if summary, ok := data["summary"].(map[string]any); ok {
		fmt.Fprintf(b, "\n%s\n", p.heading("Summary"))
		cols := []column{{"Metric", "metric", true}, {"Value", "value", true}}
		if compare {
			cols = append(cols, column{"Previous", "previous", true}, column{"Change", "change", true})
		}
		values, _ := summary["metrics"].(map[string]any)
		previous, _ := summary["previous"].(map[string]any)
		previousValues, _ := previous["metrics"].(map[string]any)
		changes, _ := summary["change"].(map[string]any)
		var rows []map[string]any
		for _, metric := range metrics {
			rows = append(rows, map[string]any{
				"metric":   metric,
				"value":    metricValue(metric, values[metric]),
				"previous": metricValue(metric, previousValues[metric]),
				"change":   metricChange(metric, changes[metric]),
			})
		}
		p.list(b, cols, rows)
	}

	if series, ok := data["time_series"].([]any); ok {
		fmt.Fprintf(b, "\n%s\n", p.heading("Time series by "+scalar(meta["interval"])))
		cols := append([]column{{"Bucket", "bucket", true}}, metricColumns(metrics, compare)...)
		var rows []map[string]any
		partial := false
		for _, item := range series {
			bucket, _ := item.(map[string]any)
			label := bucketLabel(bucket["from"], meta["interval"])
			if bucket["partial"] == true {
				label += " *"
				partial = true
			}
			row := metricCells(bucket, metrics, compare)
			row["bucket"] = label
			rows = append(rows, row)
		}
		p.list(b, cols, rows)
		if partial {
			b.WriteString("* Partial bucket.\n")
		}
	}

	if breakdown, ok := data["breakdown"].([]any); ok {
		fmt.Fprintf(b, "\n%s\n", p.heading("Breakdown"))
		var cols []column
		for i, dimension := range dimensions {
			cols = append(cols, column{Text(dimension), "d" + strconv.Itoa(i), false})
		}
		cols = append(cols, metricColumns(metrics, compare)...)
		var rows []map[string]any
		trend := false
		for _, item := range breakdown {
			group, _ := item.(map[string]any)
			row := metricCells(group, metrics, compare)
			values, _ := group["dimensions"].(map[string]any)
			for i, dimension := range dimensions {
				row["d"+strconv.Itoa(i)] = values[dimension]
			}
			trend = trend || group["trend"] != nil
			rows = append(rows, row)
		}
		p.list(b, cols, rows)
		pagination, _ := root["pagination"].(map[string]any)
		fmt.Fprintf(b, "\n%s of %s groups shown.\n", metricValue("groups", pagination["returned_groups"]), metricValue("groups", pagination["total_groups"]))
		if pagination["truncated"] == true {
			fmt.Fprintf(b, "Only the first %s groups are ranked. Add a filter to narrow the query.\n", metricValue("groups", meta["ranked_group_limit"]))
		}
		if trend {
			b.WriteString("Row trends are included in JSON output.\n")
		}
		if cursor, ok := pagination["next_cursor"].(string); ok && cursor != "" {
			fmt.Fprintf(b, "\nMore groups are available. Within 60 seconds, repeat this query with the same context and:\n--cursor %s\n", Text(cursor))
		}
	}
}

func metricColumns(metrics []string, compare bool) []column {
	var cols []column
	for i, metric := range metrics {
		cols = append(cols, column{Text(metric), "m" + strconv.Itoa(i), true})
		if compare {
			cols = append(cols, column{Text(metric) + " change", "c" + strconv.Itoa(i), true})
		}
	}
	return cols
}

func metricCells(row map[string]any, metrics []string, compare bool) map[string]any {
	values, _ := row["metrics"].(map[string]any)
	changes, _ := row["change"].(map[string]any)
	cells := map[string]any{}
	for i, metric := range metrics {
		cells["m"+strconv.Itoa(i)] = metricValue(metric, values[metric])
		if compare {
			cells["c"+strconv.Itoa(i)] = metricChange(metric, changes[metric])
		}
	}
	return cells
}

func number(value any) (float64, bool) {
	n, ok := value.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

// metricValue shows rates as percentages and counts with digit grouping.
func metricValue(metric string, value any) string {
	f, ok := number(value)
	if !ok {
		return scalar(value)
	}
	if strings.HasSuffix(metric, "_rate") {
		return strconv.FormatFloat(f*100, 'f', 2, 64) + "%"
	}
	return grouped(f)
}

func metricChange(metric string, value any) string {
	change, _ := value.(map[string]any)
	if points, ok := number(change["percentage_points"]); ok {
		return signed(strconv.FormatFloat(points, 'f', 2, 64)) + " pp"
	}
	if relative, ok := number(change["relative"]); ok {
		return signed(strconv.FormatFloat(relative*100, 'f', 2, 64)) + "%"
	}
	if absolute, ok := number(change["absolute"]); ok {
		return signed(grouped(absolute))
	}
	return "—"
}

func signed(text string) string {
	if strings.HasPrefix(text, "-") || strings.Trim(text, "0.,") == "" {
		return text
	}
	return "+" + text
}

func grouped(f float64) string {
	text := strconv.FormatFloat(f, 'f', -1, 64)
	if f != math.Trunc(f) {
		text = strings.TrimRight(strings.TrimRight(strconv.FormatFloat(f, 'f', 2, 64), "0"), ".")
	}
	whole, fraction, _ := strings.Cut(strings.TrimPrefix(text, "-"), ".")
	var b strings.Builder
	if f < 0 {
		b.WriteByte('-')
	}
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if fraction != "" {
		b.WriteString("." + fraction)
	}
	return b.String()
}

// bucketLabel keeps the bucket in the query time zone, as the API returns it.
func bucketLabel(value, interval any) string {
	raw, _ := value.(string)
	at, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return scalar(value)
	}
	if interval == "hour" {
		return at.Format("2006-01-02 15:04")
	}
	return at.Format("2006-01-02")
}
