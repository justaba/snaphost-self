package main

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: workflow_yaml_check <workflow>...")
		os.Exit(2)
	}
	for _, path := range os.Args[1:] {
		data, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		var node yaml.Node
		if err := yaml.Unmarshal(data, &node); err != nil {
			panic(fmt.Errorf("%s: %w", path, err))
		}
	}
}
