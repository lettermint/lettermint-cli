package command

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/lettermint/lettermint-cli/internal/api"
	"github.com/spf13/cobra"
)

func (a *app) resendCommand(parent *cobra.Command) {
	var key string
	var recipients []string
	cmd := &cobra.Command{Use: "resend <message-id>", Short: "Send a message's stored content again as a new message", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if c.Flags().Changed("idempotency-key") && (len(key) == 0 || len(key) > 255) {
			return errors.New("--idempotency-key must contain 1 to 255 bytes")
		}
		client, p, _, err := a.client(c)
		if err != nil {
			return err
		}
		description := "Resend message " + args[0] + " to every original recipient?"
		if len(recipients) > 0 {
			description = "Resend message " + args[0] + " to " + strings.Join(recipients, ", ") + "?"
		}
		if err = a.confirm(c, description); err != nil {
			return err
		}
		query := url.Values{}
		if p.Project != "" {
			query.Set("filter[project]", p.Project)
		}
		body := map[string]any{}
		if len(recipients) > 0 {
			body["recipients"] = recipients
		}
		headers := http.Header{}
		if key != "" {
			headers.Set("Idempotency-Key", key)
		}
		b, err := client.Do(c.Context(), http.MethodPost, "/v1/messages/"+url.PathEscape(args[0])+"/resend", query, body, headers)
		if err != nil {
			return &api.SendError{Err: err, HasIdempotencyKey: key != ""}
		}
		return a.output(c, b)
	}}
	cmd.Flags().StringArrayVar(&recipients, "recipient", nil, "Original recipient to resend to; repeat for more than one; default is every original recipient")
	cmd.Flags().StringVar(&key, "idempotency-key", "", "Optional key (1 to 255 bytes); reuse it with the same input after an uncertain response")
	parent.AddCommand(cmd)
}
