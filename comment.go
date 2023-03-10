package main

import (
	"fmt"
	"strings"
)

func generateComment(tests map[string][]GoTestLine) string {
	failures := []GoTestLine{}

	for _, lines := range tests {
		for _, line := range lines {
			if line.Action == "fail" && line.Test != "" {
				failures = append(failures, line)
			}
		}
	}

	if len(failures) == 0 {
		return "All tests passed!"
	}

	r := []string{}

	r = append(r, "Oh no, test failures:")
	for _, failure := range failures {
		r = append(r, fmt.Sprintf("\t%s %s failed after %s", failure.Package, failure.Test, failure.Time))
	}

	return strings.Join(r, "\n")
}
