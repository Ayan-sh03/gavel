package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func main() {
	runPattern := flag.String("run", "", "regex pattern for the test name to run (required)")
	pkg := flag.String("pkg", "./...", "package pattern to test")
	extra := flag.String("args", "", "extra arguments to pass to go test (comma separated)")
	flag.Parse()

	if *runPattern == "" {
		fmt.Fprintln(os.Stderr, "testone: missing -run pattern")
		os.Exit(2)
	}

	args := []string{"test", *pkg, "-count=1", "-run", *runPattern}
	if strings.TrimSpace(*extra) != "" {
		for _, piece := range strings.Split(*extra, ",") {
			if p := strings.TrimSpace(piece); p != "" {
				args = append(args, p)
			}
		}
	}

	cmd := exec.Command("go", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "testone: %v\n", err)
		os.Exit(1)
	}
}
