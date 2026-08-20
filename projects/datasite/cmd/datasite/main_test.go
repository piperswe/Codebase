package main

import "testing"

func TestGetenvOrReturnsValueWhenSet(t *testing.T) {
	t.Setenv("DATASITE_TEST_VAR", "from-env")
	if got := getenvOr("DATASITE_TEST_VAR", "fallback"); got != "from-env" {
		t.Errorf("got %q, want %q", got, "from-env")
	}
}

func TestGetenvOrReturnsEmptyValueWhenSetEmpty(t *testing.T) {
	t.Setenv("DATASITE_TEST_VAR", "")
	if got := getenvOr("DATASITE_TEST_VAR", "fallback"); got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestGetenvOrReturnsDefaultWhenUnset(t *testing.T) {
	if got := getenvOr("DATASITE_TEST_VAR_UNSET", "fallback"); got != "fallback" {
		t.Errorf("got %q, want %q", got, "fallback")
	}
}
