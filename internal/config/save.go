package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// SaveSelection rewrites the top-level defaultProfile and defaultMode values
// of path so the next start reopens the last used profile and mode (the TUI
// switch/toggle paths call it). Comments survive: the whole document is
// parsed as a yaml.Node, only the two scalars change (absent keys are
// appended), and the output uses the shipped file's 2-space indent. The
// write is temp+rename so an interrupted write keeps the previous file.
func SaveSelection(path string, profile string, mode Mode) error {
	data, err := readFile(path)
	if err != nil {
		return err
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 || root.Content[0].Kind != yaml.MappingNode {
		return fmt.Errorf("%s: not a mapping — fix the file manually", path)
	}
	doc := root.Content[0]
	if err := setScalar(doc, "defaultProfile", profile, "# last profile used — zwiftboard reopens it at startup", ""); err != nil {
		return err
	}
	// The user asked for defaultMode right under defaultProfile; when both
	// are created, that order holds because defaultProfile went in first.
	if err := setScalar(doc, "defaultMode", string(mode), "# last mode used — single | double", "defaultProfile"); err != nil {
		return err
	}
	out, err := encode(&root)
	if err != nil {
		return err
	}
	return writeFile(path, out)
}

// setScalar sets key -> value on a top-level mapping, updating the existing
// node in place (plain string scalar, comment refreshed); a missing key is
// inserted right after the insertAfter key (or appended when that key is
// absent too), with its comment.
func setScalar(doc *yaml.Node, key, value, comment, insertAfter string) error {
	for i := 0; i < len(doc.Content)-1; i += 2 {
		k, v := doc.Content[i], doc.Content[i+1]
		if k.Value != key {
			continue
		}
		v.SetString(value)
		v.Tag = "!!str"
		v.LineComment = comment
		return nil
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, LineComment: comment}
	if insertAfter != "" {
		for i := 0; i < len(doc.Content)-1; i += 2 {
			if doc.Content[i].Value != insertAfter {
				continue
			}
			at := i + 2 // right after the insertAfter pair
			n := len(doc.Content)
			doc.Content = append(doc.Content, nil, nil)
			copy(doc.Content[at+2:], doc.Content[at:n]) // shift the tail right by 2
			doc.Content[at] = keyNode
			doc.Content[at+1] = valNode
			return nil
		}
	}
	doc.Content = append(doc.Content, keyNode, valNode)
	return nil
}

// encode renders root with the shipped file's 2-space indent.
func encode(root *yaml.Node) ([]byte, error) {
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(root); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return bytes.ReplaceAll(out.Bytes(), []byte("\r\n"), []byte("\n")), nil
}

// writeFile moves out into place at path: write a same-dir temp file, chmod
// 0644 (the config is user-editable), then rename so an interrupted write
// keeps the previous file.
func writeFile(path string, out []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	name := tmp.Name()
	_, err = tmp.Write(out)
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(name, path)
	}
	if err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
