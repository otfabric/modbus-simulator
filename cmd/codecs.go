package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/otfabric/go-modbus/codec"
	"github.com/spf13/cobra"
)

func newCodecsCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "codecs",
		Short: "List go-modbus codec descriptors",
	}
	c.AddCommand(newCodecsListCmd())
	return c
}

func newCodecsListCmd() *cobra.Command {
	var (
		family string
		format string
	)
	c := &cobra.Command{
		Use:   "list",
		Short: "List available codec IDs and metadata",
		Example: `
  modbus-simulator codecs list
  modbus-simulator codecs list --family float
  modbus-simulator codecs list --format json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := checkFormat(format); err != nil {
				return err
			}
			items := filterCodecDescriptors(family)
			if strings.EqualFold(strings.TrimSpace(format), "json") {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(items)
			}
			return renderCodecList(cmd.OutOrStdout(), items)
		},
	}
	c.Flags().StringVar(&family, "family", "", "Filter by codec family (integer, float, text, bcd, bytes, network, hardware_address, decimal_limb, time, vendor_specific)")
	c.Flags().StringVar(&format, "format", envOr("MODBUS_SIMULATOR_FORMAT", "text"), "Output format: text or json")
	return c
}

type codecListItem struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Family        string `json:"family"`
	ValueKind     string `json:"valueKind"`
	RegisterCount uint16 `json:"registerCount"`
	ByteCount     uint16 `json:"byteCount"`
}

func filterCodecDescriptors(familyFilter string) []codecListItem {
	all := codec.AvailableCodecDescriptors()
	filter := strings.TrimSpace(strings.ToLower(familyFilter))
	out := make([]codecListItem, 0, len(all))
	for _, d := range all {
		if filter != "" && d.Family.String() != filter {
			continue
		}
		out = append(out, codecListItem{
			ID:            d.ID,
			Name:          d.Name,
			Family:        d.Family.String(),
			ValueKind:     d.ValueKind.String(),
			RegisterCount: d.RegisterSpec.Count,
			ByteCount:     d.ByteSpec.Count,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func renderCodecList(w io.Writer, items []codecListItem) error {
	for _, item := range items {
		if _, err := fmt.Fprintf(w, "%s\t%s\tregisters=%d bytes=%d family=%s valueKind=%s\n",
			item.ID, item.Name, item.RegisterCount, item.ByteCount, item.Family, item.ValueKind); err != nil {
			return err
		}
	}
	return nil
}
