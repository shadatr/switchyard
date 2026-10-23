// Command mockllm is a fake OpenAI-compatible upstream used for tests and
// benchmarks. Implemented in phase 1 (see PLAN.md §7).
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "mockllm: not implemented yet (PLAN.md phase 1)")
	os.Exit(1)
}
