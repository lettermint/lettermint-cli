package command

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// Webhook secret and delivery commands address one webhook by ID and do not
// use the saved project or route filters.
func (a *app) webhookCommands(parent *cobra.Command) {
	webhookPath := func(id string) string { return "/v1/webhooks/" + url.PathEscape(id) }
	deliveryPath := func(args []string) string {
		return webhookPath(args[0]) + "/deliveries/" + url.PathEscape(args[1])
	}
	parent.AddCommand(&cobra.Command{Use: "regenerate-secret <webhook-id>", Short: "Replace the webhook signing secret; the old secret stops working", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		client, _, _, err := a.client(c)
		if err != nil {
			return err
		}
		if err = a.confirm(c, "Regenerate the secret of webhook "+args[0]+"? The old secret stops working immediately."); err != nil {
			return err
		}
		b, err := client.Do(c.Context(), "POST", webhookPath(args[0])+"/regenerate-secret", nil, nil, nil)
		if err != nil {
			return err
		}
		return a.output(c, b)
	}})

	var status []string
	var event, from, to, cursor string
	list := &cobra.Command{Use: "deliveries <webhook-id>", Short: "List delivery attempts of a webhook", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		query := url.Values{}
		if len(status) > 0 {
			query.Set("filter[status]", strings.Join(status, ","))
		}
		if event != "" {
			query.Set("filter[event_type]", event)
		}
		for name, value := range map[string]string{"from": from, "to": to} {
			if value == "" {
				continue
			}
			if _, err := time.Parse(time.DateOnly, value); err != nil {
				return errors.New("--" + name + " must be a date in YYYY-MM-DD format")
			}
			query.Set("filter["+name+"_date]", value)
		}
		client, _, _, err := a.client(c)
		if err != nil {
			return err
		}
		// The API uses a fixed page size for deliveries; 15 matches it.
		b, err := client.List(c.Context(), webhookPath(args[0])+"/deliveries", query, 15, cursor)
		if err != nil {
			return err
		}
		return a.output(c, b)
	}}
	f := list.Flags()
	f.StringSliceVar(&status, "status", nil, "Delivery status: pending, success, failed, client_error, server_error, or timeout; repeat or separate with commas")
	f.StringVar(&event, "event", "", "Event type, such as message.delivered")
	f.StringVar(&from, "from", "", "Only deliveries created on or after this date (YYYY-MM-DD)")
	f.StringVar(&to, "to", "", "Only deliveries created before this date (YYYY-MM-DD)")
	f.StringVar(&cursor, "cursor", "", "Cursor from the previous response")
	parent.AddCommand(list)

	parent.AddCommand(&cobra.Command{Use: "delivery <webhook-id> <delivery-id>", Short: "Show one delivery attempt; payload and response need content access", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		client, _, _, err := a.client(c)
		if err != nil {
			return err
		}
		b, err := client.Do(c.Context(), "GET", deliveryPath(args), nil, nil, nil)
		if err != nil {
			return err
		}
		return a.output(c, b)
	}})

	parent.AddCommand(&cobra.Command{Use: "retry <webhook-id> <delivery-id>", Short: "Send the original payload of a delivery to the webhook again", Args: cobra.ExactArgs(2), RunE: func(c *cobra.Command, args []string) error {
		client, _, _, err := a.client(c)
		if err != nil {
			return err
		}
		b, err := client.Do(c.Context(), "POST", deliveryPath(args)+"/retry", nil, nil, nil)
		if err != nil {
			return err
		}
		return a.output(c, b)
	}})
}
