package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// tiny helper used by `make smoke` to pluck a JSON field from stdin.
func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: jsonget <field>")
		os.Exit(2)
	}
	var m map[string]any
	body, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := json.Unmarshal(body, &m); err != nil {
		fmt.Fprintf(os.Stderr, "json: %v\n%s\n", err, body)
		os.Exit(1)
	}
	v, ok := m[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "missing field %q in %s\n", os.Args[1], body)
		os.Exit(1)
	}
	fmt.Print(v)
}
