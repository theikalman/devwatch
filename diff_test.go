package main

import (
	"reflect"
	"testing"
)

func TestLineDiff(t *testing.T) {
	if d := lineDiff("a\nb", "a\nb"); d != nil {
		t.Fatalf("same input: got %v", d)
	}
	got := lineDiff("a\nb\nc", "a\nc\nd")
	want := []string{"- b", "+ d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
