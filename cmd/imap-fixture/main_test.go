package main

import (
	"strings"
	"testing"
)

func TestConfigRequiresExplicitCredential(t *testing.T) {
	t.Setenv("IMAP_FIXTURE_USERNAME", "")
	t.Setenv("IMAP_FIXTURE_PASSWORD", "")
	if _, err := configFromEnvironment(); err == nil {
		t.Fatal("empty fixture credential was accepted")
	}
	t.Setenv("IMAP_FIXTURE_USERNAME", "operations@infiniteocean.test")
	t.Setenv("IMAP_FIXTURE_PASSWORD", "local-fixture-password")
	value, err := configFromEnvironment()
	if err != nil || value.address != ":9993" || value.serverName != "imap-fixture" {
		t.Fatalf("config=%+v err=%v", value, err)
	}
}

func TestSeedMailboxIsDeterministicAndThreaded(t *testing.T) {
	first, second := seedMessages(), seedMessages()
	if len(first) != 2 || string(first[0]) != string(second[0]) || string(first[1]) != string(second[1]) {
		t.Fatal("seed mailbox is not deterministic")
	}
	if !strings.Contains(string(first[1]), "In-Reply-To: <brief-1@fixture.infiniteocean.test>") ||
		!strings.Contains(string(first[1]), "supporting-notes.txt") {
		t.Fatal("thread or attachment fixture is missing")
	}
}
