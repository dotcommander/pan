package cli

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
)

// The sealed mapping must resolve without loading or retaining the five source
// repositories. Behavioral proof lives in each command's domain fixtures.
func walkCommandModel(node *kong.Node, key string, visit func(*kong.Node, string)) {
	visit(node, key)
	for _, child := range node.Children {
		if child.Hidden || child.Type != kong.CommandNode {
			continue
		}
		walkCommandModel(child, key+"/"+child.Name, visit)
	}
}

func commandOptions(node *kong.Node) []string {
	values := []string{"--help", "-h"}
	for _, child := range node.Children {
		if !child.Hidden && child.Type == kong.CommandNode {
			values = append(values, child.Name)
		}
	}
	for _, group := range node.AllFlags(true) {
		for _, flag := range group {
			values = append(values, "--"+flag.Name)
			for _, alias := range flag.Aliases {
				values = append(values, "--"+alias)
			}
			if flag.Short != 0 {
				values = append(values, "-"+string(flag.Short))
			}
		}
	}
	slices.Sort(values)
	return slices.Compact(values)
}

func TestSealedSourceCommandMap(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile("../../testdata/parity/commands.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Revisions map[string]string `json:"revisions"`
		Commands  []struct {
			Source string   `json:"source"`
			Pan    string   `json:"pan"`
			Flags  []string `json:"flags"`
		} `json:"commands"`
	}
	if decodeErr := json.Unmarshal(data, &contract); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	parser, err := kong.New(&Root{}, kong.Name("pan"))
	if err != nil {
		t.Fatal(err)
	}
	nodes := map[string]*kong.Node{}
	walkCommandModel(parser.Model.Node, "", func(node *kong.Node, key string) {
		nodes[strings.ReplaceAll(strings.TrimPrefix(key, "/"), "/", " ")] = node
	})
	seen := map[string]bool{}
	for _, mapping := range contract.Commands {
		if seen[mapping.Source] {
			t.Errorf("duplicate source contract %s", mapping.Source)
		}
		seen[mapping.Source] = true
		product, _, _ := strings.Cut(mapping.Source, " ")
		if len(contract.Revisions[product]) != 40 {
			t.Errorf("missing source revision for %s", product)
		}
		node := nodes[mapping.Pan]
		if node == nil {
			t.Errorf("%s maps to missing command %s", mapping.Source, mapping.Pan)
			continue
		}
		options := commandOptions(node)
		for _, flag := range mapping.Flags {
			if !slices.Contains(options, "--"+flag) {
				t.Errorf("%s: pan %s lacks mapped --%s", mapping.Source, mapping.Pan, flag)
			}
		}
	}
}
