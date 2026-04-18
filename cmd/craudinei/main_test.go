package main

import "testing"

func TestRun(t *testing.T) {
	if err := run(); err != nil {
		t.Errorf("run() = %v, want nil", err)
	}
}
