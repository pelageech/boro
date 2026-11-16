package command

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/spf13/cobra"

	"github.com/pelageech/boro/scenario"
)

type payloadField struct {
	Name   string         `json:"name"`
	Type   string         `json:"type"`
	Rules  string         `json:"rules,omitempty"`
	Format string         `json:"format,omitempty"`
	Enum   string         `json:"enum,omitempty"`
	Min    string         `json:"min,omitempty"`
	Max    string         `json:"max,omitempty"`
	Ref    string         `json:"ref,omitempty"`
	Fields []payloadField `json:"fields,omitempty"`
}

type operationDescription struct {
	Name        string         `json:"name"`
	Kind        string         `json:"kind"`
	Capability  string         `json:"capability"`
	Role        string         `json:"role,omitempty"`
	ResetDomain string         `json:"reset_domain,omitempty"`
	With        []payloadField `json:"with"`
	Want        []payloadField `json:"want,omitempty"`
}

type resourceDescription struct {
	Name       string         `json:"name"`
	Capability string         `json:"capability"`
	Spec       []payloadField `json:"spec"`
}

func newDescribe(composition Options) *cobra.Command {
	return &cobra.Command{
		Use:   "describe {profiles|operations|resources} [name]",
		Short: "Describe compiled profiles and typed YAML fields as JSON",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(command *cobra.Command, args []string) error {
			name := ""
			if len(args) == 2 {
				name = args[1]
			}
			encoder := json.NewEncoder(command.OutOrStdout())
			encoder.SetIndent("", "  ")
			switch args[0] {
			case "profiles":
				profiles := composition.Profiles
				var selected []scenario.ProfileDeclaration
				for _, profile := range profiles {
					if name == "" || profile.Name == name {
						selected = append(selected, profile)
					}
				}
				if len(selected) > 0 {
					return encoder.Encode(selected)
				}
			case "operations":
				selected := describeOperations(composition.Composition.Schema(), name)
				if len(selected) > 0 {
					return encoder.Encode(selected)
				}
			case "resources":
				selected := describeResources(composition.Composition.Schema(), name)
				if len(selected) > 0 {
					return encoder.Encode(selected)
				}
			default:
				return fmt.Errorf("choose profiles, operations or resources: %w", scenario.ErrInput)
			}
			return fmt.Errorf("unknown %s name %q: %w", args[0], name, scenario.ErrInput)
		},
	}
}

func describeOperations(schema scenario.Schema, name string) []operationDescription {
	var selected []operationDescription
	for _, descriptor := range schema.Descriptors() {
		if name != "" && descriptor.Name != name {
			continue
		}
		kind := "do"
		if descriptor.Want != nil {
			kind = "expect"
		}
		selected = append(selected, operationDescription{
			Name: descriptor.Name, Kind: kind, Capability: descriptor.Capability,
			Role: descriptor.Role, ResetDomain: descriptor.ResetDomain,
			With: describeFields(descriptor.Input), Want: describeFields(descriptor.Want),
		})
	}
	return selected
}

func describeResources(schema scenario.Schema, name string) []resourceDescription {
	var selected []resourceDescription
	for _, descriptor := range schema.ResourceDescriptors() {
		if name == "" || descriptor.Name == name {
			selected = append(selected, resourceDescription{
				Name: descriptor.Name, Capability: descriptor.Capability, Spec: describeFields(descriptor.Type),
			})
		}
	}
	return selected
}

func describeFields(shape reflect.Type) []payloadField {
	if shape == nil {
		return nil
	}
	for shape.Kind() == reflect.Pointer || shape.Kind() == reflect.Slice || shape.Kind() == reflect.Map {
		shape = shape.Elem()
	}
	if shape.Kind() != reflect.Struct || shape == scenario.AddressType {
		return nil
	}
	var fields []payloadField
	for field := range shape.Fields() {
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		fields = append(fields, payloadField{
			Name: name, Type: field.Type.String(), Rules: field.Tag.Get("rule"),
			Format: field.Tag.Get("format"), Fields: describeFields(field.Type),
			Enum: field.Tag.Get("enum"), Min: field.Tag.Get("min"), Max: field.Tag.Get("max"), Ref: field.Tag.Get("ref"),
		})
	}
	return fields
}
