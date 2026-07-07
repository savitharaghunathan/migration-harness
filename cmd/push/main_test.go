package main

import (
	"reflect"
	"testing"
)

func TestParseArgs_MessageAndFiles(t *testing.T) {
	message, files := parseArgs([]string{"--message", "hello world", "a.txt", "b.txt"})
	if message != "hello world" {
		t.Errorf("expected message %q, got %q", "hello world", message)
	}
	if !reflect.DeepEqual(files, []string{"a.txt", "b.txt"}) {
		t.Errorf("expected files [a.txt b.txt], got %v", files)
	}
}

func TestParseArgs_DefaultMessageWhenOmitted(t *testing.T) {
	message, files := parseArgs([]string{"a.txt"})
	if message != "konveyor: update" {
		t.Errorf("expected default message, got %q", message)
	}
	if !reflect.DeepEqual(files, []string{"a.txt"}) {
		t.Errorf("expected files [a.txt], got %v", files)
	}
}

func TestParseArgs_NoFilesMeansStageAll(t *testing.T) {
	message, files := parseArgs([]string{"--message", "commit everything"})
	if message != "commit everything" {
		t.Errorf("expected message %q, got %q", "commit everything", message)
	}
	if len(files) != 0 {
		t.Errorf("expected no files, got %v", files)
	}
}
