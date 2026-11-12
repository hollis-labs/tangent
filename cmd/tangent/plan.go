package main

import (
	"slices"
	"strings"
)

// readOnly are the modules that open no file: the commands that only read or print start these.
var readOnly = []string{"config", "logging", "health", "cli"}

// plan decides, from the command line, which framework modules to start and whether the data
// directory may be created. Only serving (the default) does either; every other command (version,
// config, health, help, completion) starts no module that opens a file and creates nothing, so
// running one without TANGENT_DATA_DIR cannot touch the live directory of the app name.
//
// Every argument that does not start with "-" counts as a command word. That is right only while
// the root command has no global flag that takes a value; TestRootHasNoValueFlags fails when one
// is added, and then plan must learn it.
func plan(args []string) (modules []string, prepare bool) {
	var words []string
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "--version" {
			return pick(readOnly), false
		}
		if !strings.HasPrefix(a, "-") {
			words = append(words, a)
		}
	}
	if len(words) == 0 || words[0] == "serve" {
		return substrateModules, true
	}
	return pick(readOnly), false
}

// pick keeps the start order of substrateModules.
func pick(want []string) []string {
	var out []string
	for _, m := range substrateModules {
		if slices.Contains(want, m) {
			out = append(out, m)
		}
	}
	return out
}
