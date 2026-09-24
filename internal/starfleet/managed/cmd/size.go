package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/pgEdge/pgedge-cli/internal/module"
	"github.com/pgEdge/pgedge-cli/internal/output"
	"github.com/pgEdge/pgedge-cli/internal/starfleet/managed/api"
	"github.com/spf13/cobra"
)

var (
	sizeColumns = []string{
		"NAME", "DISPLAY", "CPU", "MEMORY", "STORAGE", "STATUS"}
	sizePriceColumns = append(
		append([]string{}, sizeColumns...), "PRICE")
)

// NewSizeCmd builds the `pgedge starfleet managed size` command group.
func NewSizeCmd(rt *module.Runtime) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "size",
		Aliases: []string{"sizes"},
		Short:   "Discover pgEdge Managed sizes",
		Long: `size lists the sizes a managed database can be created at.

The NAME column holds the accepted values for 'database create
--size' and 'database resize --size'. Sizes carry the CPU, memory and
storage a database gets, and optionally its price.

Note that resize is one-way: a database can grow but not shrink,
because its storage cannot.

Example:
  pgedge starfleet managed size list
  pgedge starfleet managed size list --pricing`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			return c.Help()
		},
	}
	cmd.AddCommand(newSizeListCmd(rt), newSizeGetCmd(rt))
	return cmd
}

// --- list ---

func newSizeListCmd(rt *module.Runtime) *cobra.Command {
	var pricing bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available sizes",
		Long: `list shows every size a managed database can be created at.

Pass --pricing to resolve each size's current price from the billing
provider. It costs an extra lookup server-side, so it is off by
default, and a size with no configured price shows a blank cell
rather than a zero.

Example:
  pgedge starfleet managed size list
  pgedge starfleet managed size list --pricing
  pgedge starfleet managed size list -o json`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			params := &api.ListSizesParams{}
			if pricing {
				include := []api.ListSizesParamsInclude{api.Pricing}
				params.Include = &include
			}

			resp, err := client.ListSizesWithResponse(
				context.Background(), params)
			if err != nil {
				return fmt.Errorf("list sizes: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			sizes := resp.JSON200
			if sizes == nil || len(*sizes) == 0 {
				fmt.Fprintln(rt.Stderr, "No sizes found.")
				return nil
			}

			columns := sizeColumns
			if pricing {
				columns = sizePriceColumns
			}
			rows := make([]output.Row, 0, len(*sizes))
			for _, s := range *sizes {
				rows = append(rows, sizeRowFrom(s, pricing))
			}
			return rt.Output.Print(rows, columns)
		},
	}
	cmd.Flags().BoolVar(&pricing, "pricing", false,
		"Include each size's current price")
	return cmd
}

// --- get ---

func newSizeGetCmd(rt *module.Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "get <size_id>",
		Short: "Show a size's details",
		Long: `get shows one size and its Postgres settings.

The argument is the size's ID, not its name — take it from the -o
json output of 'size list'. Use -o yaml to read the postgres_settings
map, which text output does not show.

Price is not available here. The endpoint takes no 'include'
parameter, so pricing comes from 'size list --pricing' only.

Example:
  pgedge starfleet managed size get <size_id>
  pgedge starfleet managed size get <size_id> -o yaml`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Sizes are addressed by UUID, not by name. Parsed before
			// the client, a name gets a usage error naming the fix,
			// rather than a 404 or, without credentials, exit 5. Not
			// cli.ParseUUIDArg, which carries no remedy.
			id, err := uuid.Parse(args[0])
			if err != nil {
				return newExitError(fmt.Sprintf(
					"invalid size id %q: sizes are addressed by UUID, "+
						"not by name; take the id from "+
						"`pgedge starfleet managed size list -o json`", args[0]),
					ExitUsage)
			}

			client, err := clientFromCmd(rt, cmd)
			if err != nil {
				return err
			}

			resp, err := client.GetSizeWithResponse(
				context.Background(), id)
			if err != nil {
				return fmt.Errorf("get size: %w", err)
			}
			if err := checkResponse(resp.StatusCode(),
				string(resp.Body)); err != nil {
				return err
			}

			if rt.Output.Structured() {
				return rt.Output.Print(resp.JSON200, nil)
			}

			s := resp.JSON200
			if s == nil {
				fmt.Fprintln(rt.Stderr, "No size data returned.")
				return nil
			}
			// No PRICE column: this endpoint takes no `include`, so
			// pricing is never populated here and the column would
			// be blank on every row.
			return rt.Output.Print(
				[]output.Row{sizeRowFrom(*s, false)}, sizeColumns)
		},
	}
}

// --- row adapter ---

type sizeRow struct {
	name, display, cpu, memory, storage, status, price string
	withPrice                                          bool
}

func (r sizeRow) Columns() []string {
	cols := []string{r.name, r.display, r.cpu, r.memory, r.storage,
		output.ColorStatus(r.status)}
	if r.withPrice {
		cols = append(cols, r.price)
	}
	return cols
}

func sizeRowFrom(s api.Size, withPrice bool) sizeRow {
	return sizeRow{
		name:      s.Name,
		display:   s.DisplayName,
		cpu:       s.CpuLimit,
		memory:    s.MemoryLimit,
		storage:   s.StorageSize,
		status:    string(s.Status),
		price:     formatPricing(s.Pricing),
		withPrice: withPrice,
	}
}

// zeroDecimalCurrencies are the ISO 4217 codes with no subdivision.
// Dividing one of these by 100 would report a plausible price 100x too
// low.
var zeroDecimalCurrencies = map[string]bool{
	"bif": true, "clp": true, "djf": true, "gnf": true, "jpy": true,
	"kmf": true, "krw": true, "mga": true, "pyg": true, "rwf": true,
	"ugx": true, "vnd": true, "vuv": true, "xaf": true, "xof": true,
	"xpf": true,
}

// formatPricing renders a size's price for a table cell, and returns
// "" when the size has no configured price, since a zero would read as
// free. price_amount is in the currency's smallest unit: the
// zeroDecimalCurrencies print whole, anything else gets two decimals.
func formatPricing(p *api.SizePricing) string {
	if p == nil {
		return ""
	}
	currency := strings.ToUpper(p.Currency)
	if zeroDecimalCurrencies[strings.ToLower(p.Currency)] {
		return fmt.Sprintf("%d %s/%s",
			p.PriceAmount, currency, p.BillingInterval)
	}
	return fmt.Sprintf("%d.%02d %s/%s",
		p.PriceAmount/100, p.PriceAmount%100,
		currency, p.BillingInterval)
}
