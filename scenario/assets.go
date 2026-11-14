package scenario

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Asset stores both the content identity and an isolated captured input.
type Asset struct {
	Resource Name   `json:"resource"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	Contents []byte `json:"-"`
}

func (s Schema) captureAssets(ctx context.Context, definition Definition, fixtureRoot string) ([]Asset, error) {
	var names []Name
	for name, resource := range definition.Resources {
		if descriptor, ok := s.LookupResource(resource.Type); ok && descriptor.Assets != nil {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	assets := make([]Asset, 0, len(names))
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("capture assets canceled: %w", err)
		}
		resource := definition.Resources[name]
		descriptor, _ := s.LookupResource(resource.Type)
		sources, err := descriptor.Assets(Clone(resource.Spec))
		if err != nil {
			return nil, PrefixIssue("resources."+string(name)+".spec", err)
		}
		sources = Clone(sources)
		slices.SortFunc(sources, func(a, b AssetSource) int {
			return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Field, b.Field))
		})
		for _, source := range sources {
			field := "resources." + string(name) + ".spec"
			if source.Field != "" {
				field += "." + source.Field
			}
			if !filepath.IsLocal(source.Path) || strings.Contains(source.Path, "\\") {
				return nil, invalid(field, "asset path must stay inside fixture root")
			}
			data, err := readAsset(fixtureRoot, source.Path)
			if err != nil {
				return nil, PrefixIssue(field, err)
			}
			assets = append(assets, Asset{Resource: name, Path: source.Path, SHA256: Digest(data), Contents: data})
		}
	}
	return assets, nil
}

func readAsset(root, path string) (data []byte, err error) {
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open fixture root: %w", err)
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	file, err := directory.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open fixture asset: %w", err)
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat fixture asset: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, invalid("", "fixture asset must be a regular file")
	}
	data, err = io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read fixture asset: %w", err)
	}
	return data, nil
}
