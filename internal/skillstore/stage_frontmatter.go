package skillstore

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// stagedSkillMD aligns the command name Claude reads with the staged directory.
// Only the run's copy changes; versions and exported files retain the original bytes.
func stagedSkillMD(original []byte, name string) ([]byte, error) {
	md := original
	var bom []byte
	if bytes.HasPrefix(md, []byte{0xef, 0xbb, 0xbf}) {
		bom = md[:3]
		md = md[3:]
	}
	newline := "\n"
	var doc yaml.Node
	body := md
	if bytes.HasPrefix(md, []byte("---\n")) || bytes.HasPrefix(md, []byte("---\r\n")) {
		first := bytes.IndexByte(md, '\n') + 1
		if first > 1 && md[first-2] == '\r' {
			newline = "\r\n"
		}
		closed := false
		for start := first; start <= len(md); {
			end := bytes.IndexByte(md[start:], '\n')
			next := len(md)
			if end >= 0 {
				end += start
				next = end + 1
			} else {
				end = len(md)
			}
			line := bytes.TrimSpace(md[start:end])
			if bytes.Equal(line, []byte("---")) {
				if err := yaml.Unmarshal(md[first:start], &doc); err != nil {
					return nil, fmt.Errorf("SKILL.md frontmatter: %w", err)
				}
				body = md[next:]
				closed = true
				break
			}
			if next == len(md) {
				break
			}
			start = next
		}
		if !closed {
			return nil, errors.New("SKILL.md frontmatter is not closed with ---")
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("SKILL.md frontmatter must be a YAML mapping")
	}
	// Decode once to reject duplicate keys and invalid aliases before changing a node.
	var check map[string]any
	if err := doc.Decode(&check); err != nil {
		return nil, fmt.Errorf("SKILL.md frontmatter: %w", err)
	}
	mapping := doc.Content[0]
	found := false
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "name" {
			v := mapping.Content[i+1]
			if v.Anchor != "" && v.Value != name {
				return nil, errors.New("SKILL.md name uses a YAML anchor; give it a plain command name before running")
			}
			v.Kind = yaml.ScalarNode
			v.Tag = "!!str"
			v.Value = name
			v.Style = yaml.DoubleQuotedStyle
			v.Content = nil
			v.Alias = nil
			found = true
			break
		}
	}
	if !found {
		mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "name"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name, Style: yaml.DoubleQuotedStyle})
	}
	var header bytes.Buffer
	encoder := yaml.NewEncoder(&header)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, err
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	yamlBytes := header.Bytes()
	if newline != "\n" {
		yamlBytes = bytes.ReplaceAll(yamlBytes, []byte("\n"), []byte(newline))
	}
	var out bytes.Buffer
	out.Write(bom)
	out.WriteString("---" + newline)
	out.Write(yamlBytes)
	out.WriteString("---" + newline)
	out.Write(body)
	return out.Bytes(), nil
}
