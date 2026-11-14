package main

import (
	"flag"
	"fmt"
	"os"
	"runtime/debug"
)

var version = ""

func main() {
	fs := flag.NewFlagSet("ai-session-to-md", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *showVersion {
		fmt.Println(versionString())
		return
	}
	fmt.Fprintln(os.Stderr, "not implemented yet")
	os.Exit(2)
}

func versionString() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
