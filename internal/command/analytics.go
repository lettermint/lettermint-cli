package command

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// analyticsQueryFlags build a query and cannot be combined with --file.
var analyticsQueryFlags = []string{"metric", "from", "to", "timezone", "interval", "group-by", "filter", "compare", "trend", "sort", "limit"}

type analyticsOptions struct {
	from, to, timezone, interval, sort string
	metrics, groupBy, filters          []string
	compare, trend                     bool
	limit                              int
	changed                            func(string) bool
}

func (a *app) analyticsCommand(root *cobra.Command) {
	var file, cursor string
	o := analyticsOptions{}
	cmd := &cobra.Command{Use: "analytics", Short: "Query delivery, engagement, and latency metrics", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if file != "" {
			for _, name := range analyticsQueryFlags {
				if c.Flags().Changed(name) {
					return errors.New("--file cannot be combined with query flags")
				}
			}
		}
		if c.Flags().Changed("cursor") && cursor == "" {
			return errors.New("--cursor must not be empty")
		}
		var body map[string]any
		var err error
		if file != "" {
			if body, err = input(c, file); err != nil {
				return err
			}
			if metrics, ok := body["metrics"].([]any); !ok || len(metrics) == 0 {
				return errors.New("the query must contain a metrics array")
			}
		} else {
			o.changed = c.Flags().Changed
			if body, err = analyticsQuery(o); err != nil {
				return err
			}
		}
		if cursor != "" {
			body["cursor"] = cursor
		}
		client, p, _, err := a.client(c)
		if err != nil {
			return err
		}
		if err = analyticsContextFilters(body, p.Project, p.Route); err != nil {
			return err
		}
		b, err := client.Do(c.Context(), "POST", "/v1/analytics", nil, body, nil)
		if err != nil {
			return err
		}
		return a.output(c, b)
	}}
	cmd.Example = "  lettermint analytics --profile work --metric delivered --metric bounce_rate --from 2026-11-01 --to 2026-11-07\n" +
		"  lettermint analytics --profile work --metric delivered,deferral_rate --group-by provider_family --sort deferral_rate\n" +
		"  lettermint analytics --profile work --file query.json --json --no-input"
	f := cmd.Flags()
	f.StringVar(&file, "file", "", "JSON query file; use - for standard input")
	f.StringSliceVar(&o.metrics, "metric", nil, "Metric to return; repeat or separate with commas")
	f.StringVar(&o.from, "from", "", "Window start: YYYY-MM-DD or RFC 3339; default is the last 30 days")
	f.StringVar(&o.to, "to", "", "Window end: YYYY-MM-DD (included) or RFC 3339 (excluded)")
	f.StringVar(&o.timezone, "timezone", "", "IANA time zone for dates and buckets; default is UTC")
	f.StringVar(&o.interval, "interval", "", "Add a time series with hour, day, or week buckets")
	f.StringSliceVar(&o.groupBy, "group-by", nil, "Add a breakdown by up to 3 dimensions; repeat or separate with commas")
	f.StringArrayVar(&o.filters, "filter", nil, "Filter as DIMENSION=VALUE; repeat a dimension to match any of its values")
	f.BoolVar(&o.compare, "compare", false, "Compare with the previous period of the same length")
	f.BoolVar(&o.trend, "trend", false, "Add a time series to each breakdown row; requires --group-by")
	f.StringVar(&o.sort, "sort", "", "Breakdown order as METRIC or METRIC:asc; default is the first metric, highest first")
	f.IntVar(&o.limit, "limit", 50, "Breakdown rows per page (1 to 200)")
	f.StringVar(&cursor, "cursor", "", "Cursor from the previous response; repeat the same query")
	root.AddCommand(cmd)
}

