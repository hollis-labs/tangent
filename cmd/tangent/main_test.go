package main

import (
	"fmt"
	"os"
	"testing"

	"github.com/hollis-labs/tangent/internal/testenv"
)

// Every test in this package boots the real stack. HOME and every XDG variable point at a
// temporary tree for the whole process, so nothing here can reach the real data of the person
// running the tests (Tangent is in use on the machines these tests run on).
func TestMain(m *testing.M) {
	_, cleanup, err := testenv.Isolate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	code := m.Run()
	cleanup()
	os.Exit(code)
}

func TestProcessIsIsolated(t *testing.T) {
	if err := testenv.Check(); err != nil {
		t.Fatal(err)
	}
}
