package command

import (
	"encoding/json"

	"github.com/spf13/cobra"
)

func newContract(composition Options) *cobra.Command {
	return &cobra.Command{
		Use: "contract", Short: "Print the compiled runner composition without acquiring resources",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := composition.Composition.Validate(); err != nil {
				return err
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(composition.Composition.Contract())
		},
	}
}