func analyticsQuery(o analyticsOptions) (map[string]any, error) {
	if len(o.metrics) == 0 {
		return nil, errors.New("select at least one metric with --metric, or use --file")
	}
	if err := distinct("--metric", o.metrics); err != nil {
		return nil, err
	}
	body := map[string]any{"metrics": o.metrics}
	include := []string{"summary"}
	if (o.from == "") != (o.to == "") {
		return nil, errors.New("--from and --to must be used together")
	}
	if o.from != "" {
		if err := analyticsWindow(o.from, o.to); err != nil {
			return nil, err
		}
		body["from"], body["to"] = o.from, o.to
	}
	if o.timezone != "" {
		body["timezone"] = o.timezone
	}
	if o.changed("interval") {
		if o.interval != "hour" && o.interval != "day" && o.interval != "week" {
			return nil, errors.New("--interval must be hour, day, or week")
		}
		body["interval"] = o.interval
		include = append(include, "time_series")
	}
	if len(o.groupBy) > 3 {
		return nil, errors.New("--group-by accepts at most 3 dimensions")
	}
	if err := distinct("--group-by", o.groupBy); err != nil {
		return nil, err
	}
	if len(o.groupBy) > 0 {
		body["group_by"] = o.groupBy
		include = append(include, "breakdown")
	} else {
		for _, name := range []string{"trend", "sort", "limit"} {
			if o.changed(name) {
				return nil, fmt.Errorf("--%s requires --group-by", name)
			}
		}
	}
	body["include"] = include
	if len(o.filters) > 0 {
		filters, err := analyticsFilters(o.filters)
		if err != nil {
			return nil, err
		}
		body["filters"] = filters
	}
	if o.compare {
		body["compare"] = "previous_period"
	}
	if o.trend {
		body["include_trend"] = true
	}
	if o.sort != "" {
		metric, direction, ok := strings.Cut(o.sort, ":")
		if !ok {
			direction = "desc"
		}
		if direction != "asc" && direction != "desc" {
			return nil, errors.New("--sort direction must be asc or desc")
		}
		found := false
		for _, m := range o.metrics {
			found = found || m == metric
		}
		if !found {
			return nil, errors.New("--sort must name a metric from --metric")
		}
		body["sort"] = map[string]string{"metric": metric, "direction": direction}
	}
	if o.changed("limit") {
		if o.limit < 1 || o.limit > 200 {
			return nil, errors.New("--limit must be between 1 and 200")
		}
		body["limit"] = o.limit
	}
	return body, nil
}

func distinct(flag string, values []string) error {
	seen := map[string]bool{}
	for _, v := range values {
		if v == "" {
			return fmt.Errorf("%s must not be empty", flag)
		}
		if seen[v] {
			return fmt.Errorf("%s contains %s more than once", flag, v)
		}
		seen[v] = true
	}
	return nil
}

// analyticsWindow accepts two dates or two RFC 3339 instants, as the API does.
func analyticsWindow(from, to string) error {
	for _, layout := range []string{time.DateOnly, time.RFC3339} {
		start, startErr := time.Parse(layout, from)
		end, endErr := time.Parse(layout, to)
		if startErr == nil && endErr == nil {
			if end.Before(start) || (layout == time.RFC3339 && !end.After(start)) {
				return errors.New("--to must be after --from")
			}
			return nil
		}
	}
	return errors.New("--from and --to must both be dates (YYYY-MM-DD) or both be RFC 3339 times")
}

// analyticsFilters turns DIMENSION=VALUE flags into eq filters, or in filters
// when a dimension is repeated. Other operators need --file.
func analyticsFilters(flags []string) ([]map[string]any, error) {
	var order []string
	values := map[string][]string{}
	for _, flag := range flags {
		dimension, value, ok := strings.Cut(flag, "=")
		if !ok || dimension == "" {
			return nil, errors.New("--filter must have the form DIMENSION=VALUE")
		}
		if _, seen := values[dimension]; !seen {
			order = append(order, dimension)
		}
		values[dimension] = append(values[dimension], value)
	}
	filters := make([]map[string]any, 0, len(order))
	for _, dimension := range order {
		operator := "eq"
		if len(values[dimension]) > 1 {
			operator = "in"
		}
		filters = append(filters, map[string]any{"dimension": dimension, "operator": operator, "values": values[dimension]})
	}
	return filters, nil
}

// analyticsContextFilters limits the query to the selected project and route.
func analyticsContextFilters(body map[string]any, project, route string) error {
	var filters []any
	switch existing := body["filters"].(type) {
	case nil:
	case []any:
		filters = existing
	case []map[string]any:
		for _, f := range existing {
			filters = append(filters, f)
		}
	default:
		return errors.New("filters must be a JSON array")
	}
	for _, scope := range []struct{ dimension, value, flag string }{{"project_id", project, "--project"}, {"route_id", route, "--route"}} {
		if scope.value == "" {
			continue
		}
		for _, f := range filters {
			if m, ok := f.(map[string]any); ok && m["dimension"] == scope.dimension {
				return fmt.Errorf("%s is set in both context and query filters; use %s \"\" to keep only the query filter", scope.dimension, scope.flag)
			}
		}
		filters = append(filters, map[string]any{"dimension": scope.dimension, "operator": "eq", "values": []string{scope.value}})
	}
	if len(filters) > 0 {
		body["filters"] = filters
	}
	return nil
}
