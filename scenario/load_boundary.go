package scenario

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type wireScenario struct {
	Version     int                     `yaml:"version" rule:"required"`
	ID          string                  `yaml:"id" rule:"required"`
	Description string                  `yaml:"description"`
	Tags        []string                `yaml:"tags"`
	Environment Environment             `yaml:"environment" rule:"required"`
	Timeouts    Timeouts                `yaml:"timeouts" rule:"required"`
	Resources   map[string]wireResource `yaml:"resources" rule:"required"`
	Setup       []wireStep              `yaml:"setup"`
	Steps       []wireStep              `yaml:"steps" rule:"required,nonempty"`
	Teardown    []wireStep              `yaml:"teardown"`
}

type wireResource struct {
	Type string    `yaml:"type" rule:"required"`
	Spec yaml.Node `yaml:"spec" rule:"required"`
}

type wireStep struct {
	ID       string       `yaml:"id" rule:"required"`
	Do       *string      `yaml:"do"`
	Expect   *string      `yaml:"expect"`
	With     yaml.Node    `yaml:"with"`
	Want     yaml.Node    `yaml:"want"`
	Wait     *Wait        `yaml:"wait"`
	Timeout  *Duration    `yaml:"timeout"`
	Parallel []wireBranch `yaml:"parallel" rule:"nonempty"`
}

type wireBranch struct {
	ID    string     `yaml:"id" rule:"required"`
	Steps []wireStep `yaml:"steps" rule:"required,nonempty"`
}

type position struct{ line, column int }

type sourceDocument struct {
	file      string
	locations map[string]position
}

func (s sourceDocument) diagnostic(err error) *InputError {
	field := ""
	if detail, ok := errors.AsType[*ValidationError](err); ok {
		field = detail.Field
	}
	lookup := strings.ReplaceAll(field, ".[", "[")
	loc := s.locations[lookup]
	for loc.line == 0 && lookup != "" {
		at := strings.LastIndexAny(lookup, ".[")
		if at < 0 {
			lookup = ""
		} else {
			lookup = lookup[:at]
		}
		loc = s.locations[lookup]
	}
	if loc.line == 0 {
		loc = position{line: 1, column: 1}
	}
	return &InputError{File: s.file, Line: loc.line, Column: loc.column, Field: field, Category: "validation", Cause: err}
}

func (s sourceDocument) decoding(err error) error {
	detail := s.diagnostic(err)
	detail.Category = "decoding"
	if detail.Field == "" {
		detail.Field = "document"
		_, tail, found := strings.Cut(err.Error(), "line ")
		if found {
			number, _, _ := strings.Cut(tail, ":")
			if line, parseErr := strconv.Atoi(number); parseErr == nil {
				detail.Line = line
			}
		}
	}
	return detail
}

func parseDocument(file string, data []byte) (wireScenario, sourceDocument, error) {
	document, source, err := parseYAML(file, data)
	if err != nil {
		return wireScenario{}, source, err
	}
	wire, err := decodeNode[wireScenario](document.Content[0])
	if err != nil {
		return wireScenario{}, source, source.decoding(err)
	}
	return wire, source, nil
}

func parseYAML(file string, data []byte) (yaml.Node, sourceDocument, error) {
	source := sourceDocument{file: file, locations: make(map[string]position)}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var document yaml.Node
	if err := decoder.Decode(&document); err != nil {
		return yaml.Node{}, source, source.decoding(fmt.Errorf("decode YAML: %w", err))
	}
	var trailing yaml.Node
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err != nil {
			return yaml.Node{}, source, source.decoding(fmt.Errorf("trailing YAML: %w", err))
		}
		return yaml.Node{}, source, source.decoding(invalid("", "trailing YAML document is forbidden"))
	}
	if len(document.Content) != 1 {
		return yaml.Node{}, source, source.decoding(invalid("", "exactly one document is required"))
	}
	if err := inspectYAML(document.Content[0], "", source.locations); err != nil {
		return yaml.Node{}, source, source.decoding(err)
	}
	return document, source, nil
}

func inspectYAML(n *yaml.Node, path string, locations map[string]position) error {
	locations[path] = position{line: n.Line, column: n.Column}
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return invalid(path, "YAML anchors and aliases are forbidden")
	}
	if n.Tag == "!!null" {
		return invalid(path, "null is forbidden; omit an optional field or supply its typed value")
	}
	switch n.Kind {
	case yaml.MappingNode:
		seen := make(map[string]bool, len(n.Content)/2)
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			childPath := key.Value
			if path != "" {
				childPath = path + "." + childPath
			}
			locations[childPath] = position{line: key.Line, column: key.Column}
			if key.Kind != yaml.ScalarNode || key.Tag != yamlString || key.Value == "<<" {
				return invalid(childPath, "only string mapping keys are allowed; merge keys are forbidden")
			}
			if seen[key.Value] {
				return invalid(childPath, "duplicate key")
			}
			seen[key.Value] = true
			if err := inspectYAML(n.Content[i+1], childPath, locations); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, child := range n.Content {
			if err := inspectYAML(child, fmt.Sprintf("%s[%d]", path, i), locations); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		switch n.Tag {
		case yamlString, "!!int", "!!bool", "!!float":
		default:
			return invalid(path, "unsupported YAML tag "+n.Tag)
		}
	default:
		return invalid(path, "unsupported YAML node")
	}
	return nil
}

func decodeRegistered(n *yaml.Node, t reflect.Type) (Payload, error) {
	if err := checkShape(n, t); err != nil {
		return nil, err
	}
	value := reflect.New(t)
	if err := n.Decode(value.Interface()); err != nil {
		return nil, fmt.Errorf("decode registered payload: %w", err)
	}
	return value.Elem().Interface(), nil
}

func decodeDefinition(w wireScenario, operations Schema) (Definition, error) {
	out := Definition{
		Version: w.Version, ID: w.ID, Description: w.Description, Tags: w.Tags,
		Environment: w.Environment, Timeouts: w.Timeouts, Resources: make(map[Name]Resource),
	}
	keys := make([]string, 0, len(w.Resources))
	for name := range w.Resources {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	registry := operations.resourceEntries()
	for _, name := range keys {
		path := "resources." + name
		resource := w.Resources[name]
		descriptor, exists := registry[resource.Type]
		if !exists {
			return Definition{}, invalid(path+".type", "unknown resource type "+resource.Type)
		}
		value, err := decodeRegistered(&resource.Spec, descriptor.Type)
		if err != nil {
			return Definition{}, PrefixIssue(path+".spec", err)
		}
		out.Resources[Name(name)] = Resource{Type: resource.Type, Spec: value}
	}
	var err error
	out.Setup, err = decodeSteps(w.Setup, SetupPhase, operations)
	if err == nil {
		out.Steps, err = decodeSteps(w.Steps, "steps", operations)
	}
	if err == nil {
		out.Teardown, err = decodeSteps(w.Teardown, "teardown", operations)
	}
	return out, err
}
